// fakedoorbell stands in for doorbelld while developing the hub and the app. It speaks the
// JSON-RPC of the door station on /, relays video, sound and speaker of a real door station on
// /video, /audio and /speaker, and presses the button when it gets POST /ring.
//
//	fakedoorbell -listen :8090 -video doorbell.home.arpa:8080
//	curl -X POST localhost:8090/ring
package main

import (
	"flag"
	"log"
	"net/http"
	"sync"
	"time"

	"doorbell-hub/ws"
)

func main() {
	listen := flag.String("listen", ":8090", "address to listen on")
	video := flag.String("video", "", "real door station whose /video and /audio are relayed (host:port)")
	flag.Parse()

	var mu sync.Mutex
	conns := map[*ws.Conn]bool{}

	http.HandleFunc("POST /ring", func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		n := 0
		for c := range conns {
			if c.Write(ws.Text, []byte(`{"jsonrpc":"2.0","method":"button.pressed","params":{}}`)) == nil {
				n++
			}
		}
		mu.Unlock()
		log.Printf("button pressed, told %d connections", n)
		w.Write([]byte("ok\n"))
	})
	relay := func(w http.ResponseWriter, r *http.Request) {
		if *video == "" {
			http.Error(w, "start with -video to relay a real door station", http.StatusNotFound)
			return
		}
		c, err := ws.Upgrade(w, r, 4096)
		if err != nil {
			return
		}
		defer c.Close()
		src, err := ws.Dial(*video, r.URL.Path, "", 4<<20, 10*time.Second)
		if err != nil {
			log.Printf("%s: %v", r.URL.Path, err)
			return
		}
		defer src.Close()
		go func() { // passes on what the hub sends (/speaker) and ends the relay when it goes away
			for {
				op, p, err := c.Read()
				if err != nil || op == ws.Binary && src.Write(op, p) != nil {
					src.Close()
					return
				}
			}
		}()
		log.Printf("%s: relaying", r.URL.Path)
		for {
			op, p, err := src.Read()
			if err != nil || c.Write(op, p) != nil {
				log.Printf("%s: done", r.URL.Path)
				return
			}
		}
	}
	http.HandleFunc("GET /video", relay)
	http.HandleFunc("GET /audio", relay)
	http.HandleFunc("GET /speaker", relay)
	http.HandleFunc("GET /", func(w http.ResponseWriter, r *http.Request) {
		c, err := ws.Upgrade(w, r, 1<<20)
		if err != nil {
			return
		}
		mu.Lock()
		conns[c] = true
		mu.Unlock()
		log.Print("hub connected")
		for {
			if _, _, err := c.Read(); err != nil {
				break
			}
		}
		mu.Lock()
		delete(conns, c)
		mu.Unlock()
		c.Close()
		log.Print("hub disconnected")
	})
	log.Printf("fake door station on %s", *listen)
	log.Fatal(http.ListenAndServe(*listen, nil))
}
