package main

import (
	"bufio"
	"fmt"
	"io"
	"log"
	"net"
	"sync"
	"time"

	proxyproto "github.com/pires/go-proxyproto"
)

type Conn struct {
	HostName string
	Peeked   []byte
	net.Conn
}

func main() {
	done := &sync.WaitGroup{}
	done.Add(5)

	t5000, err := net.Listen("tcp", ":5000")
	if err != nil {
		log.Fatal(err)
	} else {
		fmt.Println("started: tcp-server on port 5000")
	}

	t443, err := net.Listen("tcp", ":443")
	if err != nil {
		log.Fatal(err)
	} else {
		fmt.Println("started: tcp-server on port 443")
	}
	pp443 := &proxyproto.Listener{Listener: t443}

	//setting up tcp tls server
	t80, err := net.Listen("tcp", ":80")
	if err != nil {
		log.Fatal(err)
	} else {
		fmt.Println("started: tcp-server on port 80")
	}
	pp80 := &proxyproto.Listener{Listener: t80}

	// setting up udp server
	u5000, err := net.ListenPacket("udp", "fly-global-services:5000")
	if err != nil {
		log.Fatal(err)
	} else {
		fmt.Println("started: udp-server on port 5000")
	}

	t5001, err := net.Listen("tcp", "0.0.0.0:5001")
	if err != nil {
		log.Fatalf("err tcp-sever on port 5001 %q\n", err.Error())
	}
	pp5001 := &proxyproto.Listener{Listener: t5001} //converting tcp connection to proxy proto

	go handleUDP(u5000, done)
	go handleTCP(t5000, done)
	go handlePP(pp5001, done)
	go proxyPPHTTP(pp443, done)
	go proxyPPHTTP(pp80, done)

	done.Wait()
}

//function start

func handleUDP(c net.PacketConn, wg *sync.WaitGroup) {
	if c == nil {
		log.Print("Exiting udp")
		wg.Done()
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

func handleTCP(tcp net.Listener, wg *sync.WaitGroup) {
	if tcp == nil {
		log.Print("Exiting tcp")
		wg.Done()
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

func handlePP(pp *proxyproto.Listener, wg *sync.WaitGroup) {
	if pp == nil {
		log.Print("Exiting pp")
		wg.Done()
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

	httpHostName := httpHostHeader(br)
	sniServerName := clientHelloServerName(br)

	var upstream string
	if len(httpHostName) > 0 {
		upstream = httpHostName
	} else if len(sniServerName) > 0 {
		upstream = sniServerName
	} else {
		fmt.Println("host/sni missing %s %s", httpHostName, sniServerName)
		c.Close()
		return
	}

	if n := br.Buffered(); n > 0 {
		peeked, _ := br.Peek(n)
		wrappedconn := &Conn{
			HostName: upstream,
			Peeked:   peeked,
			Conn:     c,
		}
		go forwardConn(wrappedconn)
		return
	}

	// should never happen
	log.Println("buffer hasn't been peeked into...")
	c.Close()
}

func proxyHTTP(tcptls net.Listener, wg *sync.WaitGroup) {
	if tcptls == nil {
		log.Print("Exiting tcp tls")
		wg.Done()
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

func proxyPPHTTP(tls *proxyproto.Listener, wg *sync.WaitGroup) {
	if tls == nil {
		log.Print("Exiting pp tls")
		wg.Done()
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
	defer src.Close()
	c := src.(*Conn)
	_, port, err := net.SplitHostPort(src.LocalAddr().String())

	if err != nil {
		log.Println("invalid forward port")
		return
	}

	dst, err := net.DialTimeout("tcp", net.JoinHostPort((c.HostName), port), 5*time.Second)
	if err != nil {
		log.Printf("dial timeout err %v\n", err)
		return
	}
	defer dst.Close()

	wg := &sync.WaitGroup{}
	wg.Add(2)
	go proxyCopy(src, dst, wg)
	go proxyCopy(dst, src, wg)
	wg.Wait()

}

func proxyCopy(dst, src net.Conn, wg *sync.WaitGroup) {
	defer wg.Done()
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

	io.Copy(dst, src)
}

func UnderlyingConn(c net.Conn) net.Conn {
	if wrap, ok := c.(*Conn); ok {
		return wrap.Conn
	}
	return c
}

