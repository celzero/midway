package main

import (
	"bufio"
	"bytes"
	"context"
	"net/http"
)

func (p *Proxy) AddHTTPHostRoute(ipPort, httpHost string, dest Target) {
	p.AddHTTPHostMatchRoute(ipPort, equals(httpHost), dest)
}

func (p *Proxy) AddHTTPHostMatchRoute(ipPort string, match Matcher, dest Target) {
	p.addRoute(ipPort, httpHostMatch{match, dest})
}

type httpHostMatch struct {
	matcher Matcher
	target  Target
}

func (m httpHostMatch) match(br *bufio.Reader) (Target, string) {
	hh := httpHostHeader(br)
	if m.matcher(context.TODO(), hh) {
		return m.target, hh
	}
	return nil, ""
}

func httpHostHeader(br *bufio.Reader) string {
	const maxPeek = 4 << 10
	peekSize := 0
	for {
		peekSize++
		if peekSize > maxPeek {
			b, _ := br.Peek(br.Buffered())
			return httpHostHeaderFromBytes(b)
		}
		b, err := br.Peek(peekSize)
		if n := br.Buffered(); n > peekSize {
			b, _ = br.Peek(n)
			peekSize = n
		}
		if len(b) > 0 {
			if b[0] < 'A' || b[0] > 'Z' {
				// Doesn't look like an HTTP verb
				// (GET, POST, etc).
				return ""
			}
			if bytes.Index(b, crlfcrlf) != -1 || bytes.Index(b, lflf) != -1 {
				req, err := http.ReadRequest(bufio.NewReader(bytes.NewReader(b)))
				if err != nil {
					return ""
				}
				if len(req.Header["Host"]) > 1 {
					// TODO(bradfitz): what does
					// ReadRequest do if there are
					// multiple Host headers?
					return ""
				}
				return req.Host
			}
		}
		if err != nil {
			return httpHostHeaderFromBytes(b)
		}
	}
}

var (
	lfHostColon = []byte("\nHost:")
	lfhostColon = []byte("\nhost:")
	crlf        = []byte("\r\n")
	lf          = []byte("\n")
	crlfcrlf    = []byte("\r\n\r\n")
	lflf        = []byte("\n\n")
)

func httpHostHeaderFromBytes(b []byte) string {
	if i := bytes.Index(b, lfHostColon); i != -1 {
		return string(bytes.TrimSpace(untilEOL(b[i+len(lfHostColon):])))
	}
	if i := bytes.Index(b, lfhostColon); i != -1 {
		return string(bytes.TrimSpace(untilEOL(b[i+len(lfhostColon):])))
	}
	return ""
}

// untilEOL returns v, truncated before the first '\n' byte, if any.
// The returned slice may include a '\r' at the end.
func untilEOL(v []byte) []byte {
	if i := bytes.IndexByte(v, '\n'); i != -1 {
		return v[:i]
	}
	return v
}
