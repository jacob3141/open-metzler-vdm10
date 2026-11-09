// doorbell-hub connects the door station to the client apps and records visitor messages.
package main

import (
	"log"
	"net/http"
	"os"
	"strconv"
	"time"
)

func env(name, def string) string {
	if v := os.Getenv(name); v != "" {
		return v
	}
	return def
}

func envSeconds(name string, def int) time.Duration {
	if n, err := strconv.Atoi(os.Getenv(name)); err == nil && n > 0 {
		return time.Duration(n) * time.Second
	}
	return time.Duration(def) * time.Second
}

func main() {
	cfg := Config{
		Listen:        env("HUB_LISTEN", ":8088"),
		Doorbell:      env("HUB_DOORBELL", "doorbell.home.arpa:8080"),
		Data:          env("HUB_DATA", "/data"),
		AdminPassword: os.Getenv("HUB_ADMIN_PASSWORD"),
		RingTimeout:   envSeconds("HUB_RING_TIMEOUT", 30),
		RecordFor:     envSeconds("HUB_RECORD_SECONDS", 30),
		MaxMessages:   200,
	}
	if n, err := strconv.Atoi(os.Getenv("HUB_MAX_MESSAGES")); err == nil && n > 0 {
		cfg.MaxMessages = n
	}
	if cfg.AdminPassword == "" {
		log.Fatal("HUB_ADMIN_PASSWORD must be set")
	}
	store, err := OpenStore(cfg.Data)
	if err != nil {
		log.Fatal(err)
	}
	h := NewHub(cfg, store)
	go h.WatchDoorbell()
	log.Printf("hub listening on %s, doorbell %s", cfg.Listen, cfg.Doorbell)
	srv := &http.Server{Addr: cfg.Listen, Handler: h.Routes(), ReadHeaderTimeout: 10 * time.Second}
	log.Fatal(srv.ListenAndServe())
}
