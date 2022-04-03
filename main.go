// Copyright (c) 2022 RethinkDNS and its authors.
//
// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at http://mozilla.org/MPL/2.0/.
package main

import (
	"bufio"
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

func startTCP(tcptls net.Listener, wg *sync.WaitGroup) {
	if tcptls == nil {
		log.Print("Exiting tcp tls")
		wg.Done()
		return
	}

	for {
		conn, err := tcptls.Accept()
		if err != nil {
			log.Print("handle tcp tls err", err)
			continue
		}
		go proxyHTTPConn(conn)
	}
}

func startPP(tls *proxyproto.Listener, wg *sync.WaitGroup) {
	if tls == nil {
		log.Print("Exiting pp tls")
		wg.Done()
		return
	}

	for {
		conn, err := tls.Accept()
		if err != nil {
			log.Print("handle pp tls err", err)
			continue
		}
		go proxyHTTPConn(conn)
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

	if n := br.Buffered(); n <= 0 {
		// should never happen
		log.Println("buffer hasn't been peeked into...")
		time.Sleep(noproxytimeout)
		return
	}

	// FIXME: Sanitize hostname, shouldn't be site-local, for ex
	cdone := &sync.WaitGroup{}
	cdone.Add(1)
	peeked, _ := br.Peek(br.Buffered())
	wrappedconn := &Conn{
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

	dst, err := net.DialTimeout("tcp", net.JoinHostPort((c.HostName), port), conntimeout)
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
	go proxyCopy(src, dst, pwg)
	go proxyCopy(dst, src, pwg)
	pwg.Wait()

}

func proxyCopy(dst, src net.Conn, wg *sync.WaitGroup) {
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
		log.Printf("%s -> %s bytes: %d", src.RemoteAddr(), dst.RemoteAddr(), n)
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

