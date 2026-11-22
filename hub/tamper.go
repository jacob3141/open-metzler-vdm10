package main

import (
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"time"

	"doorbell-hub/ws"
)

// tamperState is what doorbelld reports about the tamper switch on the back of the door
// station: open while the station is off the wall, and whether its siren sounds.
type tamperState struct {
	Open  bool `json:"open"`
	Alarm bool `json:"alarm"`
}

var errNoDoorbell = errors.New("the door station is not connected")

// Tamper takes a "tamper" notification of the doorbell. When the station comes off the
// wall, the hub records a clip like a visitor message, and every change goes to the apps.
func (h *Hub) Tamper(st tamperState) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if st == h.tamper {
		return
	}
	now := time.Now()
	if st.Open && !h.tamper.Open {
		log.Print("tamper: the door station was taken off the wall")
		h.lastTamper = now
		h.startRecording(now)
	}
	if st.Alarm != h.tamper.Alarm {
		log.Printf("tamper: siren %s", map[bool]string{true: "on", false: "off"}[st.Alarm])
	}
	h.tamper = st
	h.broadcast(h.tamperNotification(), nil)
}

// tamperNotification tells the apps the state of the switch and the siren. h.mu must be held.
func (h *Hub) tamperNotification() []byte {
	return notification("tamper", map[string]any{"open": h.tamper.Open, "alarm": h.tamper.Alarm, "time": h.lastTamper.Unix()})
}

// StopAlarm asks the door station to silence its siren.
func (h *Hub) StopAlarm() error {
	h.mu.Lock()
	door := h.door
	h.mu.Unlock()
	if door == nil {
		return errNoDoorbell
	}
	req, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": time.Now().UnixNano(), "method": "alarm.stop"})
	log.Print("tamper: stopping the siren")
	return door.Write(ws.Text, req)
}

func (h *Hub) stopAlarm(w http.ResponseWriter, r *http.Request) {
	if err := h.StopAlarm(); err != nil {
		http.Error(w, err.Error(), http.StatusServiceUnavailable)
		return
	}
	time.Sleep(300 * time.Millisecond) // the door station answers with a notification; show its result
	http.Redirect(w, r, "/", http.StatusSeeOther)
}
