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
	"sync"
	"time"

	proxyproto "github.com/pires/go-proxyproto"
)

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

func proxyHTTPConn(c net.Conn) {
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
		time.Sleep(time.Second * 30)
		c.Close()
		return
	}

	// FIXME: Sanitize hostname, shouldn't be site-local, for ex
	if n := br.Buffered(); n > 0 {
		peeked, _ := br.Peek(n)
		wrappedconn := &Conn{
			HostName: upstream,
			Peeked:   peeked,
			Conn:     c,
		}
		go forwardConn(wrappedconn)
		return
	}

	// should never happen
	log.Println("buffer hasn't been peeked into...")
	time.Sleep(time.Second * 30)
	c.Close()
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

func forwardConn(src net.Conn) {
	defer src.Close()
	c := src.(*Conn)
	_, port, err := net.SplitHostPort(src.LocalAddr().String())

	if err != nil {
		log.Println("invalid forward port")
		return
	}

	dst, err := net.DialTimeout("tcp", net.JoinHostPort((c.HostName), port), 5*time.Second)
	if err != nil {
		log.Printf("dial timeout err %v\n", err)
		return
	}
	defer dst.Close()

	wg := &sync.WaitGroup{}
	wg.Add(2)
	go proxyCopy(src, dst, wg)
	go proxyCopy(dst, src, wg)
	wg.Wait()

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
	src = UnderlyingConn(src)
	dst = UnderlyingConn(dst)

	io.Copy(dst, src)
}

type Conn struct {
	HostName string
	Peeked   []byte
	net.Conn
}

func UnderlyingConn(c net.Conn) net.Conn {
	if wrap, ok := c.(*Conn); ok {
		return wrap.Conn
	}
	return c
}

