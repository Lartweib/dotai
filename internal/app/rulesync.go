package app

import (
	"context"

	"github.com/lartweib/dotai/internal/rulesync"
)

// RulesyncResult is the outcome of fetching the rulesync a pack pins. The
// fetch fields are empty when there are diagnostics.
type RulesyncResult struct {
	rulesync.Result
	Diagnostics []Diagnostic `json:"diagnostics"`
	Pack        *PackSummary `json:"-"` // names the pack in the text report; nil when the manifest cannot be read
}

// fetchFunc is the port that downloads a rulesync version.
type fetchFunc func(ctx context.Context, version string) (rulesync.Result, error)

// FetchRulesync validates the pack in dir and fetches the rulesync binary
// it pins into the dotai cache. A pack with problems gives diagnostics and
// fetches nothing.
func FetchRulesync(dir string) (RulesyncResult, error) {
	cache, err := rulesync.CacheDir()
	if err != nil {
		return RulesyncResult{}, err
	}
	return fetchRulesync(rulesync.Fetcher{CacheDir: cache}.Fetch, dir)
}

func fetchRulesync(fetch fetchFunc, dir string) (RulesyncResult, error) {
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
	res.Result, err = fetch(context.Background(), p.Manifest.Rulesync)
	return res, err
}
