package app_test

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"testing/fstest"

	"github.com/lartweib/dotai/internal/app"
	"github.com/lartweib/dotai/internal/pack"
)

func TestValidateExamplePack(t *testing.T) {
	res, err := app.Validate(filepath.Join("..", "..", "examples", "minimal-pack"))
	if err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
	if !res.Valid || len(res.Diagnostics) != 0 {
		t.Fatalf("result = %+v, want valid without diagnostics", res)
	}
	if res.Pack == nil || res.Pack.Name != "minimal-pack" || res.Pack.DefaultVariant == "" {
		t.Errorf("pack = %+v", res.Pack)
	}
}

func TestValidateFS(t *testing.T) {
	t.Run("valid", func(t *testing.T) {
		res, err := app.ValidateFS(fstest.MapFS{
			"dotai.yaml": {Data: []byte("schema: dotai.pack/v1\nname: p\nversion: 1.0.0\nengine: '>=0.1.0'\n" +
				"rulesync: 41.0.0\nharnesses: [opencode]\nvariants:\n  a: {path: variants/a, default: true}\n")},
			"base/.rulesync/rules/r.md":       {Data: []byte("# R\n")},
			"variants/a/.rulesync/rules/x.md": {Data: []byte("# X\n")},
		})
		want := app.ValidateResult{
			Valid: true,
			Pack: &app.PackSummary{
				Name: "p", Version: "1.0.0", Engine: ">=0.1.0", Rulesync: "41.0.0",
				Harnesses: []string{"opencode"}, Profiles: []string{}, Variants: []string{"a"}, DefaultVariant: "a",
				Layers: []app.LayerSummary{
					{Kind: "base", Name: "base", Path: "base", Items: 1},
					{Kind: "variant", Name: "a", Path: "variants/a", Items: 1},
				},
			},
			Diagnostics: []pack.Diagnostic{},
		}
		if err != nil || !reflect.DeepEqual(res, want) {
			t.Errorf("result = %+v\nwant %+v", res, want)
		}
	})
	t.Run("missing manifest", func(t *testing.T) {
		res, err := app.ValidateFS(fstest.MapFS{})
		if err != nil || res.Valid || res.Pack != nil || len(res.Diagnostics) != 1 {
			t.Errorf("result = %+v, want invalid without pack", res)
		}
	})
}

func TestValidateUnreadableManifestIsAnError(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "dotai.yaml"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := app.Validate(dir); err == nil {
		t.Error("Validate() error = nil, want error for a dotai.yaml directory")
	}
}

func TestValidateRejectsNonDirectory(t *testing.T) {
	f := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(f, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	for _, dir := range []string{f, filepath.Join(t.TempDir(), "missing")} {
		if _, err := app.Validate(dir); err == nil {
			t.Errorf("Validate(%q) error = nil, want error", dir)
		}
	}
}
