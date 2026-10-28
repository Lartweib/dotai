package app_test

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/lartweib/dotai/internal/app"
	"github.com/lartweib/dotai/internal/rulesync"
)

func TestFetchRulesync(t *testing.T) {
	var asked []string
	fetch := func(_ context.Context, version string) (rulesync.Result, error) {
		asked = append(asked, version)
		return rulesync.Result{Version: version, Asset: "a"}, nil
	}

	res, err := app.FetchRulesyncWith(fetch, filepath.Join("..", "..", "examples", "minimal-pack"))
	if err != nil || res.Version != "41.0.0" || res.Asset != "a" || len(res.Diagnostics) != 0 || res.Pack == nil {
		t.Errorf("result = %+v, %v, want the example pack's rulesync", res, err)
	}

	asked = nil
	res, err = app.FetchRulesyncWith(fetch, filepath.Join("..", "cli", "testdata", "broken-pack"))
	if err != nil || len(res.Diagnostics) == 0 || len(asked) != 0 {
		t.Errorf("broken pack: result = %+v, %v, fetched %v, want diagnostics and no fetch", res, err, asked)
	}
}
