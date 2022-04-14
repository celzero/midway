// Copyright (c) 2022 RethinkDNS and its authors.
//
// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at http://mozilla.org/MPL/2.0/.
package main

import (
	"bufio"
	"crypto/tls"
	"fmt"
	"log"
	"net"
	"net/netip"
	"time"
)

type Conn struct {
	Typ      string
	HostName string
	Port     string
	Peeked   []byte
	net.Conn
}

func (c *Conn) Read(p []byte) (int, error) {
	// cover for any buffered data
	if len(c.Peeked) > 0 {
		n := copy(p, c.Peeked)
		c.Peeked = c.Peeked[n:]
		return n, nil
	}
	return c.Conn.Read(p)
}

func (c *Conn) Write(p []byte) (int, error)        { return c.Conn.Write(p) }
func (c *Conn) Close() error                       { return c.Conn.Close() }
func (c *Conn) LocalAddr() net.Addr                { return c.Conn.LocalAddr() }
func (c *Conn) RemoteAddr() net.Addr               { return c.Conn.RemoteAddr() }
func (c *Conn) SetDeadline(t time.Time) error      { return c.Conn.SetDeadline(t) }
func (c *Conn) SetReadDeadline(t time.Time) error  { return c.Conn.SetReadDeadline(t) }
func (c *Conn) SetWriteDeadline(t time.Time) error { return c.Conn.SetWriteDeadline(t) }

func underlyingConn(c net.Conn) net.Conn {
	if wrap, ok := c.(*Conn); ok {
		return wrap.Conn
	}
	return c
}

func asTLSConn(c net.Conn) *tls.Conn {
	uc := underlyingConn(c)
	if tlsconn, ok := uc.(*tls.Conn); ok {
		return tlsconn
	}
	return tls.Server(c, tlsconfig())
}

func ProxConn(c net.Conn) *Conn {
	_, port, err := net.SplitHostPort(c.LocalAddr().String())

	if err != nil {
		log.Println("invalid forward port")
		return nil
	}

	// proxy src:local-ip4 to dst:remote-ip4 / src:local-ip6 to dst:remote-ip6
	typ, _ := tcp4or6(c.LocalAddr())

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
	}

	peeked, _ := br.Peek(br.Buffered())

	return &Conn{
		Typ:      typ,      // tcp or tcp4 or tcp6
		HostName: upstream, // may be nil
		Port:     port,
		Peeked:   peeked, // len may be 0
		Conn:     c,
	}
}

// always "tcp" for now, because for web properties that are ipv4-only
// cause connect-timeouts from incoming ipv6 connections. Instead of
// specifically returning "tcp6", we now let it be "tcp".
func tcp4or6(a net.Addr) (string, error) {
	if addrport, err := netip.ParseAddrPort(a.String()); err == nil {
		if addrport.Addr().Is6() || addrport.Addr().Is4In6() {
			return /*tcp6*/ "tcp", nil
		} else {
			return /*tcp4*/ "tcp", nil
		}
	} else {
		return "tcp", err
	}
}
