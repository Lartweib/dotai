// Package rulesync fetches the pinned rulesync binary (0021). The engine
// embeds the SHA-256 of every release asset it supports (0029): a download
// or a cached file is used only when it matches that table.
package rulesync

import (
	"cmp"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"maps"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"time"
)

// Asset is one release binary and its expected SHA-256.
type Asset struct {
	Name   string
	SHA256 string
}

// releases maps a rulesync version to its assets by GOOS/GOARCH. Values
// come from the release's SHA256SUMS, cross-checked by hashing a download.
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
	DefaultBaseURL = "https://github.com/dyoshikawa/rulesync/releases/download"
	DefaultMaxSize = 256 << 20
	DefaultTimeout = 10 * time.Minute
)

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

// CacheDir is DOTAI_CACHE_DIR when set, else dotai in the user cache.
func CacheDir() (string, error) {
	if d := os.Getenv("DOTAI_CACHE_DIR"); d != "" {
		return d, nil
	}
	d, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(d, "dotai"), nil
}

// Result is a fetched binary. Cached tells whether it was already there.
type Result struct {
	Version string `json:"version"`
	Asset   string `json:"asset"`
	Path    string `json:"path"`
	SHA256  string `json:"sha256"`
	Cached  bool   `json:"cached"`
}

// Fetcher downloads release assets into CacheDir. Zero fields take the
// defaults above, http.DefaultClient and the running platform.
type Fetcher struct {
	Client       *http.Client
	BaseURL      string
	CacheDir     string
	GOOS, GOARCH string
	MaxSize      int64
	Timeout      time.Duration
}

// Fetch returns the verified binary of version from
// <CacheDir>/rulesync/<version>/<asset>, downloading it when it is missing
// or does not match its checksum. A failed download leaves nothing behind.
func (f Fetcher) Fetch(ctx context.Context, version string) (Result, error) {
	a, err := Resolve(version, cmp.Or(f.GOOS, runtime.GOOS), cmp.Or(f.GOARCH, runtime.GOARCH))
	if err != nil {
		return Result{}, err
	}
	dir := filepath.Join(f.CacheDir, "rulesync", version)
	res := Result{Version: version, Asset: a.Name, Path: filepath.Join(dir, a.Name), SHA256: a.SHA256}
	if got, err := hashFile(res.Path); err == nil && got == a.SHA256 {
		res.Cached = true
		return res, nil
	}
	ctx, cancel := context.WithTimeout(ctx, cmp.Or(f.Timeout, DefaultTimeout))
	defer cancel()
	if err := f.download(ctx, cmp.Or(f.BaseURL, DefaultBaseURL)+"/v"+version+"/"+a.Name, res.Path, a.SHA256); err != nil {
		return Result{}, fmt.Errorf("fetching rulesync %s: %w", version, err)
	}
	return res, nil
}

// download writes url to a temporary file next to dst, hashing while it
// streams, and renames it to dst only when its SHA-256 is want.
func (f Fetcher) download(ctx context.Context, url, dst, want string) (err error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	client := f.Client
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("GET %s: %s", url, resp.Status)
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(dst), filepath.Base(dst)+".*.tmp")
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			_ = tmp.Close()
			_ = os.Remove(tmp.Name())
		}
	}()
	max := cmp.Or(f.MaxSize, DefaultMaxSize)
	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(tmp, h), io.LimitReader(resp.Body, max+1))
	switch {
	case err != nil:
		return err
	case n > max:
		return fmt.Errorf("GET %s: body is larger than %d bytes", url, max)
	}
	if got := hex.EncodeToString(h.Sum(nil)); got != want {
		return fmt.Errorf("checksum mismatch for %s: got %s, want %s", filepath.Base(dst), got, want)
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if runtime.GOOS != "windows" {
		if err := os.Chmod(tmp.Name(), 0o755); err != nil {
			return err
		}
	}
	return os.Rename(tmp.Name(), dst)
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
