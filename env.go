// Copyright (c) 2022 RethinkDNS and its authors.
//
// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at http://mozilla.org/MPL/2.0/.
package main

import (
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"log"
	"os"
	"os/user"
	"strconv"
	"strings"
	"time"
)

func connTimeoutSec_env() time.Duration {
	timeoutsec := intenv("CONN_TIMEOUT_SEC", 5)
	return time.Second * time.Duration(timeoutsec)
}

func noproxyTimeoutSec_env() time.Duration {
	timeoutsec := intenv("NOPROXY_TIMEOUT_SEC", 20)
	return time.Second * time.Duration(timeoutsec)
}

func maxInflightDNSQueries_env() int64 {
	return intenv("MAX_INFLIGHT_DNS_QUERIES", 512)
}

func upstreamDoh_env() string {
	return strenv("UPSTREAM_DOH", "https://dns.google/dns-query")
}

func tlskeycertpem_env() ([]byte, []byte) {
	// "sub.domain.tld,sub2.domain2.tld2,sub3.domain3.tld3"

	ck := strenv("TLS_CERTKEY", "")
	if len(ck) <= 0 {
		log.Print("no pem env: ", len(ck))
		return nil, nil
	}

	var key, cert []byte
	// SUB_DOMAIN_TLD="KEY=b64_key_content\nCRT=b64_cert_content"
	// why? community.fly.io/t/2984/21
	lines := strings.Split(ck, "\n")
	for i := range lines {
		kv := strings.Split(lines[i], "=")
		k := strings.ToUpper(kv[0])
		v := kv[1]
		if k == "KEY" {
			// raw-std-encoding because kv rids of b64 padding by splitting on "="
			key, _ = base64.RawStdEncoding.DecodeString(v)
		} else if k == "CRT" {
			cert, _ = base64.RawStdEncoding.DecodeString(v)
		}
		if len(key) > 0 && len(cert) > 0 {
			return cert, key
		}
	}
	return nil, nil
}

func tlscertfile_env() string {
	return strenv("TLS_CERT_PATH", "./test/certs/server.crt")
}

func tlskeyfile_env() string {
	return strenv("TLS_KEY_PATH", "./test/certs/server.key")
}

func tlscerts_env() (*tls.Certificate, []string) {
	certpem, keypem := tlskeycertpem_env() // prod
	certfile := tlscertfile_env()          // test
	keyfile := tlskeyfile_env()            // test

	if crt, err := tls.X509KeyPair(certpem, keypem); err == nil {
		log.Print("tls w key/crt PEM")
		return &crt, sni(&crt)
	} else if crt, err := tls.LoadX509KeyPair(certfile, keyfile); err == nil {
		log.Print("tls w key/crt FILE")
		return &crt, sni(&crt)
	} else {
		return nil, nil
	}
}

func flyappname_env() string {
	return strenv("FLY_APP_NAME", "")
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
func sudo() bool {
	if u, err := user.Current(); err != nil {
		log.Print("Unable to get cur-user: %s", err)
		return false
	} else {
		return u.Username == "root"
	}
}

func intenv(k string, d int64) int64 {
	if i, err := strconv.ParseInt(os.Getenv(k), 10, 0); err == nil {
		return i
	} else {
		return d
	}
}

func strenv(k string, d string) string {
	if str := os.Getenv(k); len(str) > 0 {
		return str
	} else {
		return d
	}
}
