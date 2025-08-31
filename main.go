package main

import (
	"log"
	"net"
	"strings"
	"sync"

	"golang.org/x/net/dns/dnsmessage"
)

func main() {
	ipv4Addr := &net.UDPAddr{IP: net.IPv4(224, 0, 0, 251), Port: 5353}

	ifaces, err := net.Interfaces()
	if err != nil {
		log.Fatalln(err)
	}

	wg := &sync.WaitGroup{}
	for _, iface := range ifaces {
		log.Println(iface.Flags.String())

		var ips []net.IP
		if addrs, err := iface.Addrs(); err == nil {
			for _, addr := range addrs {
				log.Printf("Interface %s has address %s\n", iface.Name, addr)
				if ipnet, ok := addr.(*net.IPNet); ok {
					log.Printf("  IP: %s\n", ipnet.IP)
					if ip := ipnet.IP.To4(); ip != nil {
						ips = append(ips, ip)
					}
				}
			}
		}

		conn, err := net.ListenMulticastUDP("udp4", &iface, ipv4Addr)
		if err != nil {
			panic(err)
		}

		wg.Go(func() { loop(conn, ips) })
	}

	wg.Wait()
}

func loop(conn *net.UDPConn, ips []net.IP) {
	buf := make([]byte, 512)
	for {
		n, addr, err := conn.ReadFromUDP(buf)
		if err != nil {
			panic(err)
		}
		var msg dnsmessage.Message
		if err := msg.Unpack(buf[:n]); err != nil {
			panic(err)
		}

		var answers []dnsmessage.Resource

		for _, q := range msg.Questions {
			switch q.Type {
			case dnsmessage.TypeA:
				if !strings.HasSuffix(q.Name.String(), "local.") {
					continue
				}
				log.Printf("A record query for %s from %s\n", q.Name, addr)
				for _, ip := range ips {
					answers = append(answers, dnsmessage.Resource{
						Header: dnsmessage.ResourceHeader{
							Name:  q.Name,
							Type:  dnsmessage.TypeA,
							Class: dnsmessage.ClassINET,
							TTL:   120,
						},
						Body: &dnsmessage.AResource{A: [4]byte(ip)},
					})
				}
			}
		}

		if len(answers) > 0 {
			msg.Header.Response = true
			msg.Header.Authoritative = true
			msg.Answers = answers

			buf, err := msg.Pack()
			if err != nil {
				panic(err)
			}
			if _, err := conn.WriteToUDP(buf, addr); err != nil {
				panic(err)
			}
		}
	}
}
