// Package ws is a small WebSocket client and server following RFC 6455.
package ws

import (
	"bufio"
	"crypto/rand"
	"crypto/sha1"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

const (
	guid   = "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"
	Text   = 0x1
	Binary = 0x2
	opCl   = 0x8
	opPing = 0x9
	opPong = 0xA
)

var ErrProtocol = errors.New("websocket protocol error")

// Conn is a WebSocket connection. Read from one goroutine, write from several.
type Conn struct {
	c      net.Conn
	r      *bufio.Reader
	client bool
	max    uint64
	mu     sync.Mutex
}

func accept(key string) string {
	s := sha1.Sum([]byte(key + guid))
	return base64.StdEncoding.EncodeToString(s[:])
}

// Upgrade turns an HTTP request into a WebSocket connection. max limits incoming messages.
func Upgrade(w http.ResponseWriter, r *http.Request, max uint64) (*Conn, error) {
	key := r.Header.Get("Sec-WebSocket-Key")
	if !strings.EqualFold(r.Header.Get("Upgrade"), "websocket") || key == "" {
		http.Error(w, "WebSocket expected", http.StatusUpgradeRequired)
		return nil, ErrProtocol
	}
	h, ok := w.(http.Hijacker)
	if !ok {
		http.Error(w, "hijacking not supported", http.StatusInternalServerError)
		return nil, ErrProtocol
	}
	c, rw, err := h.Hijack()
	if err != nil {
		return nil, err
	}
	c.SetDeadline(time.Time{})
	if _, err := fmt.Fprintf(c, "HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Accept: %s\r\n\r\n", accept(key)); err != nil {
		c.Close()
		return nil, err
	}
	return &Conn{c: c, r: rw.Reader, max: max}, nil
}

// Dial connects to ws://host/path.
func Dial(addr, path, header string, max uint64, timeout time.Duration) (*Conn, error) {
	c, err := net.DialTimeout("tcp", addr, timeout)
	if err != nil {
		return nil, err
	}
	c.SetDeadline(time.Now().Add(timeout))
	var raw [16]byte
	rand.Read(raw[:])
	key := base64.StdEncoding.EncodeToString(raw[:])
	if _, err := fmt.Fprintf(c, "GET %s HTTP/1.1\r\nHost: %s\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Key: %s\r\nSec-WebSocket-Version: 13\r\n%s\r\n", path, addr, key, header); err != nil {
		c.Close()
		return nil, err
	}
	br := bufio.NewReaderSize(c, 64*1024)
	resp, err := http.ReadResponse(br, &http.Request{Method: "GET"})
	if err != nil {
		c.Close()
		return nil, err
	}
	if resp.StatusCode != http.StatusSwitchingProtocols || resp.Header.Get("Sec-WebSocket-Accept") != accept(key) {
		c.Close()
		return nil, fmt.Errorf("ws: response %s", resp.Status)
	}
	c.SetDeadline(time.Time{})
	return &Conn{c: c, r: br, client: true, max: max}, nil
}

func (c *Conn) Close() error { return c.c.Close() }

// Read returns the next data message. Ping and pong are handled internally.
func (c *Conn) Read() (int, []byte, error) {
	for {
		var h [2]byte
		if _, err := io.ReadFull(c.r, h[:]); err != nil {
			return 0, nil, err
		}
		if h[0]&0x80 == 0 || h[0]&0x70 != 0 || h[0]&0x0F == 0 {
			return 0, nil, ErrProtocol
		}
		masked := h[1]&0x80 != 0
		if masked == c.client {
			return 0, nil, ErrProtocol
		}
		n := uint64(h[1] & 0x7F)
		switch n {
		case 126:
			var b [2]byte
			if _, err := io.ReadFull(c.r, b[:]); err != nil {
				return 0, nil, err
			}
			n = uint64(binary.BigEndian.Uint16(b[:]))
		case 127:
			var b [8]byte
			if _, err := io.ReadFull(c.r, b[:]); err != nil {
				return 0, nil, err
			}
			n = binary.BigEndian.Uint64(b[:])
		}
		if n > c.max {
			return 0, nil, ErrProtocol
		}
		var mask [4]byte
		if masked {
			if _, err := io.ReadFull(c.r, mask[:]); err != nil {
				return 0, nil, err
			}
		}
		p := make([]byte, n)
		if _, err := io.ReadFull(c.r, p); err != nil {
			return 0, nil, err
		}
		if masked {
			for i := range p {
				p[i] ^= mask[i%4]
			}
		}
		op := int(h[0] & 0x0F)
		switch op {
		case opPing:
			if err := c.Write(opPong, p); err != nil {
				return 0, nil, err
			}
		case opPong:
		case opCl:
			c.Write(opCl, p)
			return 0, nil, io.EOF
		default:
			return op, p, nil
		}
	}
}

// Write sends a message.
func (c *Conn) Write(op int, p []byte) error {
	n := len(p)
	f := make([]byte, 0, n+14)
	f = append(f, 0x80|byte(op))
	var bit byte
	if c.client {
		bit = 0x80
	}
	switch {
	case n < 126:
		f = append(f, bit|byte(n))
	case n < 65536:
		f = append(f, bit|126, byte(n>>8), byte(n))
	default:
		f = append(f, bit|127)
		f = binary.BigEndian.AppendUint64(f, uint64(n))
	}
	if c.client {
		var m [4]byte
		rand.Read(m[:])
		f = append(f, m[:]...)
		start := len(f)
		f = append(f, p...)
		for i := start; i < len(f); i++ {
			f[i] ^= m[(i-start)%4]
		}
	} else {
		f = append(f, p...)
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.c.SetWriteDeadline(time.Now().Add(10 * time.Second))
	_, err := c.c.Write(f)
	return err
}
