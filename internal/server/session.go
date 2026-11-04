package server

import (
	"crypto/subtle"
	"net/http"
	"sync"
	"time"
)

// codeTTL is how long the one-time code of the address dotai ui opens
// stays good.
const codeTTL = 2 * time.Minute

// session holds the one-time code of a run and the session token it is
// traded for. The code is good for one exchange, until it expires; the
// token is good for the whole run.
type session struct {
	mu      sync.Mutex
	code    string
	expires time.Time
	used    bool
	token   string
	now     func() time.Time
}

// exchange gives the session token for code, and uses code up. A wrong
// code is refused and leaves the code good; a code used before, or one
// that expired, is refused with what to do.
func (s *session) exchange(code string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !sameToken(code, s.code) {
		return "", unauthorized("the code is not the one of this run of dotai ui: open the address it printed")
	}
	if s.used {
		return "", unauthorized("this link was already used; restart dotai ui to get a new one")
	}
	if !s.now().Before(s.expires) {
		return "", unauthorized("this link expired; restart dotai ui to get a new one")
	}
	s.used = true
	return s.token, nil
}

// sameToken compares in constant time; an empty want never matches.
func sameToken(got, want string) bool {
	return want != "" && subtle.ConstantTimeCompare([]byte(got), []byte(want)) == 1
}

func unauthorized(msg string) error {
	return &apiError{code: CodeUnauthorized, status: http.StatusUnauthorized, msg: msg}
}
