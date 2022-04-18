// Copyright (c) 2022 RethinkDNS and its authors.
//
// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at http://mozilla.org/MPL/2.0/.
package midway

import (
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"strings"
	"sync"

	"github.com/celzero/gateway/midway/env"
	"github.com/celzero/gateway/midway/relay"
	"github.com/miekg/dns"
	proxyproto "github.com/pires/go-proxyproto"
	"golang.org/x/net/http2"
	"golang.org/x/net/http2/h2c"
)

var (
	conntimeout        = env.ConnTimeoutSec()
	maxInflightQueries = env.MaxInflightDNSQueries()
	_, tlsDNSNames     = env.TlsCerts()
)

func accept(c net.Conn) (net.Conn, bool) {
	d := relay.NewProxyConn(c)
	// if the incoming sni == our dns-server, then serve the req
	for i := range tlsDNSNames {
		if strings.Contains(d.HostName, tlsDNSNames[i]) {
			return d, false
		}
	}
	// else, proxy the request to the backend as approp
	go d.Forward()
	return d, true
}

func StartPPWithDoH(tcp *proxyproto.Listener, doh DohResolver, wg *sync.WaitGroup) {
	defer wg.Done()

	if tcp == nil {
		log.Print("Exiting pp doh")
		return
	}

	if stls := relay.NewTlsListener(tcp, accept); stls != nil {
		log.Print("mode: relay + DoH ", tcp.Addr().String())

		mux := http.NewServeMux()
		mux.HandleFunc("/", doh.DohHandler())
		dnsserver := &http.Server{
			Handler:      mux,
			ReadTimeout:  conntimeout,
			WriteTimeout: conntimeout,
		}

		// http.Server takes ownership of stls
		err := dnsserver.Serve(stls)
		log.Print("exit doh+relay:", err)
	} else {
		log.Print("mode: relay only ", tcp.Addr().String())

		h := &sync.WaitGroup{}
		h.Add(1)
		StartPP(tcp, h)
		h.Wait()
	}
}

func StartPPWithDoT(tcp *proxyproto.Listener, doh DohResolver, wg *sync.WaitGroup) {
	defer wg.Done()

	if tcp == nil {
		log.Print("Exiting pp dot")
		return
	}

	if stls := relay.NewTlsListener(tcp, accept); stls != nil {
		log.Print("mode: relay + DoT ", tcp.Addr().String())

		// ref: github.com/miekg/dns/blob/dedee46/server.go#L192
		// usage: github.com/folbricht/routedns/blob/7b8e284/dotlistener.go#L29
		dnsserver := &dns.Server{
			Net:           "tcp-tls", // unused
			Listener:      stls,
			MaxTCPQueries: int(maxInflightQueries),
			Handler:       doh.DnsHandler(),
		}

		// ref: github.com/miekg/dns/blob/dedee46/server.go#L133
		err := dnsserver.ActivateAndServe()
		log.Print("exit dot+relay:", err)
	} else {
		log.Print("mode: relay only ", tcp.Addr().String())

		h := &sync.WaitGroup{}
		h.Add(1)
		StartPP(tcp, h)
		h.Wait()
	}
}

func StartPP(tcp *proxyproto.Listener, wg *sync.WaitGroup) {
	defer wg.Done()

	if tcp == nil {
		log.Print("Exiting pp tcp")
		return
	}

	defer tcp.Close()

	for {
		if conn, err := tcp.Accept(); err == nil {
			if _, ok := accept(conn); !ok {
				log.Print("cannot accept conn")
			}
		} else {
			log.Print("handle pp tcp err")
			if errors.Is(err, net.ErrClosed) {
				log.Print(err)
				return
			}
		}
	}
}

// ref: github.com/thrawn01/h2c-golang-example
func StartPPWithDoHCleartext(tcp *proxyproto.Listener, doh DohResolver, wg *sync.WaitGroup) {
	defer wg.Done()

	if tcp == nil {
		log.Print("Exiting pp doh")
		return
	}

	log.Print("mode: DoH cleartext ", tcp.Addr().String())

	h2svc := &http2.Server{}
	// debug h2c with GODEBUG="http2debug=1" env
	// ref: cs.opensource.google/go/x/net/+/290c469a:http2/h2c/h2c.go;drc=c6fcb2dbf98596caad8f56c0c398c1c6ff1fcff9;l=35
	dohh2c := http.HandlerFunc(doh.DohHandler())
	dnsfn := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/h/w" {
			fmt.Fprintf(w, "Hello, %v, http: %v", r.URL.Path, r.TLS == nil)
			return
		}
		dohh2c.ServeHTTP(w, r)
	})

	dnsserver := &http.Server{
		// h2c-handler embed in a http.NewServerMux doesn't work
		Handler:      h2c.NewHandler(dnsfn, h2svc),
		ReadTimeout:  conntimeout,
		WriteTimeout: conntimeout,
	}

	err := dnsserver.Serve(tcp)
	log.Print("exit doh cleartext:", err)
}

func StartPPWithDoTCleartext(tcp *proxyproto.Listener, doh DohResolver, wg *sync.WaitGroup) {
	defer wg.Done()

	if tcp == nil {
		log.Print("Exiting pp dot")
		return
	}

	log.Print("mode: DoT cleartext ", tcp.Addr().String())
	// ref: github.com/miekg/dns/blob/dedee46/server.go#L192
	// usage: github.com/folbricht/routedns/blob/7b8e284/dotlistener.go#L29
	dnsserver := &dns.Server{
		Net:           "tcp", // unused
		Listener:      tcp,
		MaxTCPQueries: int(maxInflightQueries),
		Handler:       doh.DnsHandler(),
	}

	// ref: github.com/miekg/dns/blob/dedee46/server.go#L133
	err := dnsserver.ActivateAndServe()
	log.Print("exit dot cleartext:", err)
}
