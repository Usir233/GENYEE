// Package dnstest provides DNS answers for tests that validate URLs without
// contacting the remote service. It does not bypass production SSRF checks.
package dnstest

import (
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"strings"
	"sync"
	"time"

	"golang.org/x/net/dns/dnsmessage"
)

// Install replaces the process resolver for the explicitly listed hostnames.
// Call it from TestMain, before parallel tests start, and call restore after
// m.Run. Unlisted hostnames retain the original resolver's behavior. An empty
// address list represents a hostname that does not exist.
func Install(records map[string][]string) (restore func(), err error) {
	answers := make(map[string][]net.IP, len(records))
	for hostname, addresses := range records {
		hostname = strings.ToLower(strings.TrimSuffix(hostname, "."))
		answers[hostname] = nil
		for _, address := range addresses {
			ip := net.ParseIP(address)
			if ip == nil {
				return nil, fmt.Errorf("invalid DNS fixture address %q for %s", address, hostname)
			}
			answers[hostname] = append(answers[hostname], ip)
		}
	}
	udp, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	tcp, err := net.Listen("tcp", udp.LocalAddr().String())
	if err != nil {
		_ = udp.Close()
		return nil, err
	}
	original := net.DefaultResolver
	ctx, cancel := context.WithCancel(context.Background())
	respond := func(query []byte) []byte {
		var request dnsmessage.Message
		if err := request.Unpack(query); err != nil || len(request.Questions) != 1 {
			return nil
		}
		question := request.Questions[0]
		hostname := strings.ToLower(strings.TrimSuffix(question.Name.String(), "."))
		response := dnsmessage.Message{
			Header: dnsmessage.Header{
				ID:                 request.ID,
				Response:           true,
				RecursionDesired:   request.RecursionDesired,
				RecursionAvailable: true,
			},
			Questions: request.Questions,
		}
		addresses, known := answers[hostname]
		// A local search domain must not turn a fixture's NXDOMAIN response
		// into a successful lookup through the host's original resolver.
		if !known {
			for name := range answers {
				if strings.HasPrefix(hostname, name+".") {
					known = true
					break
				}
			}
		}
		if !known && (question.Type == dnsmessage.TypeA || question.Type == dnsmessage.TypeAAAA) {
			network := "ip4"
			if question.Type == dnsmessage.TypeAAAA {
				network = "ip6"
			}
			lookupCtx, lookupCancel := context.WithTimeout(ctx, 5*time.Second)
			var lookupErr error
			addresses, lookupErr = original.LookupIP(lookupCtx, network, hostname)
			lookupCancel()
			if lookupErr != nil {
				if dnsErr, ok := lookupErr.(*net.DNSError); ok && dnsErr.IsNotFound {
					response.RCode = dnsmessage.RCodeNameError
				} else {
					response.RCode = dnsmessage.RCodeServerFailure
				}
			}
		} else if known && len(addresses) == 0 {
			response.RCode = dnsmessage.RCodeNameError
		}
		for _, ip := range addresses {
			header := dnsmessage.ResourceHeader{
				Name:  question.Name,
				Type:  question.Type,
				Class: dnsmessage.ClassINET,
				TTL:   60,
			}
			if ipv4 := ip.To4(); question.Type == dnsmessage.TypeA && ipv4 != nil {
				var address [4]byte
				copy(address[:], ipv4)
				response.Answers = append(response.Answers, dnsmessage.Resource{
					Header: header,
					Body:   &dnsmessage.AResource{A: address},
				})
			} else if question.Type == dnsmessage.TypeAAAA && ip.To4() == nil {
				var address [16]byte
				copy(address[:], ip.To16())
				response.Answers = append(response.Answers, dnsmessage.Resource{
					Header: header,
					Body:   &dnsmessage.AAAAResource{AAAA: address},
				})
			}
		}
		wire, _ := response.Pack()
		return wire
	}
	var workers sync.WaitGroup
	workers.Add(2)
	go func() {
		defer workers.Done()
		buffer := make([]byte, 65535)
		for {
			n, peer, err := udp.ReadFrom(buffer)
			if err != nil {
				return
			}
			query := append([]byte(nil), buffer[:n]...)
			workers.Add(1)
			go func() {
				defer workers.Done()
				if response := respond(query); response != nil {
					_, _ = udp.WriteTo(response, peer)
				}
			}()
		}
	}()
	go func() {
		defer workers.Done()
		for {
			conn, err := tcp.Accept()
			if err != nil {
				return
			}
			workers.Add(1)
			go func() {
				defer workers.Done()
				defer func() { _ = conn.Close() }()
				_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
				var size uint16
				if binary.Read(conn, binary.BigEndian, &size) != nil {
					return
				}
				query := make([]byte, size)
				if _, err := io.ReadFull(conn, query); err != nil {
					return
				}
				if response := respond(query); response != nil {
					_ = binary.Write(conn, binary.BigEndian, uint16(len(response)))
					_, _ = conn.Write(response)
				}
			}()
		}
	}()
	net.DefaultResolver = &net.Resolver{
		PreferGo: true,
		Dial: func(ctx context.Context, network, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, network, udp.LocalAddr().String())
		},
	}
	var once sync.Once
	return func() {
		once.Do(func() {
			net.DefaultResolver = original
			cancel()
			_ = udp.Close()
			_ = tcp.Close()
			workers.Wait()
		})
	}, nil
}
