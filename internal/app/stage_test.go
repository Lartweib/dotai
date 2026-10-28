package app_test

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/lartweib/dotai/internal/app"
	"github.com/lartweib/dotai/internal/merge"
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
	res, err := app.Stage(examplePack, out, app.Selection{Profiles: []string{"web"}}, false)
	if err != nil || len(res.Diagnostics) != 0 {
		t.Fatalf("Stage() = %+v, %v", res, err)
	}
	want := []string{app.StageMarker}
	for _, f := range res.Files {
		want = append(want, f.Scope+"/"+f.Path)
		data, err := os.ReadFile(filepath.Join(out, f.Scope, filepath.FromSlash(f.Path)))
		if err != nil {
			t.Fatal(err)
		}
		src, err := os.ReadFile(filepath.Join(examplePack, filepath.FromSlash(f.Layer), filepath.FromSlash(f.Path)))
		if err != nil || !reflect.DeepEqual(data, src) {
			t.Errorf("%s/%s does not match its source in %s: %v", f.Scope, f.Path, f.Layer, err)
		}
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
	if _, err := app.Stage(examplePack, out, app.Selection{}, false); err != nil {
		t.Fatal(err)
	}
	stale := filepath.Join(out, "user", "stale.md")
	writeFile(t, stale, "stale")

	if _, err := app.Stage(examplePack, out, app.Selection{}, false); err == nil || !strings.Contains(err.Error(), "--force") {
		t.Errorf("Stage() error = %v, want a refusal that names --force", err)
	}
	if _, err := app.Stage(examplePack, out, app.Selection{}, true); err != nil {
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
		if _, err := app.Stage(examplePack, out, app.Selection{}, force); err == nil {
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
		_, err := app.Stage(dir, out, app.Selection{}, true)
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
	res, err := app.Stage(examplePack, out, app.Selection{Variant: "nope"}, false)
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
	if _, err := app.Stage(examplePack, out, app.Selection{}, true); err != nil {
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

func (f *failingFS) WriteFile(dir, name string, data []byte) error {
	if f.fail("WriteFile") {
		return errDisk
	}
	return f.OSStageFS.WriteFile(dir, name, data)
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
			if _, err := app.Stage(examplePack, out, app.Selection{}, false); err != nil {
				t.Fatal(err)
			}
			before := tree(t, out)

			_, err := app.StageWith(&fs, examplePack, out, app.Selection{Variant: "tmux"}, true)
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
