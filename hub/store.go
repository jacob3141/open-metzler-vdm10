package main

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// Client is a registered device. Only the hash of its token is stored.
type Client struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	TokenHash string    `json:"token_hash"`
	Created   time.Time `json:"created"`
	LastSeen  time.Time `json:"last_seen"`
}

// Message is a recorded visitor message.
type Message struct {
	ID   string    `json:"id"`
	Time time.Time `json:"time"`
	Size int64     `json:"size"`
}

// Store keeps clients and messages in the data directory.
type Store struct {
	mu      sync.Mutex
	dir     string
	clients []Client
}

func randomHex(n int) string {
	b := make([]byte, n)
	rand.Read(b)
	return hex.EncodeToString(b)
}

func hashToken(t string) string {
	s := sha256.Sum256([]byte(t))
	return hex.EncodeToString(s[:])
}

func OpenStore(dir string) (*Store, error) {
	if err := os.MkdirAll(filepath.Join(dir, "messages"), 0o750); err != nil {
		return nil, err
	}
	s := &Store{dir: dir}
	b, err := os.ReadFile(s.clientsFile())
	if errors.Is(err, os.ErrNotExist) {
		return s, nil
	}
	if err != nil {
		return nil, err
	}
	return s, json.Unmarshal(b, &s.clients)
}

func (s *Store) clientsFile() string { return filepath.Join(s.dir, "clients.json") }

func (s *Store) save() error {
	b, err := json.MarshalIndent(s.clients, "", " ")
	if err != nil {
		return err
	}
	tmp := s.clientsFile() + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, s.clientsFile())
}

// AddClient creates a client and returns its token once.
func (s *Store) AddClient(name string) (Client, string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	token := randomHex(32)
	c := Client{ID: randomHex(8), Name: name, TokenHash: hashToken(token), Created: time.Now()}
	s.clients = append(s.clients, c)
	return c, token, s.save()
}

func (s *Store) DeleteClient(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, c := range s.clients {
		if c.ID == id {
			s.clients = append(s.clients[:i], s.clients[i+1:]...)
			return s.save()
		}
	}
	return os.ErrNotExist
}

func (s *Store) Clients() []Client {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Client(nil), s.clients...)
}

// Authenticate finds the client of a token and records the access.
func (s *Store) Authenticate(token string) (Client, bool) {
	if token == "" {
		return Client{}, false
	}
	h := hashToken(token)
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.clients {
		if s.clients[i].TokenHash == h {
			s.clients[i].LastSeen = time.Now()
			s.save()
			return s.clients[i], true
		}
	}
	return Client{}, false
}

func (s *Store) messagePath(id string) string {
	return filepath.Join(s.dir, "messages", id+".mp4")
}

func validMessageID(id string) bool {
	if id == "" || len(id) > 32 {
		return false
	}
	for _, r := range id {
		if !(r >= '0' && r <= '9' || r == '-') {
			return false
		}
	}
	return true
}

func (s *Store) Messages() []Message {
	entries, _ := os.ReadDir(filepath.Join(s.dir, "messages"))
	var out []Message
	for _, e := range entries {
		id, ok := strings.CutSuffix(e.Name(), ".mp4")
		if !ok || !validMessageID(id) {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		out = append(out, Message{ID: id, Time: info.ModTime(), Size: info.Size()})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID > out[j].ID })
	return out
}

func (s *Store) DeleteMessage(id string) error {
	if !validMessageID(id) {
		return os.ErrNotExist
	}
	return os.Remove(s.messagePath(id))
}

// Prune deletes the oldest messages above the limit.
func (s *Store) Prune(max int) {
	m := s.Messages()
	for i := max; i < len(m); i++ {
		s.DeleteMessage(m[i].ID)
	}
}

// SaveMessage stores a message atomically and returns its ID.
func (s *Store) SaveMessage(data []byte, t time.Time) (string, error) {
	id := t.Format("20060102-150405")
	tmp := s.messagePath(id) + ".tmp"
	if err := os.WriteFile(tmp, data, 0o640); err != nil {
		return "", err
	}
	return id, os.Rename(tmp, s.messagePath(id))
}
