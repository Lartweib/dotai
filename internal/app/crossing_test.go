package app_test

import (
	"context"
	"encoding/json"
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

func (f *volumesFS) CopyIn(ctx context.Context, root, from, to string) (n int64, err error) {
	err = f.step(func() error {
		n, err = f.clockFS.CopyIn(ctx, root, from, to)
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

// TestTamperedCrossingJournalDeletesNothing checks that a journal of moves
// across volumes that names anything but a move dotai makes, between an
// adapter's area or leak entry and a backup, with its temporary names
// next to its ends, is refused before anything is deleted, naming its
// paths, and is left for the user to inspect.
func TestTamperedCrossingJournalDeletesNothing(t *testing.T) {
	const id = "20261029T100100.000Z_install"
	backed := ".dotai/backups/" + id + "/files/.claude/agents"
	valid := map[string]string{"from": ".claude/agents", "to": backed, "temp": ".dotai/backups/" + id + "/files/.claude/.agents.dotai-0123456789abcdef", "trash": ".claude/.agents.dotai-fedcba9876543210"}
	with := func(k, v string) map[string]string {
		c := map[string]string{}
		for key, val := range valid {
			c[key] = val
		}
		c[k] = v
		return c
	}
	for _, tt := range []struct {
		name string
		c    map[string]string
	}{
		{"trash names another directory", with("trash", ".ssh")},
		{"temp names another directory", with("temp", ".ssh")},
		{"trash is not next to from", with("trash", ".config/.agents.dotai-fedcba9876543210")},
		{"temp is not next to to", with("temp", ".claude/.agents.dotai-0123456789abcdef")},
		{"trash has another name", with("trash", ".claude/.other.dotai-fedcba9876543210")},
		{"temp is not a temporary name", with("temp", ".dotai/backups/"+id+"/files/.claude/.agents.dotai-xyz")},
		{"from is outside the areas", with("from", "Documents")},
		{"to is outside the backups", with("to", ".config/agents")},
		{"to is in a backup with a bad id", with("to", ".dotai/backups/elsewhere/files/.claude/agents")},
		{"a path climbs out", with("from", "../outside")},
	} {
		t.Run(tt.name, func(t *testing.T) {
			home, _ := installHome(t)
			for _, rel := range []string{".ssh/id_ed25519", ".claude/agents/mine.md", ".claude/.agents.dotai-fedcba9876543210/x", "Documents/notes.txt", ".config/.agents.dotai-fedcba9876543210/x"} {
				writeFile(t, at(home, rel), "keep\n")
			}
			data, err := json.Marshal([]map[string]string{tt.c})
			if err != nil {
				t.Fatal(err)
			}
			writeFile(t, at(home, ".dotai/crossing.json"), string(data))
			before := entries(t, home)
			_, err = restore(newClockFS(t), home, "", nil)
			if err == nil || !strings.Contains(err.Error(), "journal of a move across volumes") || !strings.Contains(err.Error(), "delete") {
				t.Fatalf("Restore() error = %v, want the journal refused", err)
			}
			for _, p := range tt.c {
				if !strings.Contains(err.Error(), filepath.FromSlash(p)) && !strings.Contains(err.Error(), p) {
					t.Errorf("error = %v, want it to name %s", err, p)
				}
			}
			diff(t, "home after the refusal", entries(t, home), before)
		})
	}
}

// TestRemoveAllInClearsReadOnly checks that removing a source's leftovers
// or a partial copy removes a read-only file in it, and a directory
// without write permission, as removing one file does.
func TestRemoveAllInClearsReadOnly(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, ".agents.dotai-0123456789abcdef", "locked", "ro.txt"), "x")
	if err := os.Chmod(filepath.Join(root, ".agents.dotai-0123456789abcdef", "locked", "ro.txt"), 0o444); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(filepath.Join(root, ".agents.dotai-0123456789abcdef", "locked"), 0o555); err != nil {
		t.Fatal(err)
	}
	if err := (app.OSInstallFS{}).RemoveAllIn(root, ".agents.dotai-0123456789abcdef"); err != nil {
		t.Fatalf("RemoveAllIn() error = %v", err)
	}
	if _, err := os.Lstat(filepath.Join(root, ".agents.dotai-0123456789abcdef")); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("leftovers: %v, want them removed", err)
	}
}

// TestCopyInStopsWhenCancelled checks that a copy across volumes stops
// when its context is done, as on Ctrl-C.
func TestCopyInStopsWhenCancelled(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "src", "a", "file"), "x")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := (app.OSInstallFS{}).CopyIn(ctx, root, "src", "dst"); !errors.Is(err, context.Canceled) {
		t.Errorf("CopyIn() after a cancel error = %v, want it cancelled", err)
	}
}
