package main

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func masked(op byte, p []byte) []byte {
	mask := []byte{1, 2, 3, 4}
	f := []byte{0x80 | op}
	if len(p) < 126 {
		f = append(f, 0x80|byte(len(p)))
	} else {
		f = append(f, 0x80|126, byte(len(p)>>8), byte(len(p)))
	}
	f = append(f, mask...)
	for i, b := range p {
		f = append(f, b^mask[i%4])
	}
	return f
}

func TestAcceptKeyRFC6455(t *testing.T) {
	if got := acceptKey("dGhlIHNhbXBsZSBub25jZQ=="); got != "s3pPLMBiTxaQ9kYGzzhZRbK+xOo=" {
		t.Fatal(got)
	}
}

func TestReadFrame(t *testing.T) {
	for _, n := range []int{0, 5, 125, 126, 1000} {
		p := bytes.Repeat([]byte("x"), n)
		op, got, err := readFrame(bytes.NewReader(masked(opText, p)))
		if err != nil || op != opText || !bytes.Equal(got, p) {
			t.Fatalf("n=%d op=%d err=%v", n, op, err)
		}
	}
}

func TestReadFrameRejects(t *testing.T) {
	cases := map[string][]byte{
		"unmasked":     {0x81, 0x00},
		"fragmented":   {0x01, 0x80, 0, 0, 0, 0},
		"continuation": {0x80, 0x80, 0, 0, 0, 0},
		"too large":    {0x81, 0xFF, 0, 0, 0, 0, 0, 0x10, 0, 0, 0, 0, 0, 0},
	}
	for name, f := range cases {
		if _, _, err := readFrame(bytes.NewReader(f)); err != errProtocol {
			t.Errorf("%s: %v", name, err)
		}
	}
}

func TestDispatch(t *testing.T) {
	h := &hub{clients: map[*client]struct{}{}, started: time.Now()}
	cases := []struct {
		in, want string
	}{
		{`{"jsonrpc":"2.0","id":1,"method":"system.ping"}`, `{"jsonrpc":"2.0","id":1,"result":"pong"}`},
		{`{"jsonrpc":"2.0","id":"a","method":"nope"}`, `{"jsonrpc":"2.0","id":"a","error":{"code":-32601,"message":"Method not found"}}`},
		{`{kaputt`, `{"jsonrpc":"2.0","id":null,"error":{"code":-32700,"message":"Parse error"}}`},
		{`[1]`, `{"jsonrpc":"2.0","id":null,"error":{"code":-32600,"message":"Invalid Request"}}`},
		{`{"id":1,"method":"system.ping"}`, `{"jsonrpc":"2.0","id":null,"error":{"code":-32600,"message":"Invalid Request"}}`},
	}
	for _, c := range cases {
		if got := string(h.dispatch([]byte(c.in))); got != c.want {
			t.Errorf("%s\n got %s\nwant %s", c.in, got, c.want)
		}
	}
	if r := h.dispatch([]byte(`{"jsonrpc":"2.0","method":"system.ping"}`)); r != nil {
		t.Errorf("notification got a response: %s", r)
	}
	var info struct {
		Result map[string]any `json:"result"`
	}
	json.Unmarshal(h.dispatch([]byte(`{"jsonrpc":"2.0","id":2,"method":"system.info"}`)), &info)
	if info.Result["device"] != "Metzler VDM10-VM-POE" {
		t.Errorf("%v", info.Result)
	}
}

func TestVideoWaitsForKeyframeAndRecovers(t *testing.T) {
	h := &hub{clients: map[*client]struct{}{}, started: time.Now()}
	c := &client{out: make(chan []byte, 1), kind: kindVideo, needKey: true}
	h.add(c)
	h.broadcastVideo([]byte{1}, false)
	if len(c.out) != 0 {
		t.Fatal("delta frame delivered before the first keyframe")
	}
	h.broadcastVideo([]byte{2}, true)
	if len(c.out) != 1 || c.needKey {
		t.Fatal("keyframe not delivered")
	}
	h.broadcastVideo([]byte{3}, false)
	if !c.needKey {
		t.Fatal("full queue not detected")
	}
	<-c.out
	h.broadcastVideo([]byte{4}, false)
	if len(c.out) != 0 {
		t.Fatal("delta frame delivered after overflow")
	}
	h.broadcastVideo([]byte{5}, true)
	if len(c.out) != 1 {
		t.Fatal("client did not rejoin at the keyframe")
	}
}

func TestRPCBroadcastSkipsVideoClients(t *testing.T) {
	h := &hub{clients: map[*client]struct{}{}, started: time.Now()}
	v := &client{out: make(chan []byte, 1), kind: kindVideo}
	r := &client{out: make(chan []byte, 1)}
	h.add(v)
	h.add(r)
	h.broadcast([]byte(`{}`))
	if len(v.out) != 0 || len(r.out) != 1 {
		t.Fatalf("video=%d rpc=%d", len(v.out), len(r.out))
	}
}

func TestMediaFramesGoToTheirClients(t *testing.T) {
	h := &hub{clients: map[*client]struct{}{}, started: time.Now()}
	v := &client{out: make(chan []byte, 4), kind: kindVideo, needKey: true}
	a := &client{out: make(chan []byte, 4), kind: kindAudio}
	r := &client{out: make(chan []byte, 4)}
	h.add(v)
	h.add(a)
	h.add(r)
	var in []byte
	frame := func(flags byte, data string) {
		in = binary.LittleEndian.AppendUint32(in, uint32(len(data)))
		in = append(in, flags)
		in = append(in, data...)
	}
	frame(flagKey, "key")
	frame(flagAudio, "pcm1")
	frame(0, "p")
	frame(flagAudio, "pcm2")
	if err := h.readMedia(bytes.NewReader(in)); err != io.EOF {
		t.Fatalf("readMedia: %v", err)
	}
	if len(v.out) != 2 || len(a.out) != 2 || len(r.out) != 0 {
		t.Fatalf("video=%d audio=%d rpc=%d", len(v.out), len(a.out), len(r.out))
	}
	if got := <-a.out; !bytes.Equal(got, makeFrame(opBinary, []byte("pcm1"))) {
		t.Fatalf("audio frame %q", got)
	}
}

func TestSpeakerFeedsMediadAndAllowsOneClient(t *testing.T) {
	h := &hub{clients: map[*client]struct{}{}, started: time.Now()}
	var in bytes.Buffer
	h.setMediaIn(&in)
	a, b := &client{}, &client{}
	if !h.claimSpeaker(a) || h.claimSpeaker(b) {
		t.Fatal("second speaker accepted")
	}
	if err := h.play([]byte("pcm")); err != nil {
		t.Fatal(err)
	}
	if want := []byte{3, 0, 0, 0, flagAudio, 'p', 'c', 'm'}; !bytes.Equal(in.Bytes(), want) {
		t.Fatalf("mediad got %v", in.Bytes())
	}
	h.releaseSpeaker(a)
	if !h.claimSpeaker(b) {
		t.Fatal("speaker not free after release")
	}
	h.setMediaIn(nil)
	if err := h.play([]byte("x")); err != nil {
		t.Fatal(err)
	}
}

func TestButtonPressRingsOnceAndIgnoresGlitches(t *testing.T) {
	h := &hub{clients: map[*client]struct{}{}, started: time.Now()}
	r := &client{out: make(chan []byte, 8)}
	h.add(r)
	path := filepath.Join(t.TempDir(), "value")
	tick := make(chan time.Time) // unbuffered: a send returns once the previous read is handled
	done := make(chan struct{})
	go func() { h.pollButton(path, tick); close(done) }()
	read := func(vals string) { // one read per character
		for _, v := range vals {
			os.WriteFile(path, []byte(string(v)+"\n"), 0o644)
			tick <- time.Now()
		}
	}
	rings := func() int {
		tick <- time.Now() // the last read is handled
		return len(r.out)
	}
	read("00010")
	if n := rings(); n != 0 {
		t.Fatalf("a glitch rang %d times", n)
	}
	read("1111111")
	if n := rings(); n != 1 {
		t.Fatalf("held button: %d rings", n)
	}
	read("1101")
	if n := rings(); n != 1 {
		t.Fatalf("a short drop rang again: %d", n)
	}
	read("0011")
	close(tick)
	<-done
	if n := len(r.out); n != 2 {
		t.Fatalf("second press: %d rings", n)
	}
	if m := string(<-r.out); !strings.Contains(m, `"button.pressed"`) {
		t.Fatalf("message %s", m)
	}
}
