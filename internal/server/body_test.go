package server_test

import (
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/lartweib/dotai/internal/server"
)

// TestABodyOverTheLimitClosesTheConnection checks that a JSON body larger
// than the server reads is refused as a usage error, and that the server
// closes the connection after the reply instead of reading the rest of a
// body it will never use.
func TestABodyOverTheLimitClosesTheConnection(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	cfg := testConfig(t)
	cfg.Port = l.Addr().(*net.TCPAddr).Port
	srv := httptest.NewUnstartedServer(server.New(cfg, fakeDeps(), testWeb))
	srv.Listener.Close()
	srv.Listener = l
	srv.Start()
	defer srv.Close()

	body := `{"pack":"` + strings.Repeat("a", 80<<10) + `"}`
	req, err := http.NewRequest(http.MethodPost, srv.URL+"/api/install/plan", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+testToken)
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", resp.StatusCode, http.StatusBadRequest)
	}
	if !resp.Close {
		t.Error("the server keeps the connection open after a body over the limit")
	}
}
