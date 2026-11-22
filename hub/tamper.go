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
// station: open while the station is off the wall, whether its siren sounds, whether the
// switch itself is pressed, and whether the mounting mode keeps the siren quiet.
type tamperState struct {
	Open    bool `json:"open"`
	Alarm   bool `json:"alarm"`
	Pressed bool `json:"pressed"`
	Mount   bool `json:"mount"`
}

var errNoDoorbell = errors.New("the door station is not connected")

// Tamper takes a "tamper" notification of the doorbell. When the station comes off the
// wall, the hub records a clip like a visitor message, and the apps learn about it. In the
// mounting mode the switch only shows on the admin page.
func (h *Hub) Tamper(st tamperState) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if st == h.tamper {
		return
	}
	old := h.tamper
	h.tamper = st
	if st.Mount != old.Mount {
		log.Printf("tamper: mounting mode %t", st.Mount)
	}
	if st.Mount {
		return
	}
	if st.Open && (!old.Open || old.Mount) {
		log.Print("tamper: the door station was taken off the wall")
		h.lastTamper = time.Now()
		h.startRecording(h.lastTamper)
	}
	if st.Alarm != old.Alarm {
		log.Printf("tamper: siren %t", st.Alarm)
	}
	if st.Open != old.Open || st.Alarm != old.Alarm || old.Mount {
		h.broadcast(h.tamperNotification(), nil)
	}
}

// tamperNotification tells the apps the state of the switch and the siren. h.mu must be held.
func (h *Hub) tamperNotification() []byte {
	return notification("tamper", map[string]any{"open": h.tamper.Open, "alarm": h.tamper.Alarm, "time": h.lastTamper.Unix()})
}

// callDoor sends a JSON-RPC request to the door station; its answer is not needed, because
// the door station reports every change as a notification.
func (h *Hub) callDoor(method string, params any) error {
	h.mu.Lock()
	door := h.door
	h.mu.Unlock()
	if door == nil {
		return errNoDoorbell
	}
	req, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": time.Now().UnixNano(), "method": method, "params": params})
	return door.Write(ws.Text, req)
}

// StopAlarm asks the door station to silence its siren.
func (h *Hub) StopAlarm() error {
	log.Print("tamper: stopping the siren")
	return h.callDoor("alarm.stop", nil)
}

// SetMount turns the mounting mode of the door station on (for 30 minutes) or off.
func (h *Hub) SetMount(on bool) error {
	return h.callDoor("mount.set", map[string]bool{"on": on})
}

// afterDoorCall shows the page again once the door station had time to report the change.
func afterDoorCall(w http.ResponseWriter, r *http.Request, err error) {
	if err != nil {
		http.Error(w, err.Error(), http.StatusServiceUnavailable)
		return
	}
	time.Sleep(300 * time.Millisecond)
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (h *Hub) stopAlarm(w http.ResponseWriter, r *http.Request) {
	afterDoorCall(w, r, h.StopAlarm())
}

func (h *Hub) setMount(w http.ResponseWriter, r *http.Request) {
	afterDoorCall(w, r, h.SetMount(r.FormValue("on") == "1"))
}

// tamperJSON is the state of the switch for the live view of the admin page.
func (h *Hub) tamperJSON(w http.ResponseWriter, _ *http.Request) {
	st := h.Status()
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	json.NewEncoder(w).Encode(map[string]any{
		"connected": st.Connected, "open": st.Tamper.Open, "alarm": st.Tamper.Alarm,
		"pressed": st.Tamper.Pressed, "mount": st.Tamper.Mount,
	})
}
