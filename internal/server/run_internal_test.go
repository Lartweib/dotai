package server

import (
	"context"
	"net/http"
	"testing"
)

func TestTheHTTPServerLimitsSlowAndIdleConnections(t *testing.T) {
	srv := newHTTPServer(context.Background(), http.NotFoundHandler())
	if srv.ReadHeaderTimeout <= 0 || srv.IdleTimeout <= 0 || srv.MaxHeaderBytes <= 0 || srv.MaxHeaderBytes > 1<<20 {
		t.Errorf("server = read header %v, idle %v, max header %d, want each limit set below the defaults",
			srv.ReadHeaderTimeout, srv.IdleTimeout, srv.MaxHeaderBytes)
	}
}
