// fakedoorbell stands in for doorbelld while developing the hub and the app. It speaks the
// JSON-RPC of the door station on /, relays video, sound and speaker of a real door station on
// /video, /audio and /speaker, and presses the button when it gets POST /ring.
// POST /tamper?open=1 takes the station off the wall and sounds its siren (alarm.stop
// silences it), POST /tamper?open=0 puts it back.
//
//	fakedoorbell -listen :8090 -video doorbell.home.arpa:8080
//	curl -X POST localhost:8090/ring
package main

import (
	"flag"
	"fmt"
	"log"
	"net/http"
	"strings"
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
	open, alarm := false, false
	// tamper tells every hub the state of the tamper switch. mu must be held.
	tamper := func(c *ws.Conn) {
		m := fmt.Sprintf(`{"jsonrpc":"2.0","method":"tamper","params":{"open":%t,"alarm":%t,"time":%d}}`, open, alarm, time.Now().Unix())
		for h := range conns {
			if c == nil || h == c {
				h.Write(ws.Text, []byte(m))
			}
		}
	}

	http.HandleFunc("POST /tamper", func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		open = r.FormValue("open") == "1"
		if open {
			alarm = true
		}
		tamper(nil)
		mu.Unlock()
		log.Printf("tamper: open %t, siren %t", open, alarm)
		w.Write([]byte("ok\n"))
	})

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
		tamper(c)
		mu.Unlock()
		log.Print("hub connected")
		for {
			_, p, err := c.Read()
			if err != nil {
				break
			}
			if strings.Contains(string(p), `"alarm.stop"`) {
				mu.Lock()
				alarm = false
				tamper(nil)
				mu.Unlock()
				log.Print("tamper: siren stopped")
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
