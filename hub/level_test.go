package main

import (
	"encoding/binary"
	"math"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

func sine(amp float64, n int) []byte {
	b := make([]byte, 2*n)
	for i := 0; i < n; i++ {
		binary.LittleEndian.PutUint16(b[2*i:], uint16(int16(amp*math.Sin(2*math.Pi*300*float64(i)/16000))))
	}
	return b
}

func peak(b []byte) float64 {
	p := 0.0
	for i := 0; i+1 < len(b); i += 2 {
		p = math.Max(p, math.Abs(float64(int16(binary.LittleEndian.Uint16(b[i:])))))
	}
	return p
}

func TestLevelerBringsQuietAndLoudVoicesToTheSameLevel(t *testing.T) {
	for _, amp := range []float64{2000, 8000, 30000} {
		var l leveler
		pcm := sine(amp, 16000*2) // 2 s
		l.process(pcm, 1)
		got := peak(pcm[len(pcm)/2:]) // after it settled
		if math.Abs(got-levelTarget) > 0.1*levelTarget {
			t.Errorf("amplitude %v: peak %v, want about %v", amp, got, levelTarget)
		}
	}
	var l leveler
	pcm := sine(8000, 16000)
	l.process(pcm, 0.5)
	if got := peak(pcm[len(pcm)/2:]); math.Abs(got-levelTarget/2) > 0.1*levelTarget {
		t.Errorf("half volume: peak %v", got)
	}
}

func TestLevelerDoesNotLiftSilenceAndNeverClips(t *testing.T) {
	var l leveler
	quiet := sine(50, 16000) // the noise of a phone
	l.process(quiet, 1)
	if got := peak(quiet); got > 50*levelMaxGain+1 {
		t.Fatalf("silence lifted to %v", got)
	}
	// a voice after a long quiet phase starts loud but must not clip for long
	loud := sine(30000, 1600)
	l.process(loud, 1)
	if got := peak(loud[len(loud)/2:]); got > levelTarget*1.05 {
		t.Fatalf("loud start: peak %v", got)
	}
}

func TestAdminSetsTheTalkVolume(t *testing.T) {
	h := newHub(t, "127.0.0.1:1")
	addr := serve(t, h)
	if v := h.store.Settings().TalkVolume; v != DefaultTalkVolume {
		t.Fatalf("default %d", v)
	}
	req, _ := http.NewRequest("POST", "http://"+addr+"/admin/settings", strings.NewReader(url.Values{"talk_volume": {"55"}}.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Origin", "http://"+addr)
	req.SetBasicAuth("admin", "pw")
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	res, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusSeeOther || h.store.Settings().TalkVolume != 55 {
		t.Fatalf("status %d, volume %d", res.StatusCode, h.store.Settings().TalkVolume)
	}
}
