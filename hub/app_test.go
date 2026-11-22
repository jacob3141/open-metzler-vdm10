package main

import (
	"bytes"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestHubHandsOutTheApp(t *testing.T) {
	h := newHub(t, "127.0.0.1:1")
	h.cfg.App = filepath.Join(t.TempDir(), "doorbell.apk")
	addr := serve(t, h)

	// without an app: no download, no section on the admin page
	if res, _ := http.Get("http://" + addr + "/app.apk"); res.StatusCode != http.StatusNotFound {
		t.Fatalf("no app: status %d", res.StatusCode)
	}
	body, _ := io.ReadAll(adminGet(t, addr, "/", true, "").Body)
	if strings.Contains(string(body), "app-qr.png") {
		t.Fatal("admin page offers an app the hub does not have")
	}

	apk := []byte("PK\x03\x04 the app")
	if err := os.WriteFile(h.cfg.App, apk, 0o644); err != nil {
		t.Fatal(err)
	}
	// the phone downloads without the admin password
	res, err := http.Get("http://" + addr + "/app.apk")
	if err != nil {
		t.Fatal(err)
	}
	got, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if res.StatusCode != http.StatusOK || !bytes.Equal(got, apk) || res.Header.Get("Content-Type") != "application/vnd.android.package-archive" {
		t.Fatalf("download: %d %q %q", res.StatusCode, res.Header.Get("Content-Type"), got)
	}
	body, _ = io.ReadAll(adminGet(t, addr, "/", true, "").Body)
	if !strings.Contains(string(body), "http://"+addr+"/app.apk") || !strings.Contains(string(body), "/admin/app-qr.png") {
		t.Fatal("admin page has no download link and QR code")
	}
	qr := adminGet(t, addr, "/admin/app-qr.png", true, "")
	png, _ := io.ReadAll(qr.Body)
	if qr.StatusCode != http.StatusOK || !bytes.HasPrefix(png, []byte("\x89PNG")) {
		t.Fatalf("QR code: %d", qr.StatusCode)
	}
	if res := adminGet(t, addr, "/admin/app-qr.png", false, ""); res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("QR code without password: %d", res.StatusCode)
	}
}
