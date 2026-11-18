package app_test

import (
	"bytes"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/lartweib/dotai/internal/app"
	"github.com/lartweib/dotai/internal/packtemplate"
)

// filled is the pack Fill writes for a, as a file system.
func filled(t *testing.T, a packtemplate.Answers) fstest.MapFS {
	t.Helper()
	files, err := packtemplate.Fill(a)
	if err != nil {
		t.Fatal(err)
	}
	fsys := fstest.MapFS{}
	for _, f := range files {
		fsys[f.Path] = &fstest.MapFile{Data: f.Data, Mode: f.Mode}
	}
	return fsys
}

// templateOnlyWarnings fails t unless res is valid, without diagnostics,
// and every warning of res is code.
func templateOnlyWarnings(t *testing.T, res app.ValidateResult, codes ...string) {
	t.Helper()
	if !res.Valid || len(res.Diagnostics) > 0 {
		t.Fatalf("the pack is not valid: %+v", res.Diagnostics)
	}
	for _, w := range res.Warnings {
		ok := false
		for _, c := range codes {
			ok = ok || w.Code == c
		}
		if !ok {
			t.Errorf("unexpected warning %s: %s: %s", w.Code, w.File, w.Message)
		}
	}
}

func TestTheEmbeddedTemplateIsAValidPack(t *testing.T) {
	res, err := app.ValidateFS(packtemplate.FS(), app.Selection{})
	if err != nil {
		t.Fatal(err)
	}
	templateOnlyWarnings(t, res, app.CodeTemplateUnchanged)
	if res.Pack.Onboarding.Mode != "dotai" {
		t.Errorf("the template's onboarding is %q, want dotai's own", res.Pack.Onboarding.Mode)
	}
}

func TestEveryFilledTemplateIsAValidPack(t *testing.T) {
	for _, a := range []packtemplate.Answers{
		{Name: "solo", Harnesses: []string{"opencode"}},
		{Name: "agy-only", Harnesses: []string{"antigravity-cli"}, Profiles: []packtemplate.Profile{{Name: "web", Description: "Web apps.", Hints: []string{"package.json", "*.config.*"}}}},
		{Name: "full", Harnesses: []string{"claudecode", "antigravity-cli", "opencode"}, Profiles: []packtemplate.Profile{
			{Name: "web", Description: "Web apps.", Mods: []string{"panel"}},
			{Name: "go", Description: "Go services: \"quoted\".", Hints: []string{"go.mod"}, Mods: []string{"panel", "lint"}},
		}},
	} {
		t.Run(a.Name, func(t *testing.T) {
			res, err := app.ValidateFS(filled(t, a), app.Selection{})
			if err != nil {
				t.Fatal(err)
			}
			templateOnlyWarnings(t, res, app.CodeTemplateUnchanged)
			if got := len(res.Pack.Profiles); got != len(a.Profiles) {
				t.Errorf("the pack has %d profiles, want %d", got, len(a.Profiles))
			}
		})
	}
}

// writePack writes fsys into a new directory and returns it.
func writePack(t *testing.T, fsys fstest.MapFS) string {
	t.Helper()
	dir := t.TempDir()
	for p, f := range fsys {
		target := filepath.Join(dir, filepath.FromSlash(p))
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(target, f.Data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// unchangedFiles are the files of the template-unchanged warnings of res.
func unchangedFiles(res app.ValidateResult) []string {
	var out []string
	for _, w := range res.Warnings {
		if w.Code == app.CodeTemplateUnchanged {
			out = append(out, w.File)
		}
	}
	return out
}

func TestValidateWarnsOfEveryTemplateFileLeftUnchanged(t *testing.T) {
	a := packtemplate.Answers{Name: "mine", Harnesses: []string{"opencode"}, Profiles: []packtemplate.Profile{{Name: "web", Description: "Web apps."}}}
	fsys := filled(t, a)
	res, err := app.ValidateFS(fsys, app.Selection{})
	if err != nil {
		t.Fatal(err)
	}
	templateOnlyWarnings(t, res, app.CodeTemplateUnchanged)
	var want []string
	for p := range fsys {
		if strings.Contains(p, "/") {
			want = append(want, p)
		}
	}
	slices.Sort(want)
	if got := unchangedFiles(res); !slices.Equal(got, want) {
		t.Errorf("template-unchanged files = %v, want every file below the top, %v", got, want)
	}

	// An edited file, and one checked out with CRLF line endings.
	fsys["base/.rulesync/rules/example-rule.md"] = &fstest.MapFile{Data: []byte("---\ntargets: [\"*\"]\n---\n\nMy own rule.\n")}
	rule := fsys["profiles/web/.rulesync/rules/web.md"]
	fsys["profiles/web/.rulesync/rules/web.md"] = &fstest.MapFile{Data: bytes.ReplaceAll(rule.Data, []byte("\n"), []byte("\r\n"))}
	res, err = app.ValidateFS(fsys, app.Selection{})
	if err != nil {
		t.Fatal(err)
	}
	got := unchangedFiles(res)
	if slices.Contains(got, "base/.rulesync/rules/example-rule.md") || !slices.Contains(got, "profiles/web/.rulesync/rules/web.md") {
		t.Errorf("template-unchanged files = %v, want the edited rule left out and the CRLF one kept", got)
	}
}

func TestTheEmbeddedTemplateWarnsOnlyOfItsOwnFiles(t *testing.T) {
	res, err := app.ValidateFS(packtemplate.FS(), app.Selection{})
	if err != nil {
		t.Fatal(err)
	}
	templateOnlyWarnings(t, res, app.CodeTemplateUnchanged)
	if len(unchangedFiles(res)) == 0 {
		t.Error("the template gives no template-unchanged warning")
	}
}

func TestAddLocalPackWarnsOfTemplateFilesLeftUnchanged(t *testing.T) {
	home := t.TempDir()
	fsys := filled(t, packtemplate.Answers{Name: "mine", Harnesses: []string{"opencode"}})
	dir := writePack(t, fsys)
	res, err := app.AddLocalPack(home, dir)
	if err != nil {
		t.Fatal(err)
	}
	i := slices.IndexFunc(res.Warnings, func(w app.PackWarning) bool { return w.Code == app.CodeTemplateUnchanged })
	if i < 0 || !strings.Contains(res.Warnings[i].Message, "base/.rulesync/rules/overview.md") {
		t.Fatalf("warnings = %+v, want template-unchanged naming the files", res.Warnings)
	}
}
