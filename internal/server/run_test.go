package server_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/lartweib/dotai/internal/server"
)

// startRun runs the server with deps until the test ends or stop is
// called, and gives its address, with the token, and the outcome of Run.
func startRun(t *testing.T, deps server.Deps) (addr *url.URL, stop func() error) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	ready := make(chan string, 1)
	done := make(chan error, 1)
	go func() {
		done <- server.Run(ctx, server.Options{Home: t.TempDir(), CacheDir: t.TempDir(), Ready: func(u string) error { ready <- u; return nil }}, deps)
	}()
	select {
	case u := <-ready:
		parsed, err := url.Parse(u)
		if err != nil {
			t.Fatal(err)
		}
		addr = parsed
	case err := <-done:
		cancel()
		t.Fatalf("Run() = %v before it was ready", err)
	case <-time.After(10 * time.Second):
		cancel()
		t.Fatal("Run() was never ready")
	}
	stopped := false
	stop = func() error {
		if stopped {
			return nil
		}
		stopped = true
		cancel()
		select {
		case err := <-done:
			return err
		case <-time.After(10 * time.Second):
			return errors.New("Run() did not stop")
		}
	}
	t.Cleanup(func() { _ = stop() })
	return addr, stop
}

// fragmentCode is the one-time code in the fragment of addr.
func fragmentCode(t *testing.T, addr *url.URL) string {
	t.Helper()
	q, err := url.ParseQuery(addr.Fragment)
	if err != nil {
		t.Fatal(err)
	}
	return q.Get(server.CodeParam)
}

func TestRunServesOn127001UntilCancelled(t *testing.T) {
	addr, stop := startRun(t, fakeDeps())
	if addr.Scheme != "http" || addr.Hostname() != "127.0.0.1" || addr.Port() == "" || addr.Port() == "0" || addr.Path != "/" || addr.RawQuery != "" {
		t.Fatalf("address = %s, want http://127.0.0.1:<port>/ with no query", addr)
	}
	code := fragmentCode(t, addr)
	if len(code) < 43 {
		t.Fatalf("code = %q, want 32 random bytes in the fragment", code)
	}
	client := &http.Client{Timeout: 10 * time.Second}
	res, err := client.Get("http://" + addr.Host + "/")
	if err != nil {
		t.Fatal(err)
	}
	page, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if res.StatusCode != http.StatusOK || !strings.Contains(string(page), "<title>dotai</title>") {
		t.Fatalf("GET / = %d, want the embedded index page", res.StatusCode)
	}
	res, err = client.Post("http://"+addr.Host+"/api/session", "application/json", strings.NewReader(`{"code":"`+code+`"}`))
	if err != nil {
		t.Fatal(err)
	}
	var session struct {
		OK   bool `json:"ok"`
		Data struct {
			Token string `json:"token"`
		} `json:"data"`
	}
	err = json.NewDecoder(res.Body).Decode(&session)
	res.Body.Close()
	if err != nil || !session.OK || session.Data.Token == "" || session.Data.Token == code {
		t.Fatalf("exchange = %+v, %v, want a session token other than the code", session, err)
	}
	req, err := http.NewRequest(http.MethodGet, "http://"+addr.Host+"/api/version", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+session.Data.Token)
	res, err = client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	var e struct {
		OK      bool   `json:"ok"`
		Command string `json:"command"`
	}
	err = json.NewDecoder(res.Body).Decode(&e)
	res.Body.Close()
	if err != nil || res.StatusCode != http.StatusOK || !e.OK || e.Command != "version" {
		t.Errorf("GET /api/version with the session token = %d %+v, %v, want the version", res.StatusCode, e, err)
	}
	if err := stop(); err != nil {
		t.Errorf("Run() after cancel = %v, want nil", err)
	}
	if _, err := client.Get("http://" + addr.Host + "/api/version"); err == nil {
		t.Error("the server still answers after it stopped")
	}
}

func TestEachRunHasItsOwnToken(t *testing.T) {
	a, _ := startRun(t, fakeDeps())
	b, _ := startRun(t, fakeDeps())
	if fragmentCode(t, a) == fragmentCode(t, b) || a.Host == b.Host {
		t.Errorf("two runs = %s and %s, want two ports and two tokens", a, b)
	}
}

func TestRunRefusesAPortOutOfRange(t *testing.T) {
	for _, port := range []int{-1, 65536} {
		if err := server.Run(context.Background(), server.Options{Port: port}, fakeDeps()); err == nil {
			t.Errorf("Run() on port %d succeeded", port)
		}
	}
}

func TestRunStopsWhenReadyFails(t *testing.T) {
	err := server.Run(context.Background(), server.Options{Ready: func(string) error { return errors.New("no terminal") }}, fakeDeps())
	if err == nil || !strings.Contains(err.Error(), "no terminal") {
		t.Errorf("Run() = %v, want the error of Ready", err)
	}
}
