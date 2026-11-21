package main

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"math/big"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"
)

// An app asks for access with POST /api/register and polls GET /api/register/{id} with the secret
// it got back. The admin approves or denies the request in the web interface. Both sides show the
// same code, so the admin can tell which phone is asking.
const (
	maxPending  = 10
	pendingLife = 10 * time.Minute
)

type registration struct {
	ID       string    `json:"id"`
	Name     string    `json:"name"`
	Code     string    `json:"code"`
	Created  time.Time `json:"created"`
	secret   string
	approved bool
	denied   bool
	token    string // set on approval until the app fetches it
	until    time.Time
}

// Registrations holds the open requests in memory. After a restart of the hub, apps ask again.
type Registrations struct {
	mu   sync.Mutex
	reqs map[string]*registration
	now  func() time.Time
}

func NewRegistrations() *Registrations {
	return &Registrations{reqs: map[string]*registration{}, now: time.Now}
}

func randomCode() string {
	n, _ := rand.Int(rand.Reader, big.NewInt(1_000_000))
	return fmt.Sprintf("%06d", n.Int64())
}

// expire drops old requests. r.mu must be held.
func (r *Registrations) expire() {
	now := r.now()
	for id, q := range r.reqs {
		if now.After(q.until) {
			delete(r.reqs, id)
		}
	}
}

// Request creates a request. It fails when too many are open.
func (r *Registrations) Request(name string) (*registration, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.expire()
	open := 0
	for _, q := range r.reqs {
		if !q.approved && !q.denied {
			open++
		}
	}
	if open >= maxPending {
		return nil, false
	}
	now := r.now()
	q := &registration{ID: randomHex(8), Name: name, Code: randomCode(), Created: now, secret: randomHex(32), until: now.Add(pendingLife)}
	r.reqs[q.ID] = q
	c := *q
	return &c, true
}

// Pending lists the requests that wait for the admin, oldest first.
func (r *Registrations) Pending() []registration {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.expire()
	var out []registration
	for _, q := range r.reqs {
		if !q.approved && !q.denied {
			out = append(out, *q)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Created.Before(out[j].Created) })
	return out
}

// Decide approves (with the token of the new client) or denies a pending request.
func (r *Registrations) Decide(id string, approve func(name string) (string, error)) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.expire()
	q := r.reqs[id]
	if q == nil || q.approved || q.denied {
		return fmt.Errorf("request %q is not pending", id)
	}
	q.until = r.now().Add(pendingLife)
	if approve == nil {
		q.denied = true
		return nil
	}
	token, err := approve(q.Name)
	if err != nil {
		return err
	}
	q.approved, q.token = true, token
	return nil
}

// Poll returns the state of a request for the app that made it. An approved request hands out
// its token once and is then gone.
func (r *Registrations) Poll(id, secret string) (status, token string, ok bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.expire()
	q := r.reqs[id]
	if q == nil || subtle.ConstantTimeCompare([]byte(secret), []byte(q.secret)) != 1 {
		return "", "", false
	}
	switch {
	case q.approved:
		delete(r.reqs, id)
		return "approved", q.token, true
	case q.denied:
		delete(r.reqs, id)
		return "denied", "", true
	}
	return "pending", "", true
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(v)
}

// fromBrowserPage is true for requests that a web page on another site sends.
func fromBrowserPage(r *http.Request) bool {
	s := r.Header.Get("Sec-Fetch-Site")
	return s != "" && s != "none" && s != "same-origin"
}

func (h *Hub) register(w http.ResponseWriter, r *http.Request) {
	if fromBrowserPage(r) {
		http.Error(w, "Foreign origin", http.StatusForbidden)
		return
	}
	var req struct {
		Name string `json:"name"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "JSON with a name expected"})
		return
	}
	name := strings.TrimSpace(req.Name)
	if name == "" || len(name) > 64 || strings.ContainsAny(name, "\r\n\x00") {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "The name is missing or too long"})
		return
	}
	q, ok := h.regs.Request(name)
	if !ok {
		writeJSON(w, http.StatusTooManyRequests, map[string]string{"error": "Too many open requests, try again later"})
		return
	}
	writeJSON(w, http.StatusCreated, map[string]string{"id": q.ID, "secret": q.secret, "code": q.Code})
}

func (h *Hub) registerStatus(w http.ResponseWriter, r *http.Request) {
	secret, _ := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	status, token, ok := h.regs.Poll(r.PathValue("id"), secret)
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "Unknown or expired request"})
		return
	}
	res := map[string]string{"status": status}
	if token != "" {
		res["token"] = token
	}
	writeJSON(w, http.StatusOK, res)
}

func (h *Hub) approveRegistration(w http.ResponseWriter, r *http.Request) {
	err := h.regs.Decide(r.PathValue("id"), func(name string) (string, error) {
		_, token, err := h.store.AddClient(name)
		return token, err
	})
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (h *Hub) denyRegistration(w http.ResponseWriter, r *http.Request) {
	if err := h.regs.Decide(r.PathValue("id"), nil); err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

// pendingJSON lets the admin page show new requests without a reload.
func (h *Hub) pendingJSON(w http.ResponseWriter, _ *http.Request) {
	p := h.regs.Pending()
	if p == nil {
		p = []registration{}
	}
	writeJSON(w, http.StatusOK, p)
}
