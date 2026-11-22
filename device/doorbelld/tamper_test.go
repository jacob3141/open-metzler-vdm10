package main

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"sync"
	"testing"
	"time"
)

// lockedBuffer stands in for the stdin of mediad, written by the siren goroutine.
type lockedBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (l *lockedBuffer) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}

func (l *lockedBuffer) Len() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Len()
}

type tamperParams struct {
	Open    bool `json:"open"`
	Alarm   bool `json:"alarm"`
	Pressed bool `json:"pressed"`
	Mount   bool `json:"mount"`
}

// nextTamper waits for the next "tamper" notification to the client.
func nextTamper(t *testing.T, c *client) tamperParams {
	t.Helper()
	select {
	case f := <-c.out:
		var m struct {
			Method string       `json:"method"`
			Params tamperParams `json:"params"`
		}
		if len(f) < 2 || json.Unmarshal(f[2:], &m) != nil || m.Method != "tamper" {
			t.Fatalf("not a tamper notification: %q", f)
		}
		return m.Params
	case <-time.After(2 * time.Second):
		t.Fatal("no tamper notification")
	}
	return tamperParams{}
}

func TestTakingTheStationOffSoundsTheSiren(t *testing.T) {
	audio := &lockedBuffer{}
	h := &hub{clients: map[*client]struct{}{}, started: time.Now()}
	h.setMediaIn(audio)
	c := &client{out: make(chan []byte, 16)}
	h.add(c)

	var sw debounce
	h.tamperReading(&sw, true, time.Hour) // a single reading is a glitch
	h.tamperReading(&sw, false, time.Hour)
	if len(c.out) != 0 || h.sirenActive() {
		t.Fatal("a glitch raised the alarm")
	}
	h.tamperReading(&sw, true, time.Hour)
	h.tamperReading(&sw, true, time.Hour)
	if p := nextTamper(t, c); !p.Open {
		t.Fatalf("first notification %+v, want open", p)
	}
	if p := nextTamper(t, c); !p.Open || !p.Alarm {
		t.Fatalf("second notification %+v, want the siren", p)
	}
	time.Sleep(100 * time.Millisecond)
	if audio.Len() == 0 {
		t.Fatal("the siren played nothing")
	}

	// Putting it back does not silence it, only alarm.stop does.
	h.tamperReading(&sw, false, time.Hour)
	h.tamperReading(&sw, false, time.Hour)
	if p := nextTamper(t, c); p.Open || !p.Alarm {
		t.Fatalf("after closing %+v, want closed with the siren", p)
	}
	reply := h.dispatch([]byte(`{"jsonrpc":"2.0","id":1,"method":"alarm.stop"}`))
	if !bytes.Contains(reply, []byte(`"result":true`)) {
		t.Fatalf("alarm.stop: %s", reply)
	}
	if p := nextTamper(t, c); p.Alarm {
		t.Fatalf("after alarm.stop %+v", p)
	}
	time.Sleep(50 * time.Millisecond) // a chunk that was on its way
	n := audio.Len()
	time.Sleep(100 * time.Millisecond)
	if audio.Len() != n {
		t.Fatal("the siren goes on after alarm.stop")
	}
	if reply := h.dispatch([]byte(`{"jsonrpc":"2.0","id":2,"method":"alarm.stop"}`)); !bytes.Contains(reply, []byte(`"result":false`)) {
		t.Fatalf("second alarm.stop: %s", reply)
	}
}

func TestSirenEndsByItself(t *testing.T) {
	h := &hub{clients: map[*client]struct{}{}, started: time.Now()}
	c := &client{out: make(chan []byte, 16)}
	h.add(c)
	h.startSiren(60 * time.Millisecond)
	if p := nextTamper(t, c); !p.Alarm {
		t.Fatalf("start %+v", p)
	}
	if p := nextTamper(t, c); p.Alarm {
		t.Fatalf("end %+v", p)
	}
}

func TestSirenToneIsLoudAndSweeps(t *testing.T) {
	var s sirenTone
	crossings := func() (n int, peak int16) { // zero crossings in one chunk of 20 ms
		b := s.next()
		if len(b) != 640 {
			t.Fatalf("chunk of %d bytes", len(b))
		}
		prev := int16(0)
		for i := 0; i < len(b); i += 2 {
			v := int16(binary.LittleEndian.Uint16(b[i:]))
			if (v >= 0) != (prev >= 0) {
				n++
			}
			peak = max(peak, v)
			prev = v
		}
		return n, peak
	}
	low, peak := crossings()
	if peak < 29000 {
		t.Fatalf("peak %d, too quiet", peak)
	}
	for i := 0; i < 28; i++ { // up to the middle of the sweep, 0.6 s
		s.next()
	}
	if high, _ := crossings(); high < low*3/2 {
		t.Fatalf("pitch does not rise: %d then %d crossings", low, high)
	}
}

// In the mounting mode the switch is shown live, but the siren stays quiet.
func TestMountingModeKeepsTheSirenQuiet(t *testing.T) {
	h := &hub{clients: map[*client]struct{}{}, started: time.Now()}
	c := &client{out: make(chan []byte, 16)}
	h.add(c)

	h.startSiren(time.Hour)
	nextTamper(t, c)
	if r := h.dispatch([]byte(`{"jsonrpc":"2.0","id":1,"method":"mount.set","params":{"on":true}}`)); !bytes.Contains(r, []byte(`"result":true`)) {
		t.Fatalf("mount.set: %s", r)
	}
	if p := nextTamper(t, c); p.Alarm { // the mounting mode silences a siren that sounds
		t.Fatalf("after mount.set %+v", p)
	}
	if p := nextTamper(t, c); !p.Mount {
		t.Fatalf("mounting mode not reported: %+v", p)
	}

	var sw debounce
	pressed := tamperOpenWhenPressed // the reading that means "off the wall"
	h.tamperReading(&sw, pressed, time.Hour)
	h.tamperReading(&sw, pressed, time.Hour)
	if p := nextTamper(t, c); !p.Open || p.Pressed != pressed || !p.Mount || p.Alarm {
		t.Fatalf("switch in the mounting mode: %+v", p)
	}
	if h.sirenActive() {
		t.Fatal("the siren sounds in the mounting mode")
	}

	h.dispatch([]byte(`{"jsonrpc":"2.0","id":2,"method":"mount.set","params":{"on":false}}`))
	if p := nextTamper(t, c); p.Mount {
		t.Fatalf("mounting mode still on: %+v", p)
	}
	h.tamperReading(&sw, !pressed, time.Hour)
	h.tamperReading(&sw, !pressed, time.Hour)
	nextTamper(t, c)
	h.tamperReading(&sw, pressed, time.Hour)
	h.tamperReading(&sw, pressed, time.Hour)
	nextTamper(t, c)
	if !h.sirenActive() {
		t.Fatal("no siren after the mounting mode")
	}
	h.stopSiren()

	if r := h.dispatch([]byte(`{"jsonrpc":"2.0","id":3,"method":"mount.set","params":"x"}`)); !bytes.Contains(r, []byte(`-32602`)) {
		t.Fatalf("bad params: %s", r)
	}
}

func TestMountingModeEndsByItself(t *testing.T) {
	h := &hub{clients: map[*client]struct{}{}, started: time.Now()}
	c := &client{out: make(chan []byte, 16)}
	h.add(c)
	h.setMount(true, 50*time.Millisecond)
	if p := nextTamper(t, c); !p.Mount {
		t.Fatalf("%+v", p)
	}
	if p := nextTamper(t, c); p.Mount {
		t.Fatalf("still mounting: %+v", p)
	}
}
