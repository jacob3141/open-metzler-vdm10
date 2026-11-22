package main

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"doorbell-hub/ws"
)

type tamperMsg struct {
	Method string      `json:"method"`
	Params tamperState `json:"params"`
}

func readTamper(t *testing.T, c *ws.Conn) tamperState {
	t.Helper()
	c.SetIdle(3 * time.Second)
	_, p, err := c.Read()
	if err != nil {
		t.Fatal(err)
	}
	var m tamperMsg
	if json.Unmarshal(p, &m) != nil || m.Method != "tamper" {
		t.Fatalf("not a tamper notification: %s", p)
	}
	return m.Params
}

// The door station reports that it came off the wall with the siren on. The hub records a
// clip, tells the apps, and an app silences the siren.
func TestTamperAlarmRecordsAndCanBeStopped(t *testing.T) {
	stopped := make(chan struct{})
	addr := fakeDoorbellRPC(t, func(c *ws.Conn) {
		c.Write(ws.Text, []byte(`{"jsonrpc":"2.0","method":"tamper","params":{"open":false,"alarm":false,"time":1}}`))
		c.Write(ws.Text, []byte(`{"jsonrpc":"2.0","method":"tamper","params":{"open":true,"alarm":true,"time":2}}`))
		for {
			_, p, err := c.Read()
			if err != nil {
				return
			}
			if strings.Contains(string(p), `"alarm.stop"`) {
				close(stopped)
				c.Write(ws.Text, []byte(`{"jsonrpc":"2.0","method":"tamper","params":{"open":true,"alarm":false,"time":3}}`))
			}
		}
	})
	h := newHub(t, addr)
	srv := serve(t, h)
	_, tok, _ := h.store.AddClient("one")
	go h.WatchDoorbell()

	waitMessages(t, h, 1) // the clip of the tamper alarm
	st := h.Status()
	if !st.Tamper.Open || !st.Tamper.Alarm || st.LastTamper.IsZero() {
		t.Fatalf("status %+v", st)
	}

	req, _ := http.NewRequest("GET", "http://"+srv+"/", nil)
	req.SetBasicAuth("admin", "pw")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	page, _ := io.ReadAll(res.Body)
	res.Body.Close()
	for _, s := range []string{"Tamper alarm", "off the wall", "Its siren sounds", `action="/admin/alarm/stop"`} {
		if !strings.Contains(string(page), s) {
			t.Fatalf("admin page lacks %q", s)
		}
	}

	// An app that connects now learns about the alarm and stops it.
	a := connect(t, srv, tok)
	if s := readTamper(t, a); !s.Open || !s.Alarm {
		t.Fatalf("on connect %+v", s)
	}
	a.Write(ws.Text, []byte(`{"jsonrpc":"2.0","id":7,"method":"alarm.stop"}`))
	if r := read(t, a); r.Error != nil || string(r.Result) != "true" {
		t.Fatalf("alarm.stop: %+v", r)
	}
	select {
	case <-stopped:
	case <-time.After(2 * time.Second):
		t.Fatal("the door station got no alarm.stop")
	}
	if s := readTamper(t, a); !s.Open || s.Alarm {
		t.Fatalf("after alarm.stop %+v", s)
	}
}

func TestStopAlarmWithoutDoorbell(t *testing.T) {
	h := newHub(t, "127.0.0.1:1")
	addr := serve(t, h)
	_, tok, _ := h.store.AddClient("one")
	a := connect(t, addr, tok)
	waitSessions(t, h, 1)
	a.Write(ws.Text, []byte(`{"jsonrpc":"2.0","id":1,"method":"alarm.stop"}`))
	if r := read(t, a); r.Error == nil {
		t.Fatalf("%+v", r)
	}
	req, _ := http.NewRequest("POST", "http://"+addr+"/admin/alarm/stop", nil)
	req.SetBasicAuth("admin", "pw")
	req.Header.Set("Origin", "http://"+addr)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("status %d", res.StatusCode)
	}
}
