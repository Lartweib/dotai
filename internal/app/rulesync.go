package app

import (
	"context"

	"github.com/lartweib/dotai/internal/rulesync"
)

// RulesyncResult is the outcome of fetching the rulesync a pack pins. The
// fetch fields are empty when there are diagnostics.
type RulesyncResult struct {
	Version     string       `json:"version"`
	Asset       string       `json:"asset"`
	Path        string       `json:"path"`
	SHA256      string       `json:"sha256"`
	Cached      bool         `json:"cached"`
	Diagnostics []Diagnostic `json:"diagnostics"`
	Pack        *PackSummary `json:"-"` // names the pack in the text report; nil when the manifest cannot be read
}

// Progress is told the version and size in bytes (-1 when unknown) of a
// download before it starts. It is never called for a cached binary.
type Progress = func(version string, size int64)

// fetchFunc is the port that puts a verified rulesync into cacheDir.
type fetchFunc func(ctx context.Context, cacheDir, version string, progress Progress) (rulesync.Result, error)

// FetchRulesync validates the pack in dir and fetches the rulesync binary
// it pins into cacheDir, an absolute path. A pack with problems gives
// diagnostics and fetches nothing. progress may be nil.
func FetchRulesync(ctx context.Context, cacheDir, dir string, progress Progress) (RulesyncResult, error) {
	return fetchRulesync(ctx, download, cacheDir, dir, progress)
}

func download(ctx context.Context, cacheDir, version string, progress Progress) (rulesync.Result, error) {
	return rulesync.Fetcher{Progress: progress}.Fetch(ctx, cacheDir, version)
}

func fetchRulesync(ctx context.Context, fetch fetchFunc, cacheDir, dir string, progress Progress) (RulesyncResult, error) {
	fsys, err := packFS(dir)
	if err != nil {
		return RulesyncResult{}, err
	}
	v, p, err := validate(fsys, Selection{})
	if err != nil {
		return RulesyncResult{}, err
	}
	res := RulesyncResult{Diagnostics: v.Diagnostics, Pack: v.Pack}
	if !v.Valid {
		return res, nil
	}
	r, err := fetch(ctx, cacheDir, p.Manifest.Rulesync, progress)
	if err != nil {
		return RulesyncResult{}, err
	}
	res.Version, res.Asset, res.Path, res.SHA256, res.Cached = r.Version, r.Asset, r.Path, r.SHA256, r.Cached
	return res, nil
}

// Rulesync is the port Stage renders harness output through. Fetch returns
// the verified binary of a rulesync version. Run renders the .rulesync/ of
// the scope tree in dir for harnesses, into dir; global selects the user
// scope.
type Rulesync struct {
	Fetch func(ctx context.Context, version string) (rulesync.Result, error)
	Run   func(ctx context.Context, bin rulesync.Result, dir string, global bool, harnesses []string) error
}

// NewRulesync is the Rulesync that fetches into cacheDir, an absolute path,
// and runs the binary as a process. progress may be nil.
func NewRulesync(cacheDir string, progress Progress) Rulesync {
	return Rulesync{
		Fetch: func(ctx context.Context, version string) (rulesync.Result, error) {
			return download(ctx, cacheDir, version, progress)
		},
		Run: func(ctx context.Context, bin rulesync.Result, dir string, global bool, harnesses []string) error {
			return rulesync.Run(ctx, rulesync.OSExec, bin, dir, global, harnesses)
		},
	}
}
