package main

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"doorbell-hub/ws"
)

const key = "\x00\x00\x00\x01\x67\x4d\x00\x28\xaa\x00\x00\x00\x01\x68\xee\x00\x00\x00\x01\x65\x11\x22"
const delta = "\x00\x00\x00\x01\x41\x33\x44"

func newHub(t *testing.T, doorbell string) *Hub {
	t.Helper()
	s, err := OpenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return NewHub(Config{Doorbell: doorbell, AdminPassword: "pw", RingTimeout: 150 * time.Millisecond, RecordFor: 200 * time.Millisecond, MaxMessages: 2}, s)
}

// pcmFrame is 20 ms of audio as the doorbell sends it.
var pcmFrame = bytes.Repeat([]byte{0x10, 0x20}, audioChunkFrames)

// fakeDoorbell serves frames on /video and /audio and accepts /.
func fakeDoorbell(t *testing.T) string {
	t.Helper()
	return fakeDoorbellRPC(t, nil)
}

// fakeDoorbellRPC is fakeDoorbell with rpc handling the JSON-RPC connection on /.
func fakeDoorbellRPC(t *testing.T, rpc func(*ws.Conn)) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := ws.Upgrade(w, r, 4096)
		if err != nil {
			return
		}
		defer c.Close()
		if r.URL.Path == "/audio" {
			for c.Write(ws.Binary, pcmFrame) == nil {
				time.Sleep(10 * time.Millisecond)
			}
			return
		}
		if r.URL.Path == "/" && rpc != nil {
			rpc(c)
			return
		}
		if r.URL.Path != "/video" {
			c.Read()
			return
		}
		for i := 0; ; i++ {
			f := delta
			if i%5 == 0 {
				f = key
			}
			if c.Write(ws.Binary, []byte(f)) != nil {
				return
			}
			time.Sleep(10 * time.Millisecond)
		}
	}))
	t.Cleanup(srv.Close)
	u, _ := url.Parse(srv.URL)
	return u.Host
}

func serve(t *testing.T, h *Hub) string {
	t.Helper()
	srv := httptest.NewServer(h.Routes())
	t.Cleanup(srv.Close)
	u, _ := url.Parse(srv.URL)
	return u.Host
}

func connect(t *testing.T, addr, token string) *ws.Conn {
	t.Helper()
	c, err := ws.Dial(addr, "/api/events", "Authorization: Bearer "+token+"\r\n", 1<<20, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	return c
}

type msg struct {
	Method string `json:"method"`
	Params struct {
		ID string `json:"id"`
		By string `json:"by"`
	} `json:"params"`
	Result json.RawMessage `json:"result"`
	Error  json.RawMessage `json:"error"`
}

func read(t *testing.T, c *ws.Conn) msg {
	t.Helper()
	type res struct {
		p   []byte
		err error
	}
	ch := make(chan res, 1)
	go func() {
		_, p, err := c.Read()
		ch <- res{p, err}
	}()
	select {
	case r := <-ch:
		if r.err != nil {
			t.Fatal(r.err)
		}
		var m msg
		json.Unmarshal(r.p, &m)
		return m
	case <-time.After(3 * time.Second):
		t.Fatal("no message")
	}
	return msg{}
}

func waitSessions(t *testing.T, h *Hub, n int) {
	t.Helper()
	for i := 0; i < 200; i++ {
		h.mu.Lock()
		l := len(h.sessions)
		h.mu.Unlock()
		if l == n {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("expected %d sessions", n)
}

func waitMessages(t *testing.T, h *Hub, n int) []Message {
	t.Helper()
	for i := 0; i < 300; i++ {
		if m := h.store.Messages(); len(m) == n {
			return m
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("expected %d messages, got %d", n, len(h.store.Messages()))
	return nil
}

func TestAcceptNotifiesOthersAndDoesNotRecord(t *testing.T) {
	h := newHub(t, fakeDoorbell(t))
	addr := serve(t, h)
	_, t1, _ := h.store.AddClient("one")
	_, t2, _ := h.store.AddClient("two")
	a, b := connect(t, addr, t1), connect(t, addr, t2)
	waitSessions(t, h, 2)

	h.Press()
	ra, rb := read(t, a), read(t, b)
	if ra.Method != "ring" || rb.Params.ID != ra.Params.ID {
		t.Fatalf("%+v %+v", ra, rb)
	}
	h.Press() // a second press during the ring must not start another
	a.Write(ws.Text, []byte(`{"jsonrpc":"2.0","id":1,"method":"ring.accept","params":{"id":"`+ra.Params.ID+`"}}`))
	if r := read(t, a); r.Error != nil || !strings.Contains(string(r.Result), "/api/video") {
		t.Fatalf("%+v", r)
	}
	if r := read(t, b); r.Method != "ring.taken" || r.Params.By != "one" {
		t.Fatalf("%+v", r)
	}
	time.Sleep(300 * time.Millisecond)
	if len(h.store.Messages()) != 0 {
		t.Fatal("answered ring was recorded")
	}
}

func TestUnansweredRingRecordsMessage(t *testing.T) {
	h := newHub(t, fakeDoorbell(t))
	addr := serve(t, h)
	_, tok, _ := h.store.AddClient("one")
	a := connect(t, addr, tok)
	waitSessions(t, h, 1)
	h.Press()
	if r := read(t, a); r.Method != "ring" {
		t.Fatalf("%+v", r)
	}
	if r := read(t, a); r.Method != "ring.missed" {
		t.Fatalf("%+v", r)
	}
	if r := read(t, a); r.Method != "message.new" {
		t.Fatalf("%+v", r)
	}
	m := waitMessages(t, h, 1)
	if m[0].Size == 0 {
		t.Fatal("empty file")
	}
	d, err := os.ReadFile(h.store.messagePath(m[0].ID))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(d, []byte("sowt")) || !bytes.Contains(d, pcmFrame) {
		t.Fatal("recording has no audio")
	}
}

func TestMP4WithAudio(t *testing.T) {
	// 2.5 frames of audio: the half frame is dropped, chunks stay whole
	audio := bytes.Repeat(pcmFrame, 3)[:len(pcmFrame)*5/2]
	d, err := WriteMP4([]Frame{{Data: []byte(key), Key: true}, {Data: []byte(delta)}}, audio, 0.1)
	if err != nil {
		t.Fatal(err)
	}
	names, bodies := boxes(t, d)
	if strings.Join(names, ",") != "ftyp,moov,mdat" {
		t.Fatal(names)
	}
	moov, mdat := bodies[1], bodies[2]
	for _, b := range []string{"avc1", "soun", "sowt", "smhd"} {
		if !bytes.Contains(moov, []byte(b)) {
			t.Fatal(b)
		}
	}
	if !bytes.HasSuffix(mdat, bytes.Repeat(pcmFrame, 2)) || bytes.HasSuffix(mdat, bytes.Repeat(pcmFrame, 3)) {
		t.Fatal("audio data must hold two whole frames")
	}
	// the chunk offsets of the audio track point at the audio in the file
	i := bytes.LastIndex(moov, []byte("stco"))
	n := binary.BigEndian.Uint32(moov[i+8:])
	if n != 2 {
		t.Fatalf("%d audio chunks", n)
	}
	first := binary.BigEndian.Uint32(moov[i+12:])
	if !bytes.Equal(d[first:first+uint32(len(pcmFrame))], pcmFrame) {
		t.Fatal("audio chunk offset is wrong")
	}
	if second := binary.BigEndian.Uint32(moov[i+16:]); second != first+uint32(len(pcmFrame)) {
		t.Fatal("second chunk offset is wrong")
	}
}

func TestAPIAudioStreamsPCM(t *testing.T) {
	h := newHub(t, fakeDoorbell(t))
	addr := serve(t, h)
	_, tok, _ := h.store.AddClient("one")
	c, err := ws.Dial(addr, "/api/audio", "Authorization: Bearer "+tok+"\r\n", 1<<20, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	op, p, err := c.Read()
	if err != nil || op != ws.Binary || !bytes.Equal(p, pcmFrame) {
		t.Fatalf("op=%d len=%d err=%v", op, len(p), err)
	}
}

func TestRecordsWhenAllRejectOrNobodyConnected(t *testing.T) {
	h := newHub(t, fakeDoorbell(t))
	h.cfg.RingTimeout = time.Hour
	addr := serve(t, h)
	_, tok, _ := h.store.AddClient("one")
	a := connect(t, addr, tok)
	waitSessions(t, h, 1)
	h.Press()
	r := read(t, a)
	a.Write(ws.Text, []byte(`{"jsonrpc":"2.0","id":1,"method":"ring.reject","params":{"id":"`+r.Params.ID+`"}}`))
	waitMessages(t, h, 1)
	a.Close()
	waitSessions(t, h, 0)
	time.Sleep(1100 * time.Millisecond) // IDs have a resolution of one second
	h.Press()
	waitMessages(t, h, 2)
}

func TestDisconnectCountsAsReject(t *testing.T) {
	h := newHub(t, fakeDoorbell(t))
	h.cfg.RingTimeout = time.Hour
	addr := serve(t, h)
	_, tok, _ := h.store.AddClient("one")
	a := connect(t, addr, tok)
	waitSessions(t, h, 1)
	h.Press()
	read(t, a)
	a.Close()
	waitMessages(t, h, 1)
}

func TestVideoOnlyConnectedOnDemand(t *testing.T) {
	h := newHub(t, fakeDoorbell(t))
	if h.feed.cancel != nil {
		t.Fatal("feed runs without subscribers")
	}
	s := h.feed.Subscribe()
	select {
	case f := <-s.C:
		if !f.Key {
			t.Fatal("first frame is not a keyframe")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no frame")
	}
	h.feed.Unsubscribe(s)
	if h.feed.cancel != nil {
		t.Fatal("feed keeps running without subscribers")
	}
}

func TestAPIRequiresToken(t *testing.T) {
	h := newHub(t, fakeDoorbell(t))
	addr := serve(t, h)
	if _, err := ws.Dial(addr, "/api/events", "Authorization: Bearer falsch\r\n", 1024, time.Second); err == nil {
		t.Fatal("wrong token accepted")
	}
	resp, err := http.Get("http://" + addr + "/api/messages")
	if err != nil || resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("%v %v", resp.StatusCode, err)
	}
}

func TestAdminAuthAndCSRF(t *testing.T) {
	h := newHub(t, fakeDoorbell(t))
	addr := serve(t, h)
	resp, _ := http.Get("http://" + addr + "/")
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatal(resp.StatusCode)
	}
	post := func(origin string) int {
		req, _ := http.NewRequest("POST", "http://"+addr+"/admin/clients", strings.NewReader("name=Pixel"))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.Header.Set("Origin", origin)
		req.SetBasicAuth("admin", "pw")
		r, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		return r.StatusCode
	}
	if c := post("http://evil.example"); c != http.StatusForbidden {
		t.Fatal(c)
	}
	if len(h.store.Clients()) != 0 {
		t.Fatal("foreign origin created a client")
	}
	if c := post("http://" + addr); c != http.StatusOK {
		t.Fatal(c)
	}
	if len(h.store.Clients()) != 1 {
		t.Fatal("client not created")
	}
}

func TestTokenStoredOnlyHashed(t *testing.T) {
	s, _ := OpenStore(t.TempDir())
	c, tok, _ := s.AddClient("x")
	if c.TokenHash == tok || strings.Contains(c.TokenHash, tok) {
		t.Fatal("token stored in plain text")
	}
	if _, ok := s.Authenticate(tok); !ok {
		t.Fatal("token rejected")
	}
}

func TestPruneKeepsNewest(t *testing.T) {
	s, _ := OpenStore(t.TempDir())
	base := time.Date(2026, 1, 1, 10, 0, 0, 0, time.UTC)
	for i := 0; i < 4; i++ {
		s.SaveMessage([]byte("x"), base.Add(time.Duration(i)*time.Second))
	}
	s.Prune(2)
	m := s.Messages()
	if len(m) != 2 || m[0].ID != "20260101-100003" {
		t.Fatalf("%+v", m)
	}
}

func TestMessageIDRejectsTraversal(t *testing.T) {
	for _, id := range []string{"", "../x", "a/b", "..", "20260101-100003.mp4"} {
		if validMessageID(id) {
			t.Fatal(id)
		}
	}
}

func TestMP4Structure(t *testing.T) {
	d, err := WriteMP4([]Frame{{Data: []byte(key), Key: true}, {Data: []byte(delta)}}, nil, 0.1)
	if err != nil {
		t.Fatal(err)
	}
	for _, b := range []string{"ftyp", "moov", "avcC", "stss", "mdat"} {
		if !strings.Contains(string(d), b) {
			t.Fatal(b)
		}
	}
	if strings.Index(string(d), "moov") > strings.Index(string(d), "mdat") {
		t.Fatal("index must precede the data")
	}
	if _, err := WriteMP4([]Frame{{Data: []byte(delta)}}, nil, 1); err == nil {
		t.Fatal("recording without a keyframe accepted")
	}
}

// boxes lists the top-level MP4 boxes of b.
func boxes(t *testing.T, b []byte) (names []string, bodies [][]byte) {
	t.Helper()
	for len(b) > 0 {
		if len(b) < 8 {
			t.Fatalf("truncated box header")
		}
		n := int(binary.BigEndian.Uint32(b))
		if n < 8 || n > len(b) {
			t.Fatalf("bad box size %d", n)
		}
		names = append(names, string(b[4:8]))
		bodies = append(bodies, b[8:n])
		b = b[n:]
	}
	return names, bodies
}

func TestFragmentedMP4(t *testing.T) {
	m, err := NewFragmenter(Frame{Data: []byte(key), Key: true})
	if err != nil {
		t.Fatal(err)
	}
	if got := codecString(m.SPS); got != "avc1.4D0028" {
		t.Fatal(got)
	}
	init := m.Init()
	if names, _ := boxes(t, init); strings.Join(names, ",") != "ftyp,moov" || !bytes.Contains(init, []byte("trex")) {
		t.Fatal(names)
	}
	var base uint64
	for i, f := range []Frame{{Data: []byte(key), Key: true}, {Data: []byte(delta)}} {
		seg := m.Fragment(f, 3000)
		names, bodies := boxes(t, seg)
		if strings.Join(names, ",") != "moof,mdat" {
			t.Fatal(names)
		}
		moof := bodies[0]
		tfdt := moof[bytes.Index(moof, []byte("tfdt"))+8:]
		if got := binary.BigEndian.Uint64(tfdt); got != base {
			t.Fatalf("fragment %d: decode time %d, want %d", i, got, base)
		}
		base += 3000
		trun := moof[bytes.Index(moof, []byte("trun"))+4:]
		offset := binary.BigEndian.Uint32(trun[8:])
		size := binary.BigEndian.Uint32(trun[16:])
		if int(offset) != len(moof)+8+8 || int(size) != len(bodies[1]) {
			t.Fatalf("fragment %d: data offset %d size %d, moof %d mdat %d", i, offset, size, len(moof)+8, len(bodies[1]))
		}
		if bytes.Contains(bodies[1], []byte{0x67, 0x4d}) {
			t.Fatal("SPS must not be inside the sample")
		}
	}
	if _, err := NewFragmenter(Frame{Data: []byte(delta)}); err == nil {
		t.Fatal("fragmenter without keyframe accepted")
	}
}

func TestFrameDur(t *testing.T) {
	t0 := time.Now()
	for _, c := range []struct {
		d    time.Duration
		want uint32
	}{{40 * time.Millisecond, 3600}, {0, 3000}, {-time.Second, 3000}, {2 * time.Second, 3000}} {
		if got := frameDur(t0, t0.Add(c.d)); got != c.want {
			t.Fatalf("%v: %d, want %d", c.d, got, c.want)
		}
	}
}

func liveRequest(t *testing.T, addr string, auth bool, site string) *http.Response {
	return adminGet(t, addr, "/admin/live", auth, site)
}

func adminGet(t *testing.T, addr, path string, auth bool, site string) *http.Response {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	req, _ := http.NewRequestWithContext(ctx, "GET", "http://"+addr+path, nil)
	if auth {
		req.SetBasicAuth("admin", "pw")
	}
	if site != "" {
		req.Header.Set("Sec-Fetch-Site", site)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { res.Body.Close() })
	return res
}

func TestLiveStreamsFragmentedMP4(t *testing.T) {
	h := newHub(t, fakeDoorbell(t))
	addr := serve(t, h)
	res := liveRequest(t, addr, true, "same-origin")
	if res.StatusCode != 200 || res.Header.Get("Content-Type") != "video/mp4" || res.Header.Get("X-Video-Codec") != "avc1.4D0028" {
		t.Fatal(res.Status, res.Header)
	}
	var got []byte
	buf := make([]byte, 4096)
	for !bytes.Contains(got, []byte("mdat")) || bytes.Count(got, []byte("moof")) < 3 {
		n, err := res.Body.Read(buf)
		if err != nil {
			t.Fatal(err)
		}
		got = append(got, buf[:n]...)
	}
	if !bytes.HasPrefix(got[4:], []byte("ftyp")) {
		t.Fatal("stream must start with the init segment")
	}
}

func TestLiveNeedsAdminAndSameSite(t *testing.T) {
	h := newHub(t, fakeDoorbell(t))
	addr := serve(t, h)
	if res := liveRequest(t, addr, false, ""); res.StatusCode != http.StatusUnauthorized {
		t.Fatal(res.Status)
	}
	if res := liveRequest(t, addr, true, "cross-site"); res.StatusCode != http.StatusForbidden {
		t.Fatal(res.Status)
	}
}

func TestAdminWithoutPassword(t *testing.T) {
	h := newHub(t, fakeDoorbell(t))
	h.cfg.AdminPassword = ""
	addr := serve(t, h)
	resp, err := http.Get("http://" + addr + "/")
	if err != nil || resp.StatusCode != http.StatusOK {
		t.Fatal(err, resp.StatusCode)
	}
	req, _ := http.NewRequest("POST", "http://"+addr+"/admin/clients", strings.NewReader("name=Pixel"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Origin", "http://evil.example")
	r, err := http.DefaultClient.Do(req)
	if err != nil || r.StatusCode != http.StatusForbidden {
		t.Fatal(err, r.StatusCode)
	}
	if res := liveRequest(t, addr, false, "cross-site"); res.StatusCode != http.StatusForbidden {
		t.Fatal(res.Status)
	}
}

func TestLiveAudioStreamsPCMToTheAdmin(t *testing.T) {
	h := newHub(t, fakeDoorbell(t))
	addr := serve(t, h)
	if res := adminGet(t, addr, "/admin/live-audio", false, ""); res.StatusCode != http.StatusUnauthorized {
		t.Fatal(res.Status)
	}
	if res := adminGet(t, addr, "/admin/live-audio", true, "cross-site"); res.StatusCode != http.StatusForbidden {
		t.Fatal(res.Status)
	}
	res := adminGet(t, addr, "/admin/live-audio", true, "same-origin")
	b := make([]byte, len(pcmFrame))
	if _, err := io.ReadFull(res.Body, b); err != nil || !bytes.Equal(b, pcmFrame) {
		t.Fatalf("%v %x", err, b[:4])
	}
}

func TestTalkReachesTheSpeaker(t *testing.T) {
	got := make(chan []byte, 10)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := ws.Upgrade(w, r, 4096)
		if err != nil {
			return
		}
		defer c.Close()
		for {
			op, p, err := c.Read()
			if err != nil {
				return
			}
			if r.URL.Path == "/speaker" && op == ws.Binary {
				got <- p
			}
		}
	}))
	t.Cleanup(srv.Close)
	u, _ := url.Parse(srv.URL)
	h := newHub(t, u.Host)
	addr := serve(t, h)
	if _, err := ws.Dial(addr, "/api/talk", "", 4096, time.Second); err == nil {
		t.Fatal("talk without a token accepted")
	}
	_, tok, _ := h.store.AddClient("one")
	c, err := ws.Dial(addr, "/api/talk", "Authorization: Bearer "+tok+"\r\n", 4096, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	c.Write(ws.Binary, pcmFrame)
	select {
	case p := <-got:
		want := bytes.Clone(pcmFrame) // leveled at the default volume
		var l leveler
		l.process(want, DefaultTalkVolume/100.0)
		if !bytes.Equal(p, want) {
			t.Fatal("speaker got other data")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("nothing reached the speaker")
	}
}
