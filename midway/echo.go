// Copyright (c) 2022 RethinkDNS and its authors.
//
// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at http://mozilla.org/MPL/2.0/.
package midway

import (
	"bufio"
	"errors"
	"fmt"
	"log"
	"net"
	"sync"

	"github.com/celzero/gateway/midway/env"
	proxyproto "github.com/pires/go-proxyproto"
)

var (
	mtu         = env.Mtu()
	udproutines = env.TotalUdpServerRoutines()
)

func EchoUDP(c net.PacketConn, wg *sync.WaitGroup) {
	defer wg.Done()

	if c == nil {
		log.Println("Exiting udp")
		return
	}

	udpwg := &sync.WaitGroup{}
	udpwg.Add(udproutines)
	for i := 0; i < udproutines; i++ {
		// c => packet-conn, and so multiple goroutines can read without issue
		go processudp(c, udpwg)
	}
	udpwg.Wait()
}

func processudp(c net.PacketConn, uwg *sync.WaitGroup) {
	defer uwg.Done()

	packet := make([]byte, mtu)
	for {
		n, raddr, err := c.ReadFrom(packet)

		if err != nil {
			fmt.Println("err accepting udp packets")
			if errors.Is(err, net.ErrClosed) {
				log.Print(err)
				return
			}
			continue
		}

		log.Printf("umsg: %d - %s / by: %s\n", n, string(packet[:n]), raddr.String())
		// echo packet and raddr back
		c.WriteTo(packet[:n], raddr)
		c.WriteTo([]byte(raddr.String()), raddr)
	}
}

func EchoTCP(tcp net.Listener, wg *sync.WaitGroup) {
	defer wg.Done()

	if tcp == nil {
		log.Println("Exiting tcp")
		return
	}

	defer tcp.Close()

	for {
		if conn, err := tcp.Accept(); err == nil {
			go processtcp(conn)
		} else {
			fmt.Println("err accepting tcp conn")
			if errors.Is(err, net.ErrClosed) {
				log.Print(err)
				return
			}
		}
	}
}

func EchoPP(pp *proxyproto.Listener, wg *sync.WaitGroup) {
	defer wg.Done()

	if pp == nil {
		log.Println("Exiting pp")
		return
	}

	defer pp.Close()

	for {
		if conn, err := pp.Accept(); err == nil {
			go processtcp(conn)
		} else {
			fmt.Println("err accepting proxy-proto conn")
			if errors.Is(err, net.ErrClosed) {
				log.Print(err)
				return
			}
		}
	}
}

func processtcp(c net.Conn) {
	defer c.Close()

	line, _ := bufio.NewReader(c).ReadString('\n')
	log.Printf("tmsg: %d - %s / by: %s\n", len(line), string(line), c.RemoteAddr().String())
	// echo msg and rip back
	fmt.Fprint(c, line)
	fmt.Fprint(c, c.RemoteAddr())
}
