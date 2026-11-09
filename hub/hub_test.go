package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
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

// fakeDoorbell serves frames on /video and accepts /.
func fakeDoorbell(t *testing.T) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := ws.Upgrade(w, r, 4096)
		if err != nil {
			return
		}
		defer c.Close()
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
	d, err := WriteMP4([]Frame{{[]byte(key), true}, {[]byte(delta), false}}, 0.1)
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
	if _, err := WriteMP4([]Frame{{[]byte(delta), false}}, 1); err == nil {
		t.Fatal("recording without a keyframe accepted")
	}
}
