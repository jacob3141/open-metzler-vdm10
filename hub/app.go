package main

import (
	"net/http"
	"os"

	"rsc.io/qr"
)

// The hub hands out the Android app, so a phone in the home network installs it from the
// admin page: scan the QR code, open the link, install. The Docker image carries the app
// that CI built for this version of the hub.

// hasApp tells whether the hub has an app to hand out.
func (h *Hub) hasApp() bool {
	fi, err := os.Stat(h.cfg.App)
	return err == nil && fi.Mode().IsRegular()
}

// appURL is where a phone downloads the app, under the name the admin page was opened with.
func appURL(r *http.Request) string {
	return "http://" + r.Host + "/app.apk"
}

// serveApp needs no login: the app holds no secret, a phone only gets access once approved.
func (h *Hub) serveApp(w http.ResponseWriter, r *http.Request) {
	f, err := os.Open(h.cfg.App)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil || !fi.Mode().IsRegular() {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "application/vnd.android.package-archive")
	w.Header().Set("Content-Disposition", `attachment; filename="doorbell.apk"`)
	w.Header().Set("Cache-Control", "no-cache")
	http.ServeContent(w, r, "doorbell.apk", fi.ModTime(), f)
}

func appQR(w http.ResponseWriter, r *http.Request) {
	code, err := qr.Encode(appURL(r), qr.M)
	if err != nil {
		http.Error(w, "QR code failed", http.StatusInternalServerError)
		return
	}
	code.Scale = 6
	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("Cache-Control", "no-cache")
	w.Write(code.PNG())
}
