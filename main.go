package main

import (
	"fmt"
	"net"
	"os"
	"os/signal"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"github.com/samber/lo"
	"golang.org/x/net/dns/dnsmessage"
)

var ipv4Addr = &net.UDPAddr{IP: net.IPv4(224, 0, 0, 251), Port: 5353}
var ipv6Addr = &net.UDPAddr{IP: net.ParseIP("ff02::fb"), Port: 5353}

func main() {
	log.Logger = log.Output(zerolog.ConsoleWriter{Out: os.Stdout})

	server := NewMDNSServer([]string{"hello.local."})
	if err := server.Start(); err != nil {
		panic(err)
	}
	defer server.Stop()

	chSignal := make(chan os.Signal, 1)
	signal.Notify(chSignal, os.Interrupt)
	<-chSignal
}

type MDNSServer struct {
	shutdown *atomic.Bool
	mu       *sync.RWMutex
	wg       *sync.WaitGroup
	domains  map[string]struct{}
}

func NewMDNSServer(domains []string) *MDNSServer {
	return &MDNSServer{
		shutdown: &atomic.Bool{},
		mu:       &sync.RWMutex{},
		wg:       &sync.WaitGroup{},
		domains:  lo.Keyify(domains),
	}
}

func (s *MDNSServer) Start() error {
	ifaces, err := net.Interfaces()
	if err != nil {
		return fmt.Errorf("failed to get network interfaces: %w", err)
	}

	for _, iface := range ifaces {
		if strings.HasPrefix(iface.Name, "br") || strings.HasPrefix(iface.Name, "veth") {
			continue
		}
		s.wg.Go(func() { s.listen(&iface, ipv6Addr) })
		s.wg.Go(func() { s.listen(&iface, ipv4Addr) })
	}

	return nil
}

func (s *MDNSServer) listen(iface *net.Interface, addr *net.UDPAddr) {
	var ipNets []*net.IPNet
	if addrs, err := iface.Addrs(); err == nil {
		for _, addr := range addrs {
			if ipnet, ok := addr.(*net.IPNet); ok {
				ipNets = append(ipNets, ipnet)
			}
		}
	}

	conn, err := net.ListenMulticastUDP("udp", iface, addr)
	if err != nil {
		panic(err)
	}
	defer conn.Close()

	log.Info().Msgf("Started mDNS server on %s (%s)", iface.Name, addr)

	ipv4Addrs := lo.FilterMap(ipNets, func(ipNet *net.IPNet, _ int) (net.IP, bool) {
		ip := ipNet.IP.To4()
		return ip, ip != nil
	})
	ipv6Addrs := lo.FilterMap(ipNets, func(ipNet *net.IPNet, _ int) (net.IP, bool) {
		ip := ipNet.IP.To16()
		return ip, ip != nil && ip.To4() == nil
	})

	buf := make([]byte, iface.MTU)
	for !s.shutdown.Load() {
		if err := conn.SetDeadline(time.Now().Add(time.Second)); err != nil {
			panic(err)
		}
		n, src, err := conn.ReadFromUDP(buf)
		if err != nil {
			if os.IsTimeout(err) {
				continue
			}
			panic(err)
		}

		// Ignore packets not destined to one of our IPs
		if !lo.ContainsBy(ipNets, func(ipNet *net.IPNet) bool { return ipNet.Contains(src.IP) }) {
			continue
		}

		var msg dnsmessage.Message
		if err := msg.Unpack(buf[:n]); err != nil {
			panic(err)
		}

		s.wg.Go(func() {
			if replied := s.reply(conn, addr, &msg, ipv4Addrs, ipv6Addrs); replied {
				queries := lo.Map(msg.Questions, func(q dnsmessage.Question, _ int) string {
					return q.Name.String()
				})
				log.Info().Msgf("Replied to %s for %s", src, strings.Join(queries, ", "))
			}
		})
	}
}

func (s *MDNSServer) reply(
	conn *net.UDPConn,
	addr *net.UDPAddr,
	msg *dnsmessage.Message,
	ipv4Addrs, ipv6Addrs []net.IP,
) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var answers []dnsmessage.Resource
	for _, q := range msg.Questions {
		if !lo.HasKey(s.domains, q.Name.String()) {
			continue
		}
		switch q.Type {
		case dnsmessage.TypeA:
			for _, ip := range ipv4Addrs {
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
		case dnsmessage.TypeAAAA:
			for _, ip := range ipv6Addrs {
				answers = append(answers, dnsmessage.Resource{
					Header: dnsmessage.ResourceHeader{
						Name:  q.Name,
						Type:  dnsmessage.TypeAAAA,
						Class: dnsmessage.ClassINET,
						TTL:   120,
					},
					Body: &dnsmessage.AAAAResource{AAAA: [16]byte(ip)},
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

		return true
	}

	return false
}

func (s *MDNSServer) AddDomain(domain string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.domains[domain] = struct{}{}
}

func (s *MDNSServer) RemoveDomain(domain string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.domains, domain)
}

func (s *MDNSServer) Stop() {
	s.shutdown.Store(true)
	s.wg.Wait()
}
