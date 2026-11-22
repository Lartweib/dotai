package app_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/lartweib/dotai/internal/app"
	"github.com/lartweib/dotai/internal/pack"
)

// racingFS simulates entries that vanish under a scan or a move: the
// temporary file a running tool renames over its own file, listed and then
// gone.
type racingFS struct {
	*clockFS
	phantoms map[string]string // directory -> name listed but missing
	onWrite  string            // removed when the first backup file is written
	onMove   string            // removed just before its move
}

func (f *racingFS) List(dir string) ([]string, error) {
	names, err := f.clockFS.List(dir)
	if name, ok := f.phantoms[dir]; ok && err == nil {
		names = append(names, name)
	}
	return names, err
}

func (f *racingFS) WriteSynced(dir, name string, data []byte, perm os.FileMode) error {
	if f.onWrite != "" {
		if err := os.RemoveAll(f.onWrite); err != nil {
			f.t.Fatal(err)
		}
		f.onWrite = ""
	}
	return f.clockFS.WriteSynced(dir, name, data, perm)
}

func (f *racingFS) MoveIn(root, from, to string) error {
	if f.onMove != "" {
		if rel, err := filepath.Rel(root, f.onMove); err == nil && from == filepath.ToSlash(rel) {
			if err := os.RemoveAll(f.onMove); err != nil {
				f.t.Fatal(err)
			}
		}
	}
	return f.clockFS.MoveIn(root, from, to)
}

// TestPlanReplaceSkipsAnEntryThatVanishesWhileScanning checks that an
// entry listed in a harness directory, or in one a replace wipes, and gone
// before it is looked at is not there, not a failed plan.
func TestPlanReplaceSkipsAnEntryThatVanishesWhileScanning(t *testing.T) {
	home, h := realHome(t)
	d := &racingFS{clockFS: newClockFS(t), phantoms: map[string]string{
		at(home, ".claude"):         "rate_limits.json.40884.tmp",
		at(home, ".claude/plugins"): "cache.json.1.tmp",
	}}
	f := quiet(t)
	p, err := app.PlanInstallHost(context.Background(), d, h, f.fetch, replaceRun(t), examplePack, home, app.InstallOptions{Mode: app.ModeReplace, Harnesses: pack.Harnesses})
	if err != nil || len(p.Refusals) != 0 {
		t.Fatalf("PlanInstall(replace) = refusals %+v, %v, want a plan", p.Refusals, err)
	}
	if slices.ContainsFunc(p.Wipes, func(w app.WipePlan) bool { return strings.HasSuffix(w.Path, ".tmp") }) {
		t.Errorf("wipes = %+v, want the vanished entry left out", p.Wipes)
	}
	if !slices.ContainsFunc(p.Wipes, func(w app.WipePlan) bool { return w.Path == at(home, ".claude/plugins") }) {
		t.Errorf("wipes = %+v, want the others planned", p.Wipes)
	}
}

// TestInstallReplaceSkipsAWipedEntryThatVanished checks that an entry the
// plan wipes and that is gone before its move, whether before the backup
// reads it or between its check and its move, is already wiped: the
// install works, the backup does not record it, and the restore is exact.
func TestInstallReplaceSkipsAWipedEntryThatVanished(t *testing.T) {
	for name, hook := range map[string]func(f *racingFS, gone string){
		"before the backup": func(f *racingFS, gone string) { f.onWrite = gone },
		"before the move":   func(f *racingFS, gone string) { f.onMove = gone },
	} {
		t.Run(name, func(t *testing.T) {
			home, h := realHome(t)
			gone := at(home, ".claude/from-another-tool")
			before := homeEntries(t, home)
			for k := range before {
				if k == ".claude/from-another-tool" || strings.HasPrefix(k, ".claude/from-another-tool/") {
					delete(before, k)
				}
			}
			d := &racingFS{clockFS: newClockFS(t)}
			hook(d, gone)
			res, err := installReplace(t, d, h, home)
			var ie *app.InstallError
			if err != nil || errors.As(err, &ie) || res.Backup == nil {
				t.Fatalf("Install() = %+v, %v, want it installed", res.Backup, err)
			}
			if _, err := os.Lstat(gone); err == nil {
				t.Errorf("%s is back", gone)
			}
			if _, err := restore(newClockFS(t), home, "", nil); err != nil {
				t.Fatalf("Restore() error = %v", err)
			}
			diff(t, "home after the restore", homeEntries(t, home), before)
		})
	}
}
