package main

import (
	"log"
	"net/http"
	"time"

	"doorbell-hub/ws"
)

// talk sends the microphone of a client app to the speaker of the door station: binary
// messages with 16 kHz mono 16-bit little-endian PCM, like /api/audio. The door station
// takes one speaker at a time; while someone else talks, the connection closes right away.
func (h *Hub) talk(w http.ResponseWriter, r *http.Request, c Client) {
	conn, err := ws.Upgrade(w, r, 64*1024)
	if err != nil {
		return
	}
	defer conn.Close()
	door, err := ws.Dial(h.cfg.Doorbell, "/speaker", "", 4096, 5*time.Second)
	if err != nil {
		log.Printf("talk: %v", err)
		return
	}
	defer door.Close()
	log.Printf("talk: %s speaks", c.Name)
	defer log.Printf("talk: %s stopped", c.Name)

	go func() { // the door station closes the connection when the speaker is taken
		for {
			if _, _, err := door.Read(); err != nil {
				conn.Close()
				return
			}
		}
	}()
	for {
		op, p, err := conn.Read()
		if err != nil {
			return
		}
		if op == ws.Binary && door.Write(ws.Binary, p) != nil {
			return
		}
	}
}

// liveAudio streams the microphone of the door station as raw PCM (16 kHz mono 16-bit little
// endian) for the live view of the admin page.
func (h *Hub) liveAudio(w http.ResponseWriter, r *http.Request) {
	if site := r.Header.Get("Sec-Fetch-Site"); site != "" && site != "same-origin" {
		http.Error(w, "Foreign origin", http.StatusForbidden)
		return
	}
	sub := h.audio.Subscribe()
	defer h.audio.Unsubscribe(sub)
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Sample-Rate", "16000")
	rc := http.NewResponseController(w)
	w.WriteHeader(http.StatusOK)
	rc.Flush()
	for {
		select {
		case f := <-sub.C:
			rc.SetWriteDeadline(time.Now().Add(10 * time.Second))
			if _, err := w.Write(f.Data); err != nil || rc.Flush() != nil {
				return
			}
		case <-r.Context().Done():
			return
		}
	}
}
