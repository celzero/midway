// Copyright (c) 2022 RethinkDNS and its authors.
//
// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at http://mozilla.org/MPL/2.0/.
package main

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/netip"
	"sync"
	"time"

	proxyproto "github.com/pires/go-proxyproto"
)

const conntimeout = time.Second * 5
const noproxytimeout = time.Second * 20

// Adopted from: github.com/inetaf/tcpproxy/blob/be3ee21/tcpproxy.go
func main() {
	done := &sync.WaitGroup{}
	done.Add(5)

	t443, err := net.Listen("tcp", ":443")
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println("started: pptcp-server on port 443")
	pp443 := &proxyproto.Listener{Listener: t443}

	t80, err := net.Listen("tcp", ":80")
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println("started: pptcp-server on port 80")
	pp80 := &proxyproto.Listener{Listener: t80}

	// ref: fly.io/docs/app-guides/udp-and-tcp/
	u5000, err := net.ListenPacket("udp", "fly-global-services:5000")
	if err != nil {
		log.Println(err)
		if pc5000, err := net.ListenPacket("udp", ":5000"); err == nil {
			u5000 = pc5000
		} else {
			log.Fatal(err)
		}
	}
	fmt.Println("started: udp-server on port 5000")

	t5000, err := net.Listen("tcp", ":5000")
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println("started: tcp-server on port 5000")

	t5001, err := net.Listen("tcp", ":5001")
	if err != nil {
		log.Fatalf("err tcp-sever on port 5001 %q\n", err.Error())
	}
	fmt.Println("started: pptcp-server on port 5001")
	pp5001 := &proxyproto.Listener{Listener: t5001}

	go echoUDP(u5000, done)
	go echoTCP(t5000, done)
	go echoPP(pp5001, done)
	go startPP(pp443, done)
	go startPP(pp80, done)

	done.Wait()
}

func startTCP(tcp net.Listener, wg *sync.WaitGroup) {
	defer wg.Done()

	if tcp == nil {
		log.Print("Exiting tcp")
		return
	}

	for {
		if conn, err := tcp.Accept(); err == nil {
			go proxyHTTPConn(conn)
		} else {
			log.Print("handle tcp err", err)
			if errors.Is(err, net.ErrClosed) {
				// unrecoverable err
				log.Print(err)
				return
			}
		}
	}
}

func startPP(tcp *proxyproto.Listener, wg *sync.WaitGroup) {
	defer wg.Done()

	if tcp == nil {
		log.Print("Exiting pp tcp")
		return
	}

	for {
		if conn, err := tcp.Accept(); err == nil {
			go proxyHTTPConn(conn)
		} else {
			log.Print("handle pp tcp err")
			if errors.Is(err, net.ErrClosed) {
				log.Print(err)
				return
			}
		}
	}
}

func proxyHTTPConn(c net.Conn) {
	defer c.Close()

	// TODO: discard health-checks conns appear from fly-edge w.x.y.z
	// 19:49 [info] host/sni missing 172.19.0.170:443 103.x.y.z:38862
	// 20:07 [info] host/sni missing 172.19.0.170:80 w.254.y.z:49008
	// 20:19 [info] host/sni missing 172.19.0.170:443 w.x.161.z:42676
	// 20:37 [info] host/sni missing 172.19.0.170:80 w.x.y.146:52548
	if y := discardConn(c); y {
		time.Sleep(noproxytimeout)
		return
	}

	br := bufio.NewReader(c)

	httpHostName := httpHostHeader(br)
	sniServerName := clientHelloServerName(br)

	var upstream string
	if len(httpHostName) > 0 {
		upstream = httpHostName
	} else if len(sniServerName) > 0 {
		upstream = sniServerName
	} else {
		fmt.Printf("host/sni missing %s %s\n", c.LocalAddr(), c.RemoteAddr())
		time.Sleep(noproxytimeout)
		return
	}

	cdone := &sync.WaitGroup{}
	cdone.Add(1)
	peeked, _ := br.Peek(br.Buffered())

	wrappedconn := &Conn{
		// FIXME: Sanitize hostname, shouldn't be site-local, for ex?
		HostName: upstream,
		Peeked:   peeked,
		Conn:     c,
		ConnDone: cdone,
	}

	go forwardConn(wrappedconn)
	cdone.Wait()

	return
}

func forwardConn(src net.Conn) {
	c := src.(*Conn)

	defer c.ConnDone.Done()

	_, port, err := net.SplitHostPort(src.LocalAddr().String())

	if err != nil {
		log.Println("invalid forward port")
		return
	}

	// proxy src:local-ip4 to dst:remote-ip4 / src:local-ip6 to dst:remote-ip6
	typ, _ := tcp4or6(src.LocalAddr())
	log.Printf("dailing %s from %s => %s via %s", typ, src.RemoteAddr(), c.HostName, src.LocalAddr())
	dst, err := net.DialTimeout(typ, net.JoinHostPort((c.HostName), port), conntimeout)
	if err != nil {
		log.Printf("dial timeout err %v\n", err)
		return
	}
	defer dst.Close()

	if y := discardConn(dst); y {
		time.Sleep(noproxytimeout)
		return
	}

	pwg := &sync.WaitGroup{}
	pwg.Add(2)
	go proxyCopy("download", src, dst, pwg)
	go proxyCopy("upload", dst, src, pwg)
	pwg.Wait()

}

func proxyCopy(label string, dst, src net.Conn, wg *sync.WaitGroup) {
	defer wg.Done()

	// Before we unwrap src and/or dst, copy any buffered data.
	if wc, ok := src.(*Conn); ok && len(wc.Peeked) > 0 {
		if _, err := dst.Write(wc.Peeked); err != nil {
			return
		}
		wc.Peeked = nil
	}

	// Unwrap the src and dst from *Conn to *net.TCPConn so Go
	// 1.11's splice optimization kicks in.
	src = underlyingConn(src)
	dst = underlyingConn(dst)

	if n, err := io.Copy(dst, src); err == nil {
		from := src.RemoteAddr()
		to := dst.RemoteAddr()
		leg := src.LocalAddr()
		returnleg := dst.LocalAddr()
		log.Printf("%s: (src) %s -> (dst) %s via (src %s and dst %s); tx: %d", label, from, to, leg, returnleg, n)
	} else {
		log.Print(err)
	}
}

func discardConn(c net.Conn) bool {
	ipportaddr, err := netip.ParseAddrPort(c.RemoteAddr().String())
	if err != nil {
		log.Println(err)
		return true
	}

	ipaddr := ipportaddr.Addr()
	if ipaddr.IsPrivate() || !ipaddr.IsValid() || ipaddr.IsUnspecified() {
		return true
	}
	return false
}

func tcp4or6(a net.Addr) (string, error) {
	if addrport, err := netip.ParseAddrPort(a.String()); err == nil {
		if addrport.Addr().Is6() || addrport.Addr().Is4In6() {
			return "tcp6", nil
		} else {
			return "tcp4", nil
		}
	} else {
		return "", err
	}
}

type Conn struct {
	HostName string
	Peeked   []byte
	ConnDone *sync.WaitGroup
	net.Conn
}

func underlyingConn(c net.Conn) net.Conn {
	if wrap, ok := c.(*Conn); ok {
		return wrap.Conn
	}
	return c
}

