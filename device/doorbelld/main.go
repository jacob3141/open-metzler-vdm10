// doorbelld: WebSocket server of the door station, speaking JSON-RPC 2.0.
package main

import (
	"bufio"
	"crypto/sha1"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const (
	wsGUID     = "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"
	maxPayload = 64 * 1024
	opText     = 0x1
	opBinary   = 0x2
	opClose    = 0x8
	opPing     = 0x9
	opPong     = 0xA
	evKey      = 1
	eventSize  = 16 // struct input_event on 32-bit ARM

	maxVideoFrame = 4 << 20
)

var errProtocol = errors.New("websocket protocol error")

type client struct {
	conn    net.Conn
	out     chan []byte
	video   bool
	needKey bool
}

type hub struct {
	mu      sync.Mutex
	clients map[*client]struct{}
	started time.Time
}

func (h *hub) add(c *client) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.clients[c] = struct{}{}
}

func (h *hub) remove(c *client) {
	h.mu.Lock()
	defer h.mu.Unlock()
	delete(h.clients, c)
}

func (h *hub) count() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.clients)
}

// A client that stops reading loses its connection instead of holding up everyone else.
func (h *hub) broadcast(msg []byte) {
	frame := makeFrame(opText, msg)
	h.mu.Lock()
	defer h.mu.Unlock()
	for c := range h.clients {
		if c.video {
			continue
		}
		select {
		case c.out <- frame:
		default:
			c.conn.Close()
		}
	}
}

// A client that falls behind loses frames and rejoins at the next keyframe.
func (h *hub) broadcastVideo(data []byte, key bool) {
	var frame []byte
	h.mu.Lock()
	defer h.mu.Unlock()
	for c := range h.clients {
		if !c.video || (c.needKey && !key) {
			continue
		}
		if frame == nil {
			frame = makeFrame(opBinary, data)
		}
		select {
		case c.out <- frame:
			c.needKey = false
		default:
			c.needKey = true
		}
	}
}

func makeFrame(op byte, payload []byte) []byte {
	n := len(payload)
	var f []byte
	switch {
	case n < 126:
		f = []byte{0x80 | op, byte(n)}
	case n < 65536:
		f = []byte{0x80 | op, 126, byte(n >> 8), byte(n)}
	default:
		f = make([]byte, 10)
		f[0], f[1] = 0x80|op, 127
		binary.BigEndian.PutUint64(f[2:], uint64(n))
	}
	return append(f, payload...)
}

func readFrame(r io.Reader) (byte, []byte, error) {
	var h [2]byte
	if _, err := io.ReadFull(r, h[:]); err != nil {
		return 0, nil, err
	}
	if h[0]&0x80 == 0 || h[0]&0x70 != 0 || h[0]&0x0F == 0 || h[1]&0x80 == 0 {
		return 0, nil, errProtocol
	}
	n := uint64(h[1] & 0x7F)
	switch n {
	case 126:
		var b [2]byte
		if _, err := io.ReadFull(r, b[:]); err != nil {
			return 0, nil, err
		}
		n = uint64(binary.BigEndian.Uint16(b[:]))
	case 127:
		var b [8]byte
		if _, err := io.ReadFull(r, b[:]); err != nil {
			return 0, nil, err
		}
		n = binary.BigEndian.Uint64(b[:])
	}
	if n > maxPayload {
		return 0, nil, errProtocol
	}
	var mask [4]byte
	if _, err := io.ReadFull(r, mask[:]); err != nil {
		return 0, nil, err
	}
	p := make([]byte, n)
	if _, err := io.ReadFull(r, p); err != nil {
		return 0, nil, err
	}
	for i := range p {
		p[i] ^= mask[i%4]
	}
	return h[0] & 0x0F, p, nil
}

func acceptKey(key string) string {
	s := sha1.Sum([]byte(key + wsGUID))
	return base64.StdEncoding.EncodeToString(s[:])
}

func handshake(br *bufio.Reader, conn net.Conn) (string, error) {
	line, err := br.ReadString('\n')
	if err != nil {
		return "", err
	}
	fields := strings.Fields(line)
	if len(fields) < 2 || fields[0] != "GET" {
		return "", errProtocol
	}
	var key string
	upgrade := false
	for i := 0; ; i++ {
		l, err := br.ReadString('\n')
		if err != nil {
			return "", err
		}
		l = strings.TrimSpace(l)
		if l == "" {
			break
		}
		if i > 100 {
			return "", errProtocol
		}
		name, val, ok := strings.Cut(l, ":")
		if !ok {
			continue
		}
		val = strings.TrimSpace(val)
		switch strings.ToLower(name) {
		case "upgrade":
			upgrade = strings.EqualFold(val, "websocket")
		case "sec-websocket-key":
			key = val
		}
	}
	if !upgrade || key == "" {
		io.WriteString(conn, "HTTP/1.1 426 Upgrade Required\r\nUpgrade: websocket\r\nConnection: close\r\nContent-Length: 0\r\n\r\n")
		return "", errProtocol
	}
	_, err = fmt.Fprintf(conn, "HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Accept: %s\r\n\r\n", acceptKey(key))
	return fields[1], err
}

type rpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Method  string          `json:"method"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

// dispatch returns nil for notifications, which get no response.
func (h *hub) dispatch(msg []byte) []byte {
	var req rpcRequest
	resp := rpcResponse{JSONRPC: "2.0", ID: json.RawMessage("null")}
	if err := json.Unmarshal(msg, &req); err != nil {
		var syn *json.SyntaxError
		if errors.As(err, &syn) {
			resp.Error = &rpcError{-32700, "Parse error"}
		} else {
			resp.Error = &rpcError{-32600, "Invalid Request"}
		}
		return encode(resp)
	}
	if req.JSONRPC != "2.0" || req.Method == "" {
		resp.Error = &rpcError{-32600, "Invalid Request"}
		return encode(resp)
	}
	if req.ID == nil {
		return nil
	}
	resp.ID = req.ID
	switch req.Method {
	case "system.ping":
		resp.Result = "pong"
	case "system.info":
		resp.Result = map[string]any{
			"device":   "Metzler VDM10-VM-POE",
			"uptime_s": int64(time.Since(h.started).Seconds()),
			"clients":  h.count(),
		}
	default:
		resp.Error = &rpcError{-32601, "Method not found"}
	}
	return encode(resp)
}

func encode(v any) []byte {
	b, _ := json.Marshal(v)
	return b
}

func (h *hub) serve(conn net.Conn) {
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(10 * time.Second))
	br := bufio.NewReader(conn)
	path, err := handshake(br, conn)
	if err != nil {
		return
	}
	conn.SetDeadline(time.Time{})

	c := &client{conn: conn, out: make(chan []byte, 64), video: path == "/video", needKey: true}
	h.add(c)
	defer h.remove(c)
	done := make(chan struct{})
	defer close(done)
	go func() {
		for {
			select {
			case f := <-c.out:
				conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
				if _, err := conn.Write(f); err != nil {
					conn.Close()
					return
				}
			case <-done:
				return
			}
		}
	}()

	for {
		op, p, err := readFrame(br)
		if err != nil {
			return
		}
		var reply []byte
		switch op {
		case opText:
			if c.video {
				continue
			}
			if r := h.dispatch(p); r != nil {
				reply = makeFrame(opText, r)
			}
		case opPing:
			reply = makeFrame(opPong, p)
		case opPong:
		case opClose:
			select {
			case c.out <- makeFrame(opClose, p):
			default:
			}
			time.Sleep(100 * time.Millisecond)
			return
		default:
			return
		}
		if reply != nil {
			select {
			case c.out <- reply:
			default:
				return
			}
		}
	}
}

func (h *hub) watchButton(path string, code uint16) {
	for {
		f, err := os.Open(path)
		if err != nil {
			log.Printf("button: %v", err)
			time.Sleep(5 * time.Second)
			continue
		}
		buf := make([]byte, eventSize)
		for {
			if _, err = io.ReadFull(f, buf); err != nil {
				log.Printf("button: %v", err)
				break
			}
			typ := binary.LittleEndian.Uint16(buf[8:])
			key := binary.LittleEndian.Uint16(buf[10:])
			val := int32(binary.LittleEndian.Uint32(buf[12:]))
			if typ == evKey && key == code && val == 1 {
				log.Print("button pressed")
				h.broadcast(encode(map[string]any{
					"jsonrpc": "2.0",
					"method":  "button.pressed",
					"params":  map[string]any{"time": int64(binary.LittleEndian.Uint32(buf[0:]))},
				}))
			}
		}
		f.Close()
		time.Sleep(time.Second)
	}
}

// runMedia starts mediad and hands its frames to the clients of /video.
func (h *hub) runMedia(path string) {
	for {
		cmd := exec.Command(path)
		cmd.Env = append(os.Environ(), "LD_LIBRARY_PATH="+filepath.Dir(path))
		cmd.Stderr = os.Stderr
		out, err := cmd.StdoutPipe()
		if err == nil {
			err = cmd.Start()
		}
		if err != nil {
			log.Printf("mediad: %v", err)
			time.Sleep(10 * time.Second)
			continue
		}
		err = h.readMedia(bufio.NewReaderSize(out, 256*1024))
		cmd.Process.Kill()
		log.Printf("mediad exited: %v (%v)", err, cmd.Wait())
		time.Sleep(5 * time.Second)
	}
}

func (h *hub) readMedia(r io.Reader) error {
	var hdr [5]byte
	for {
		if _, err := io.ReadFull(r, hdr[:]); err != nil {
			return err
		}
		n := binary.LittleEndian.Uint32(hdr[:4])
		if n == 0 || n > maxVideoFrame {
			return fmt.Errorf("invalid frame length %d", n)
		}
		data := make([]byte, n)
		if _, err := io.ReadFull(r, data); err != nil {
			return err
		}
		h.broadcastVideo(data, hdr[4]&1 != 0)
	}
}

func main() {
	listen := flag.String("listen", ":8080", "address of the WebSocket server")
	media := flag.String("media", "/home/app/oi/mediad", "path to mediad, empty to disable")
	input := flag.String("input", "/dev/event0", "input device of the bell button")
	code := flag.Uint("key", 5, "key code of the bell button")
	flag.Parse()

	h := &hub{clients: map[*client]struct{}{}, started: time.Now()}
	go h.watchButton(*input, uint16(*code))
	if *media != "" {
		go h.runMedia(*media)
	}
	ln, err := net.Listen("tcp", *listen)
	if err != nil {
		log.Fatal(err)
	}
	log.Printf("listening on %s", *listen)
	for {
		conn, err := ln.Accept()
		if err != nil {
			log.Print(err)
			time.Sleep(100 * time.Millisecond)
			continue
		}
		go h.serve(conn)
	}
}
