package server

import (
	"net/http"
	"time"
)

// WaitForActions is the wait of a stop for the actions in flight of h, a
// handler of New.
func WaitForActions(h http.Handler, d time.Duration) bool { return h.(*ui).wait(d) }
