package app_test

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/lartweib/dotai/internal/app"
	"github.com/lartweib/dotai/internal/rulesync"
)

type ctxKey struct{}

func TestFetchRulesync(t *testing.T) {
	example := filepath.Join("..", "..", "examples", "minimal-pack")
	ctx := context.WithValue(context.Background(), ctxKey{}, "run")
	var asked []string
	fetch := func(c context.Context, cacheDir, version string, _ app.Progress) (rulesync.Result, error) {
		if c.Value(ctxKey{}) != "run" {
			t.Error("fetch did not get the caller's context")
		}
		asked = append(asked, cacheDir+" "+version)
		return rulesync.Result{Version: version, Asset: "a", Path: "p", SHA256: "s", Cached: true}, nil
	}

	res, err := app.FetchRulesyncWith(ctx, fetch, "cache", example)
	want := app.RulesyncResult{Version: "43.12.0", Asset: "a", Path: "p", SHA256: "s", Cached: true, Diagnostics: []app.Diagnostic{}}
	if err != nil || res.Pack == nil || len(asked) != 1 || asked[0] != "cache 43.12.0" {
		t.Fatalf("result = %+v, %v, asked %v, want the example pack's rulesync", res, err, asked)
	}
	res.Pack = nil
	if res.Version != want.Version || res.Asset != want.Asset || res.Path != want.Path || res.SHA256 != want.SHA256 || !res.Cached || len(res.Diagnostics) != 0 {
		t.Errorf("result = %+v, want %+v", res, want)
	}

	asked = nil
	res, err = app.FetchRulesyncWith(ctx, fetch, "cache", filepath.Join("..", "cli", "testdata", "broken-pack"))
	if err != nil || len(res.Diagnostics) == 0 || len(asked) != 0 {
		t.Errorf("broken pack: result = %+v, %v, fetched %v, want diagnostics and no fetch", res, err, asked)
	}

	boom := errors.New("boom")
	failing := func(context.Context, string, string, app.Progress) (rulesync.Result, error) {
		return rulesync.Result{}, boom
	}
	if _, err := app.FetchRulesyncWith(ctx, failing, "cache", example); !errors.Is(err, boom) {
		t.Errorf("error = %v, want the fetch error", err)
	}
}
