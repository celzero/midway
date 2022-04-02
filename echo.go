// Copyright (c) 2022 RethinkDNS and its authors.
//
// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at http://mozilla.org/MPL/2.0/.
package main

import (
	"bufio"
	"fmt"
	"log"
	"net"
	"sync"

	proxyproto "github.com/pires/go-proxyproto"
)

// mtu on fly is 1420
const mtu = 1600
// runtime.NumCPU() instead?
const udproutines = 4

func echoUDP(c net.PacketConn, wg *sync.WaitGroup) {
	if c == nil {
		log.Println("Exiting udp")
		wg.Done()
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
	packet := make([]byte, mtu)
	for {
		n, raddr, err := c.ReadFrom(packet)

		if err != nil {
			fmt.Println("exit, err accepting udp packets")
			uwg.Done()
			return
		}

		log.Println("umsg: " + string(packet[:n]) + " / by: " + raddr.String())
		// echo packet and raddr back
		c.WriteTo(packet[:n], raddr)
		c.WriteTo([]byte(raddr.String()), raddr)
	}
}

func echoTCP(tcp net.Listener, wg *sync.WaitGroup) {
	if tcp == nil {
		log.Println("Exiting tcp")
		wg.Done()
		return
	}

	for {
		conn, err := tcp.Accept()
		if err != nil {
			fmt.Println("err accepting tcp conn")
		} else {
			go processtcp(conn)
		}
	}
}

func echoPP(pp *proxyproto.Listener, wg *sync.WaitGroup) {
	if pp == nil {
		log.Println("Exiting pp")
		wg.Done()
		return
	}

	for {
		conn, err := pp.Accept()
		if err != nil {
			fmt.Println("err accepting proxy-proto conn")
		} else {
			go processtcp(conn)
		}
	}
}


func processtcp(c net.Conn) {
	defer c.Close()
	line, _ := bufio.NewReader(c).ReadString('\n')
	log.Println("tmsg: " + string(line) + " / by: " + c.RemoteAddr().String())
	// echo msg and rip back
	fmt.Fprint(c, line)
	fmt.Fprint(c, c.RemoteAddr())
}
