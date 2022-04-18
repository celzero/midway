// Copyright (c) 2022 RethinkDNS and its authors.
//
// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at http://mozilla.org/MPL/2.0/.
package main

import (
	"fmt"
	"log"
	"net"
	"sync"

	"github.com/celzero/gateway/midway"
	"github.com/celzero/gateway/midway/env"
	proxyproto "github.com/pires/go-proxyproto"
)

// Adopted from: github.com/inetaf/tcpproxy/blob/be3ee21/tcpproxy.go
func main() {
	portmap := map[string]string{
		"h11":    ":80",
		"tls":    ":443",
		"dot":    ":853",
		"flydoh": ":1443",
		"flydot": ":1853",
		"echo":   ":5000",
		"ppecho": ":5001",
	}
	if !env.Sudo() {
		portmap["tls"] = ":8443"
		portmap["dot"] = ":8853"
		portmap["h11"] = ":8080"
	}

	totallisteners := len(portmap)
	hold := barrier(totallisteners)

	// cleartext http1.x on port 80
	t80, err := net.Listen("tcp", portmap["h11"])
	ko(err)
	fmt.Println("started: pptcp-server on port ", portmap["h11"])
	pp80 := &proxyproto.Listener{Listener: t80}

	// tcp-tls (http2 / http1.1) on port 443
	t443, err := net.Listen("tcp", portmap["tls"])
	ko(err)
	fmt.Println("started: pptcp-server on port ", portmap["tls"])
	pp443 := &proxyproto.Listener{Listener: t443}

	// tcp-tls (DNS over TLS) on port 853
	t853, err := net.Listen("tcp", portmap["dot"])
	ko(err)
	fmt.Println("started: pptcp-server on port ", portmap["dot"])
	pp853 := &proxyproto.Listener{Listener: t853}

	// fly terminated tls (http2 and http1.1) on port 1443
	t1443, err := net.Listen("tcp", portmap["flydoh"])
	ko(err)
	fmt.Println("started: pptcp-server on port ", portmap["flydoh"])
	pp1443 := &proxyproto.Listener{Listener: t1443}

	// fly terminated tls (DNS over TLS) on port 1853
	t1853, err := net.Listen("tcp", portmap["flydot"])
	ko(err)
	fmt.Println("started: pptcp-server on port ", portmap["flydot"])
	pp1853 := &proxyproto.Listener{Listener: t1853}

	// ref: fly.io/docs/app-guides/udp-and-tcp/
	u5000, err := net.ListenPacket("udp", "fly-global-services:5000")
	if err != nil {
		log.Println(err)
		if pc5000, err := net.ListenPacket("udp", portmap["echo"]); err != nil {
			ko(err)
		} else {
			u5000 = pc5000
		}
	}
	fmt.Println("started: udp-server on port ", portmap["echo"])

	t5000, err := net.Listen("tcp", portmap["echo"])
	ko(err)
	fmt.Println("started: tcp-server on port ", portmap["echo"])

	t5001, err := net.Listen("tcp", portmap["ppecho"])
	ko(err)
	fmt.Println("started: pptcp-server on port ", portmap["ppecho"])
	pp5001 := &proxyproto.Listener{Listener: t5001}

	resolver := midway.NewDohStub(env.UpstreamDoh())

	// proxyproto listener works with plain tcp, too
	go midway.StartPP(pp80, hold)
	go midway.StartPPWithDoH(pp443, resolver, hold)
	go midway.StartPPWithDoT(pp853, resolver, hold)
	go midway.StartPPWithDoHCleartext(pp1443, resolver, hold)
	go midway.StartPPWithDoTCleartext(pp1853, resolver, hold)
	// echo servers on tcp and udp
	go midway.EchoUDP(u5000, hold)
	go midway.EchoTCP(t5000, hold)
	go midway.EchoPP(pp5001, hold)

	hold.Wait()
}

func barrier(count int) *sync.WaitGroup {
	w := &sync.WaitGroup{}
	w.Add(count)
	return w
}

func ko(err error) {
	if err != nil {
		log.Fatal(err)
	}
}
