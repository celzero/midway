### A port 80 (HTTP with Host header) and port 443 (TLS with SNI) proxy

Demonstrates a reverse CDN: Forwards all TCP traffic to backends as indicated
in the HTTP Host header or in TLS SNI (server name identification) fields.

[A single IP to narrow the waist of the Internet](https://research.cloudflare.com/publications/Fayed2021/),
as it were.

### Demo
Point your local DNS resolver to return IP of wherever midway is deployed to,
for every query. Note that midway works on port 80 / 443 (that is, HTTP
traffic only, for all intents and purposes). Ideally, one'd set their browser's
DoH endpoint to a stub resolver that returns midway's IP for all DNS queries.

Try midway with curl, like so:

```bash
# TLS with SNI on port 443
curl https://www.example.com --resolve 'www.example.com:443:<midway-ip>' -v
* Added www.example.com:443:<midway-ip> to DNS cache
* Uses proxy env variable no_proxy == 'localhost,127.0.0.0/8,::1'
* Hostname www.example.com was found in DNS cache
#
# this next log line is a confirmation that the traffic was routd to midway:
#
*   Trying <midway-ip>:443...
* TCP_NODELAY set
* Connected to www.example.com (<midway-ip>) port 443 (#0)
...

# Host header with HTTP 1.x on port 80
curl abcxyz.neverssl.com --resolve 'abcxyz.neverssl.com:80:<midway-ip>' -v
```

### DNS
midway runs DoT and DoH stub resolver on ports 443 and 853 (or 8443 and 8853 in
non-previledge mode), forwarding queries to `UPSTREAM_DOH` env var (The Google
Public Resolver is the default). For TLS termination, Cert/Key pair can be ethier
supplied by setting env vars, `TLS_CERT_PATH` / `TLS_KEY_PATH` pointing to cert
/ key files, or by base64 encoding the contents of the file into env var,
`TLS_CERTKEY` like so:

```bash
TLS_CERTKEY = "KEY=b64(key-contents)\nCRT=b64(cert-contents)"
```

Test certs for DNS over TLS and DNS over HTTPS in `/test/certs/` are generated
via openssl ([ref](https://github.com/denji/golang-tls)).

### A note on HTTP/3 and QUIC
QUIC takes the stakes even higher with [Connection IDs](https://www.rfc-editor.org/rfc/rfc9000.html#connections)
facilitating routes between peers (a client source and a server destination),
but alas they are not meaningful to middleboxes (such as this code) to control
network flows. That is, after the 1-RTT QUIC handshake (with SNI in the clear),
the QUIC Connection IDs may change and are exchanged under encryption
(with no way to spoof it nor sniff it out). Speak nothing of the 0-RTT QUIC handshake.

Since QUIC relies on Connection IDs for routes, the UDP 4 tuple doesn't matter
in terms of flows, either. QUIC is perhaps SPDY, SCTP, MP-TCP rolled into one.

Wouldn't you know that [makes some people real giddy](https://apenwarr.ca/log/20170810).

### Deploy

Deploys to fly.io out-of-the-box on every commit. See [fly.toml](https://github.com/celzero/midway/blob/d554e82/fly.toml)
and the deploy [github action](https://github.com/celzero/midway/blob/d554e82/.github/workflows/fly.yml).

