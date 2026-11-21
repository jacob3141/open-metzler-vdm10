package main

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

func postJSON(t *testing.T, url, body string, header map[string]string) (*http.Response, map[string]string) {
	t.Helper()
	req, _ := http.NewRequest("POST", url, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	for k, v := range header {
		req.Header.Set(k, v)
	}
	return doJSON(t, req)
}

func doJSON(t *testing.T, req *http.Request) (*http.Response, map[string]string) {
	t.Helper()
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(res.Body)
	var m map[string]string
	json.Unmarshal(b, &m)
	return res, m
}

func pollReg(t *testing.T, addr, id, secret string) (*http.Response, map[string]string) {
	t.Helper()
	req, _ := http.NewRequest("GET", "http://"+addr+"/api/register/"+id, nil)
	req.Header.Set("Authorization", "Bearer "+secret)
	return doJSON(t, req)
}

func adminPost(t *testing.T, addr, path string) int {
	t.Helper()
	req, _ := http.NewRequest("POST", "http://"+addr+path, nil)
	req.SetBasicAuth("admin", "pw")
	req.Header.Set("Origin", "http://"+addr)
	c := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	res, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	return res.StatusCode
}

func TestRegistrationApproved(t *testing.T) {
	h := newHub(t, fakeDoorbell(t))
	addr := serve(t, h)
	res, reg := postJSON(t, "http://"+addr+"/api/register", `{"name":"Pixel 8"}`, nil)
	if res.StatusCode != http.StatusCreated || reg["id"] == "" || reg["secret"] == "" || len(reg["code"]) != 6 {
		t.Fatal(res.Status, reg)
	}
	if _, m := pollReg(t, addr, reg["id"], reg["secret"]); m["status"] != "pending" {
		t.Fatal(m)
	}
	if res, _ := pollReg(t, addr, reg["id"], "wrong"); res.StatusCode != http.StatusNotFound {
		t.Fatal("wrong secret accepted:", res.Status)
	}

	// The admin page lists the request with its code.
	req, _ := http.NewRequest("GET", "http://"+addr+"/", nil)
	req.SetBasicAuth("admin", "pw")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	page, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if !strings.Contains(string(page), reg["code"]) || !strings.Contains(string(page), "Pixel 8") {
		t.Fatal("request missing on the admin page")
	}

	if code := adminPost(t, addr, "/admin/registrations/"+reg["id"]+"/approve"); code != http.StatusSeeOther {
		t.Fatal(code)
	}
	_, m := pollReg(t, addr, reg["id"], reg["secret"])
	if m["status"] != "approved" || m["token"] == "" {
		t.Fatal(m)
	}
	if c, ok := h.store.Authenticate(m["token"]); !ok || c.Name != "Pixel 8" {
		t.Fatal("token does not work", c, ok)
	}
	// The token is handed out once.
	if res, _ := pollReg(t, addr, reg["id"], reg["secret"]); res.StatusCode != http.StatusNotFound {
		t.Fatal("token handed out twice:", res.Status)
	}
	connect(t, addr, m["token"])
	waitSessions(t, h, 1)

	// The app can remove itself again; its connection ends.
	del, _ := http.NewRequest("DELETE", "http://"+addr+"/api/client", nil)
	del.Header.Set("Authorization", "Bearer "+m["token"])
	if res, _ := doJSON(t, del); res.StatusCode != http.StatusNoContent {
		t.Fatal(res.Status)
	}
	waitSessions(t, h, 0)
	if _, ok := h.store.Authenticate(m["token"]); ok {
		t.Fatal("token still valid after removal")
	}
}

func TestRegistrationDenied(t *testing.T) {
	h := newHub(t, fakeDoorbell(t))
	addr := serve(t, h)
	_, reg := postJSON(t, "http://"+addr+"/api/register", `{"name":"Unknown"}`, nil)
	if code := adminPost(t, addr, "/admin/registrations/"+reg["id"]+"/deny"); code != http.StatusSeeOther {
		t.Fatal(code)
	}
	if _, m := pollReg(t, addr, reg["id"], reg["secret"]); m["status"] != "denied" || m["token"] != "" {
		t.Fatal(m)
	}
	if len(h.store.Clients()) != 0 {
		t.Fatal("denied request created a client")
	}
	// Approving afterwards is not possible.
	if code := adminPost(t, addr, "/admin/registrations/"+reg["id"]+"/approve"); code != http.StatusNotFound {
		t.Fatal(code)
	}
}

func TestRegistrationLimits(t *testing.T) {
	h := newHub(t, fakeDoorbell(t))
	addr := serve(t, h)
	url := "http://" + addr + "/api/register"
	for _, body := range []string{`{}`, `{"name":""}`, `{"name":"` + strings.Repeat("x", 65) + `"}`, `no json`} {
		if res, _ := postJSON(t, url, body, nil); res.StatusCode != http.StatusBadRequest {
			t.Fatalf("%s: %s", body, res.Status)
		}
	}
	if res, _ := postJSON(t, url, `{"name":"x"}`, map[string]string{"Sec-Fetch-Site": "cross-site"}); res.StatusCode != http.StatusForbidden {
		t.Fatal("request from a foreign page accepted:", res.Status)
	}
	for i := 0; i < maxPending; i++ {
		if res, _ := postJSON(t, url, `{"name":"x"}`, nil); res.StatusCode != http.StatusCreated {
			t.Fatal(i, res.Status)
		}
	}
	if res, _ := postJSON(t, url, `{"name":"x"}`, nil); res.StatusCode != http.StatusTooManyRequests {
		t.Fatal("limit not enforced:", res.Status)
	}
	// Admin endpoints need the password.
	req, _ := http.NewRequest("GET", "http://"+addr+"/admin/registrations", nil)
	if res, _ := doJSON(t, req); res.StatusCode != http.StatusUnauthorized {
		t.Fatal(res.Status)
	}
}

func TestRegistrationExpires(t *testing.T) {
	r := NewRegistrations()
	now := time.Now()
	r.now = func() time.Time { return now }
	q, _ := r.Request("Phone")
	now = now.Add(pendingLife + time.Second)
	if len(r.Pending()) != 0 {
		t.Fatal("request did not expire")
	}
	if _, _, ok := r.Poll(q.ID, q.secret); ok {
		t.Fatal("expired request still answers")
	}
}

// A client that stops answering pings is dropped, so rings do not wait for it.
func TestSilentClientIsDropped(t *testing.T) {
	old := eventsPing
	eventsPing = 50 * time.Millisecond
	t.Cleanup(func() { eventsPing = old })
	h := newHub(t, fakeDoorbell(t))
	addr := serve(t, h)
	_, token, _ := h.store.AddClient("phone")

	live := connect(t, addr, token)
	go func() {
		for {
			if _, _, err := live.Read(); err != nil { // Read answers pings
				return
			}
		}
	}()
	connect(t, addr, token) // never reads, so never answers a ping
	waitSessions(t, h, 2)
	waitSessions(t, h, 1)
	time.Sleep(4 * eventsPing)
	waitSessions(t, h, 1) // the live one stays
}
