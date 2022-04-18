// Copyright (c) 2022 RethinkDNS and its authors.
//
// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at http://mozilla.org/MPL/2.0/.
package env

import (
	"crypto/tls"
	"crypto/x509"
	"log"
	"os/user"
)

func TlsConfig() *tls.Config {
	// ref: cs.opensource.google/go/go/+/refs/tags/go1.18:src/net/http/h2_bundle.go;drc=refs%2Ftags%2Fgo1.18;l=3983
	certificate, _ := TlsCerts()
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

func sni(cx *tls.Certificate) []string {
	// ref: cs.opensource.google/go/go/+/refs/tags/go1.18.1:src/crypto/tls/tls.go;drc=860704317e02d699e4e4a24103853c4782d746c1;l=252
	// ref: cs.opensource.google/go/go/+/refs/tags/go1.18.1:src/crypto/tls/common.go;drc=2580d0e08d5e9f979b943758d3c49877fb2324cb;l=1374
	// index0 contains der encoded cert
	der, _ := x509.ParseCertificate(cx.Certificate[0])
	var snis []string
	snis = append(snis, der.Subject.CommonName)
	snis = append(snis, der.DNSNames...)
	log.Print("TLS with: ", der.Subject.String(), " | for: ", snis)

	return snis
}

// ref: stackoverflow.com/a/66624820
func Sudo() bool {
	if u, err := user.Current(); err != nil {
		log.Print("Unable to get cur-user: %s", err)
		return false
	} else {
		return u.Username == "root"
	}
}

func Mtu() int {
	// mtu on fly is 1420
	return 1420
}

func TotalUdpServerRoutines() int {
	// runtime.NumCPU() instead?
	return 4
}
