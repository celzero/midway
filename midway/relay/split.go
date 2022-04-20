// Copyright (c) 2022 RethinkDNS and its authors.
//
// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at http://mozilla.org/MPL/2.0/.
package relay

import (
	"crypto/tls"
	"log"
	"net"

	"github.com/celzero/gateway/midway/env"
	proxyproto "github.com/pires/go-proxyproto"
)

type HandlerFunc func(net.Conn) (net.Conn, bool)

type splitListener struct {
	listener *proxyproto.Listener
	onConn   HandlerFunc
}

func (l *splitListener) Accept() (net.Conn, error) {
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

func (l *splitListener) Close() error {
	return l.listener.Close()
}

func (l *splitListener) Addr() net.Addr {
	return l.listener.Addr()
}

// ref: stackoverflow.com/a/69828625
func NewTlsListener(tcp *proxyproto.Listener, in HandlerFunc) net.Listener {
	if cfg := env.TlsConfig(); cfg == nil {
		// no tls-certs setup, so split-listener isn't really required
		return nil
	} else {
		return tls.NewListener(
			&splitListener{listener: tcp, onConn: in},
			cfg,
		)
	}
}
