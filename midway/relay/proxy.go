// Copyright (c) 2022 RethinkDNS and its authors.
//
// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at http://mozilla.org/MPL/2.0/.
package relay

import (
	"bufio"
	"crypto/tls"
	"fmt"
	"io"
	"log"
	"net"
	"net/netip"
	"strings"
	"sync"
	"time"

	"github.com/celzero/gateway/midway/env"
)

var (
	flyappname     = env.FlyAppName()
	flyurl         = flyappname + ".fly.dev"
	noproxytimeout = env.NoProxyTimeoutSec()
	conntimeout    = env.ConnTimeoutSec()
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

func NewProxyConn(c net.Conn) *Conn {
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

func (src *Conn) Forward() {
	defer src.Close()

	// TODO: discard health-checks conns appear from fly-edge w.x.y.z
	// 19:49 [info] host/sni missing 172.19.0.170:443 103.x.y.z:38862
	// 20:07 [info] host/sni missing 172.19.0.170:80 w.254.y.z:49008
	// 20:19 [info] host/sni missing 172.19.0.170:443 w.x.161.z:42676
	// 20:37 [info] host/sni missing 172.19.0.170:80 w.x.y.146:52548
	if src.disallow() {
		time.Sleep(noproxytimeout)
		return
	}

	log.Printf("relay: %s from %s => %s via %s", src.Typ, src.RemoteAddr(), src.HostName, src.LocalAddr())
	dst, err := net.DialTimeout(src.Typ, net.JoinHostPort((src.HostName), src.Port), conntimeout)
	if err != nil {
		log.Printf("relay: dial timeout err %v\n", err)
		return
	}

	defer dst.Close()

	if src.validroute(dst) {
		log.Print("relay: drop conn to ", dst.RemoteAddr().String())
		time.Sleep(noproxytimeout)
		return
	}

	pwg := &sync.WaitGroup{}
	pwg.Add(2)
	go proxyCopy("relay: download", src, dst, pwg)
	go proxyCopy("relay: upload", dst, src, pwg)
	pwg.Wait()
}

func (c *Conn) disallow() bool {
	dsturl := c.HostName

	if len(dsturl) <= 0 {
		// discard conn without host/sni
		return true
	} else if len(flyappname) > 0 && strings.Contains(dsturl, flyurl) {
		// discard conn to this host
		return true
	}
	return false // can proxy
}

func (c *Conn) validroute(to net.Conn) bool {
	ipportaddr, err := netip.ParseAddrPort(to.RemoteAddr().String())
	if err != nil {
		log.Println("disallow err: ", err)
		return true
	}

	ipaddr := ipportaddr.Addr()
	if !ipaddr.IsValid() ||
		ipaddr.IsPrivate() ||
		ipaddr.IsUnspecified() ||
		ipaddr.IsLoopback() ||
		ipaddr.IsMulticast() ||
		ipaddr.IsLinkLocalUnicast() ||
		ipaddr.IsLinkLocalMulticast() {
		log.Print("relay: conn remoting lo/mc/priv/invalid/unspecified ip:", ipaddr)
		return true
	}
	return false
}

// TODO: admission control with tc / htb
func proxyCopy(label string, dst, src net.Conn, wg *sync.WaitGroup) {
	defer wg.Done()

	// Before we unwrap src and/or dst, copy any buffered data.
	if wc, ok := src.(*Conn); ok && len(wc.Peeked) > 0 {
		if _, err := dst.Write(wc.Peeked); err != nil {
			log.Print(label, " peek ", err)
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
		log.Printf("%s; [src %s (via %s)] -> [dst %s via (%s)]; tx: %d", label, from, leg, to, returnleg, n)
	} else {
		log.Print(label, " copy ", err)
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
	return tls.Server(c, env.TlsConfig())
}
