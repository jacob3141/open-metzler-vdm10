package main

import (
	"encoding/binary"
	"errors"
	"log"
	"net"
	"strconv"
	"strings"
	"time"
)

// A small mDNS responder (RFC 6762, DNS-SD RFC 6763). It announces the hub as
// _doorbell-hub._tcp.local, so the Android app finds it in the Wi-Fi without typing an address.
// It only answers questions about its own names.

const (
	mdnsService = "_doorbell-hub._tcp.local."
	mdnsHost    = "doorbell-hub.local."
	mdnsTTL     = 120

	typeA   = 1
	typePTR = 12
	typeTXT = 16
	typeSRV = 33
	typeANY = 255

	classIN    = 1
	cacheFlush = 0x8000
	unicastQU  = 0x8000
)

var mdnsGroup = &net.UDPAddr{IP: net.IPv4(224, 0, 0, 251), Port: 5353}

type mdnsResponder struct {
	instance string // e.g. "Doorbell hub"
	port     uint16
	ips      func() []net.IP
}

func (m *mdnsResponder) instanceName() string { return m.instance + "." + mdnsService }

// listenPort extracts the port from an address like ":8088".
func listenPort(addr string) uint16 {
	_, p, err := net.SplitHostPort(addr)
	if err != nil {
		return 0
	}
	n, _ := strconv.Atoi(p)
	return uint16(n)
}

// ownIPv4 returns the IPv4 addresses under which the hub can be reached.
func ownIPv4() []net.IP {
	addrs, _ := net.InterfaceAddrs()
	var out []net.IP
	for _, a := range addrs {
		n, ok := a.(*net.IPNet)
		if !ok {
			continue
		}
		ip := n.IP.To4()
		if ip == nil || ip.IsLoopback() || ip.IsLinkLocalUnicast() {
			continue
		}
		out = append(out, ip)
	}
	return out
}

// Run answers questions until the socket fails. It announces the service at the start.
func (m *mdnsResponder) Run() error {
	c, err := net.ListenMulticastUDP("udp4", nil, mdnsGroup)
	if err != nil {
		return err
	}
	defer c.Close()
	for i := 0; i < 3; i++ {
		go func(d time.Duration) {
			time.Sleep(d)
			c.WriteToUDP(m.announcement(), mdnsGroup)
		}(time.Duration(1<<i) * time.Second)
	}
	buf := make([]byte, 9000)
	for {
		n, from, err := c.ReadFromUDP(buf)
		if err != nil {
			return err
		}
		resp, unicast := m.answer(buf[:n], from.Port != 5353)
		if resp == nil {
			continue
		}
		to := mdnsGroup
		if unicast {
			to = from
		}
		c.WriteToUDP(resp, to)
	}
}

type question struct {
	name  string
	qtype uint16
	qu    bool
}

// answer builds the response to a query, or nil when it is not about us. legacy is true for
// queries from a port other than 5353 (simple resolvers): they get a unicast answer with the
// question and its ID.
func (m *mdnsResponder) answer(p []byte, legacy bool) ([]byte, bool) {
	if len(p) < 12 || p[2]&0x80 != 0 { // too short or a response
		return nil, false
	}
	qd := int(binary.BigEndian.Uint16(p[4:]))
	off := 12
	var qs []question
	for i := 0; i < qd; i++ {
		name, next, err := readName(p, off)
		if err != nil || next+4 > len(p) {
			return nil, false
		}
		qt := binary.BigEndian.Uint16(p[next:])
		qc := binary.BigEndian.Uint16(p[next+2:])
		qs = append(qs, question{strings.ToLower(name), qt, qc&unicastQU != 0})
		off = next + 4
	}

	var ans, add [][]byte
	unicast := legacy
	have := map[string]bool{}
	addRR := func(list *[][]byte, rr []byte, key string) {
		if list == &add {
			key = "add:" + key
		}
		if !have[key] {
			have[key] = true
			*list = append(*list, rr)
		}
	}
	inst := strings.ToLower(m.instanceName())
	for _, q := range qs {
		match := func(t uint16) bool { return q.qtype == t || q.qtype == typeANY }
		switch q.name {
		case mdnsService:
			if !match(typePTR) {
				continue
			}
			addRR(&ans, m.ptr(), "ptr")
			addRR(&add, m.srv(), "srv")
			addRR(&add, m.txt(), "txt")
			for _, ip := range m.ips() {
				addRR(&add, m.a(ip), "a"+ip.String())
			}
		case "_services._dns-sd._udp.local.":
			if match(typePTR) {
				addRR(&ans, rr("_services._dns-sd._udp.local.", typePTR, classIN, mdnsTTL, name(mdnsService)), "svc")
			}
		case inst:
			if match(typeSRV) {
				addRR(&ans, m.srv(), "srv")
				for _, ip := range m.ips() {
					addRR(&add, m.a(ip), "a"+ip.String())
				}
			}
			if match(typeTXT) {
				addRR(&ans, m.txt(), "txt")
			}
		case mdnsHost:
			if match(typeA) {
				for _, ip := range m.ips() {
					addRR(&ans, m.a(ip), "a"+ip.String())
				}
			}
		default:
			continue
		}
		if q.qu {
			unicast = true
		}
	}
	if len(ans) == 0 {
		return nil, false
	}
	// Additional records that are already answers are not repeated.
	var extra [][]byte
	for _, r := range add {
		dup := false
		for _, a := range ans {
			if string(a) == string(r) {
				dup = true
			}
		}
		if !dup {
			extra = append(extra, r)
		}
	}
	id := uint16(0)
	var qsec []byte
	nq := 0
	if legacy {
		id = binary.BigEndian.Uint16(p)
		qsec = p[12:off]
		nq = qd
	}
	return message(id, nq, qsec, ans, extra), unicast
}

// announcement is the unsolicited response that tells everyone about the service.
func (m *mdnsResponder) announcement() []byte {
	ans := [][]byte{m.ptr(), m.srv(), m.txt()}
	for _, ip := range m.ips() {
		ans = append(ans, m.a(ip))
	}
	return message(0, 0, nil, ans, nil)
}

func (m *mdnsResponder) ptr() []byte {
	return rr(mdnsService, typePTR, classIN, mdnsTTL, name(m.instanceName()))
}

func (m *mdnsResponder) srv() []byte {
	d := make([]byte, 6)
	binary.BigEndian.PutUint16(d[4:], m.port) // priority 0, weight 0
	return rr(m.instanceName(), typeSRV, classIN|cacheFlush, mdnsTTL, append(d, name(mdnsHost)...))
}

func (m *mdnsResponder) txt() []byte {
	t := "path=/"
	return rr(m.instanceName(), typeTXT, classIN|cacheFlush, mdnsTTL, append([]byte{byte(len(t))}, t...))
}

func (m *mdnsResponder) a(ip net.IP) []byte {
	return rr(mdnsHost, typeA, classIN|cacheFlush, mdnsTTL, ip.To4())
}

func message(id uint16, nq int, qsec []byte, ans, add [][]byte) []byte {
	h := make([]byte, 12)
	binary.BigEndian.PutUint16(h[0:], id)
	binary.BigEndian.PutUint16(h[2:], 0x8400) // response, authoritative
	binary.BigEndian.PutUint16(h[4:], uint16(nq))
	binary.BigEndian.PutUint16(h[6:], uint16(len(ans)))
	binary.BigEndian.PutUint16(h[10:], uint16(len(add)))
	h = append(h, qsec...)
	for _, r := range ans {
		h = append(h, r...)
	}
	for _, r := range add {
		h = append(h, r...)
	}
	return h
}

func rr(owner string, typ, class uint16, ttl uint32, data []byte) []byte {
	b := name(owner)
	var f [10]byte
	binary.BigEndian.PutUint16(f[0:], typ)
	binary.BigEndian.PutUint16(f[2:], class)
	binary.BigEndian.PutUint32(f[4:], ttl)
	binary.BigEndian.PutUint16(f[8:], uint16(len(data)))
	return append(append(b, f[:]...), data...)
}

// name encodes a DNS name without compression. The instance part of a service instance name
// ("Doorbell hub" in "Doorbell hub._doorbell-hub._tcp.local.") is one label, even with dots in it.
func name(n string) []byte {
	var labels []string
	if inst, ok := strings.CutSuffix(n, "."+mdnsService); ok && !strings.HasPrefix(n, "_") {
		labels = append([]string{inst}, strings.Split(strings.TrimSuffix(mdnsService, "."), ".")...)
	} else {
		labels = strings.Split(strings.TrimSuffix(n, "."), ".")
	}
	var b []byte
	for _, l := range labels {
		if len(l) > 63 {
			l = l[:63]
		}
		b = append(b, byte(len(l)))
		b = append(b, l...)
	}
	return append(b, 0)
}

var errName = errors.New("bad DNS name")

// readName reads a possibly compressed name at off and returns it with a trailing dot, and the
// offset after it.
func readName(p []byte, off int) (string, int, error) {
	var sb strings.Builder
	next := -1
	for jumps := 0; jumps < 16; {
		if off >= len(p) {
			return "", 0, errName
		}
		l := int(p[off])
		switch {
		case l == 0:
			if next < 0 {
				next = off + 1
			}
			if sb.Len() == 0 {
				sb.WriteByte('.')
			}
			return sb.String(), next, nil
		case l&0xC0 == 0xC0:
			if off+1 >= len(p) {
				return "", 0, errName
			}
			if next < 0 {
				next = off + 2
			}
			off = int(binary.BigEndian.Uint16(p[off:]) & 0x3FFF)
			jumps++
		case l&0xC0 != 0:
			return "", 0, errName
		default:
			if off+1+l > len(p) {
				return "", 0, errName
			}
			sb.Write(p[off+1 : off+1+l])
			sb.WriteByte('.')
			off += 1 + l
		}
	}
	return "", 0, errName
}

// announceHub runs the responder in the background; failures are logged, the hub works without it.
func announceHub(instance, listen string) {
	port := listenPort(listen)
	if port == 0 {
		log.Printf("mdns: cannot tell the port from %q, not announcing", listen)
		return
	}
	m := &mdnsResponder{instance: instance, port: port, ips: ownIPv4}
	go func() {
		for {
			err := m.Run()
			log.Printf("mdns: %v", err)
			time.Sleep(30 * time.Second)
		}
	}()
}
