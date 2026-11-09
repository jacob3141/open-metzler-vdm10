package main

import (
	"crypto/subtle"
	"embed"
	"encoding/json"
	"fmt"
	"html/template"
	"net/http"
	"net/url"
	"strings"
	"time"

	"doorbell-hub/ws"
)

//go:embed templates/index.html
var templates embed.FS

var page = template.Must(template.New("index.html").Funcs(template.FuncMap{
	"when": func(t time.Time) string {
		if t.IsZero() {
			return "never"
		}
		return t.Local().Format("2006-01-02 15:04:05")
	},
	"mb": func(n int64) string { return fmt.Sprintf("%.1f MB", float64(n)/1e6) },
}).ParseFS(templates, "templates/index.html"))

// Routes builds the HTTP interface of the hub.
func (h *Hub) Routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", h.admin(h.index))
	mux.HandleFunc("POST /admin/clients", h.admin(h.addClient))
	mux.HandleFunc("POST /admin/clients/{id}/delete", h.admin(h.deleteClient))
	mux.HandleFunc("POST /admin/messages/{id}/delete", h.admin(h.deleteMessage))
	mux.HandleFunc("GET /admin/messages/{id}", h.admin(h.serveMessage))

	mux.HandleFunc("GET /api/events", h.api(h.events))
	mux.HandleFunc("GET /api/video", h.api(h.video))
	mux.HandleFunc("GET /api/messages", h.api(h.listMessages))
	mux.HandleFunc("GET /api/messages/{id}", h.api(h.apiMessage))
	return mux
}

func sameOrigin(r *http.Request) bool {
	o := r.Header.Get("Origin")
	if o == "" {
		return r.Method == http.MethodGet || strings.Contains(r.Referer(), "//"+r.Host+"/")
	}
	u, err := url.Parse(o)
	return err == nil && u.Host == r.Host
}

func (h *Hub) admin(next http.HandlerFunc) http.HandlerFunc {
	want := []byte(h.cfg.AdminPassword)
	return func(w http.ResponseWriter, r *http.Request) {
		_, pass, ok := r.BasicAuth()
		if !ok || subtle.ConstantTimeCompare([]byte(pass), want) != 1 {
			w.Header().Set("WWW-Authenticate", `Basic realm="Doorbell hub", charset="UTF-8"`)
			http.Error(w, "Authentication required", http.StatusUnauthorized)
			return
		}
		if r.Method != http.MethodGet && !sameOrigin(r) {
			http.Error(w, "Foreign origin", http.StatusForbidden)
			return
		}
		w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'self' 'unsafe-inline'; media-src 'self'; form-action 'self'; frame-ancestors 'none'; base-uri 'none'")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "same-origin")
		next(w, r)
	}
}

type apiHandler func(w http.ResponseWriter, r *http.Request, c Client)

func (h *Hub) api(next apiHandler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		token, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
		c, valid := h.store.Authenticate(token)
		if !ok || !valid {
			http.Error(w, "Invalid token", http.StatusUnauthorized)
			return
		}
		next(w, r, c)
	}
}

type pageData struct {
	Status    Status
	Clients   []Client
	Messages  []Message
	NewName   string
	NewToken  string
	Timeout   int
	RecordFor int
}

func (h *Hub) render(w http.ResponseWriter, name, token string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if token != "" {
		w.Header().Set("Cache-Control", "no-store")
	}
	page.Execute(w, pageData{
		Status: h.Status(), Clients: h.store.Clients(), Messages: h.store.Messages(), NewName: name, NewToken: token,
		Timeout: int(h.cfg.RingTimeout.Seconds()), RecordFor: int(h.cfg.RecordFor.Seconds()),
	})
}

func (h *Hub) index(w http.ResponseWriter, r *http.Request) { h.render(w, "", "") }

func (h *Hub) addClient(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimSpace(r.FormValue("name"))
	if name == "" || len(name) > 64 {
		http.Error(w, "Name is missing or too long", http.StatusBadRequest)
		return
	}
	_, token, err := h.store.AddClient(name)
	if err != nil {
		http.Error(w, "Saving failed", http.StatusInternalServerError)
		return
	}
	h.render(w, name, token)
}

func (h *Hub) deleteClient(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	h.store.DeleteClient(id)
	h.dropSessions(id)
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

// dropSessions disconnects a deleted client immediately.
func (h *Hub) dropSessions(id string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for s := range h.sessions {
		if s.client.ID == id {
			s.conn.Close()
		}
	}
}

func (h *Hub) deleteMessage(w http.ResponseWriter, r *http.Request) {
	h.store.DeleteMessage(r.PathValue("id"))
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (h *Hub) serveMessage(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !validMessageID(id) {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "video/mp4")
	http.ServeFile(w, r, h.store.messagePath(id))
}

func (h *Hub) apiMessage(w http.ResponseWriter, r *http.Request, _ Client) { h.serveMessage(w, r) }

func (h *Hub) listMessages(w http.ResponseWriter, _ *http.Request, _ Client) {
	w.Header().Set("Content-Type", "application/json")
	m := h.store.Messages()
	if m == nil {
		m = []Message{}
	}
	json.NewEncoder(w).Encode(m)
}

type rpcRequest struct {
	ID     json.RawMessage `json:"id"`
	Method string          `json:"method"`
	Params struct {
		ID string `json:"id"`
	} `json:"params"`
}

// events is the JSON-RPC connection of a client app.
func (h *Hub) events(w http.ResponseWriter, r *http.Request, c Client) {
	conn, err := ws.Upgrade(w, r, 64*1024)
	if err != nil {
		return
	}
	defer conn.Close()
	s := &Session{client: c, conn: conn, out: make(chan []byte, 32)}
	h.addSession(s)
	defer h.removeSession(s)
	done := make(chan struct{})
	defer close(done)
	go func() {
		for {
			select {
			case m := <-s.out:
				if conn.Write(ws.Text, m) != nil {
					conn.Close()
					return
				}
			case <-done:
				return
			}
		}
	}()
	for {
		op, p, err := conn.Read()
		if err != nil {
			return
		}
		if op != ws.Text {
			continue
		}
		if reply := h.handleRPC(s, p); reply != nil {
			s.send(reply)
		}
	}
}

func (h *Hub) handleRPC(s *Session, p []byte) []byte {
	var req rpcRequest
	if err := json.Unmarshal(p, &req); err != nil {
		return rpcReply(json.RawMessage("null"), nil, -32700, "Parse error")
	}
	if req.ID == nil {
		return nil
	}
	switch req.Method {
	case "system.ping":
		return rpcReply(req.ID, "pong", 0, "")
	case "ring.accept":
		if h.Accept(s, req.Params.ID) {
			return rpcReply(req.ID, map[string]any{"video": "/api/video"}, 0, "")
		}
		return rpcReply(req.ID, nil, -32000, "Ring is no longer active")
	case "ring.reject":
		if h.Reject(s, req.Params.ID) {
			return rpcReply(req.ID, true, 0, "")
		}
		return rpcReply(req.ID, nil, -32000, "Ring is no longer active")
	}
	return rpcReply(req.ID, nil, -32601, "Method not found")
}

func rpcReply(id json.RawMessage, result any, code int, msg string) []byte {
	m := map[string]any{"jsonrpc": "2.0", "id": id}
	if code != 0 {
		m["error"] = map[string]any{"code": code, "message": msg}
	} else {
		m["result"] = result
	}
	b, _ := json.Marshal(m)
	return b
}

// video streams frames to a client app; the doorbell connection exists only while needed.
func (h *Hub) video(w http.ResponseWriter, r *http.Request, _ Client) {
	conn, err := ws.Upgrade(w, r, 4096)
	if err != nil {
		return
	}
	defer conn.Close()
	sub := h.feed.Subscribe()
	defer h.feed.Unsubscribe(sub)

	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			if _, _, err := conn.Read(); err != nil {
				return
			}
		}
	}()
	for {
		select {
		case f := <-sub.C:
			if conn.Write(ws.Binary, f.Data) != nil {
				return
			}
		case <-done:
			return
		}
	}
}
