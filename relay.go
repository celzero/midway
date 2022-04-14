// Copyright (c) 2022 RethinkDNS and its authors.
//
// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at http://mozilla.org/MPL/2.0/.
package main

import (
	"io"
	"log"
	"net"
	"net/netip"
	"sync"
	"time"
)

func forwardConn(src *Conn) {
	defer src.Close()

	// TODO: discard health-checks conns appear from fly-edge w.x.y.z
	// 19:49 [info] host/sni missing 172.19.0.170:443 103.x.y.z:38862
	// 20:07 [info] host/sni missing 172.19.0.170:80 w.254.y.z:49008
	// 20:19 [info] host/sni missing 172.19.0.170:443 w.x.161.z:42676
	// 20:37 [info] host/sni missing 172.19.0.170:80 w.x.y.146:52548
	if discardConn(src) {
		time.Sleep(noproxytimeout)
		return
	}

	log.Printf("relay: %s from %s => %s via %s", src.Typ, src.RemoteAddr(), src.HostName, src.LocalAddr())
	dst, err := net.DialTimeout(src.Typ, net.JoinHostPort((src.HostName), src.Port), conntimeout)
	if err != nil {
		log.Printf("dial timeout err %v\n", err)
		return
	}

	defer dst.Close()

	if discardConn(dst) {
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
