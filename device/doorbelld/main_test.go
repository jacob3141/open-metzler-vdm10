package main

import (
	"bytes"
	"encoding/json"
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
	c := &client{out: make(chan []byte, 1), video: true, needKey: true}
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
	v := &client{out: make(chan []byte, 1), video: true}
	r := &client{out: make(chan []byte, 1)}
	h.add(v)
	h.add(r)
	h.broadcast([]byte(`{}`))
	if len(v.out) != 0 || len(r.out) != 1 {
		t.Fatalf("video=%d rpc=%d", len(v.out), len(r.out))
	}
}
