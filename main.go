package main

import (
	"bufio"
	"fmt"
	"log"
	"net"
	"sync"

	proxyproto "github.com/pires/go-proxyproto"
)

func main() {
	var wg sync.WaitGroup
	wg.Add(1)
	port := 5000
	// setting up tcp server
	tcp, err := net.Listen("tcp", fmt.Sprintf(":%d", port))
	if err != nil {
		fmt.Println("error in starting the tcp server")
	} else {
		fmt.Println("server started on port 5000")
	}

	// setting up udp server
	udp, err := net.ListenPacket("udp", fmt.Sprintf("fly-global-services:%d", port))
	if err != nil {
		log.Fatalf("can't listen on %d/udp: %s", port, err)
	}

	// setting up proxy protocol
	addr := "0.0.0.0:5001"
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		log.Fatalf("couldn't listen to %q: %q\n", addr, err.Error())
	}
	pp := &proxyproto.Listener{Listener: ln}

	go handleUDP(udp)
	go handleTCP(tcp)
	go handlePP(pp)
	
	wg.Wait()
}

func handleUDP(c net.PacketConn) {
	packet := make([]byte, 2000)

	for {
		n, addr, err := c.ReadFrom(packet)

		if err != nil {
			fmt.Println("error in accepting packets")
		}
		c.WriteTo(packet[:n], addr)
		c.WriteTo([]byte(addr.String()), addr)
	}
}

func handleTCP(tcp net.Listener) {
	for {
		conn, err := tcp.Accept()
		if err != nil {
			fmt.Println("error in accepting tcp package")
		} else {
			go process(conn)
		}
	}
}

func handlePP(pp *proxyproto.Listener) {
	for {
		conn, err := pp.Accept()
		if err != nil {
			fmt.Println("error in accepting proxy-proto tcp package")
		} else {
			go process(conn)
		}
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
