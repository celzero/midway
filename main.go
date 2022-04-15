// Copyright (c) 2022 RethinkDNS and its authors.
//
// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at http://mozilla.org/MPL/2.0/.
package main

import (
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/miekg/dns"
	proxyproto "github.com/pires/go-proxyproto"
)

var (
	conntimeout        = connTimeoutSec_env()
	noproxytimeout     = noproxyTimeoutSec_env()
	maxInflightQueries = maxInflightDNSQueries_env()
	upstreamdoh        = upstreamDoh_env()
	_, tlsDNSNames     = tlscerts_env()
)

// Adopted from: github.com/inetaf/tcpproxy/blob/be3ee21/tcpproxy.go
func main() {
	totallisteners := 6

	portmap := map[string]string{
		"tls":    ":443",
		"dot":    ":853",
		"h11":    ":80",
		"echo":   ":5000",
		"ppecho": ":5001",
	}
	if !sudo() {
		portmap["tls"] = ":8443"
		portmap["dot"] = ":8853"
		portmap["h11"] = ":8080"
	}

	done := &sync.WaitGroup{}
	done.Add(totallisteners)

	t443, err := net.Listen("tcp", portmap["tls"])
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println("started: pptcp-server on port ", portmap["tls"])
	pp443 := &proxyproto.Listener{Listener: t443}

	t853, err := net.Listen("tcp", portmap["dot"])
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println("started: pptcp-server on port ", portmap["dot"])
	pp853 := &proxyproto.Listener{Listener: t853}

	t80, err := net.Listen("tcp", portmap["h11"])
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println("started: pptcp-server on port ", portmap["h11"])
	pp80 := &proxyproto.Listener{Listener: t80}

	// ref: fly.io/docs/app-guides/udp-and-tcp/
	u5000, err := net.ListenPacket("udp", "fly-global-services:5000")
	if err != nil {
		log.Println(err)
		if pc5000, err := net.ListenPacket("udp", portmap["echo"]); err == nil {
			u5000 = pc5000
		} else {
			log.Fatal(err)
		}
	}
	fmt.Println("started: udp-server on port ", portmap["echo"])

	t5000, err := net.Listen("tcp", portmap["echo"])
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println("started: tcp-server on port ", portmap["echo"])

	t5001, err := net.Listen("tcp", portmap["ppecho"])
	if err != nil {
		log.Fatalf("err tcp-sever on port %s %q\n", portmap["ppecho"], err.Error())
	}
	fmt.Println("started: pptcp-server on port ", portmap["ppecho"])
	pp5001 := &proxyproto.Listener{Listener: t5001}

	tr := &http.Transport{
		ResponseHeaderTimeout: 10 * time.Second,
		IdleConnTimeout:       30 * time.Second,
	}
	dohresolver := &http.Client{
		Transport: tr,
	}

	go echoUDP(u5000, done)
	go echoTCP(t5000, done)
	go echoPP(pp5001, done)
	// proxyproto listener works with plain tcp, too
	go startPPWithDoH(pp443, dohresolver, done)
	go startPPWithDoT(pp853, dohresolver, done)
	go startPP(pp80, done)

	done.Wait()
}

func onNewConn(c net.Conn) (net.Conn, bool) {
	d := ProxConn(c)
	// if the incoming sni == our dns-server, then serve the req
	for i := range tlsDNSNames {
		if strings.Contains(d.HostName, tlsDNSNames[i]) {
			return d, false
		}
	}
	// else, proxy the request to the backend as approp
	go forwardConn(d)
	return nil, true
}

func startPPWithDoH(tcp *proxyproto.Listener, doh *http.Client, wg *sync.WaitGroup) {
	defer wg.Done()

	if tcp == nil {
		log.Print("Exiting pp doh")
		return
	}

	stls := splitTlsListener(tcp, onNewConn)
	mux := http.NewServeMux()
	mux.HandleFunc("/", dohHandler(doh))
	dnsserver := &http.Server{
		Handler:      mux,
		ReadTimeout:  conntimeout,
		WriteTimeout: conntimeout,
	}

	// http.Server takes ownership of stls
	_ = dnsserver.Serve(stls)
}

func startPPWithDoT(tcp *proxyproto.Listener, doh *http.Client, wg *sync.WaitGroup) {
	defer wg.Done()

	if tcp == nil {
		log.Print("Exiting pp dot")
		return
	}

	stls := splitTlsListener(tcp, onNewConn)

	// ref: github.com/miekg/dns/blob/dedee46/server.go#L192
	// usage: github.com/folbricht/routedns/blob/7b8e284/dotlistener.go#L29
	dnsserver := &dns.Server{
		Net:           "tcp-tls", // unused
		Listener:      stls,
		MaxTCPQueries: int(maxInflightQueries),
		Handler:       dnsHandler(doh),
	}

	// ref: github.com/miekg/dns/blob/dedee46/server.go#L133
	_ = dnsserver.ActivateAndServe()
}

func startPP(tcp *proxyproto.Listener, wg *sync.WaitGroup) {
	defer wg.Done()

	if tcp == nil {
		log.Print("Exiting pp tcp")
		return
	}

	defer tcp.Close()

	for {
		if conn, err := tcp.Accept(); err == nil {
			go forwardConn(ProxConn(conn))
		} else {
			log.Print("handle pp tcp err")
			if errors.Is(err, net.ErrClosed) {
				log.Print(err)
				return
			}
		}
	}
}
