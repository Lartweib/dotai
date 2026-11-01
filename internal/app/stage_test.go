package app_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/lartweib/dotai/internal/app"
	"github.com/lartweib/dotai/internal/merge"
	"github.com/lartweib/dotai/internal/rulesync"
)

var examplePack = filepath.Join("..", "..", "examples", "minimal-pack")

// tree lists the files under dir as slash paths.
func tree(t *testing.T, dir string) []string {
	t.Helper()
	var out []string
	err := filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, err := filepath.Rel(dir, p)
		out = append(out, filepath.ToSlash(rel))
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func writeFile(t *testing.T, name, data string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(name), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(name, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestStageWritesEveryStagedFile(t *testing.T) {
	out := filepath.Join(t.TempDir(), "staging")
	fr := quiet(t)
	fr.tools = app.ToolsIn("home", "project")
	res, err := fr.stage(examplePack, out, app.Selection{Profiles: []string{"web"}}, false)
	if err != nil || len(res.Diagnostics) != 0 {
		t.Fatalf("Stage() = %+v, %v", res, err)
	}
	// The relative tools paths read the same on every platform.
	expand := strings.NewReplacer("${DOTAI_TOOLS}", "home/.dotai/tools", "${DOTAI_PROJECT_TOOLS}", "project/.dotai/tools")
	// The files merged from the layers of the default variant and the web
	// profile, comments dropped (decision 0040).
	merged := map[string]string{
		"user/.rulesync/permissions.jsonc": `{
  "permission": {
    "bash": {
      "git status": "allow",
      "git diff *": "allow",
      "git push --force *": "deny"
    },
    "read": {
      ".env": "deny"
    }
  }
}
`,
	}
	want := []string{app.StageMarker}
	for _, f := range res.Files {
		want = append(want, f.Scope+"/"+f.Path)
		if f.Generator != "" {
			continue
		}
		data, err := os.ReadFile(filepath.Join(out, f.Scope, filepath.FromSlash(f.Path)))
		if err != nil {
			t.Fatal(err)
		}
		if f.Layer == "" { // merged: no single source
			if w, ok := merged[f.Scope+"/"+f.Path]; !ok || string(data) != w {
				t.Errorf("merged %s/%s =\n%s\nwant\n%s", f.Scope, f.Path, data, w)
			}
			continue
		}
		rel := strings.Replace(f.Path, ".dotai/tools/", "dotai/tools/", 1)
		src, err := os.ReadFile(filepath.Join(examplePack, filepath.FromSlash(f.Layer), filepath.FromSlash(rel)))
		if err != nil || string(data) != expand.Replace(string(src)) || strings.Contains(string(data), "${DOTAI_") {
			t.Errorf("%s/%s does not match its source in %s with the tools paths: %v", f.Scope, f.Path, f.Layer, err)
		}
	}
	if data, _ := os.ReadFile(filepath.Join(out, "user", ".rulesync", "rules", "overview.md")); !strings.Contains(string(data), "home/.dotai/tools/hello.sh") {
		t.Errorf("overview.md = %q, want the tools path", data)
	}
	slices.Sort(want)
	if got := tree(t, out); len(want) < 2 || !reflect.DeepEqual(got, want) {
		t.Errorf("tree = %v, want %v", got, want)
	}
	if siblings, _ := os.ReadDir(filepath.Dir(out)); len(siblings) != 1 {
		t.Errorf("entries next to --out = %v, want only --out", siblings)
	}
}

func TestStageForceReplacesAStagingDirectory(t *testing.T) {
	out := t.TempDir()
	if _, err := quiet(t).stage(examplePack, out, app.Selection{}, false); err != nil {
		t.Fatal(err)
	}
	stale := filepath.Join(out, "user", "stale.md")
	writeFile(t, stale, "stale")

	if _, err := quiet(t).stage(examplePack, out, app.Selection{}, false); err == nil || !strings.Contains(err.Error(), "--force") {
		t.Errorf("Stage() error = %v, want a refusal that names --force", err)
	}
	if _, err := quiet(t).stage(examplePack, out, app.Selection{}, true); err != nil {
		t.Fatalf("Stage(force) error = %v", err)
	}
	if _, err := os.Stat(stale); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("stale file still there: %v", err)
	}
}

func TestStageRefusesAForeignDirectoryEvenWithForce(t *testing.T) {
	out := t.TempDir()
	writeFile(t, filepath.Join(out, "keep.txt"), "keep")
	for _, force := range []bool{false, true} {
		if _, err := quiet(t).stage(examplePack, out, app.Selection{}, force); err == nil {
			t.Errorf("Stage(force=%v) error = nil, want a refusal", force)
		}
	}
	if got := tree(t, out); !reflect.DeepEqual(got, []string{"keep.txt"}) {
		t.Errorf("tree = %v, want only keep.txt", got)
	}
}

func TestStageRejectsOutInsideThePack(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "dotai.yaml"), "schema: dotai.pack/v1\nname: p\nversion: 1.0.0\nengine: '>=0.1.0'\nrulesync: 41.0.0\nharnesses: [opencode]\n")
	writeFile(t, filepath.Join(dir, "base", ".rulesync", "rules", "r.md"), "# R\n")
	for _, out := range []string{dir, filepath.Join(dir, "staging"), filepath.Join(dir, "base", "..", "x")} {
		_, err := quiet(t).stage(dir, out, app.Selection{}, true)
		var usage *app.UsageError
		if !errors.As(err, &usage) {
			t.Errorf("Stage(out=%s) error = %v, want a usage error", out, err)
		}
	}
	if got := tree(t, dir); len(got) != 2 {
		t.Errorf("pack tree = %v, want it unchanged", got)
	}
}

func TestStageInvalidPackWritesNothing(t *testing.T) {
	out := filepath.Join(t.TempDir(), "staging")
	res, err := quiet(t).stage(examplePack, out, app.Selection{Variant: "nope"}, false)
	if err != nil {
		t.Fatalf("Stage() error = %v", err)
	}
	if len(res.Diagnostics) != 1 || res.Diagnostics[0].Code != merge.CodeLayerSelection || len(res.Files) != 0 {
		t.Errorf("result = %+v, want one layer-selection diagnostic", res)
	}
	if _, err := os.Stat(out); !os.IsNotExist(err) {
		t.Errorf("out exists after an invalid stage: %v", err)
	}
}

func TestStageNeverWritesThroughLinks(t *testing.T) {
	out, outside := filepath.Join(t.TempDir(), "staging"), t.TempDir()
	writeFile(t, filepath.Join(out, app.StageMarker), "")
	if err := os.Symlink(outside, filepath.Join(out, "user")); err != nil {
		t.Skipf("symbolic links not supported here: %v", err)
	}
	if _, err := quiet(t).stage(examplePack, out, app.Selection{}, true); err != nil {
		t.Fatalf("Stage() error = %v", err)
	}
	if got := tree(t, outside); len(got) != 0 {
		t.Errorf("files written outside --out: %v", got)
	}
}

// failingFS fails one call of method, after skipping skip calls, and does
// the rest on disk.
type failingFS struct {
	app.OSStageFS
	method string
	skip   int
}

var errDisk = errors.New("disk full")

func (f *failingFS) fail(method string) bool {
	if f.method == method && f.skip > 0 {
		f.skip--
		return false
	}
	if f.method == method {
		f.method = ""
		return true
	}
	return false
}

func (f *failingFS) WriteFile(dir, name string, data []byte, perm os.FileMode) error {
	if f.fail("WriteFile") {
		return errDisk
	}
	return f.OSStageFS.WriteFile(dir, name, data, perm)
}

func (f *failingFS) Rename(from, to string) error {
	if f.fail("Rename") {
		return errDisk
	}
	return f.OSStageFS.Rename(from, to)
}

func TestStageFailureLeavesOutUntouched(t *testing.T) {
	for _, fs := range []failingFS{{method: "WriteFile"}, {method: "Rename"}, {method: "Rename", skip: 1}} {
		t.Run(fmt.Sprint(fs.method, fs.skip), func(t *testing.T) {
			parent := t.TempDir()
			out := filepath.Join(parent, "staging")
			if _, err := quiet(t).stage(examplePack, out, app.Selection{}, false); err != nil {
				t.Fatal(err)
			}
			before := tree(t, out)

			_, err := quiet(t).stageWith(&fs, examplePack, out, app.Selection{Variant: "tmux"}, true)
			if !errors.Is(err, errDisk) || !strings.Contains(err.Error(), out) {
				t.Errorf("error = %v, want the failure and the --out path", err)
			}
			if got := tree(t, out); !reflect.DeepEqual(got, before) {
				t.Errorf("tree = %v, want %v", got, before)
			}
			if entries, _ := os.ReadDir(parent); len(entries) != 1 {
				t.Errorf("entries next to --out = %v, want the temporary directories removed", entries)
			}
		})
	}
}

// fakeRulesync stands in for fetching and running rulesync. It records
// its runs and, unless quiet, writes one CLAUDE.md per scope where rulesync
// writes the root rule of claudecode, and settings, when set, as the user
// scope's .claude/settings.json.
type fakeRulesync struct {
	t                *testing.T
	quiet            bool
	settings         string
	tools            app.ToolPaths
	fetchErr, runErr error
	runs             []rulesyncRun
}

// rulesyncRun is one scope a fake rulesync rendered: its tree, whether it
// was global, the targets, the version and what the tree held.
type rulesyncRun struct {
	dir     string
	global  bool
	targets []string
	version string
	tree    []string
}

func quiet(t *testing.T) *fakeRulesync { return &fakeRulesync{t: t, quiet: true} }

func (f *fakeRulesync) fetch(_ context.Context, _, version string, _ app.Progress) (rulesync.Result, error) {
	return rulesync.Result{Version: version}, f.fetchErr
}

func (f *fakeRulesync) run(_ context.Context, bin rulesync.Result, targets []string, jobs ...rulesync.Job) error {
	for _, j := range jobs {
		f.runs = append(f.runs, rulesyncRun{j.Dir, j.Global, targets, bin.Version, tree(f.t, j.Dir)})
		if f.runErr != nil {
			return f.runErr
		}
		name := "CLAUDE.md"
		if j.Global {
			name = filepath.Join(".claude", "CLAUDE.md")
		}
		if !f.quiet {
			writeFile(f.t, filepath.Join(j.Dir, name), "# generated\n")
		}
		if f.settings != "" && j.Global {
			writeFile(f.t, filepath.Join(j.Dir, ".claude", "settings.json"), f.settings)
		}
	}
	return nil
}

func (f *fakeRulesync) stage(dir, out string, sel app.Selection, force bool) (app.StageResult, error) {
	return f.stageWith(app.OSStageFS{}, dir, out, sel, force)
}

func (f *fakeRulesync) stageWith(w app.StageFS, dir, out string, sel app.Selection, force bool) (app.StageResult, error) {
	return app.StageWith(w, f.fetch, f.run, dir, out, sel, f.tools, force)
}

func TestStageRunsRulesyncInEachScope(t *testing.T) {
	out := filepath.Join(t.TempDir(), "staging")
	f := &fakeRulesync{t: t}
	res, err := f.stage(examplePack, out, app.Selection{Profiles: []string{"web"}}, false)
	if err != nil {
		t.Fatalf("Stage() error = %v", err)
	}
	if len(f.runs) != 2 {
		t.Fatalf("runs = %+v, want one per scope", f.runs)
	}
	harnesses := []string{"claudecode", "antigravity-cli", "opencode"}
	for i, want := range []struct {
		scope, generated string
		global           bool
		source           string
	}{{app.ScopeUser, ".claude/CLAUDE.md", true, ".rulesync/rules/overview.md"}, {app.ScopeProject, "CLAUDE.md", false, ".rulesync/rules/web.md"}} {
		r := f.runs[i]
		if filepath.Base(r.dir) != want.scope || filepath.Dir(r.dir) == out || r.global != want.global || r.version != "41.0.0" ||
			!reflect.DeepEqual(r.targets, harnesses) || !slices.Contains(r.tree, want.source) {
			t.Errorf("run %d = %+v, want %s in a temporary tree before the swap, global %v, the pack's rulesync and harnesses", i, r, want.scope, want.global)
		}
		if _, err := os.Stat(filepath.Join(out, want.scope, filepath.FromSlash(want.generated))); err != nil {
			t.Errorf("generated file missing: %v", err)
		}
		if !slices.ContainsFunc(res.Files, func(f app.StagedFile) bool {
			return reflect.DeepEqual(f, app.StagedFile{Scope: want.scope, Path: want.generated, Generator: app.GeneratorRulesync})
		}) {
			t.Errorf("files = %+v, want %s/%s from rulesync", res.Files, want.scope, want.generated)
		}
	}
}

// TestStageRendersTheOptionalHarnesses checks that stage renders every
// harness the pack supports, the optional ones too (decision 0038).
func TestStageRendersTheOptionalHarnesses(t *testing.T) {
	dir := copyPack(t)
	editPack(t, dir, "dotai.yaml", "harnesses: [claudecode, antigravity-cli, opencode]", "harnesses: [claudecode]\noptional-harnesses: [antigravity-cli, opencode]")
	out := filepath.Join(t.TempDir(), "staging")
	f := &fakeRulesync{t: t}
	if _, err := f.stage(dir, out, app.Selection{}, false); err != nil {
		t.Fatalf("Stage() error = %v", err)
	}
	if len(f.runs) == 0 || !reflect.DeepEqual(f.runs[0].targets, []string{"claudecode", "antigravity-cli", "opencode"}) {
		t.Errorf("runs = %+v, want rulesync for the required harnesses, then the optional ones", f.runs)
	}
	for _, name := range []string{"user/.gemini/antigravity-cli/settings.json", "user/.config/opencode/opencode.jsonc"} {
		if _, err := os.Stat(filepath.Join(out, filepath.FromSlash(name))); err != nil {
			t.Errorf("the settings of an optional harness are missing: %v", err)
		}
	}
}

func TestStageRendersARelativeOutWithAbsolutePaths(t *testing.T) {
	pack, err := filepath.Abs(examplePack)
	if err != nil {
		t.Fatal(err)
	}
	t.Chdir(t.TempDir())
	f := &fakeRulesync{t: t}
	if _, err := f.stage(pack, "staging", app.Selection{Profiles: []string{"web"}}, false); err != nil {
		t.Fatalf("Stage() error = %v", err)
	}
	for _, r := range f.runs {
		if !filepath.IsAbs(r.dir) {
			t.Errorf("rulesync ran in %s, want an absolute directory", r.dir)
		}
	}
	for _, name := range []string{"user/.claude/CLAUDE.md", "project/CLAUDE.md"} {
		if _, err := os.Stat(filepath.Join("staging", filepath.FromSlash(name))); err != nil || len(f.runs) != 2 {
			t.Errorf("%s not rendered after %d runs: %v", name, len(f.runs), err)
		}
	}
}

func TestStageRunsRulesyncOnlyWhereThereAreSources(t *testing.T) {
	for _, tt := range []struct {
		name, source string
		sel          app.Selection
		want         string
	}{
		{"user only", filepath.Join("base", ".rulesync", "rules", "r.md"), app.Selection{}, app.ScopeUser},
		{"project only", filepath.Join("profiles", "p", ".rulesync", "rules", "r.md"), app.Selection{Profiles: []string{"p"}}, app.ScopeProject},
	} {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			writeFile(t, filepath.Join(dir, "dotai.yaml"), "schema: dotai.pack/v1\nname: p\nversion: 1.0.0\nengine: '>=0.1.0'\nrulesync: 41.0.0\nharnesses: [opencode]\nprofiles:\n  p:\n    path: profiles/p\n    description: P.\n")
			writeFile(t, filepath.Join(dir, "base", "dotai", "settings.yaml"), "{}\n")
			writeFile(t, filepath.Join(dir, "profiles", "p", "dotai", "settings.yaml"), "{}\n")
			writeFile(t, filepath.Join(dir, tt.source), "# R\n")
			f := quiet(t)
			if res, err := f.stage(dir, filepath.Join(t.TempDir(), "s"), tt.sel, false); err != nil || len(res.Diagnostics) > 0 {
				t.Fatalf("Stage() = %+v, %v", res, err)
			}
			if len(f.runs) != 1 || filepath.Base(f.runs[0].dir) != tt.want {
				t.Errorf("runs = %+v, want only the %s scope", f.runs, tt.want)
			}
		})
	}
}

func TestStageRulesyncFailureLeavesOutAsItWas(t *testing.T) {
	errRulesync := errors.New("rulesync generate failed")
	for _, tt := range []struct {
		name   string
		f      *fakeRulesync
		before bool // an earlier staging is replaced with --force
	}{
		{"fetch", &fakeRulesync{t: t, fetchErr: errRulesync}, false},
		{"run", &fakeRulesync{t: t, runErr: errRulesync}, false},
		{"run while replacing", &fakeRulesync{t: t, runErr: errRulesync}, true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			parent := t.TempDir()
			out := filepath.Join(parent, "staging")
			var before []string
			if tt.before {
				if _, err := (&fakeRulesync{t: t}).stage(examplePack, out, app.Selection{}, false); err != nil {
					t.Fatal(err)
				}
				before = tree(t, out)
			}
			_, err := tt.f.stage(examplePack, out, app.Selection{}, tt.before)
			if !errors.Is(err, errRulesync) {
				t.Errorf("Stage() error = %v, want the rulesync failure", err)
			}
			want := 0
			if tt.before {
				want = 1
			}
			if entries, _ := os.ReadDir(parent); len(entries) != want {
				t.Errorf("entries next to --out = %v, want only what was there", entries)
			}
			if tt.before {
				if got := tree(t, out); !reflect.DeepEqual(got, before) || !slices.Contains(got, "user/.claude/CLAUDE.md") {
					t.Errorf("tree = %v, want the earlier staging %v", got, before)
				}
			}
		})
	}
}

func TestStageRendersSettingsAfterRulesync(t *testing.T) {
	// agy's statusLine runs the tool hello (decision 0037), unquoted since
	// agy splits the command itself, in JSON.
	statusLine := `sh home/.dotai/tools/hello.sh`
	if runtime.GOOS == "windows" {
		statusLine = `powershell.exe -NoProfile -ExecutionPolicy Bypass -File home\\.dotai\\tools\\hello.ps1`
	}
	out := filepath.Join(t.TempDir(), "staging")
	f := &fakeRulesync{t: t, quiet: true, settings: "{\n  \"hooks\": {}\n}\n", tools: app.ToolsIn("home", "project")}
	res, err := f.stage(examplePack, out, app.Selection{Variant: "tmux"}, false)
	if err != nil {
		t.Fatalf("Stage() error = %v", err)
	}
	for _, want := range []struct {
		file app.StagedFile
		data string
	}{
		{app.StagedFile{Scope: app.ScopeUser, Path: ".claude/settings.json", Origins: map[string]string{"model": "variants/tmux", "env.DOTAI_TERMINAL": "variants/tmux"}, Generator: app.GeneratorRulesyncDotai},
			"{\n  \"hooks\": {},\n  \"model\": \"opus\",\n  \"env\": {\n    \"DOTAI_TERMINAL\": \"tmux\"\n  }\n}\n"},
		{app.StagedFile{Scope: app.ScopeUser, Path: ".gemini/antigravity-cli/settings.json", Origins: map[string]string{"statusLine": "base"}, Generator: app.GeneratorDotai},
			"{\n  \"statusLine\": {\n    \"type\": \"command\",\n    \"command\": \"" + statusLine + "\"\n  }\n}\n"},
	} {
		if !slices.ContainsFunc(res.Files, func(f app.StagedFile) bool { return reflect.DeepEqual(f, want.file) }) {
			t.Errorf("files = %+v, want %+v", res.Files, want.file)
		}
		if data, err := os.ReadFile(filepath.Join(out, want.file.Scope, filepath.FromSlash(want.file.Path))); err != nil || string(data) != want.data {
			t.Errorf("%s = %q, %v, want %q", want.file.Path, data, err, want.data)
		}
	}
}

func TestStageSettingsConflictWritesNothing(t *testing.T) {
	parent := t.TempDir()
	out := filepath.Join(parent, "staging")
	if _, err := quiet(t).stage(examplePack, out, app.Selection{}, false); err != nil {
		t.Fatal(err)
	}
	before := tree(t, out)
	f := &fakeRulesync{t: t, quiet: true, settings: `{"model": "haiku"}`}
	res, err := f.stage(examplePack, out, app.Selection{}, true)
	want := app.Diagnostic{File: "base/dotai/settings.yaml", Code: app.CodeSettingsConflict}
	if err != nil || len(res.Diagnostics) != 1 || len(res.Files) != 0 || res.Diagnostics[0].File != want.File || res.Diagnostics[0].Code != want.Code ||
		!strings.Contains(res.Diagnostics[0].Message, "user/.claude/settings.json") {
		t.Errorf("Stage() = %+v, %v, want one diagnostic at %s about user/.claude/settings.json", res, err, want.File)
	}
	if got := tree(t, out); !reflect.DeepEqual(got, before) {
		t.Errorf("tree = %v, want %v", got, before)
	}
	if entries, _ := os.ReadDir(parent); len(entries) != 1 {
		t.Errorf("entries next to --out = %v, want only --out", entries)
	}
}

func TestStageRendersSettingsWithoutOtherFiles(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "dotai.yaml"), "schema: dotai.pack/v1\nname: p\nversion: 1.0.0\nengine: '>=0.1.0'\nrulesync: 41.0.0\nharnesses: [opencode]\nprofiles:\n  p:\n    path: profiles/p\n    description: P.\n")
	writeFile(t, filepath.Join(dir, "base", "dotai", "settings.yaml"), "opencode: {model: a}\n")
	writeFile(t, filepath.Join(dir, "profiles", "p", "dotai", "settings.yaml"), "opencode: {model: b}\n")
	out := filepath.Join(t.TempDir(), "s")
	f := quiet(t)
	res, err := f.stage(dir, out, app.Selection{Profiles: []string{"p"}}, false)
	if err != nil || len(f.runs) != 0 {
		t.Fatalf("Stage() = %+v, %v after %d runs, want no rulesync run", res, err, len(f.runs))
	}
	for _, want := range []app.StagedFile{
		{Scope: app.ScopeUser, Path: ".config/opencode/opencode.jsonc", Origins: map[string]string{"model": "base"}, Generator: app.GeneratorDotai},
		{Scope: app.ScopeProject, Path: "opencode.jsonc", Origins: map[string]string{"model": "profiles/p"}, Generator: app.GeneratorDotai},
	} {
		if !slices.ContainsFunc(res.Files, func(f app.StagedFile) bool { return reflect.DeepEqual(f, want) }) {
			t.Errorf("files = %+v, want %+v", res.Files, want)
		}
		if _, err := os.Stat(filepath.Join(out, want.Scope, filepath.FromSlash(want.Path))); err != nil {
			t.Errorf("settings file missing: %v", err)
		}
	}
}
