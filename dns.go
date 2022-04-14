// Copyright (c) 2022 RethinkDNS and its authors.
//
// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at http://mozilla.org/MPL/2.0/.
package main

import (
	"bytes"
	"encoding/base64"
	"io/ioutil"
	"log"
	"net/http"

	"github.com/miekg/dns"
)

// Adopted from: github.com/folbricht/routedns

func dnsHandler(doh *http.Client) dns.HandlerFunc {
	return func(w dns.ResponseWriter, msg *dns.Msg) {
		ans := refused(msg)
		defer func() {
			_ = w.WriteMsg(ans)
			w.Close()
		}()

		q, err := msg.Pack()
		if err != nil {
			return
		}

		req, err := http.NewRequest("POST", upstreamdoh, bytes.NewReader(q))
		if err != nil {
			return
		}

		ans = servfail(msg)
		req.Header.Add("accept", "application/dns-message")
		req.Header.Add("content-type", "application/dns-message")

		// TODO: rm and restore query-id
		res, err := doh.Do(req)

		if err != nil {
			return
		}
		defer res.Body.Close()

		if res.StatusCode < 200 || res.StatusCode > 299 {
			return
		}

		ansb, err := ioutil.ReadAll(res.Body)
		if err != nil {
			return
		}

		p := new(dns.Msg)
		if err = p.Unpack(ansb); err == nil {
			ans = p
		}
		return
	}
}

func servfail(q *dns.Msg) *dns.Msg {
	return responseWithCode(q, dns.RcodeServerFailure)
}

func refused(q *dns.Msg) *dns.Msg {
	return responseWithCode(q, dns.RcodeRefused)
}

func responseWithCode(q *dns.Msg, rcode int) *dns.Msg {
	a := new(dns.Msg)
	a.SetRcode(q, rcode)
	return a
}

func dohHandler(resolver *http.Client) func(http.ResponseWriter, *http.Request) {
	// ref: github.com/folbricht/routedns/blob/5932594/dohlistener.go#L153
	return func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case "GET":
			getHandler(resolver, w, r)
		case "POST":
			postHandler(resolver, w, r)
		default:
			http.Error(w, "GET or POST only", http.StatusMethodNotAllowed)
		}
	}
}

func getHandler(resolver *http.Client, w http.ResponseWriter, r *http.Request) {
	b64, ok := r.URL.Query()["dns"]
	if !ok {
		http.Error(w, "query missing", http.StatusBadRequest)
		return
	}
	if len(b64) < 1 {
		http.Error(w, "query empty", http.StatusBadRequest)
		return
	}
	b, err := base64.RawURLEncoding.DecodeString(b64[0])
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	upstreamDNS(resolver, b, w, r)
}

func postHandler(resolver *http.Client, w http.ResponseWriter, r *http.Request) {
	b, err := ioutil.ReadAll(r.Body)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	upstreamDNS(resolver, b, w, r)
}

func upstreamDNS(resolver *http.Client, b []byte, w http.ResponseWriter, r *http.Request) {
	q := new(dns.Msg)
	if err := q.Unpack(b); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	var err error
	a := dodoh(resolver, b)

	// A nil response from the resolvers means "drop", return blank response
	if a == nil {
		w.WriteHeader(http.StatusForbidden)
		return
	}

	querystr := q.Question[0].String()
	ansstr := a.Answer[0].String()
	log.Printf("dns: q0 %s => ans0 %s | len(ans): %d", querystr, ansstr, len(a.Answer))

	// Pad the packet according to rfc8467 and rfc7830
	// TODO: padAnswer(q, a)

	out, err := a.Pack()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("content-type", "application/dns-message")
	w.Header().Set("content-type", "application/dns-message")
	_, _ = w.Write(out)
}

// TODO: rm query-id before request and restore after response
func dodoh(resolver *http.Client, b []byte) *dns.Msg {
	req, err := http.NewRequest("POST", upstreamdoh, bytes.NewReader(b))
	if err != nil {
		return nil
	}

	req.Header.Add("accept", "application/dns-message")
	req.Header.Add("content-type", "application/dns-message")

	res, err := resolver.Do(req)

	if err != nil {
		return nil
	}
	defer res.Body.Close()

	if res.StatusCode < 200 || res.StatusCode > 299 {
		return nil
	}

	ans, err := ioutil.ReadAll(res.Body)
	if err != nil {
		return nil
	}

	x := new(dns.Msg)
	if err = x.Unpack(ans); err == nil {
		querystr := x.Question[0].String()
		ansstr := x.Answer[0].String()
		log.Printf("dns: q0 %s  => ans0 %s | len(ans): %d", querystr, ansstr, len(x.Answer))

		return x
	}

	return nil
}
