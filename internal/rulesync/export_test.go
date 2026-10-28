package rulesync

import (
	"net/http"
	"testing"
	"time"
)

// SetReleases replaces the checksum table for the duration of t.
func SetReleases(t *testing.T, r map[string]map[string]Asset) {
	t.Helper()
	old := releases
	releases = r
	t.Cleanup(func() { releases = old })
}

// NewTestFetcher returns a linux/amd64 Fetcher that downloads from baseURL
// through client and retries without waiting.
func NewTestFetcher(client *http.Client, baseURL string) Fetcher {
	return Fetcher{client: client, baseURL: baseURL, goos: "linux", goarch: "amd64", retryDelay: time.Nanosecond}
}

func (f *Fetcher) SetPlatform(goos, goarch string)          { f.goos, f.goarch = goos, goarch }
func (f *Fetcher) SetMaxSize(n int64)                       { f.maxSize = n }
func (f *Fetcher) SetStallTimeout(d time.Duration)          { f.stallTimeout = d }
func (f *Fetcher) SetRename(fn func(from, to string) error) { f.rename = fn }
