package app_test

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/lartweib/dotai/internal/app"
)

// newPackInput is the answers of a pack with one profile and one mod.
var newPackInput = app.PackNewInput{
	Name:      "my-pack",
	Harnesses: []string{"claudecode", "opencode"},
	Profiles:  []app.PackNewProfile{{Name: "web", Description: "Web apps with a JavaScript toolchain.", Hints: []string{"package.json"}, Mods: []string{"panel"}}},
}

func TestPlanPackNewShowsTheFilesAndTheSkillAndWritesNothing(t *testing.T) {
	home := t.TempDir()
	dir := filepath.Join(t.TempDir(), "my-pack")
	p, err := app.PlanPackNew(home, dir, newPackInput)
	if err != nil {
		t.Fatal(err)
	}
	if p.Dir != dir || p.ID != "local/my-pack" || !slices.Contains(p.Files, "dotai.yaml") || !slices.Contains(p.Files, "profiles/web/.rulesync/rules/web.md") ||
		!slices.Contains(p.Files, "mods/panel/hooks/hooks.json") {
		t.Errorf("plan = %+v, want the files of the filled template", p)
	}
	if !strings.Contains(p.Skill, "name: dotai-profiles") || !strings.Contains(p.Skill, "### web\n\nWeb apps with a JavaScript toolchain.") || !strings.Contains(p.Skill, "- Mods: `panel`") {
		t.Errorf("skill = %q, want the profiling skill of the new pack", p.Skill)
	}
	if len(p.Profiles) != 1 || p.Profiles[0].Path != "profiles/web" {
		t.Errorf("profiles = %+v, want web", p.Profiles)
	}
	for _, w := range p.Warnings {
		if w.Code == app.CodeTemplateUnchanged {
			t.Errorf("the plan warns %s about %s: every file of a new pack is the template's", w.Code, w.File)
		}
	}
	if _, err := os.Stat(dir); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("the plan wrote %s", dir)
	}
	if _, err := os.Stat(filepath.Join(home, ".dotai")); !errors.Is(err, os.ErrNotExist) {
		t.Error("the plan wrote the registry")
	}
}

func TestNewPackWritesAValidPackAndAddsIt(t *testing.T) {
	home := t.TempDir()
	dir := filepath.Join(t.TempDir(), "my-pack")
	var confirmed *app.PackNewPlan
	res, err := app.NewPack(home, dir, newPackInput, app.PackNewOptions{Confirm: func(p app.PackNewPlan) error { confirmed = &p; return nil }})
	if err != nil {
		t.Fatal(err)
	}
	if confirmed == nil || confirmed.Dir != dir {
		t.Fatalf("Confirm got %+v, want the plan", confirmed)
	}
	if res.Added.Outcome != app.PackAdded || res.Added.Pack.ID != "local/my-pack" || !strings.EqualFold(res.Added.Pack.Path, dir) {
		t.Errorf("added = %+v, want local/my-pack at %s", res.Added, dir)
	}
	v, err := app.Validate(dir, app.Selection{})
	if err != nil || !v.Valid {
		t.Fatalf("the new pack is not valid: %v %+v", err, v.Diagnostics)
	}
	for _, f := range res.Files {
		if _, err := os.Stat(filepath.Join(dir, filepath.FromSlash(f))); err != nil {
			t.Errorf("the plan's file %s was not written: %v", f, err)
		}
	}
	if !slices.ContainsFunc(res.Added.Warnings, func(w app.PackWarning) bool { return w.Code == app.CodeTemplateUnchanged }) {
		t.Errorf("warnings = %+v, want template-unchanged", res.Added.Warnings)
	}
	entries, _ := os.ReadDir(filepath.Dir(dir))
	if len(entries) != 1 {
		t.Errorf("the parent holds %d entries, want only the pack: nothing temporary is left", len(entries))
	}
}

func TestNewPackWritesIntoAnEmptyDirectory(t *testing.T) {
	home := t.TempDir()
	dir := t.TempDir()
	if _, err := app.NewPack(home, dir, newPackInput, app.PackNewOptions{}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "dotai.yaml")); err != nil {
		t.Error(err)
	}
}

func TestNewPackRefusesBeforeWritingAnything(t *testing.T) {
	home := t.TempDir()
	full := t.TempDir()
	writeFile(t, filepath.Join(full, "notes.txt"), "mine\n")
	file := filepath.Join(t.TempDir(), "file")
	writeFile(t, file, "x")
	taken := t.TempDir()
	if _, err := app.NewPack(home, filepath.Join(taken, "first"), newPackInput, app.PackNewOptions{}); err != nil {
		t.Fatal(err)
	}
	bad := newPackInput
	bad.Profiles = []app.PackNewProfile{{Name: "web", Description: "Web.", Mods: []string{"Bad_Mod"}}}
	noHarness := newPackInput
	noHarness.Harnesses = nil
	for _, tt := range []struct {
		name string
		dir  string
		in   app.PackNewInput
		home string
		want func(error) bool
	}{
		{"a directory that is not empty", full, newPackInput, home, is[*app.NewRefusedError]},
		{"a file", file, newPackInput, home, is[*app.NewRefusedError]},
		{"no parent", filepath.Join(t.TempDir(), "a", "b"), newPackInput, home, is[*app.NewRefusedError]},
		{"a relative path", "rel", newPackInput, home, is[*app.UsageError]},
		{"a name that breaks the mod rule", filepath.Join(t.TempDir(), "x"), app.PackNewInput{Name: "My Pack", Harnesses: []string{"opencode"}}, home, is[*app.UsageError]},
		{"a mod name that breaks the rule", filepath.Join(t.TempDir(), "x"), bad, home, is[*app.UsageError]},
		{"a name the registry has", filepath.Join(t.TempDir(), "second"), newPackInput, home, is[*app.PackNameTakenError]},
		{"no harness", filepath.Join(t.TempDir(), "x"), noHarness, t.TempDir(), is[*app.PackInvalidError]},
	} {
		t.Run(tt.name, func(t *testing.T) {
			before, _ := os.ReadDir(filepath.Dir(tt.dir))
			_, err := app.NewPack(tt.home, tt.dir, tt.in, app.PackNewOptions{})
			if err == nil || !tt.want(err) {
				t.Fatalf("NewPack() = %v (%T)", err, err)
			}
			if after, _ := os.ReadDir(filepath.Dir(tt.dir)); len(after) != len(before) {
				t.Errorf("the parent went from %d to %d entries", len(before), len(after))
			}
		})
	}
}

// is reports whether err is or wraps a T.
func is[T error](err error) bool {
	_, ok := errors.AsType[T](err)
	return ok
}

func TestCopyPackWithoutATargetCopiesNextToThePack(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "my-pack")
	if _, err := app.NewPack(t.TempDir(), dir, newPackInput, app.PackNewOptions{}); err != nil {
		t.Fatal(err)
	}
	res, err := app.CopyPack(t.Context(), dir, "")
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(filepath.Dir(dir), "my-pack-copy"); res.To != want || res.Files == 0 {
		t.Errorf("copy = %+v, want %d files at %s", res, res.Files, want)
	}
	again, err := app.CopyPack(t.Context(), dir, "")
	if err != nil || again.To != filepath.Join(filepath.Dir(dir), "my-pack-copy-2") {
		t.Errorf("a second copy = %+v, %v; want my-pack-copy-2", again, err)
	}
}

// TestCopyPackWithoutATargetNeverLeavesThePacksParent: the manifest's name
// is the pack author's, so a name that is not one path segment, such as
// ../../x, never takes the copy out of the directory that holds the pack;
// the directory's own name stands in for it.
func TestCopyPackWithoutATargetNeverLeavesThePacksParent(t *testing.T) {
	hostile := []string{"../../escaped", "..", "a/b", `..\..\escaped`, `C:\escaped`, "C:escaped", "/escaped"}
	for _, name := range hostile {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			dir := filepath.Join(root, "a", "b", "my-pack")
			if err := os.MkdirAll(filepath.Dir(dir), 0o755); err != nil {
				t.Fatal(err)
			}
			if _, err := app.NewPack(t.TempDir(), dir, newPackInput, app.PackNewOptions{}); err != nil {
				t.Fatal(err)
			}
			manifest := filepath.Join(dir, "dotai.yaml")
			data, err := os.ReadFile(manifest)
			if err != nil {
				t.Fatal(err)
			}
			data = []byte(strings.Replace(string(data), "name: my-pack\n", "name: "+strconv.Quote(name)+"\n", 1))
			if err := os.WriteFile(manifest, data, 0o644); err != nil {
				t.Fatal(err)
			}
			res, err := app.CopyPack(t.Context(), dir, "")
			if err != nil {
				t.Fatalf("CopyPack() = %v, want a copy next to the pack", err)
			}
			if want := filepath.Join(filepath.Dir(dir), "my-pack-copy"); res.To != want {
				t.Errorf("the copy went to %s, want %s", res.To, want)
			}
			for _, outside := range []string{filepath.Join(root, "escaped-copy"), filepath.Join(root, "a", "escaped-copy"), filepath.Join(root, "a", "b", "a")} {
				if _, err := os.Lstat(outside); err == nil {
					t.Errorf("the copy wrote %s", outside)
				}
			}
		})
	}
}
