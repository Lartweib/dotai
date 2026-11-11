package server

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"net/http"
	"strings"

	"github.com/lartweib/dotai/internal/envelope"
)

// NewToken returns a random secret for one run of the server: 32 bytes
// from crypto/rand, in unpadded base64url. A run makes two, the one-time
// code and the session token.
func NewToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("making a secret of the web UI: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// CodeParam names the one-time code in the fragment of the address dotai
// ui opens. A fragment never reaches the server or a Referer header.
const CodeParam = "code"

// sessionPath is the one API path that needs no session token: it is the
// path that gives it.
const sessionPath = "/api/session"

// guard lets a request reach next only when the request is for this
// server, and, on the API, carries the session token:
//
//   - its Host is 127.0.0.1:<port> or localhost:<port>, which a page that
//     renamed its own host to 127.0.0.1 by DNS rebinding does not send;
//   - on /api/, its Origin, when it has one, is the server's own, and its
//     Sec-Fetch-Site, when it has one, is not another site's, so that no
//     other page can make the browser call the API (CSRF). The frontend
//     takes a navigation from anywhere, since the page that opens dotai ui
//     is a file on disk;
//   - on /api/, except the session exchange, it carries the session token
//     in an Authorization: Bearer header. No cookie is a credential, since
//     a browser sends a cookie of 127.0.0.1 to every port.
//
// The embedded frontend is public and needs no token. Every response also
// tells the browser not to frame the page, sniff types or send a referrer.
func guard(cfg Config, next http.Handler) http.Handler {
	hosts := map[string]bool{fmt.Sprintf("127.0.0.1:%d", cfg.Port): true, fmt.Sprintf("localhost:%d", cfg.Port): true}
	origins := map[string]bool{}
	for h := range hosts {
		origins["http://"+h] = true
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		securityHeaders(w.Header())
		if !hosts[strings.ToLower(r.Host)] {
			refuse(w, r, http.StatusForbidden, CodeForbidden, fmt.Sprintf("the request is for the host %q: dotai ui answers only to 127.0.0.1:%d and localhost:%d", r.Host, cfg.Port, cfg.Port))
			return
		}
		if o := r.Header.Get("Origin"); isAPI(r) && o != "" && !origins[strings.ToLower(o)] {
			refuse(w, r, http.StatusForbidden, CodeForbidden, fmt.Sprintf("the request comes from the origin %q, not from dotai ui", o))
			return
		}
		if site := r.Header.Get("Sec-Fetch-Site"); isAPI(r) && (site == "cross-site" || site == "same-site") {
			refuse(w, r, http.StatusForbidden, CodeForbidden, "the request comes from another site, not from dotai ui")
			return
		}
		if isAPI(r) && r.URL.Path != sessionPath && !authorized(r, cfg.Session) {
			refuse(w, r, http.StatusUnauthorized, CodeUnauthorized, "the request carries no session token of this run of dotai ui: open the address it printed")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// authorized reports whether r carries token in its Authorization header.
func authorized(r *http.Request, token string) bool {
	h, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	return ok && sameToken(h, token)
}

func isAPI(r *http.Request) bool { return strings.HasPrefix(r.URL.Path, "/api/") }

// refuse answers a request the guard refuses: with an envelope on the
// API, and with plain text elsewhere, where a person reads it.
func refuse(w http.ResponseWriter, r *http.Request, status int, code, msg string) {
	if isAPI(r) {
		writeEnvelope(w, status, envelope.Failure(command, code, msg))
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	http.Error(w, msg, status)
}

func securityHeaders(h http.Header) {
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("X-Frame-Options", "DENY")
	h.Set("Referrer-Policy", "no-referrer")
	h.Set("Cross-Origin-Opener-Policy", "same-origin")
	h.Set("Cross-Origin-Resource-Policy", "same-origin")
	h.Set("Content-Security-Policy", "default-src 'self'; base-uri 'none'; form-action 'self'; frame-ancestors 'none'")
}
