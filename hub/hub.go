package main

import (
	"context"
	"encoding/json"
	"log"
	"sync"
	"time"

	"doorbell-hub/ws"
)

// Config holds the hub settings.
type Config struct {
	Listen        string
	Doorbell      string
	Data          string
	AdminPassword string
	RingTimeout   time.Duration
	RecordFor     time.Duration
	MaxMessages   int
}

// Session is a connected client app.
type Session struct {
	client Client
	conn   *ws.Conn
	out    chan []byte
}

// send queues a message. A client that stops reading is disconnected and blocks nobody.
func (s *Session) send(m []byte) {
	select {
	case s.out <- m:
	default:
		s.conn.Close()
	}
}

type ring struct {
	id       string
	time     time.Time
	rejected map[*Session]bool
	timer    *time.Timer
}

// Hub connects the doorbell, the clients and the recorder.
type Hub struct {
	cfg   Config
	store *Store
	feed  *Feed

	mu        sync.Mutex
	sessions  map[*Session]struct{}
	current   *ring
	recording bool
	connected bool
	lastRing  time.Time
}

func NewHub(cfg Config, store *Store) *Hub {
	return &Hub{cfg: cfg, store: store, feed: NewFeed(cfg.Doorbell), sessions: map[*Session]struct{}{}}
}

func notification(method string, params any) []byte {
	b, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "method": method, "params": params})
	return b
}

func (h *Hub) broadcast(msg []byte, except *Session) {
	for s := range h.sessions {
		if s != except {
			s.send(msg)
		}
	}
}

// Press handles a button press. It is ignored while a ring is in progress.
func (h *Hub) Press() {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.current != nil {
		return
	}
	now := time.Now()
	h.lastRing = now
	if len(h.sessions) == 0 {
		log.Print("ring without connected clients, recording")
		h.startRecording(now)
		return
	}
	r := &ring{id: randomHex(8), time: now, rejected: map[*Session]bool{}}
	h.current = r
	r.timer = time.AfterFunc(h.cfg.RingTimeout, func() { h.expire(r) })
	log.Printf("ring %s sent to %d clients", r.id, len(h.sessions))
	h.broadcast(notification("ring", map[string]any{"id": r.id, "time": now.Unix()}), nil)
}

func (h *Hub) expire(r *ring) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.current == r {
		h.miss(r)
	}
}

// miss ends an unanswered ring. h.mu must be held.
func (h *Hub) miss(r *ring) {
	r.timer.Stop()
	h.current = nil
	log.Printf("ring %s unanswered, recording", r.id)
	h.broadcast(notification("ring.missed", map[string]any{"id": r.id}), nil)
	h.startRecording(r.time)
}

func (h *Hub) startRecording(t time.Time) {
	if h.recording {
		return
	}
	h.recording = true
	go func() {
		h.record(t)
		h.mu.Lock()
		h.recording = false
		h.mu.Unlock()
	}()
}

func (h *Hub) record(t time.Time) {
	ctx, cancel := context.WithTimeout(context.Background(), h.cfg.RecordFor+15*time.Second)
	defer cancel()
	sub := h.feed.Subscribe()
	defer h.feed.Unsubscribe(sub)

	var frames []Frame
	var start time.Time
	for {
		select {
		case f := <-sub.C:
			if start.IsZero() {
				start = time.Now()
			}
			frames = append(frames, f)
			if time.Since(start) < h.cfg.RecordFor {
				continue
			}
		case <-ctx.Done():
		}
		break
	}
	if len(frames) == 0 {
		log.Print("recording: no frames from the doorbell")
		return
	}
	data, err := WriteMP4(frames, time.Since(start).Seconds())
	if err != nil {
		log.Printf("recording: %v", err)
		return
	}
	id, err := h.store.SaveMessage(data, t)
	if err != nil {
		log.Printf("recording: %v", err)
		return
	}
	h.store.Prune(h.cfg.MaxMessages)
	log.Printf("visitor message %s: %d frames, %d bytes", id, len(frames), len(data))
	h.mu.Lock()
	h.broadcast(notification("message.new", map[string]any{"id": id, "time": t.Unix()}), nil)
	h.mu.Unlock()
}

// Accept answers the ring. The other clients learn that it was taken.
func (h *Hub) Accept(s *Session, id string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	r := h.current
	if r == nil || r.id != id {
		return false
	}
	r.timer.Stop()
	h.current = nil
	log.Printf("ring %s answered by %s", id, s.client.Name)
	h.broadcast(notification("ring.taken", map[string]any{"id": id, "by": s.client.Name}), s)
	return true
}

// Reject declines. Once all connected clients have declined, the ring counts as unanswered.
func (h *Hub) Reject(s *Session, id string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	r := h.current
	if r == nil || r.id != id {
		return false
	}
	r.rejected[s] = true
	h.missIfAllRejected(r)
	return true
}

func (h *Hub) missIfAllRejected(r *ring) {
	for s := range h.sessions {
		if !r.rejected[s] {
			return
		}
	}
	h.miss(r)
}

func (h *Hub) addSession(s *Session) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.sessions[s] = struct{}{}
}

// removeSession treats a disconnected client like a rejection.
func (h *Hub) removeSession(s *Session) {
	h.mu.Lock()
	defer h.mu.Unlock()
	delete(h.sessions, s)
	if h.current != nil {
		h.missIfAllRejected(h.current)
	}
}

// Status is the state shown in the web interface.
type Status struct {
	Connected bool
	Sessions  map[string]bool
	Recording bool
	Ringing   bool
	LastRing  time.Time
}

func (h *Hub) Status() Status {
	h.mu.Lock()
	defer h.mu.Unlock()
	st := Status{Connected: h.connected, Sessions: map[string]bool{}, Recording: h.recording, Ringing: h.current != nil, LastRing: h.lastRing}
	for s := range h.sessions {
		st.Sessions[s.client.ID] = true
	}
	return st
}

func (h *Hub) setConnected(v bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.connected = v
}

// WatchDoorbell keeps the JSON-RPC connection to the doorbell and handles button presses.
func (h *Hub) WatchDoorbell() {
	for {
		c, err := ws.Dial(h.cfg.Doorbell, "/", "", 1<<20, 10*time.Second)
		if err != nil {
			log.Printf("doorbell: %v", err)
			time.Sleep(5 * time.Second)
			continue
		}
		log.Print("doorbell connected")
		h.setConnected(true)
		for {
			op, p, err := c.Read()
			if err != nil {
				log.Printf("doorbell disconnected: %v", err)
				break
			}
			var m struct {
				Method string `json:"method"`
			}
			if op == ws.Text && json.Unmarshal(p, &m) == nil && m.Method == "button.pressed" {
				h.Press()
			}
		}
		c.Close()
		h.setConnected(false)
		time.Sleep(2 * time.Second)
	}
}
