package app_test

import (
	"encoding/json"
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
				"rulesync: 41.0.0\nharnesses: [opencode]\noptional-harnesses: [claudecode]\n" +
				"profiles:\n  web: {path: profiles/web, description: Web projects., hints: [astro.config.*, next.config.*]}\n  api: {path: ./profiles/api, description: APIs.}\n" +
				"variants:\n  a: {path: variants/a, default: true}\n  b: {path: variants/b, shared-skills: true}\n")},
			"base/.rulesync/rules/r.md":         {Data: []byte("# R\n")},
			"profiles/web/.rulesync/rules/w.md": {Data: []byte("# W\n")},
			"profiles/api/.rulesync/rules/p.md": {Data: []byte("# P\n")},
			"variants/a/.rulesync/rules/x.md":   {Data: []byte("# X\n")},
			"variants/b/.rulesync/rules/y.md":   {Data: []byte("# Y\n")},
		}, app.Selection{})
		want := app.ValidateResult{
			Valid: true,
			Pack: &app.PackSummary{
				Name: "p", Version: "1.0.0", Engine: ">=0.1.0", Rulesync: "41.0.0",
				Harnesses: []string{"opencode"}, OptionalHarnesses: []string{"claudecode"}, Variants: []string{"a", "b"}, DefaultVariant: "a",
				Profiles: []app.ProfileSummary{
					{Name: "web", Path: "profiles/web", Description: "Web projects.", Hints: []string{"astro.config.*", "next.config.*"}, Mods: []string{}},
					{Name: "api", Path: "profiles/api", Description: "APIs.", Hints: []string{}, Mods: []string{}},
				},
				SharedSkills: []string{"b"},
				Onboarding:   app.OnboardingSummary{Mode: "dotai"},
				Layers: []app.LayerSummary{
					{Kind: "base", Name: "base", Path: "base", Items: 1},
					{Kind: "profile", Name: "web", Path: "profiles/web", Items: 1},
					{Kind: "profile", Name: "api", Path: "profiles/api", Items: 1},
					{Kind: "variant", Name: "a", Path: "variants/a", Items: 1},
					{Kind: "variant", Name: "b", Path: "variants/b", Items: 1},
				},
			},
			Diagnostics: []pack.Diagnostic{},
			Warnings:    []pack.Diagnostic{},
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

// TestValidateFSProfilesJSON checks the serialized profiles of a pack whose
// profiles declare no hints, one with a path outside the pack and one with
// a path that does not exist: hints are [] and an invalid path is "".
func TestValidateFSProfilesJSON(t *testing.T) {
	res, err := app.ValidateFS(fstest.MapFS{
		"dotai.yaml": {Data: []byte("schema: dotai.pack/v1\nname: p\nversion: 1.0.0\nengine: '>=0.1.0'\nrulesync: 41.0.0\n" +
			"harnesses: [opencode]\nprofiles:\n  out: {path: ../out, description: Outside.}\n  gone: {path: profiles/gone, description: Missing.}\n")},
		"base/.rulesync/rules/r.md": {Data: []byte("# R\n")},
	}, app.Selection{})
	if err != nil || res.Valid || res.Pack == nil {
		t.Fatalf("result = %+v, err = %v, want an invalid pack with a summary", res, err)
	}
	got, err := json.Marshal(res.Pack.Profiles)
	if err != nil {
		t.Fatal(err)
	}
	const want = `[{"name":"out","path":"","description":"Outside.","hints":[],"mods":[]},` +
		`{"name":"gone","path":"","description":"Missing.","hints":[],"mods":[]}]`
	if string(got) != want {
		t.Errorf("profiles = %s\nwant %s", got, want)
	}
}

// TestValidateFSOnboardingJSON checks pack.onboarding of validate --json
// (decision 0045): dotai's onboarding has a null path, a custom one the
// path the manifest gives, with its warning.
func TestValidateFSOnboardingJSON(t *testing.T) {
	const manifest = "schema: dotai.pack/v1\nname: p\nversion: 1.0.0\nengine: '>=0.1.0'\nrulesync: 41.0.0\nharnesses: [opencode]\n"
	for _, tt := range []struct {
		name, extra, want string
		warnings          []string
	}{
		{name: "dotai's", want: `{"mode":"dotai","path":null}`},
		{name: "custom", extra: "onboarding:\n  custom: base/.rulesync/subagents/setup.md\n", want: `{"mode":"custom","path":"base/.rulesync/subagents/setup.md"}`,
			warnings: []string{"onboarding-replaced"}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			res, err := app.ValidateFS(fstest.MapFS{
				"dotai.yaml":                        {Data: []byte(manifest + tt.extra)},
				"base/.rulesync/rules/r.md":         {Data: []byte("# R\n")},
				"base/.rulesync/subagents/setup.md": {Data: []byte("Setup.\n")},
			}, app.Selection{})
			if err != nil || !res.Valid || res.Pack == nil {
				t.Fatalf("result = %+v, err = %v, want a valid pack", res, err)
			}
			got, err := json.Marshal(res.Pack.Onboarding)
			if err != nil || string(got) != tt.want {
				t.Errorf("onboarding = %s, %v, want %s", got, err, tt.want)
			}
			var codes []string
			for _, w := range res.Warnings {
				codes = append(codes, w.Code)
			}
			if !reflect.DeepEqual(codes, tt.warnings) {
				t.Errorf("warnings = %v, want %v", res.Warnings, tt.warnings)
			}
		})
	}
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
