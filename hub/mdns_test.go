package main

import (
	"encoding/binary"
	"net"
	"testing"
)

type record struct {
	name  string
	typ   uint16
	class uint16
	data  []byte
	off   int // offset of data in the message, for names inside it
}

// parse reads the sections of a DNS message (enough for the tests).
func parse(t *testing.T, p []byte) (id uint16, questions []string, answers, additional []record) {
	t.Helper()
	id = binary.BigEndian.Uint16(p)
	qd, an, ar := int(binary.BigEndian.Uint16(p[4:])), int(binary.BigEndian.Uint16(p[6:])), int(binary.BigEndian.Uint16(p[10:]))
	off := 12
	for i := 0; i < qd; i++ {
		n, next, err := readName(p, off)
		if err != nil {
			t.Fatal(err)
		}
		questions = append(questions, n)
		off = next + 4
	}
	read := func(count int) []record {
		var out []record
		for i := 0; i < count; i++ {
			n, next, err := readName(p, off)
			if err != nil {
				t.Fatal(err)
			}
			typ := binary.BigEndian.Uint16(p[next:])
			class := binary.BigEndian.Uint16(p[next+2:])
			l := int(binary.BigEndian.Uint16(p[next+8:]))
			out = append(out, record{n, typ, class, p[next+10 : next+10+l], next + 10})
			off = next + 10 + l
		}
		return out
	}
	answers = read(an)
	read(int(binary.BigEndian.Uint16(p[8:])))
	additional = read(ar)
	return
}

func query(id uint16, qtype uint16, names ...string) []byte {
	h := make([]byte, 12)
	binary.BigEndian.PutUint16(h, id)
	binary.BigEndian.PutUint16(h[4:], uint16(len(names)))
	for _, n := range names {
		h = append(h, name(n)...)
		h = binary.BigEndian.AppendUint16(h, qtype)
		h = binary.BigEndian.AppendUint16(h, classIN)
	}
	return h
}

func testResponder() *mdnsResponder {
	return &mdnsResponder{instance: "Doorbell hub", port: 8088, ips: func() []net.IP { return []net.IP{net.IPv4(10, 0, 3, 10)} }}
}

func TestMDNSAnswersServiceQuery(t *testing.T) {
	m := testResponder()
	resp, unicast := m.answer(query(0, typePTR, mdnsService), false)
	if resp == nil || unicast {
		t.Fatal("no multicast answer", unicast)
	}
	id, qs, ans, add := parse(t, resp)
	if id != 0 || len(qs) != 0 || len(ans) != 1 || ans[0].typ != typePTR {
		t.Fatal(id, qs, ans)
	}
	target, _, _ := readName(resp, ans[0].off)
	if target != "Doorbell hub._doorbell-hub._tcp.local." {
		t.Fatal(target)
	}
	var port uint16
	var ip net.IP
	for _, r := range add {
		switch r.typ {
		case typeSRV:
			port = binary.BigEndian.Uint16(r.data[4:])
			host, _, _ := readName(resp, r.off+6)
			if host != mdnsHost || r.class != classIN|cacheFlush {
				t.Fatal(host, r.class)
			}
		case typeA:
			ip = net.IP(r.data)
		}
	}
	if port != 8088 || !ip.Equal(net.IPv4(10, 0, 3, 10)) {
		t.Fatal(port, ip)
	}
}

func TestMDNSLegacyAndOtherQueries(t *testing.T) {
	m := testResponder()
	// A resolver that does not use port 5353 gets a unicast answer with its ID and question.
	resp, unicast := m.answer(query(0x1234, typeA, "doorbell-hub.local."), true)
	id, qs, ans, _ := parse(t, resp)
	if !unicast || id != 0x1234 || len(qs) != 1 || len(ans) != 1 || ans[0].typ != typeA {
		t.Fatal(unicast, id, qs, ans)
	}
	// SRV of the instance, asked in any letter case.
	resp, _ = m.answer(query(0, typeSRV, "doorbell HUB._doorbell-hub._tcp.local."), false)
	if _, _, ans, _ := parse(t, resp); len(ans) != 1 || ans[0].typ != typeSRV {
		t.Fatal(ans)
	}
	// Questions about other names stay unanswered.
	if resp, _ := m.answer(query(0, typePTR, "_printer._tcp.local."), false); resp != nil {
		t.Fatal("answered a foreign question")
	}
	// Responses and garbage are ignored.
	r := query(0, typePTR, mdnsService)
	r[2] |= 0x80
	if resp, _ := m.answer(r, false); resp != nil {
		t.Fatal("answered a response")
	}
	if resp, _ := m.answer([]byte{1, 2, 3}, false); resp != nil {
		t.Fatal("answered garbage")
	}
}

func TestMDNSCompressedQuestion(t *testing.T) {
	m := testResponder()
	// Two questions, the second points into the first: "_doorbell-hub._tcp.local." and
	// "doorbell-hub" + pointer to "local." (offset of "local" label in the first name).
	q := query(0, typePTR, mdnsService)
	binary.BigEndian.PutUint16(q[4:], 2)
	localOff := 12 + 1 + len("_doorbell-hub") + 1 + len("_tcp")
	q = append(q, byte(len("doorbell-hub")))
	q = append(q, "doorbell-hub"...)
	q = binary.BigEndian.AppendUint16(q, 0xC000|uint16(localOff))
	q = binary.BigEndian.AppendUint16(q, typeA)
	q = binary.BigEndian.AppendUint16(q, classIN)
	resp, _ := m.answer(q, false)
	_, _, ans, _ := parse(t, resp)
	if len(ans) != 2 {
		t.Fatal(ans)
	}
}

func TestMDNSAnnouncement(t *testing.T) {
	_, _, ans, _ := parse(t, testResponder().announcement())
	if len(ans) != 4 {
		t.Fatal(ans)
	}
	if listenPort(":8088") != 8088 || listenPort("bad") != 0 {
		t.Fatal("listenPort")
	}
}
