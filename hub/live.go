package main

import (
	"bytes"
	"log"
	"net/http"
	"time"
)

const (
	liveStartTimeout = 15 * time.Second
	defaultFrameDur  = timescale / 30
)

func liveScript(w http.ResponseWriter, _ *http.Request) {
	js, _ := templates.ReadFile("templates/live.js")
	w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	w.Write(js)
}

// frameDur converts the gap between two received frames to MP4 ticks. A gap from
// dropped frames or a stall counts as one normal frame, so the timeline stays dense.
func frameDur(prev, next time.Time) uint32 {
	d := next.Sub(prev)
	if d <= 0 || d > 500*time.Millisecond {
		return defaultFrameDur
	}
	return uint32(d * timescale / time.Second)
}

// live streams the doorbell video as fragmented MP4 for Media Source Extensions in the
// admin page. The response starts at a keyframe; X-Video-Codec names the codec.
func (h *Hub) live(w http.ResponseWriter, r *http.Request) {
	if site := r.Header.Get("Sec-Fetch-Site"); site != "" && site != "same-origin" {
		http.Error(w, "Foreign origin", http.StatusForbidden)
		return
	}
	sub := h.feed.Subscribe()
	defer h.feed.Unsubscribe(sub)

	timeout := time.NewTimer(liveStartTimeout)
	defer timeout.Stop()
	var m *Fragmenter
	var prev Frame
	for m == nil {
		select {
		case f := <-sub.C:
			if fm, err := NewFragmenter(f); err == nil {
				m, prev = fm, f
			}
		case <-timeout.C:
			http.Error(w, "No video from the doorbell", http.StatusGatewayTimeout)
			return
		case <-r.Context().Done():
			return
		}
	}

	w.Header().Set("Content-Type", "video/mp4")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Video-Codec", codecString(m.SPS))
	rc := http.NewResponseController(w)
	if _, err := w.Write(m.Init()); err != nil {
		return
	}
	rc.Flush()
	log.Print("live: viewer connected")
	defer log.Print("live: viewer disconnected")

	// Each frame is sent once the next one arrives, because only then is its duration known.
	for {
		select {
		case f := <-sub.C:
			if f.Key {
				if _, sps, pps := sample(f); sps != nil && (!bytes.Equal(sps, m.SPS) || pps != nil && !bytes.Equal(pps, m.PPS)) {
					return // the stream format changed; the page reconnects with a new init segment
				}
			}
			rc.SetWriteDeadline(time.Now().Add(10 * time.Second))
			if _, err := w.Write(m.Fragment(prev, frameDur(prev.At, f.At))); err != nil {
				return
			}
			if rc.Flush() != nil {
				return
			}
			prev = f
		case <-r.Context().Done():
			return
		}
	}
}
