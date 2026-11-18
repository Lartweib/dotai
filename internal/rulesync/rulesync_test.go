package rulesync_test

import (
	"cmp"
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
	"sync/atomic"
	"testing"
	"time"

	"github.com/lartweib/dotai/internal/rulesync"
)

var binary = []byte("rulesync binary")

const asset = "rulesync-linux-x64"

func sum(b []byte) string {
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

// server serves the 9.9.9 asset with handle and counts the requests.
func server(t *testing.T, handle func(w http.ResponseWriter, r *http.Request, hit int32)) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	var hits atomic.Int32
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hit := hits.Add(1)
		if r.URL.Path != "/v9.9.9/"+asset {
			http.NotFound(w, r)
			return
		}
		handle(w, r, hit)
	}))
	t.Cleanup(s.Close)
	return s, &hits
}

// serve answers every request with status and body.
func serve(status int, body []byte) func(http.ResponseWriter, *http.Request, int32) {
	return func(w http.ResponseWriter, _ *http.Request, _ int32) {
		w.WriteHeader(status)
		_, _ = w.Write(body)
	}
}

func fetcher(t *testing.T, s *httptest.Server) rulesync.Fetcher {
	t.Helper()
	rulesync.SetReleases(t, map[string]map[string]rulesync.Asset{
		"9.9.9": {"linux/amd64": {Name: asset, SHA256: sum(binary)}},
	})
	return rulesync.NewTestFetcher(s.Client(), s.URL)
}

func versionDir(cache string) string { return filepath.Join(cache, "rulesync", "9.9.9") }

func TestFetch(t *testing.T) {
	s, hits := server(t, serve(http.StatusOK, binary))
	f := fetcher(t, s)
	var announced []int64
	f.Progress = func(version string, size int64) { announced = append(announced, size) }
	cache := t.TempDir()

	got, err := f.Fetch(context.Background(), cache, "9.9.9")
	want := rulesync.Result{Version: "9.9.9", Asset: asset, SHA256: sum(binary), Path: filepath.Join(versionDir(cache), asset)}
	if err != nil || got != want || !slices.Equal(announced, []int64{int64(len(binary))}) {
		t.Fatalf("first Fetch() = %+v, %v, announced %v, want %+v", got, err, announced, want)
	}
	if b, err := os.ReadFile(want.Path); err != nil || string(b) != string(binary) {
		t.Errorf("cached file = %q, %v", b, err)
	}

	got, err = f.Fetch(context.Background(), cache, "9.9.9")
	want.Cached = true
	if err != nil || got != want || hits.Load() != 1 || len(announced) != 1 {
		t.Errorf("second Fetch() = %+v, %v after %d requests, want %+v from the cache", got, err, hits.Load(), want)
	}
}

func TestFetchRedownloadsCorruptCache(t *testing.T) {
	s, hits := server(t, serve(http.StatusOK, binary))
	f := fetcher(t, s)
	cache := t.TempDir()
	path := filepath.Join(versionDir(cache), asset)
	write(t, path, "tampered", time.Now())

	got, err := f.Fetch(context.Background(), cache, "9.9.9")
	if err != nil || got.Cached || hits.Load() != 1 {
		t.Fatalf("Fetch() = %+v, %v after %d requests, want a download", got, err, hits.Load())
	}
	if b, _ := os.ReadFile(path); string(b) != string(binary) {
		t.Errorf("cached file = %q, want the verified binary", b)
	}
}

func TestFetchRemovesStaleTempFiles(t *testing.T) {
	s, _ := server(t, serve(http.StatusOK, binary))
	f := fetcher(t, s)
	cache := t.TempDir()
	stale := filepath.Join(versionDir(cache), asset+".111.tmp")
	live := filepath.Join(versionDir(cache), asset+".222.tmp")
	write(t, stale, "partial", time.Now().Add(-2*time.Hour))
	write(t, live, "partial", time.Now())

	if _, err := f.Fetch(context.Background(), cache, "9.9.9"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(stale); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("stale temp file still there: %v", err)
	}
	if _, err := os.Stat(live); err != nil {
		t.Errorf("a concurrent run's temp file was removed: %v", err)
	}
}

func TestFetchRenameLostToAnotherRun(t *testing.T) {
	s, _ := server(t, serve(http.StatusOK, binary))
	f := fetcher(t, s)
	f.SetRename(func(_, to string) error {
		if err := os.WriteFile(to, binary, 0o755); err != nil {
			return err
		}
		return errors.New("access denied")
	})
	cache := t.TempDir()

	if _, err := f.Fetch(context.Background(), cache, "9.9.9"); err != nil {
		t.Fatalf("Fetch() error = %v, want the other run's verified binary", err)
	}
	if entries, _ := os.ReadDir(versionDir(cache)); len(entries) != 1 {
		t.Errorf("version dir = %v, want only the binary", entries)
	}
}

func TestFetchRetriesOnceOnServerError(t *testing.T) {
	s, hits := server(t, func(w http.ResponseWriter, r *http.Request, hit int32) {
		if hit == 1 {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		serve(http.StatusOK, binary)(w, r, hit)
	})
	if _, err := fetcher(t, s).Fetch(context.Background(), t.TempDir(), "9.9.9"); err != nil || hits.Load() != 2 {
		t.Errorf("Fetch() error = %v after %d requests, want success on the retry", err, hits.Load())
	}
}

func TestFetchFailures(t *testing.T) {
	block := func(w http.ResponseWriter, r *http.Request, _ int32) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(binary[:4])
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	}
	tests := []struct {
		name     string
		handle   func(http.ResponseWriter, *http.Request, int32)
		version  string
		setup    func(*rulesync.Fetcher)
		want     string
		wantHits int32
	}{
		{name: "checksum mismatch", handle: serve(200, []byte("evil")), want: "checksum mismatch", wantHits: 1},
		{name: "server error is retried once", handle: serve(500, nil), want: "500", wantHits: 2},
		{name: "not found", handle: serve(404, nil), want: "404", wantHits: 1},
		{name: "rate limited", handle: serve(403, nil), want: "rate limited, retry later", wantHits: 1},
		{name: "too many requests", handle: serve(429, nil), want: "rate limited, retry later", wantHits: 1},
		{name: "proxy authentication", handle: serve(407, nil), want: "check HTTPS_PROXY", wantHits: 1},
		{name: "oversized body", handle: serve(200, binary), setup: func(f *rulesync.Fetcher) { f.SetMaxSize(4) }, want: "larger than", wantHits: 1},
		{name: "stalled body", handle: block, setup: func(f *rulesync.Fetcher) { f.SetStallTimeout(50 * time.Millisecond) }, want: "stalled", wantHits: 1},
		{name: "rename keeps failing", handle: serve(200, binary), setup: func(f *rulesync.Fetcher) {
			f.SetRename(func(string, string) error { return errors.New("locked") })
		}, want: "locked", wantHits: 1},
		{name: "unknown version", handle: serve(200, binary), version: "1.0.0", want: "unsupported rulesync version"},
		{name: "unsupported platform", handle: serve(200, binary), setup: func(f *rulesync.Fetcher) { f.SetPlatform("plan9", "amd64") }, want: "unsupported platform plan9/amd64"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s, hits := server(t, tt.handle)
			f := fetcher(t, s)
			if tt.setup != nil {
				tt.setup(&f)
			}
			cache := t.TempDir()

			_, err := f.Fetch(context.Background(), cache, cmp.Or(tt.version, "9.9.9"))
			if err == nil || !strings.Contains(err.Error(), tt.want) || hits.Load() != tt.wantHits {
				t.Errorf("Fetch() error = %v after %d requests, want %q after %d", err, hits.Load(), tt.want, tt.wantHits)
			}
			if entries, _ := os.ReadDir(versionDir(cache)); len(entries) != 0 {
				t.Errorf("cache holds %v after a failure, want nothing", entries)
			}
		})
	}
}

func TestFetchOffline(t *testing.T) {
	s, _ := server(t, serve(200, binary))
	f := fetcher(t, s)
	s.Close()

	if _, err := f.Fetch(context.Background(), t.TempDir(), "9.9.9"); err == nil || !strings.Contains(err.Error(), "offline") {
		t.Errorf("Fetch() error = %v, want an offline or proxy hint", err)
	}
}

func TestFetchCancelled(t *testing.T) {
	s, _ := server(t, func(w http.ResponseWriter, r *http.Request, _ int32) {
		w.Header().Set("Content-Length", "1000")
		w.WriteHeader(http.StatusOK)
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	})
	f := fetcher(t, s)
	ctx, cancel := context.WithCancel(context.Background())
	f.Progress = func(string, int64) { cancel() } // cancel once the download started
	cache := t.TempDir()

	_, err := f.Fetch(ctx, cache, "9.9.9")
	if !errors.Is(err, context.Canceled) || !strings.Contains(err.Error(), "cancelled") {
		t.Errorf("Fetch() error = %v, want a cancelled download", err)
	}
	if entries, _ := os.ReadDir(versionDir(cache)); len(entries) != 0 {
		t.Errorf("cache holds %v after a cancel, want nothing", entries)
	}
}

func TestBuiltInTable(t *testing.T) {
	if got := rulesync.Versions(); !slices.Equal(got, []string{"43.9.2"}) || rulesync.Supported("41.0.0") {
		t.Errorf("Versions() = %v, want 43.9.2 only", got)
	}
	// The SHA-256 of each v43.9.2 asset, as its SHA256SUMS and the GitHub
	// API digest give it (0029).
	tests := []struct{ goos, goarch, want, sha string }{
		{"windows", "amd64", "rulesync-windows-x64.exe", "e24d352549059a42b4a9d9e654398f8e67c49a01d90cf93ec0000fcb8b6f6c7d"},
		{"linux", "amd64", "rulesync-linux-x64", "4b91e172ca1c48084911e8f08b1a45cc0cae860cead8d1bd65cba4d5e438c608"},
		{"linux", "arm64", "rulesync-linux-arm64", "25c97255e051a0fb9fd3043ffd4ea2daf8443e58553b19bbead83cf7c563b5d1"},
		{"darwin", "amd64", "rulesync-darwin-x64", "546f4af78d1411121b87fc7479da59d280afbd6a6ddf91f5f221fd99b02447b9"},
		{"darwin", "arm64", "rulesync-darwin-arm64", "dc0d8e4517c643ef60b38b183b7e8251b89405f97656365af939427151cc8026"},
	}
	for _, tt := range tests {
		a, err := rulesync.Resolve("43.9.2", tt.goos, tt.goarch)
		if err != nil || a.Name != tt.want || a.SHA256 != tt.sha {
			t.Errorf("Resolve(%s/%s) = %+v, %v, want %s with %s", tt.goos, tt.goarch, a, err, tt.want, tt.sha)
		}
	}
	if _, err := rulesync.Resolve("43.9.2", "windows", "arm64"); err == nil || !strings.Contains(err.Error(), "unsupported platform") {
		t.Errorf("Resolve(windows/arm64) error = %v, want unsupported platform", err)
	}
}

func write(t *testing.T, path, data string, mtime time.Time) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, mtime, mtime); err != nil {
		t.Fatal(err)
	}
}
