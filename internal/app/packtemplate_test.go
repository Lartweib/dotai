package app_test

import (
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
	templateOnlyWarnings(t, res)
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
			templateOnlyWarnings(t, res)
			if got := len(res.Pack.Profiles); got != len(a.Profiles) {
				t.Errorf("the pack has %d profiles, want %d", got, len(a.Profiles))
			}
		})
	}
}
