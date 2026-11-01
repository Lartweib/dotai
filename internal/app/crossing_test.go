package app_test

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/lartweib/dotai/internal/app"
)

// volumesFS puts the dotai directory on a volume of its own: a rename
// between it and the rest of the home directory fails as the OS fails one
// across volumes, so that each wiped entry, and each entry moved back, is
// copied. With killAt set it panics, as a killed process stops, at the
// step number killAt of those copies: each copy, each rename of a source
// or a copy, and each removal of a source's leftovers, counted from 1;
// with after set, right after that step instead of before it. With failAt
// set it fails the write number failAt of the pack's files instead, so
// that the install rolls back across volumes.
type volumesFS struct {
	*clockFS
	killAt, failAt int
	after          bool
	steps, writes  int
}

func onDotai(name string) bool { return name == ".dotai" || strings.HasPrefix(name, ".dotai/") }

func (f *volumesFS) step(do func() error) error {
	f.steps++
	if f.steps == f.killAt && !f.after {
		panic(errCrash)
	}
	err := do()
	if f.steps == f.killAt {
		panic(errCrash)
	}
	return err
}

func (f *volumesFS) MoveIn(root, from, to string) error {
	if onDotai(from) != onDotai(to) {
		return &os.LinkError{Op: "rename", Old: from, New: to, Err: app.ErrCrossDevice}
	}
	if strings.Contains(from, ".dotai-") || strings.Contains(to, ".dotai-") { // a step of a copy
		return f.step(func() error { return f.clockFS.MoveIn(root, from, to) })
	}
	return f.clockFS.MoveIn(root, from, to)
}

func (f *volumesFS) CopyIn(root, from, to string) (n int64, err error) {
	err = f.step(func() error {
		n, err = f.clockFS.CopyIn(root, from, to)
		return err
	})
	return n, err
}

func (f *volumesFS) RemoveAllIn(root, name string) error {
	return f.step(func() error { return f.clockFS.RemoveAllIn(root, name) })
}

func (f *volumesFS) WriteIn(root, name string, data []byte, mode os.FileMode) error {
	if f.failAt > 0 && name != "state.json" && name != "manifest.json" && name != "crossing.json" {
		f.writes++
		if f.writes == f.failAt {
			return errInjected
		}
	}
	return f.clockFS.WriteIn(root, name, data, mode)
}

// leftovers are the temporary names a copy across volumes uses, and its
// journal, anywhere under home.
func leftovers(t *testing.T, home string) []string {
	t.Helper()
	var out []string
	err := filepath.WalkDir(home, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if strings.Contains(d.Name(), ".dotai-") || d.Name() == "crossing.json" {
			out = append(out, p)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// TestReplaceMovesAcrossVolumes checks a replace install whose backup is
// on another volume than the harnesses' directories: each wiped entry is
// copied into the backup exactly, links as links, and deleted, with a
// warning, and the restore copies each one back, so that the home ends as
// it was; a failure while the pack installs rolls the entries back across
// volumes too.
func TestReplaceMovesAcrossVolumes(t *testing.T) {
	home, h, outside := replaceHome(t)
	before, outsideBefore := homeEntries(t, home), entries(t, outside)
	res, err := installReplace(t, &volumesFS{clockFS: newClockFS(t)}, h, home)
	if err != nil {
		t.Fatalf("Install(replace) error = %v", err)
	}
	for _, w := range res.Wipes {
		rel := filepath.ToSlash(strings.TrimPrefix(strings.TrimPrefix(w.Path, home), string(filepath.Separator)))
		diff(t, "the backup of "+rel, entries(t, filepath.Join(res.Backup.Path, "files", filepath.FromSlash(rel))), within(before, rel))
		if _, err := os.Lstat(w.Path); !errors.Is(err, fs.ErrNotExist) && !slices.ContainsFunc(res.Ops, func(op app.InstallOp) bool { return op.Target == w.Path }) {
			t.Errorf("%s: %v, want it deleted once copied", w.Path, err)
		}
	}
	copied := 0
	for _, w := range res.Warnings {
		if strings.Contains(w, "is on another volume than") {
			copied++
		}
	}
	if copied != len(res.Wipes) {
		t.Errorf("warnings = %q, want one per entry copied across volumes (%d)", res.Warnings, len(res.Wipes))
	}
	if l := leftovers(t, home); len(l) > 0 {
		t.Errorf("leftovers = %q, want none", l)
	}
	if _, err := restore(&volumesFS{clockFS: newClockFS(t)}, home, "", nil); err != nil {
		t.Fatalf("Restore() error = %v", err)
	}
	diff(t, "home after the restore", homeEntries(t, home), before)
	diff(t, "the directory the links point at", entries(t, outside), outsideBefore)

	t.Run("rollback", func(t *testing.T) {
		home, h, _ := replaceHome(t)
		before := homeEntries(t, home)
		_, err := installReplace(t, &volumesFS{clockFS: newClockFS(t), failAt: 2}, h, home)
		var ie *app.InstallError
		if !errors.As(err, &ie) || !errors.Is(err, errInjected) || ie.Rollback != nil {
			t.Fatalf("Install(replace) error = %v, want the injected failure rolled back", err)
		}
		diff(t, "home after the rollback", homeEntries(t, home), before)
		if l := leftovers(t, home); len(l) > 0 {
			t.Errorf("leftovers = %q, want none", l)
		}
	})
}

// TestKilledMoveAcrossVolumesIsSettled kills a replace install at each
// step of its first copies across volumes, before and after the step:
// during the copy, once the copy is checked and the source renamed, once
// the copy has its name, and while the source's leftovers go. The next
// install or restore settles the move from its journal first, and a
// restore then puts the home back as it was.
func TestKilledMoveAcrossVolumesIsSettled(t *testing.T) {
	for _, k := range []int{1, 2, 3, 4, 7} {
		for _, after := range []bool{false, true} {
			for _, next := range []string{"install", "restore"} {
				t.Run(fmt.Sprintf("step %d after %v then %s", k, after, next), func(t *testing.T) {
					home, h, outside := replaceHome(t)
					before, outsideBefore := homeEntries(t, home), entries(t, outside)
					killInstall(t, &volumesFS{clockFS: newClockFS(t), killAt: k, after: after}, h, home)
					if _, err := os.Stat(at(home, ".dotai/crossing.json")); err != nil {
						t.Fatalf("the journal of the move across volumes: %v, want it left by the kill", err)
					}
					id := ""
					if next == "install" {
						if _, err := installAgain(t, &volumesFS{clockFS: newClockFS(t)}, h, home); err != nil {
							t.Fatalf("Install() after the kill error = %v", err)
						}
						id = app.PinnedBackup(t, home)
					}
					if _, err := restore(&volumesFS{clockFS: newClockFS(t)}, home, id, nil); err != nil {
						t.Fatalf("Restore(%q) error = %v", id, err)
					}
					if l := leftovers(t, home); len(l) > 0 {
						t.Errorf("leftovers after the settling %s and the restore = %q, want none", next, l)
					}
					diff(t, "home after the restore", homeEntries(t, home), before)
					diff(t, "the directory the links point at", entries(t, outside), outsideBefore)
				})
			}
		}
	}
}
