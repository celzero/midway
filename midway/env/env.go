// Copyright (c) 2022 RethinkDNS and its authors.
//
// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at http://mozilla.org/MPL/2.0/.
package env

import (
	"crypto/tls"
	"encoding/base64"
	"log"
	"os"
	"strconv"
	"strings"
	"time"
)

func ConnTimeoutSec() time.Duration {
	timeoutsec := intenv("CONN_TIMEOUT_SEC", 5)
	return time.Second * time.Duration(timeoutsec)
}

func NoProxyTimeoutSec() time.Duration {
	timeoutsec := intenv("NOPROXY_TIMEOUT_SEC", 20)
	return time.Second * time.Duration(timeoutsec)
}

func MaxInflightDNSQueries() int64 {
	return intenv("MAX_INFLIGHT_DNS_QUERIES", 512)
}

func UpstreamDoh() string {
	return strenv("UPSTREAM_DOH", "https://dns.google/dns-query")
}

func tlsKeyCertPem() ([]byte, []byte) {
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

func tlsCertFile() string {
	return strenv("TLS_CERT_PATH", "./test/certs/server.crt")
}

func tlsKeyFile() string {
	return strenv("TLS_KEY_PATH", "./test/certs/server.key")
}

func TlsCerts() (*tls.Certificate, []string) {
	certpem, keypem := tlsKeyCertPem() // prod
	certfile := tlsCertFile()          // test
	keyfile := tlsKeyFile()            // test

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

func FlyAppName() string {
	return strenv("FLY_APP_NAME", "")
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
