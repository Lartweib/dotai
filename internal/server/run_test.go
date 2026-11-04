package server_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
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

// startOpened runs the server with an Open that records the page it is
// asked to open, and gives the address, the page and stop.
func startOpened(t *testing.T) (addr *url.URL, page string, stop func() error) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	ready, opened := make(chan string, 1), make(chan string, 1)
	done := make(chan error, 1)
	go func() {
		done <- server.Run(ctx, server.Options{Home: t.TempDir(), CacheDir: t.TempDir(),
			Ready: func(u string) error { ready <- u; return nil },
			Open:  func(p string) error { opened <- p; return nil }}, fakeDeps())
	}()
	u, err := url.Parse(<-ready)
	if err != nil {
		t.Fatal(err)
	}
	page = <-opened
	stop = func() error {
		cancel()
		return <-done
	}
	return u, page, stop
}

func TestTheBrowserOpensAPageOnlyTheUserReadsInsteadOfTheCode(t *testing.T) {
	addr, page, stop := startOpened(t)
	defer stop()
	code := fragmentCode(t, addr)
	if strings.Contains(page, code) || !strings.HasSuffix(page, ".html") || !filepath.IsAbs(page) {
		t.Fatalf("opened %q, want an absolute .html path without the code", page)
	}
	info, err := os.Stat(page)
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" {
		dir, _ := os.Stat(filepath.Dir(page))
		if info.Mode().Perm() != 0o600 || dir.Mode().Perm() != 0o700 {
			t.Errorf("page %v in a directory %v, want 0600 in 0700", info.Mode().Perm(), dir.Mode().Perm())
		}
	}
	data, err := os.ReadFile(page)
	if err != nil || !strings.Contains(string(data), "#code="+code) || !strings.Contains(string(data), addr.Host) {
		t.Errorf("page = %s, %v, want it to send the browser to the address with the code", data, err)
	}
	res, err := http.Post("http://"+addr.Host+"/api/session", "application/json", strings.NewReader(`{"code":"`+code+`"}`))
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if _, err := os.Stat(page); !os.IsNotExist(err) {
		t.Errorf("page after the exchange = %v, want it deleted", err)
	}
	if _, err := os.Stat(filepath.Dir(page)); !os.IsNotExist(err) {
		t.Errorf("its directory after the exchange = %v, want it deleted", err)
	}
}

func TestThePageToOpenIsDeletedOnShutdown(t *testing.T) {
	_, page, stop := startOpened(t)
	if _, err := os.Stat(page); err != nil {
		t.Fatal(err)
	}
	if err := stop(); err != nil {
		t.Fatalf("Run() = %v", err)
	}
	if _, err := os.Stat(filepath.Dir(page)); !os.IsNotExist(err) {
		t.Errorf("page directory after shutdown = %v, want it deleted", err)
	}
}

func TestWithoutOpenNoPageIsWritten(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("TMPDIR", tmp)
	t.Setenv("TMP", tmp)
	t.Setenv("TEMP", tmp)
	_, stop := startRun(t, fakeDeps())
	_ = stop()
	if entries, _ := os.ReadDir(tmp); len(entries) != 0 {
		t.Errorf("temporary directory holds %d entries, want none without Open", len(entries))
	}
}
