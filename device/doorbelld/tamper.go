package main

import (
	"encoding/binary"
	"log"
	"math"
	"os"
	"time"
)

// The tamper switch on the back of the door station is pressed while the station is
// mounted. The vendor driver DDM reads it (GPIO7_4); the original firmware asks it with the
// ioctl DDM_IOC_DOOR_TAMPER on /dev/DDM/alarmV2, which gives 1 while the switch is pressed
// and 0 once the station comes off the wall.
const (
	tamperDevice     = "/dev/DDM/alarmV2"
	ddmIocDoorTamper = 0x40044204
)

// watchTamper reports the tamper switch to the RPC clients and sounds the siren for
// sirenFor when the station is taken off. The state at start counts as normal, so a
// station that boots on the bench stays quiet.
func (h *hub) watchTamper(sirenFor time.Duration) {
	f, err := os.OpenFile(tamperDevice, os.O_RDWR, 0)
	if err != nil {
		log.Printf("tamper: %v", err)
		return
	}
	defer f.Close()
	open, err := readTamper(f)
	if err != nil {
		log.Printf("tamper: %v", err)
		return
	}
	sw := debounce{pressed: open}
	h.setTamper(open)
	for range time.Tick(25 * time.Millisecond) {
		open, err := readTamper(f)
		if err != nil {
			continue
		}
		h.tamperReading(&sw, open, sirenFor)
	}
}

// tamperReading takes one reading of the switch (true while open).
func (h *hub) tamperReading(sw *debounce, open bool, sirenFor time.Duration) {
	if !sw.update(open) {
		return
	}
	if sw.pressed {
		log.Print("tamper: the station was taken off")
	} else {
		log.Print("tamper: the station is back on the wall")
	}
	h.setTamper(sw.pressed)
	if sw.pressed && sirenFor > 0 {
		h.startSiren(sirenFor)
	}
}

func (h *hub) setTamper(open bool) {
	h.alarmMu.Lock()
	h.tamperOpen = open
	h.alarmMu.Unlock()
	h.broadcast(h.tamperMessage())
}

// tamperMessage is the notification "tamper" with the state of the switch and the siren.
// Every RPC client gets it when it connects and whenever something changes.
func (h *hub) tamperMessage() []byte {
	h.alarmMu.Lock()
	defer h.alarmMu.Unlock()
	return encode(map[string]any{
		"jsonrpc": "2.0",
		"method":  "tamper",
		"params":  map[string]any{"open": h.tamperOpen, "alarm": h.sirenStop != nil, "time": time.Now().Unix()},
	})
}

func (h *hub) sirenActive() bool {
	h.alarmMu.Lock()
	defer h.alarmMu.Unlock()
	return h.sirenStop != nil
}

// startSiren sounds the siren for d, starting over if it already sounds.
func (h *hub) startSiren(d time.Duration) {
	h.alarmMu.Lock()
	if h.sirenStop != nil {
		close(h.sirenStop)
	}
	stop := make(chan struct{})
	h.sirenStop = stop
	h.alarmMu.Unlock()
	h.broadcast(h.tamperMessage())
	go func() {
		h.siren(d, stop)
		h.alarmMu.Lock()
		ended := h.sirenStop == stop
		if ended {
			h.sirenStop = nil
		}
		h.alarmMu.Unlock()
		if ended {
			h.broadcast(h.tamperMessage())
		}
	}()
}

// stopSiren silences the siren. It returns false if it did not sound.
func (h *hub) stopSiren() bool {
	h.alarmMu.Lock()
	stop := h.sirenStop
	h.sirenStop = nil
	h.alarmMu.Unlock()
	if stop == nil {
		return false
	}
	close(stop)
	log.Print("tamper: siren stopped")
	h.broadcast(h.tamperMessage())
	return true
}

// siren plays a wailing tone through the speaker until d passed or stop closes. Talk from
// /speaker is dropped meanwhile.
func (h *hub) siren(d time.Duration, stop <-chan struct{}) {
	var s sirenTone
	t := time.NewTicker(sirenChunk)
	defer t.Stop()
	end := time.Now().Add(d)
	h.play(s.next()) // one chunk ahead, so the speaker does not run dry between ticks
	for time.Now().Before(end) {
		select {
		case <-stop:
			return
		case <-t.C:
			select {
			case <-stop: // stopped while the tick was due
				return
			default:
			}
			if err := h.play(s.next()); err != nil {
				log.Printf("siren: %v", err)
			}
		}
	}
}

const (
	sirenRate  = 16000
	sirenChunk = 20 * time.Millisecond
	sirenLow   = 650.0  // Hz
	sirenHigh  = 1450.0 // Hz
	sirenSweep = 1.2    // seconds from low to high and back
)

// sirenTone makes 16 kHz mono 16-bit little-endian PCM: a tone that sweeps up and down,
// slightly clipped so it sounds harsh and loud.
type sirenTone struct {
	n     int     // samples so far
	phase float64 // of the tone, in radians
}

func (s *sirenTone) next() []byte {
	samples := int(sirenRate * sirenChunk / time.Second)
	out := make([]byte, 0, 2*samples)
	for i := 0; i < samples; i++ {
		t := float64(s.n) / sirenRate
		s.n++
		sweep := (1 - math.Cos(2*math.Pi*t/sirenSweep)) / 2 // 0..1..0
		f := sirenLow + (sirenHigh-sirenLow)*sweep
		s.phase = math.Mod(s.phase+2*math.Pi*f/sirenRate, 2*math.Pi)
		v := math.Max(-1, math.Min(1, 1.6*math.Sin(s.phase)))
		out = binary.LittleEndian.AppendUint16(out, uint16(int16(v*0.9*32767)))
	}
	return out
}
