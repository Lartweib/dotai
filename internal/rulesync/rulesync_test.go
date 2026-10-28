package rulesync_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/lartweib/dotai/internal/rulesync"
)

var binary = []byte("rulesync binary")

func sum(b []byte) string {
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

// server serves body for the 9.9.9 asset and counts the requests.
func server(t *testing.T, status int, body []byte) (*httptest.Server, *int) {
	t.Helper()
	var hits int
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		if r.URL.Path != "/v9.9.9/rulesync-linux-x64" {
			http.NotFound(w, r)
			return
		}
		w.WriteHeader(status)
		_, _ = w.Write(body)
	}))
	t.Cleanup(s.Close)
	return s, &hits
}

func fetcher(t *testing.T, s *httptest.Server) rulesync.Fetcher {
	t.Helper()
	rulesync.SetReleases(t, map[string]map[string]rulesync.Asset{
		"9.9.9": {"linux/amd64": {Name: "rulesync-linux-x64", SHA256: sum(binary)}},
	})
	return rulesync.Fetcher{Client: s.Client(), BaseURL: s.URL, CacheDir: t.TempDir(), GOOS: "linux", GOARCH: "amd64"}
}

func TestFetch(t *testing.T) {
	s, hits := server(t, http.StatusOK, binary)
	f := fetcher(t, s)

	got, err := f.Fetch(context.Background(), "9.9.9")
	want := rulesync.Result{
		Version: "9.9.9", Asset: "rulesync-linux-x64", SHA256: sum(binary),
		Path: filepath.Join(f.CacheDir, "rulesync", "9.9.9", "rulesync-linux-x64"),
	}
	if err != nil || got != want {
		t.Fatalf("first Fetch() = %+v, %v, want %+v", got, err, want)
	}
	if b, err := os.ReadFile(want.Path); err != nil || string(b) != string(binary) {
		t.Errorf("cached file = %q, %v", b, err)
	}

	got, err = f.Fetch(context.Background(), "9.9.9")
	want.Cached = true
	if err != nil || got != want || *hits != 1 {
		t.Errorf("second Fetch() = %+v, %v after %d requests, want %+v from the cache", got, err, *hits, want)
	}
}

func TestFetchRedownloadsCorruptCache(t *testing.T) {
	s, hits := server(t, http.StatusOK, binary)
	f := fetcher(t, s)
	path := filepath.Join(f.CacheDir, "rulesync", "9.9.9", "rulesync-linux-x64")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("tampered"), 0o755); err != nil {
		t.Fatal(err)
	}

	got, err := f.Fetch(context.Background(), "9.9.9")
	if err != nil || got.Cached || *hits != 1 {
		t.Fatalf("Fetch() = %+v, %v after %d requests, want a download", got, err, *hits)
	}
	if b, _ := os.ReadFile(path); string(b) != string(binary) {
		t.Errorf("cached file = %q, want the verified binary", b)
	}
}

func TestFetchFailures(t *testing.T) {
	tests := []struct {
		name    string
		status  int
		body    []byte
		version string
		goos    string
		maxSize int64
		want    string
	}{
		{name: "checksum mismatch", status: 200, body: []byte("evil"), version: "9.9.9", want: "checksum mismatch"},
		{name: "http error", status: 500, body: binary, version: "9.9.9", want: "500"},
		{name: "oversized body", status: 200, body: binary, version: "9.9.9", maxSize: 4, want: "larger than"},
		{name: "unknown version", status: 200, body: binary, version: "1.0.0", want: "unsupported rulesync version"},
		{name: "unsupported platform", status: 200, body: binary, version: "9.9.9", goos: "plan9", want: "unsupported platform plan9/amd64"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s, _ := server(t, tt.status, tt.body)
			f := fetcher(t, s)
			f.MaxSize = tt.maxSize
			if tt.goos != "" {
				f.GOOS = tt.goos
			}

			_, err := f.Fetch(context.Background(), tt.version)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Errorf("Fetch() error = %v, want %q", err, tt.want)
			}
			entries, _ := os.ReadDir(filepath.Join(f.CacheDir, "rulesync", tt.version))
			if len(entries) != 0 {
				t.Errorf("cache holds %v after a failure, want nothing", entries)
			}
		})
	}
}

func TestFetchHonorsContext(t *testing.T) {
	s, _ := server(t, http.StatusOK, binary)
	f := fetcher(t, s)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if _, err := f.Fetch(ctx, "9.9.9"); !errors.Is(err, context.Canceled) {
		t.Errorf("Fetch() error = %v, want context.Canceled", err)
	}
}

func TestBuiltInTable(t *testing.T) {
	if !slices.Contains(rulesync.Versions(), "41.0.0") || rulesync.Supported("40.0.0") {
		t.Errorf("Versions() = %v, want 41.0.0 only", rulesync.Versions())
	}
	tests := []struct{ goos, goarch, want string }{
		{"windows", "amd64", "rulesync-windows-x64.exe"},
		{"linux", "amd64", "rulesync-linux-x64"},
		{"linux", "arm64", "rulesync-linux-arm64"},
		{"darwin", "amd64", "rulesync-darwin-x64"},
		{"darwin", "arm64", "rulesync-darwin-arm64"},
	}
	for _, tt := range tests {
		a, err := rulesync.Resolve("41.0.0", tt.goos, tt.goarch)
		if err != nil || a.Name != tt.want || len(a.SHA256) != 64 {
			t.Errorf("Resolve(%s/%s) = %+v, %v, want %s", tt.goos, tt.goarch, a, err, tt.want)
		}
	}
	if _, err := rulesync.Resolve("41.0.0", "windows", "arm64"); err == nil || !strings.Contains(err.Error(), "unsupported platform") {
		t.Errorf("Resolve(windows/arm64) error = %v, want unsupported platform", err)
	}
}

func TestCacheDir(t *testing.T) {
	t.Setenv("DOTAI_CACHE_DIR", filepath.Join("some", "dir"))
	if got, err := rulesync.CacheDir(); err != nil || got != filepath.Join("some", "dir") {
		t.Errorf("CacheDir() = %q, %v, want DOTAI_CACHE_DIR", got, err)
	}
}
