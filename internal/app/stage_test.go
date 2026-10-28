package app_test

import (
	"os"
	"path/filepath"
	"reflect"
	"slices"
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

func TestStageWritesEveryStagedFile(t *testing.T) {
	out := filepath.Join(t.TempDir(), "staging")
	res, err := app.Stage(examplePack, out, app.Selection{Profiles: []string{"web"}}, false)
	if err != nil || len(res.Diagnostics) != 0 {
		t.Fatalf("Stage() = %+v, %v", res, err)
	}
	var want []string
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
	if got := tree(t, out); len(want) == 0 || !reflect.DeepEqual(got, want) {
		t.Errorf("tree = %v, want %v", got, want)
	}
}

func TestStageRefusesNonEmptyOutWithoutForce(t *testing.T) {
	out := t.TempDir()
	if err := os.WriteFile(filepath.Join(out, "keep.txt"), []byte("keep"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := app.Stage(examplePack, out, app.Selection{}, false); err == nil {
		t.Fatal("Stage() error = nil, want a refusal for a non-empty directory")
	}
	if got := tree(t, out); !reflect.DeepEqual(got, []string{"keep.txt"}) {
		t.Errorf("tree = %v, want only keep.txt", got)
	}
	if _, err := app.Stage(examplePack, out, app.Selection{}, true); err != nil {
		t.Fatalf("Stage(force) error = %v", err)
	}
	if got := tree(t, out); len(got) < 2 || !slices.Contains(got, "keep.txt") {
		t.Errorf("tree = %v, want keep.txt and the staged files", got)
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
	out, outside := t.TempDir(), t.TempDir()
	if err := os.Symlink(outside, filepath.Join(out, "user")); err != nil {
		t.Skipf("symbolic links not supported here: %v", err)
	}
	if _, err := app.Stage(examplePack, out, app.Selection{}, true); err == nil {
		t.Error("Stage() error = nil, want an error for a link out of --out")
	}
	if got := tree(t, outside); len(got) != 0 {
		t.Errorf("files written outside --out: %v", got)
	}
}
