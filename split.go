// Copyright (c) 2022 RethinkDNS and its authors.
//
// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at http://mozilla.org/MPL/2.0/.
package main

import (
	"crypto/tls"
	"log"
	"net"
)

type splitListener struct {
	listener net.Listener
	onConn   func(net.Conn) (net.Conn, bool)
}

func (l splitListener) Accept() (net.Conn, error) {
	for {
		c, err := l.listener.Accept()
		if err != nil {
			log.Print("err accepting tcp-conn in split", err)
			return nil, err
		}

		if d, ok := l.onConn(c); !ok {
			// parent tls-listener handles conn
			return d, nil
		} // else: fn onConn handled conn
	}
}

func (l splitListener) Close() error {
	return l.listener.Close()
}

func (l splitListener) Addr() net.Addr {
	return l.listener.Addr()
}

func tlsconfig() *tls.Config {

	// ref: cs.opensource.google/go/go/+/refs/tags/go1.18:src/net/http/h2_bundle.go;drc=refs%2Ftags%2Fgo1.18;l=3983
	certificate := tlscerts_env()
	if certificate == nil {
		log.Print("empty cert")
		return nil
	}
	c := []tls.Certificate{*certificate}

	alpn := []string{"h2", "http/1.1"}

	// ref: github.com/caddyserver/caddy/blob/c48fadc/modules/caddytls/connpolicy.go#L441
	// ref: github.com/folbricht/routedns/blob/35c9051/tls.go#L12
	return &tls.Config{
		Certificates: c,
		MinVersion:   tls.VersionTLS12,
		NextProtos:   alpn,
	}
}

// ref: stackoverflow.com/a/69828625
func splitTlsListener(tcp net.Listener, in func(net.Conn) (net.Conn, bool)) net.Listener {
	return tls.NewListener(
		&splitListener{listener: tcp, onConn: in},
		tlsconfig(),
	)
}
