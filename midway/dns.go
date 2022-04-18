// Copyright (c) 2022 RethinkDNS and its authors.
//
// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at http://mozilla.org/MPL/2.0/.
package midway

import (
	"bytes"
	"encoding/base64"
	"io/ioutil"
	"log"
	"net/http"
	"time"

	"github.com/miekg/dns"
)

// Adopted from: github.com/folbricht/routedns

type DohResolver interface {
	DnsHandler() dns.HandlerFunc
	DohHandler() http.HandlerFunc
}

type dohstub struct {
	url string
	doh *http.Client
	DohResolver
}

func NewDohStub(url string) DohResolver {
	tr := &http.Transport{
		ResponseHeaderTimeout: 10 * time.Second,
		IdleConnTimeout:       30 * time.Second,
	}
	hc := &http.Client{
		Transport: tr,
	}
	return &dohstub{url: url, doh: hc}
}

func (s *dohstub) DnsHandler() dns.HandlerFunc {
	return func(w dns.ResponseWriter, msg *dns.Msg) {
		ans := s.servfail(msg)
		defer func() {
			_ = w.WriteMsg(ans)
			w.Close()
		}()

		if q, err := msg.Pack(); err == nil {
			if x := s.dodoh(q); x != nil {
				ans = x
			}
		}
	}
}

func (s *dohstub) servfail(q *dns.Msg) *dns.Msg {
	return responseWithCode(q, dns.RcodeServerFailure)
}

func (s *dohstub) refused(q *dns.Msg) *dns.Msg {
	return responseWithCode(q, dns.RcodeRefused)
}

func responseWithCode(q *dns.Msg, rcode int) *dns.Msg {
	a := new(dns.Msg)
	a.SetRcode(q, rcode)
	return a
}

func (s *dohstub) DohHandler() http.HandlerFunc {
	// ref: github.com/folbricht/routedns/blob/5932594/dohlistener.go#L153
	return func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case "GET":
			s.getHandler(w, r)
		case "POST":
			s.postHandler(w, r)
		default:
			http.Error(w, "GET or POST only", http.StatusMethodNotAllowed)
		}
	}
}

func (s *dohstub) getHandler(w http.ResponseWriter, r *http.Request) {
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
	s.upstreamDNS(b, w, r)
}

func (s *dohstub) postHandler(w http.ResponseWriter, r *http.Request) {
	b, err := ioutil.ReadAll(r.Body)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	s.upstreamDNS(b, w, r)
}

func (s *dohstub) upstreamDNS(b []byte, w http.ResponseWriter, r *http.Request) {
	q := new(dns.Msg)
	if err := q.Unpack(b); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	var err error
	a := s.dodoh(b)

	// A nil response from the resolvers means "drop", return blank response
	if a == nil {
		w.WriteHeader(http.StatusForbidden)
		return
	}

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
func (s *dohstub) dodoh(b []byte) *dns.Msg {
	req, err := http.NewRequest("POST", s.url, bytes.NewReader(b))
	if err != nil {
		return nil
	}

	req.Header.Add("accept", "application/dns-message")
	req.Header.Add("content-type", "application/dns-message")

	res, err := s.doh.Do(req)

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
		log.Printf("doh: q0 %s => a0 %s | len(ans): %d", s.querystr(x), s.ansstr(x), len(x.Answer))
		return x
	}

	return nil
}

func (s *dohstub) querystr(m *dns.Msg) string {
	if m == nil || m.Question == nil || len(m.Question) <= 0 {
		return "no-query"
	} else {
		return m.Question[0].String()
	}
}

func (s *dohstub) ansstr(m *dns.Msg) string {
	if m == nil || m.Answer == nil || len(m.Answer) <= 0 {
		return "no-ans"
	} else {
		return m.Answer[0].String()
	}
}
