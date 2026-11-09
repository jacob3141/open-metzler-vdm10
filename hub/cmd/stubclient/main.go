// stubclient stands in for the future Android app: it connects to the hub and reacts to rings.
//
//	stubclient -hub localhost:8088 -token <token> -mode accept|reject|ignore [-video 5]
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"time"

	"doorbell-hub/ws"
)

func main() {
	hub := flag.String("hub", "localhost:8088", "address of the hub")
	token := flag.String("token", "", "token from the web interface")
	mode := flag.String("mode", "accept", "reaction to rings: accept, reject or ignore")
	videoSecs := flag.Int("video", 5, "seconds of video after answering")
	flag.Parse()

	auth := fmt.Sprintf("Authorization: Bearer %s\r\n", *token)
	c, err := ws.Dial(*hub, "/api/events", auth, 1<<20, 10*time.Second)
	if err != nil {
		log.Fatal(err)
	}
	defer c.Close()
	log.Print("connected, waiting for a ring")

	next := 1
	for {
		op, p, err := c.Read()
		if err != nil {
			log.Fatal(err)
		}
		if op != ws.Text {
			continue
		}
		var m struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
			Params struct {
				ID string `json:"id"`
			} `json:"params"`
			Result json.RawMessage `json:"result"`
		}
		json.Unmarshal(p, &m)
		log.Printf("received: %s", p)
		if m.Method == "ring" && *mode != "ignore" {
			method := "ring." + *mode
			req := fmt.Sprintf(`{"jsonrpc":"2.0","id":%d,"method":%q,"params":{"id":%q}}`, next, method, m.Params.ID)
			next++
			if err := c.Write(ws.Text, []byte(req)); err != nil {
				log.Fatal(err)
			}
		}
		if len(m.Result) > 0 && string(m.Result) != "true" && *mode == "accept" {
			watch(*hub, auth, *videoSecs)
		}
	}
}

func watch(hub, auth string, secs int) {
	v, err := ws.Dial(hub, "/api/video", auth, 4<<20, 10*time.Second)
	if err != nil {
		log.Printf("video: %v", err)
		return
	}
	defer v.Close()
	frames, size := 0, 0
	end := time.Now().Add(time.Duration(secs) * time.Second)
	for time.Now().Before(end) {
		_, p, err := v.Read()
		if err != nil {
			break
		}
		frames++
		size += len(p)
	}
	log.Printf("video: %d frames, %d bytes in %d s", frames, size, secs)
}
