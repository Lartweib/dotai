package app_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/lartweib/dotai/internal/app"
	"github.com/lartweib/dotai/internal/packtemplate"
	"github.com/lartweib/dotai/internal/plan"
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
	if len(unchangedFiles(res)) == 0 {
		t.Error("the template gives no template-unchanged warning")
	}
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

// templateFile is the content of the template's file p.
func templateFile(t *testing.T, p string) string {
	t.Helper()
	data, err := fs.ReadFile(packtemplate.FS(), p)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// templateWarnings are the items of the template-unchanged warnings ws.
func templateWarnings(ws []plan.Warning) []string {
	var out []string
	for _, w := range ws {
		if w.Code == app.CodeTemplateUnchanged {
			out = append(out, w.Item)
		}
	}
	return out
}

// TestAnIsolatedInstallOfATemplateFileAsksConsent: an isolated install,
// which never asked before, asks for consent while the user scope keeps a
// file of the template unchanged (decision 0046), and the profiles' files
// are left to apply.
func TestAnIsolatedInstallOfATemplateFileAsksConsent(t *testing.T) {
	home, _ := installHome(t)
	dir := copyPack(t)
	writeFile(t, filepath.Join(dir, "base", ".rulesync", "rules", "example-rule.md"), templateFile(t, "base/.rulesync/rules/example-rule.md"))
	writeFile(t, filepath.Join(dir, "profiles", "web", ".rulesync", "rules", "example.md"), templateFile(t, "profiles/example/.rulesync/rules/example.md"))
	f := quiet(t)
	p, err := f.planInstall(dir, "", home)
	if err != nil {
		t.Fatal(err)
	}
	if got := templateWarnings(p.Warnings); !slices.Equal(got, []string{"base/.rulesync/rules/example-rule.md"}) || !p.Consent || p.Blocked {
		t.Fatalf("plan: template files %v, consent %v, blocked %v; want the base's rule only, needing consent", got, p.Consent, p.Blocked)
	}
	if !slices.Equal(p.TemplateFiles(), []string{"base/.rulesync/rules/example-rule.md"}) || p.ConsentForeign {
		t.Errorf("TemplateFiles() = %v, ConsentForeign = %v, want the rule and no foreign content", p.TemplateFiles(), p.ConsentForeign)
	}
	h := app.NewHost(found(at(home, "bin"), "claude", "agy", "opencode"), nil, "")
	asked := 0
	_, err = app.InstallConsent(context.Background(), app.OSInstallFS{}, h, f.fetch, f.run, dir, home, app.InstallOptions{}, func(app.InstallPlan) (bool, error) { asked++; return false, nil })
	ce, ok := errors.AsType[*app.ConsentError](err)
	if !ok || asked != 1 || !slices.Equal(ce.Templates, []string{"base/.rulesync/rules/example-rule.md"}) {
		t.Fatalf("Install() = %v, asked %d times; want one question and a ConsentError naming the rule", err, asked)
	}
	if !strings.Contains(err.Error(), "example-rule.md") || !strings.Contains(err.Error(), "--yes to accept what the plan asks to confirm") {
		t.Errorf("Error() = %q, want the file and --yes", err)
	}
	if _, serr := os.Stat(at(home, ".dotai/state.json")); serr == nil {
		t.Error("the refused install wrote the install state")
	}
	if _, err := app.InstallHost(context.Background(), app.OSInstallFS{}, h, f.fetch, f.run, dir, home, app.InstallOptions{}); err != nil {
		t.Fatalf("Install() with consent: %v", err)
	}
}

func TestAnApplyOfATemplateFileAsksConsent(t *testing.T) {
	project, home, g := applyRepo(t)
	dir := copyPack(t)
	writeFile(t, filepath.Join(dir, "profiles", "web", ".rulesync", "rules", "example.md"), templateFile(t, "profiles/example/.rulesync/rules/example.md"))
	writeFile(t, filepath.Join(dir, "mods", "hello", "hooks", "example-mod.js"), templateFile(t, "mods/example-mod/hooks/example-mod.js"))
	writeFile(t, filepath.Join(dir, "base", ".rulesync", "rules", "example-rule.md"), templateFile(t, "base/.rulesync/rules/example-rule.md"))
	f := &treeRulesync{t: t, tree: profileTree}
	opts := app.ApplyOptions{Profiles: []string{"web"}}
	p, err := app.PlanProjectWith(context.Background(), app.OSInstallFS{}, g, f.fetch, f.run, dir, project, home, opts)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"mods/hello/hooks/example-mod.js", "profiles/web/.rulesync/rules/example.md"}
	if got := templateWarnings(p.Warnings); !slices.Equal(got, want) || !p.Consent || !slices.Equal(p.TemplateFiles(), want) {
		t.Fatalf("plan: template files %v, consent %v; want %v, needing consent", got, p.Consent, want)
	}
	_, err = app.ApplyWith(context.Background(), newClockFS(t), g, f.fetch, f.run, dir, project, home, opts, nil)
	if ce, ok := errors.AsType[*app.ApplyConsentError](err); !ok || !slices.Equal(ce.Templates, want) || !strings.Contains(err.Error(), "--yes to accept what the plan asks to confirm") {
		t.Fatalf("Apply() = %v, want an ApplyConsentError naming %v", err, want)
	}
	if _, err := app.ApplyWith(context.Background(), newClockFS(t), g, f.fetch, f.run, dir, project, home, opts, yes); err != nil {
		t.Fatalf("Apply() with consent: %v", err)
	}
}

// TestAReplaceInstallOfTemplateFilesOnlyAsksNoForeignConsent: a replace
// install that wipes and overwrites nothing dotai does not own, but
// installs a template file unchanged, needs consent for that file only,
// and its plan says so with consentForeign false, which the web UI reads.
func TestAReplaceInstallOfTemplateFilesOnlyAsksNoForeignConsent(t *testing.T) {
	home, _ := installHome(t)
	dir := copyPack(t)
	writeFile(t, filepath.Join(dir, "base", ".rulesync", "rules", "example-rule.md"), templateFile(t, "base/.rulesync/rules/example-rule.md"))
	h := app.NewHost(found(at(home, "bin"), "claude", "agy", "opencode"), nil, "")
	p, err := app.PlanInstallHost(context.Background(), app.OSInstallFS{}, h, quiet(t).fetch, replaceRun(t), dir, home, app.InstallOptions{Mode: app.ModeReplace})
	if err != nil {
		t.Fatal(err)
	}
	if !p.Consent || p.ConsentForeign || !slices.Equal(p.TemplateFiles(), []string{"base/.rulesync/rules/example-rule.md"}) {
		t.Fatalf("plan: consent %v, consentForeign %v, template files %v; want consent for the rule only", p.Consent, p.ConsentForeign, p.TemplateFiles())
	}
	data, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(data, []byte(`"consent":true,"consentForeign":false`)) {
		t.Errorf("the plan's JSON is %s, want consent true and consentForeign false", data)
	}
}
