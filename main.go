package main

import (
	"bufio"
	"bytes"
	"crypto/tls"
	"fmt"
	"io"
	"log"
	"net"
	"sync"
	"time"

	proxyproto "github.com/pires/go-proxyproto"
)

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

	//setting up tls server
	t, err := net.Listen("tcp", ":443")
	if err != nil {
		log.Fatal(err)
	}
	pptls := &proxyproto.Listener{Listener: t}

	tcptls, err := net.Listen("tcp", ":4443")
	if err != nil {
		log.Fatal(err)
	}

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
	go handlePPTLS(pptls, wg)
	go handleTCPTLS(tcptls, wg)

	wg.Wait()
}

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

func handleTCPTLS(tcptls net.Listener, wg sync.WaitGroup) {
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
		go handleTlsConnection(conn)
	}
}

func handlePPTLS(tls *proxyproto.Listener, wg sync.WaitGroup) {
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
		go handleTlsConnection(conn)
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

//peek function for tls

func peekClientHello(reader io.Reader) (*tls.ClientHelloInfo, io.Reader, error) {
	peekedBytes := new(bytes.Buffer)
	hello, err := readClientHello(io.TeeReader(reader, peekedBytes))
	if err != nil {
		return nil, nil, err
	}
	return hello, io.MultiReader(peekedBytes, reader), nil
}

type readOnlyConn struct {
	reader io.Reader
}

func (conn readOnlyConn) Read(p []byte) (int, error)         { return conn.reader.Read(p) }
func (conn readOnlyConn) Write(p []byte) (int, error)        { return 0, io.ErrClosedPipe }
func (conn readOnlyConn) Close() error                       { return nil }
func (conn readOnlyConn) LocalAddr() net.Addr                { return nil }
func (conn readOnlyConn) RemoteAddr() net.Addr               { return nil }
func (conn readOnlyConn) SetDeadline(t time.Time) error      { return nil }
func (conn readOnlyConn) SetReadDeadline(t time.Time) error  { return nil }
func (conn readOnlyConn) SetWriteDeadline(t time.Time) error { return nil }

func readClientHello(reader io.Reader) (*tls.ClientHelloInfo, error) {
	var hello *tls.ClientHelloInfo

	err := tls.Server(readOnlyConn{reader: reader}, &tls.Config{
		GetConfigForClient: func(argHello *tls.ClientHelloInfo) (*tls.Config, error) {
			hello = new(tls.ClientHelloInfo)
			*hello = *argHello
			return nil, nil
		},
	}).Handshake()

	if hello == nil {
		return nil, err
	}

	return hello, nil
}

//handling tls connection

func handleTlsConnection(clientConn net.Conn) {
	defer clientConn.Close()

	if err := clientConn.SetReadDeadline(time.Now().Add(5 * time.Second)); err != nil {
		log.Print(err)
		return
	}

	clientHello, clientReader, err := peekClientHello(clientConn)
	if err != nil || clientHello == nil || len(clientHello.ServerName) <= 0 {
		log.Printf("peek client hello err %v %v %d", err, clientHello, len(clientHello.ServerName))
		return
	}

	if err := clientConn.SetReadDeadline(time.Time{}); err != nil {
		log.Print("set rea ddeadline err", err)
		return
	}

	backendConn, err := net.DialTimeout("tcp", net.JoinHostPort(clientHello.ServerName, "443"), 5*time.Second)
	if err != nil {
		log.Print("dial timeout err", err)
		return
	}
	defer backendConn.Close()

	log.Print("proxy to >", clientHello.ServerName)
	var wg sync.WaitGroup
	wg.Add(2)

	go func() {
		io.Copy(clientConn, backendConn)
		clientConn.(*net.TCPConn).CloseWrite()
		wg.Done()
	}()
	go func() {
		io.Copy(backendConn, clientReader)
		backendConn.(*net.TCPConn).CloseWrite()
		wg.Done()
	}()

	wg.Wait()
}
