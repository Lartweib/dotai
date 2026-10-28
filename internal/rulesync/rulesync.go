// Package rulesync fetches the pinned rulesync binary (0021). The engine
// embeds the SHA-256 of every release asset it supports (0029): a download
// or a cached file is used only when it matches that table.
package rulesync

import (
	"cmp"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"maps"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"syscall"
	"time"
)

// Asset is one release binary and its expected SHA-256.
type Asset struct {
	Name   string
	SHA256 string
}

// releases maps a rulesync version to its assets by GOOS/GOARCH. Every
// value was checked three ways before it was added (0029): the release's
// SHA256SUMS, the GitHub API digest of the asset, and a hash of the asset
// downloaded independently.
var releases = map[string]map[string]Asset{
	"41.0.0": {
		"windows/amd64": {"rulesync-windows-x64.exe", "0a83edb66cb548868929836ae67682e608240d9a06b0ee459a9928fbd4334e0b"},
		"linux/amd64":   {"rulesync-linux-x64", "6598756e1f57959645589de9c2ce7b686018eb754bf1c994e8137ea2fba80ed6"},
		"linux/arm64":   {"rulesync-linux-arm64", "3bc83122250d12c027aac9acfb7243092ed29aa27e52f3a9a62aa0ae33e30a3c"},
		"darwin/amd64":  {"rulesync-darwin-x64", "dd46f4f02fd9810b3a1a3fb548d10865d8b4b7f378928e286c49f5f68526e569"},
		"darwin/arm64":  {"rulesync-darwin-arm64", "004dc87ef1c2d3b0d1c1811ac41ca0bc64ce220dd02986521d2a3261aa5fda31"},
	},
}

// Defaults of a zero Fetcher.
const (
	baseURL        = "https://github.com/dyoshikawa/rulesync/releases/download"
	maxSize        = 256 << 20        // larger than any release asset
	stallTimeout   = 60 * time.Second // also the wait for response headers
	retryDelay     = 2 * time.Second
	renameAttempts = 3
	staleAge       = time.Hour // a temp file older than this belongs to a dead run
)

// errStalled ends a download that received no data for stallTimeout.
var errStalled = errors.New("download stalled")

var defaultClient = func() *http.Client {
	t := http.DefaultTransport.(*http.Transport).Clone()
	t.ResponseHeaderTimeout = stallTimeout
	return &http.Client{Transport: t}
}()

// Versions returns the supported rulesync versions, sorted.
func Versions() []string { return slices.Sorted(maps.Keys(releases)) }

// Supported reports whether the engine knows the checksums of version.
func Supported(version string) bool { return releases[version] != nil }

// Resolve returns the asset of version for goos/goarch.
func Resolve(version, goos, goarch string) (Asset, error) {
	assets := releases[version]
	if assets == nil {
		return Asset{}, fmt.Errorf("unsupported rulesync version %s, this engine supports %v", version, Versions())
	}
	a, ok := assets[goos+"/"+goarch]
	if !ok {
		return Asset{}, fmt.Errorf("unsupported platform %s/%s for rulesync %s", goos, goarch, version)
	}
	return a, nil
}

// Result is a verified binary. Cached tells whether it was already there.
type Result struct {
	Version, Asset, Path, SHA256 string
	Cached                       bool
}

// Fetcher downloads release assets. The zero value downloads from GitHub
// for the running platform.
type Fetcher struct {
	// Progress, when set, is called once before a download starts, with
	// the size of the asset in bytes (-1 when unknown).
	Progress func(version string, size int64)

	client                   *http.Client
	baseURL, goos, goarch    string
	maxSize                  int64
	stallTimeout, retryDelay time.Duration
	rename                   func(from, to string) error
}

// Fetch returns the verified binary of version from
// <cacheDir>/rulesync/<version>/<asset>, downloading it when it is missing
// or does not match its checksum. A failed download leaves no file of its
// own behind; a corrupt cached file may remain and is checked again on the
// next run.
func (f Fetcher) Fetch(ctx context.Context, cacheDir, version string) (Result, error) {
	goos := cmp.Or(f.goos, runtime.GOOS)
	a, err := Resolve(version, goos, cmp.Or(f.goarch, runtime.GOARCH))
	if err != nil {
		return Result{}, err
	}
	dir := filepath.Join(cacheDir, "rulesync", version)
	res := Result{Version: version, Asset: a.Name, Path: filepath.Join(dir, a.Name), SHA256: a.SHA256}
	if got, err := hashFile(res.Path); err == nil && got == a.SHA256 {
		res.Cached = true
		return res, nil
	}
	removeStale(res.Path)
	url := cmp.Or(f.baseURL, baseURL) + "/v" + version + "/" + a.Name
	for attempt := 1; ; attempt++ {
		err = f.download(ctx, url, res, goos)
		if err == nil || attempt == 2 || !transient(err) || ctx.Err() != nil {
			break
		}
		if err := sleep(ctx, cmp.Or(f.retryDelay, retryDelay)); err != nil {
			break
		}
	}
	switch {
	case err == nil:
		return res, nil
	case ctx.Err() != nil:
		return Result{}, fmt.Errorf("download of rulesync %s cancelled: %w", version, ctx.Err())
	}
	return Result{}, fmt.Errorf("fetching rulesync %s: %w", version, explain(err))
}

// download writes url to a temporary file next to res.Path, hashing while
// it streams, and moves it to res.Path only when it matches res.SHA256.
func (f Fetcher) download(ctx context.Context, url string, res Result, goos string) (err error) {
	stall := cmp.Or(f.stallTimeout, stallTimeout)
	ctx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	resp, err := cmp.Or(f.client, defaultClient).Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return &statusError{url: url, code: resp.StatusCode, status: resp.Status}
	}
	if err := os.MkdirAll(filepath.Dir(res.Path), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(res.Path), res.Asset+".*.tmp")
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			_ = tmp.Close()
			_ = os.Remove(tmp.Name())
		}
	}()
	if f.Progress != nil {
		f.Progress(res.Version, resp.ContentLength)
	}
	timer := time.AfterFunc(stall, func() { cancel(errStalled) })
	defer timer.Stop()
	limit := cmp.Or(f.maxSize, maxSize)
	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(tmp, h), io.LimitReader(stallReader{resp.Body, timer, stall}, limit+1))
	switch {
	case errors.Is(context.Cause(ctx), errStalled):
		return fmt.Errorf("%w: no data received for %s", errStalled, stall)
	case err != nil:
		return err
	case n > limit:
		return fmt.Errorf("GET %s: body is larger than %d bytes", url, limit)
	}
	if got := hex.EncodeToString(h.Sum(nil)); got != res.SHA256 {
		return fmt.Errorf("checksum mismatch for %s: got %s, want %s", res.Asset, got, res.SHA256)
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if goos != "windows" {
		if err := os.Chmod(tmp.Name(), 0o755); err != nil {
			return err
		}
	}
	return f.place(ctx, tmp.Name(), res.Path, res.SHA256)
}

// place renames the verified tmp to dst. A failed rename succeeds anyway
// when dst already holds the verified binary, as when another run won the
// race; otherwise it is retried a few times, since on Windows a scanner or
// another process may briefly hold dst.
func (f Fetcher) place(ctx context.Context, tmp, dst, want string) error {
	rename := f.rename
	if rename == nil {
		rename = os.Rename
	}
	for attempt := 1; ; attempt++ {
		err := rename(tmp, dst)
		if err == nil {
			return nil
		}
		if got, herr := hashFile(dst); herr == nil && got == want {
			return os.Remove(tmp)
		}
		if attempt == renameAttempts {
			return err
		}
		if err := sleep(ctx, time.Duration(attempt)*cmp.Or(f.retryDelay, retryDelay)/4); err != nil {
			return err
		}
	}
}

// stallReader restarts the stall timer whenever data arrives.
type stallReader struct {
	r     io.Reader
	timer *time.Timer
	stall time.Duration
}

func (s stallReader) Read(p []byte) (int, error) {
	n, err := s.r.Read(p)
	if n > 0 {
		s.timer.Reset(s.stall)
	}
	return n, err
}

// statusError is a download answered with a status other than 200.
type statusError struct {
	url    string
	code   int
	status string
}

func (e *statusError) Error() string {
	switch e.code {
	case http.StatusForbidden, http.StatusTooManyRequests:
		return fmt.Sprintf("the release server refused the download (%s): rate limited, retry later", e.status)
	case http.StatusProxyAuthRequired:
		return fmt.Sprintf("the proxy requires authentication (%s): check HTTPS_PROXY", e.status)
	}
	return fmt.Sprintf("GET %s: %s", e.url, e.status)
}

// transient reports whether a second attempt may succeed.
func transient(err error) bool {
	var s *statusError
	return errors.As(err, &s) && s.code >= 500 || errors.Is(err, syscall.ECONNRESET)
}

// explain adds a hint to an error that means the server cannot be reached.
func explain(err error) error {
	var dns *net.DNSError
	var op *net.OpError
	if errors.As(err, &dns) || errors.As(err, &op) && op.Op == "dial" {
		return fmt.Errorf("cannot reach the release server, you may be offline or behind a proxy (set HTTPS_PROXY): %w", err)
	}
	return err
}

// removeStale removes temporary files of dst left by runs that died, but
// not those of a run that may still be downloading.
func removeStale(dst string) {
	tmps, _ := filepath.Glob(dst + ".*.tmp")
	for _, t := range tmps {
		if info, err := os.Stat(t); err == nil && time.Since(info.ModTime()) > staleAge {
			_ = os.Remove(t)
		}
	}
}

func sleep(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

func hashFile(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
