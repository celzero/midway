package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"log"
	"net"
	"sync"
	"time"

	proxyproto "github.com/pires/go-proxyproto"
)

type Proxy struct {
	configs map[string]*config // ip:port => config

	lns        []net.Listener
	donec      chan struct{} // closed before err
	err        error         // any error from listening
	ListenFunc func(net, laddr string) (net.Listener, error)
}

type Matcher func(ctx context.Context, hostname string) bool

type Target interface {
	HandleConn(net.Conn)
}

type DialProxy struct {
	Addr                 string
	KeepAlivePeriod      time.Duration
	DialTimeout          time.Duration
	DialContext          func(ctx context.Context, network, address string) (net.Conn, error)
	OnDialError          func(src net.Conn, dstDialErr error)
	ProxyProtocolVersion int
}

type Conn struct {
	HostName string
	Peeked   []byte
	net.Conn
}

type fixedTarget struct {
	t Target
}

type route interface {
	match(*bufio.Reader) (Target, string)
}

type config struct {
	routes      []route
	acmeTargets []Target
	stopACME    bool
}

func (p *Proxy) addRoute(ipPort string, r route) {
	cfg := p.configFor(ipPort)
	cfg.routes = append(cfg.routes, r)
}

func (p *Proxy) configFor(ipPort string) *config {
	if p.configs == nil {
		p.configs = make(map[string]*config)
	}
	if p.configs[ipPort] == nil {
		p.configs[ipPort] = &config{}
	}
	return p.configs[ipPort]
}

func equals(want string) Matcher {
	return func(_ context.Context, got string) bool {
		return want == got
	}
}

func main() {
	var wg sync.WaitGroup
	wg.Add(5)
	port := 5000
	// setting up tcp server
	tcp, err := net.Listen("tcp", fmt.Sprintf(":%d", port))
	if err != nil {
		fmt.Println("error starting the tcp server")
	} else {
		fmt.Println("server started on port 5000")
	}

	//setting up pp tls server
	t443, err := net.Listen("tcp", ":443")
	if err != nil {
		log.Fatal(err)
	}
	pp443 := &proxyproto.Listener{Listener: t443}
        
	//setting up tcp tls server
	t80, err := net.Listen("tcp", ":80")
	if err != nil {
		log.Fatal(err)
	}
	pp80 := &proxyproto.Listener{Listener: t80}

	// setting up udp server
	udp, err := net.ListenPacket("udp", fmt.Sprintf("fly-global-services:%d", port))
	if err != nil {
		log.Printf("can't listen on %d/udp: %s", port, err)
	}

	// setting up proxy protocol
	addr := "0.0.0.0:5001"
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		log.Fatalf("couldn't listen to %q: %q\n", addr, err.Error())
	}
	pp := &proxyproto.Listener{Listener: ln} //converting tcp connection to proxy proto

	go handleUDP(udp, wg)
	go handleTCP(tcp, wg)
	go handlePP(pp, wg)
	go proxyPPHTTP(pp443, wg)
	go proxyPPHTTP(pp80, wg)

	wg.Wait()
}

//function start

func handleUDP(c net.PacketConn, wg sync.WaitGroup) {
	if c == nil {
		log.Print("Exiting udp")
		//wg.Done()
		return
	}

	packet := make([]byte, 2000)

	for {
		n, addr, err := c.ReadFrom(packet)

		if err != nil {
			fmt.Println("error in accepting udp packets")
		}
		c.WriteTo(packet[:n], addr)
		c.WriteTo([]byte(addr.String()), addr)
	}
}

func handleTCP(tcp net.Listener, wg sync.WaitGroup) {
	if tcp == nil {
		log.Print("Exiting tcp")
		//wg.Done()
		return
	}

	for {
		conn, err := tcp.Accept()
		if err != nil {
			fmt.Println("error in accepting tcp conn")
		} else {
			go process(conn)
		}
	}
}

func handlePP(pp *proxyproto.Listener, wg sync.WaitGroup) {
	if pp == nil {
		log.Print("Exiting pp")
		//wg.Done()
		return
	}

	for {
		conn, err := pp.Accept()
		if err != nil {
			fmt.Println("error in accepting proxy-proto conn")
		} else {
			go process(conn)
		}
	}
}

//function for getting the hostname
func proxyHTTPConn(c net.Conn) {
	br := bufio.NewReader(c)

	c1 := c
	c2 := c
	httpHostName := httpHostHeader(br)
	if n := br.Buffered(); n > 0 {
		peeked, _ := br.Peek(br.Buffered())
		c1 = &Conn{
			HostName: httpHostName,
			Peeked:   peeked,
			Conn:     c1,
		}
	}

	if len(httpHostName) > 0 {
		go forwardConn(c1)
	} else {
		sniServerName := clientHelloServerName(br)
		if n := br.Buffered(); n > 0 {
			peeked, _ := br.Peek(br.Buffered())
			c2 = &Conn{
				HostName: sniServerName,
				Peeked:   peeked,
				Conn:     c2,
			}
		}
		go forwardConn(c2)
	}

}

func proxyHTTP(tcptls net.Listener, wg sync.WaitGroup) {
	if tcptls == nil {
		log.Print("Exiting tcp tls")
		//wg.Done()
		return
	}

	for {
		conn, err := tcptls.Accept()
		if err != nil {
			log.Print("handle tcp tls err", err)
			continue
		}
		go proxyHTTPConn(conn)
	}
}

func proxyPPHTTP(tls *proxyproto.Listener, wg sync.WaitGroup) {
	if tls == nil {
		log.Print("Exiting pp tls")
		//wg.Done()
		return
	}

	for {
		conn, err := tls.Accept()
		if err != nil {
			log.Print("handle pp tls err", err)
			continue
		}
		go proxyHTTPConn(conn)
	}
}

func process(conn net.Conn) {
	message, _ := bufio.NewReader(conn).ReadString('\n')
	fmt.Print("Message Received:" + string(message))
	//send to socket
	//clientIp := strconv.Itoa(conn.LocalAddr())

	fmt.Printf("%T", conn.RemoteAddr())
	fmt.Fprint(conn, message)
	fmt.Fprint(conn, conn.RemoteAddr())
	//print client ip
	fmt.Print(conn.RemoteAddr())
}

//handling the connection
func forwardConn(src net.Conn) {
	c := src.(*Conn)
	_, port, err := net.SplitHostPort(src.LocalAddr().String())
	
	if err != nil {
		log.Print("invalid forward port")
		return
	}

	dst, err := net.DialTimeout("tcp", net.JoinHostPort((c.HostName), port), 5*time.Second)
	if err != nil {
		log.Print("dial timeout err", err)
		return
	}
	defer dst.Close()

	go proxyCopy(src, dst)
	go proxyCopy(dst, src)

}

func proxyCopy(dst, src net.Conn) {
	// Before we unwrap src and/or dst, copy any buffered data.
	if wc, ok := src.(*Conn); ok && len(wc.Peeked) > 0 {
		if _, err := dst.Write(wc.Peeked); err != nil {
			return
		}
		wc.Peeked = nil
	}

	// Unwrap the src and dst from *Conn to *net.TCPConn so Go
	// 1.11's splice optimization kicks in.
	src = UnderlyingConn(src)
	dst = UnderlyingConn(dst)
}

func UnderlyingConn(c net.Conn) net.Conn {
	if wrap, ok := c.(*Conn); ok {
		return wrap.Conn
	}
	return c
}

