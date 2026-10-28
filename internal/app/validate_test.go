package app_test

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/lartweib/dotai/internal/app"
	"github.com/lartweib/dotai/internal/merge"
	"github.com/lartweib/dotai/internal/pack"
)

func TestValidateExamplePack(t *testing.T) {
	res, err := app.Validate(filepath.Join("..", "..", "examples", "minimal-pack"), app.Selection{})
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
		}, app.Selection{})
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
		res, err := app.ValidateFS(fstest.MapFS{}, app.Selection{})
		if err != nil || res.Valid || res.Pack != nil || len(res.Diagnostics) != 1 {
			t.Errorf("result = %+v, want invalid without pack", res)
		}
	})
}

func TestValidateFSMergeRules(t *testing.T) {
	const manifest = "schema: dotai.pack/v1\nname: p\nversion: 1.0.0\nengine: '>=0.1.0'\nrulesync: 41.0.0\n" +
		"harnesses: [opencode]\nprofiles:\n  web: {path: profiles/web, description: Web.}\n"
	conflict := fstest.MapFS{
		"dotai.yaml":                        {Data: []byte(manifest)},
		"base/.rulesync/rules/r.md":         {Data: []byte("# R\n")},
		"profiles/web/.rulesync/rules/r.md": {Data: []byte("# R\n")},
	}
	tests := []struct {
		name      string
		fsys      fstest.MapFS
		sel       app.Selection
		wantCodes []string
	}{
		{name: "merge rules run over every layer", fsys: conflict, wantCodes: []string{merge.CodeScopeConflict}},
		{name: "selection is checked", fsys: conflict, sel: app.Selection{Profiles: []string{"web", "api"}}, wantCodes: []string{merge.CodeLayerSelection}},
		{name: "empty profile selection skips profiles", fsys: conflict, sel: app.Selection{Profiles: []string{}}},
		{
			name: "an unsupported rulesync does not stop the merge rules",
			fsys: fstest.MapFS{
				"dotai.yaml":                        {Data: []byte(strings.Replace(manifest, "41.0.0", "40.0.0", 1))},
				"base/.rulesync/rules/r.md":         {Data: []byte("# R\n")},
				"profiles/web/.rulesync/rules/r.md": {Data: []byte("# R\n")},
			},
			wantCodes: []string{pack.CodeRulesyncUnsupported, merge.CodeScopeConflict},
		},
		{
			name: "merge rules wait for structural checks",
			fsys: fstest.MapFS{
				"dotai.yaml":                        {Data: []byte(strings.Replace(manifest, "1.0.0", "1.0", 1))},
				"base/.rulesync/rules/r.md":         {Data: []byte("# R\n")},
				"profiles/web/.rulesync/rules/r.md": {Data: []byte("# R\n")},
			},
			wantCodes: []string{pack.CodeVersionInvalid},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			res, err := app.ValidateFS(tt.fsys, tt.sel)
			if err != nil {
				t.Fatalf("ValidateFS() error = %v", err)
			}
			var codes []string
			for _, d := range res.Diagnostics {
				codes = append(codes, d.Code)
			}
			if !reflect.DeepEqual(codes, tt.wantCodes) || res.Valid != (len(tt.wantCodes) == 0) {
				t.Errorf("valid = %v, codes = %v, want %v", res.Valid, codes, tt.wantCodes)
			}
		})
	}
}

func TestValidateDoesNotFollowSymlinksOnDisk(t *testing.T) {
	dir := t.TempDir()
	manifest := "schema: dotai.pack/v1\nname: p\nversion: 1.0.0\nengine: '>=0.1.0'\nrulesync: 41.0.0\nharnesses: [opencode]\n"
	rules := filepath.Join(dir, "base", ".rulesync", "rules")
	if err := os.MkdirAll(rules, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "dotai.yaml"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(dir, "dotai.yaml"), filepath.Join(rules, "link.md")); err != nil {
		t.Skipf("symbolic links not supported here: %v", err)
	}

	res, err := app.Validate(dir, app.Selection{})
	if err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
	if res.Valid || len(res.Diagnostics) != 1 || res.Diagnostics[0].Code != pack.CodeSymlinkNotAllowed {
		t.Errorf("result = %+v, want one symlink-not-allowed diagnostic", res)
	}
}

func TestValidateUnreadableManifestIsAnError(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "dotai.yaml"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := app.Validate(dir, app.Selection{}); err == nil {
		t.Error("Validate() error = nil, want error for a dotai.yaml directory")
	}
}

func TestValidateRejectsNonDirectory(t *testing.T) {
	f := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(f, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	for _, dir := range []string{f, filepath.Join(t.TempDir(), "missing")} {
		if _, err := app.Validate(dir, app.Selection{}); err == nil {
			t.Errorf("Validate(%q) error = nil, want error", dir)
		}
	}
}

func TestValidateFSRulesyncVersion(t *testing.T) {
	manifest := func(v string) fstest.MapFS {
		return fstest.MapFS{"dotai.yaml": {Data: []byte("schema: dotai.pack/v1\nname: p\nversion: 1.0.0\nengine: '>=0.1.0'\n" +
			"rulesync: " + v + "\nharnesses: [opencode]\n")}}
	}
	t.Run("unsupported", func(t *testing.T) {
		res, err := app.ValidateFS(manifest("40.0.0"), app.Selection{})
		want := []pack.Diagnostic{pack.NewDiagnostic("dotai.yaml", 5, 11, pack.CodeRulesyncUnsupported,
			"rulesync 40.0.0 is not supported by this engine").WithHint("pin one of: 41.0.0")}
		if err != nil || res.Valid || !reflect.DeepEqual(res.Diagnostics, want) {
			t.Errorf("result = %+v, %v\nwant diagnostics %+v", res, err, want)
		}
	})
	t.Run("invalid is reported once", func(t *testing.T) {
		res, _ := app.ValidateFS(manifest("latest"), app.Selection{})
		if len(res.Diagnostics) != 1 || res.Diagnostics[0].Code != pack.CodeRulesyncInvalid {
			t.Errorf("diagnostics = %+v, want only %s", res.Diagnostics, pack.CodeRulesyncInvalid)
		}
	})
}
