package server

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"fmt"
	"net/http"
	"strings"

	"github.com/lartweib/dotai/internal/cli/output"
)

// NewToken is a random token for one run of the server: 32 bytes from
// crypto/rand, in unpadded base64url.
func NewToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("making the token of the web UI: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// TokenParam is the query parameter of the address dotai ui opens, which
// carries the token; bearerPrefix starts the Authorization header that
// carries it on an API request instead of the cookie.
const (
	TokenParam   = "token"
	bearerPrefix = "Bearer "
)

// cookieName is the name of the cookie that carries the token for the
// server on port: a cookie is not bound to a port, so that two servers on
// two ports keep theirs apart.
func cookieName(port int) string { return fmt.Sprintf("dotai_ui_%d", port) }

// guard lets a request reach next only when it is for this server and
// carries its token:
//
//   - its Host is 127.0.0.1:<port> or localhost:<port>, which a page that
//     renamed its own host to 127.0.0.1 by DNS rebinding does not send;
//   - its Origin, when it has one, is the server's own, and its
//     Sec-Fetch-Site, when it has one, is not another site's, so that no
//     other page can make the browser call the API (CSRF);
//   - it carries the token in the cookie or, on an API request, in an
//     Authorization: Bearer header. The address dotai ui opens has the
//     token as a query parameter of /: that request sets the cookie,
//     HttpOnly and SameSite=Strict, and is redirected to / without it, so
//     that the token leaves the address bar.
//
// Every response also tells the browser not to frame the page, sniff
// types or send a referrer.
func guard(cfg Config, next http.Handler) http.Handler {
	hosts := map[string]bool{fmt.Sprintf("127.0.0.1:%d", cfg.Port): true, fmt.Sprintf("localhost:%d", cfg.Port): true}
	origins := map[string]bool{}
	for h := range hosts {
		origins["http://"+h] = true
	}
	cookie := cookieName(cfg.Port)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		securityHeaders(w.Header())
		if !hosts[strings.ToLower(r.Host)] {
			refuse(w, r, http.StatusForbidden, CodeForbidden, fmt.Sprintf("the request is for the host %q: dotai ui answers only to 127.0.0.1:%d and localhost:%d", r.Host, cfg.Port, cfg.Port))
			return
		}
		if o := r.Header.Get("Origin"); o != "" && !origins[strings.ToLower(o)] {
			refuse(w, r, http.StatusForbidden, CodeForbidden, fmt.Sprintf("the request comes from the origin %q, not from dotai ui", o))
			return
		}
		if site := r.Header.Get("Sec-Fetch-Site"); site == "cross-site" || site == "same-site" {
			refuse(w, r, http.StatusForbidden, CodeForbidden, "the request comes from another site, not from dotai ui")
			return
		}
		if t, ok := r.URL.Query()[TokenParam]; ok && r.URL.Path == "/" {
			if len(t) != 1 || !sameToken(t[0], cfg.Token) {
				refuse(w, r, http.StatusUnauthorized, CodeUnauthorized, "the token in the address is not the one of this run of dotai ui: open the address it printed")
				return
			}
			http.SetCookie(w, &http.Cookie{Name: cookie, Value: cfg.Token, Path: "/", HttpOnly: true, SameSite: http.SameSiteStrictMode})
			http.Redirect(w, r, "/", http.StatusSeeOther)
			return
		}
		if !authorized(r, cookie, cfg.Token) {
			refuse(w, r, http.StatusUnauthorized, CodeUnauthorized, "the request carries no token of this run of dotai ui: open the address it printed")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// authorized reports whether r carries token, in the cookie or, on an API
// request, in the Authorization header.
func authorized(r *http.Request, cookie, token string) bool {
	if c, err := r.Cookie(cookie); err == nil && sameToken(c.Value, token) {
		return true
	}
	if !isAPI(r) {
		return false
	}
	h, ok := strings.CutPrefix(r.Header.Get("Authorization"), bearerPrefix)
	return ok && sameToken(h, token)
}

// sameToken compares in constant time; an empty token never matches.
func sameToken(got, want string) bool {
	return want != "" && subtle.ConstantTimeCompare([]byte(got), []byte(want)) == 1
}

func isAPI(r *http.Request) bool { return strings.HasPrefix(r.URL.Path, "/api/") }

// refuse answers a request the guard refuses: an envelope on the API,
// plain text elsewhere, where a person reads it in the browser.
func refuse(w http.ResponseWriter, r *http.Request, status int, code, msg string) {
	if isAPI(r) {
		writeEnvelope(w, status, output.Failure(command, code, msg))
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
