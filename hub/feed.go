package main

import (
	"context"
	"log"
	"sync"
	"time"

	"doorbell-hub/ws"
)

const maxVideoFrame = 4 << 20

// Sub is a video subscriber. It receives frames from the next keyframe on.
type Sub struct {
	C       chan Frame
	needKey bool
}

// Feed connects to the doorbell video only while someone is watching.
type Feed struct {
	addr   string
	mu     sync.Mutex
	subs   map[*Sub]struct{}
	cancel context.CancelFunc
}

func NewFeed(addr string) *Feed { return &Feed{addr: addr, subs: map[*Sub]struct{}{}} }

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
		if s.needKey && !fr.Key {
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
	log.Print("video: connecting to the doorbell")
	defer log.Print("video: connection closed")
	for ctx.Err() == nil {
		c, err := ws.Dial(f.addr, "/video", "", maxVideoFrame, 10*time.Second)
		if err != nil {
			log.Printf("video: %v", err)
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
				f.publish(Frame{Data: p, Key: isKey(p)})
			}
		}
		stop()
		c.Close()
	}
}
