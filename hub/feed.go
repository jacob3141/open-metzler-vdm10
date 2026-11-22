package main

import (
	"context"
	"log"
	"sync"
	"time"

	"doorbell-hub/ws"
)

const maxVideoFrame = 4 << 20

// Sub is a subscriber of a feed. A video subscriber receives frames from the next keyframe on.
type Sub struct {
	C       chan Frame
	needKey bool
}

// Feed connects to a stream of the doorbell only while someone is subscribed.
type Feed struct {
	addr   string
	path   string // "/video" or "/audio" on the doorbell
	video  bool   // frames are H.264, subscribers start at a keyframe
	mu     sync.Mutex
	subs   map[*Sub]struct{}
	cancel context.CancelFunc
}

// NewFeed is the video of the doorbell: H.264 frames in Annex B format.
func NewFeed(addr string) *Feed {
	return &Feed{addr: addr, path: "/video", video: true, subs: map[*Sub]struct{}{}}
}

// NewAudioFeed is the microphone of the doorbell: 20 ms of PCM per frame, see audioRate.
// A doorbell without audio never sends a frame.
func NewAudioFeed(addr string) *Feed {
	return &Feed{addr: addr, path: "/audio", subs: map[*Sub]struct{}{}}
}

func (f *Feed) Subscribe() *Sub {
	s := &Sub{C: make(chan Frame, 90), needKey: true}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.subs[s] = struct{}{}
	if f.cancel == nil {
		ctx, cancel := context.WithCancel(context.Background())
		f.cancel = cancel
		go f.run(ctx)
	}
	return s
}

func (f *Feed) Unsubscribe(s *Sub) {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.subs, s)
	if len(f.subs) == 0 && f.cancel != nil {
		f.cancel()
		f.cancel = nil
	}
}

func (f *Feed) publish(fr Frame) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for s := range f.subs {
		if f.video && s.needKey && !fr.Key {
			continue
		}
		select {
		case s.C <- fr:
			s.needKey = false
		default:
			s.needKey = true
		}
	}
}

func (f *Feed) run(ctx context.Context) {
	log.Printf("%s: connecting to the doorbell", f.path)
	defer log.Printf("%s: connection closed", f.path)
	for ctx.Err() == nil {
		c, err := ws.Dial(f.addr, f.path, "", maxVideoFrame, 10*time.Second)
		if err != nil {
			log.Printf("%s: %v", f.path, err)
			select {
			case <-ctx.Done():
			case <-time.After(3 * time.Second):
			}
			continue
		}
		stop := context.AfterFunc(ctx, func() { c.Close() })
		for {
			op, p, err := c.Read()
			if err != nil {
				break
			}
			if op == ws.Binary {
				f.publish(Frame{Data: p, Key: !f.video || isKey(p), At: time.Now()})
			}
		}
		stop()
		c.Close()
	}
}
