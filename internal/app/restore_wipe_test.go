package app_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/lartweib/dotai/internal/app"
	"github.com/lartweib/dotai/internal/state"
)

// TestRestoreReplacePutsBackTheOriginal checks that a restore after a
// replace install moves every wiped entry back to its exact path, links
// included and never followed, puts back the bytes of ~/.claude.json and
// removes the pack's files, so that the whole seeded home is as it was.
func TestRestoreReplacePutsBackTheOriginal(t *testing.T) {
	home, h, outside := replaceHome(t)
	before, outsideBefore := homeEntries(t, home), entries(t, outside)
	inst, err := installReplace(t, newClockFS(t), h, home)
	if err != nil {
		t.Fatal(err)
	}
	res, err := restore(newClockFS(t), home, "", nil)
	if err != nil {
		t.Fatalf("Restore() error = %v", err)
	}
	if !res.Uninstalled || len(res.Conflicts) != 0 || len(res.Wipes) != len(inst.Wipes) || len(res.WipeKeys) != len(inst.WipeKeys) || res.Backup == nil {
		t.Errorf("Restore() = %+v, want every wipe and key put back, without conflicts, with a backup", res)
	}
	for _, w := range res.Wipes {
		if w.From != inst.Backup.ID || w.Conflict {
			t.Errorf("wipe %+v, want it moved back from %s", w, inst.Backup.ID)
		}
	}
	if !slices.Equal(res.Harnesses, []string{"claudecode", "antigravity-cli", "opencode"}) {
		t.Errorf("harnesses = %v, want all three", res.Harnesses)
	}
	diff(t, "home after the restore", homeEntries(t, home), before)
	diff(t, "the directory the links point at", entries(t, outside), outsideBefore)
	if m := manifest(t, home, inst.Backup.ID); m.Restored == nil {
		t.Error("the replace backup is not marked restored")
	}
	if left := entries(t, filepath.Join(inst.Backup.Path, "files")); len(left) != 2 || !strings.HasPrefix(left[".claude.json"], "file") {
		t.Errorf("the replace backup still holds %v, want every entry moved back, and the copy of ~/.claude.json kept", left)
	}
	if rm := manifest(t, home, res.Backup.ID); len(rm.Splits) != 1 || rm.Splits[0].File != "files/.claude.json" {
		t.Errorf("restore manifest splits = %+v, want the copy of ~/.claude.json it overwrote", rm.Splits)
	}
}

// TestRestoreReplaceConflicts checks what a restore of a replace install
// does not overwrite without consent: a configuration key of
// ~/.claude.json written again since with another value, and a file the
// user put since where a wiped entry goes back. With consent, the key gets
// its value back while every other key keeps what it holds now, and the
// file goes into the restore's backup.
func TestRestoreReplaceConflicts(t *testing.T) {
	home, h, _ := replaceHome(t)
	before := homeEntries(t, home)
	if _, err := installReplace(t, newClockFS(t), h, home); err != nil {
		t.Fatal(err)
	}
	now := strings.Replace(claudeJSON, `"numStartups":3,`, `"numStartups":9,"mcpServers":{"theirs":{"command":"y"}},`, 1)
	writeFile(t, at(home, ".claude.json"), now)
	writeFile(t, at(home, "CLAUDE.md"), "# written since\n")
	_, err := restore(newClockFS(t), home, "", nil)
	var ce *app.ConflictError
	if !errors.As(err, &ce) {
		t.Fatalf("Restore() error = %v, want conflicts", err)
	}
	type conflict struct{ target, key string }
	var got []conflict
	for _, c := range ce.Conflicts {
		got = append(got, conflict{c.Target, c.Key})
	}
	want := []conflict{{at(home, ".claude.json"), "mcpServers"}, {at(home, "CLAUDE.md"), ""}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("conflicts = %+v, want %+v", got, want)
	}
	if got, _ := os.ReadFile(at(home, ".claude.json")); string(got) != now {
		t.Error("a refused restore wrote ~/.claude.json")
	}
	res, err := restore(newClockFS(t), home, "", consent)
	if err != nil {
		t.Fatalf("Restore(--yes) error = %v", err)
	}
	var top map[string]any
	data, _ := os.ReadFile(at(home, ".claude.json"))
	if err := json.Unmarshal(data, &top); err != nil {
		t.Fatal(err)
	}
	work := top["projects"].(map[string]any)["C:/work"].(map[string]any)
	if fmt.Sprint(top["mcpServers"]) != "map[mine:map[command:x]]" || top["numStartups"] != float64(9) ||
		fmt.Sprint(work["allowedTools"]) != "[Bash]" || fmt.Sprint(work["mcpServers"]) != "map[p:map[]]" {
		t.Errorf("~/.claude.json = %s, want the wiped keys back and numStartups as it is now", data)
	}
	after := homeEntries(t, home)
	delete(before, ".claude.json")
	delete(after, ".claude.json")
	diff(t, "home after the restore", after, before)
	if got, _ := os.ReadFile(filepath.Join(res.Backup.Path, "moved", res.Undone[0].ID, "CLAUDE.md")); string(got) != "# written since\n" {
		t.Errorf("the restore backup holds %q for ~/CLAUDE.md, want the file written since", got)
	}
	if m := manifest(t, home, res.Backup.ID); len(m.Wipes) != 1 || m.Wipes[0].Path != at(home, "CLAUDE.md") {
		t.Errorf("restore manifest wipes = %+v, want the file moved out of the way", m.Wipes)
	}
}

// TestReplaceAgainWipesAgain checks a replace install over a replace
// install: what the user put since in a wiped area, and the keys of
// ~/.claude.json written again, are wiped again into a backup of their
// own, which is not pinned. A restore then goes back one install at a
// time, and a restore to the pinned backup, with consent for what was put
// since where the first install's entries go back, to the original.
func TestReplaceAgainWipesAgain(t *testing.T) {
	for _, how := range []string{"one at a time", "to the pinned backup"} {
		t.Run(how, func(t *testing.T) {
			home, h, outside := replaceHome(t)
			before, outsideBefore := homeEntries(t, home), entries(t, outside)
			first, err := installReplace(t, newClockFS(t), h, home)
			if err != nil {
				t.Fatal(err)
			}
			writeFile(t, at(home, ".claude/agents/new.md"), "new\n")
			writeFile(t, at(home, "CLAUDE.md"), "# written since\n")
			writeFile(t, at(home, ".claude/new-tool/x"), "x\n")
			writeFile(t, at(home, ".claude.json"), realConfig[".claude.json"])
			between := homeEntries(t, home)
			second, err := installAgain(t, newClockFS(t), h, home)
			if err != nil {
				t.Fatalf("Install() again error = %v", err)
			}
			if got := rels(home, wipePaths(second.Wipes)); !reflect.DeepEqual(got, []string{".claude/agents", ".claude/new-tool", "CLAUDE.md"}) {
				t.Errorf("second wipes = %q, want what was put since", got)
			}
			if len(second.WipeKeys) != 3 || second.Backup == nil || app.PinnedBackup(t, home) != first.Backup.ID {
				t.Errorf("second install = %+v, want the keys wiped again into a backup that is not pinned", second)
			}
			diff(t, "the second backup", within(entries(t, second.Backup.Path), "files/.claude/new-tool"), within(between, ".claude/new-tool"))
			want := maps.Clone(before)
			if how == "one at a time" {
				if _, err := restore(newClockFS(t), home, "", nil); err != nil {
					t.Fatalf("Restore() of the second install error = %v", err)
				}
				diff(t, "home after undoing the second install", homeEntries(t, home), between)
				// What was put since stands where the first install's entries
				// go back.
				_, err := restore(newClockFS(t), home, "", nil)
				var ce *app.ConflictError
				if !errors.As(err, &ce) || len(ce.Conflicts) != 2 {
					t.Fatalf("Restore() of the first install error = %v, want the two entries put since", err)
				}
				if _, err := restore(newClockFS(t), home, "", consent); err != nil {
					t.Fatalf("Restore(--yes) of the first install error = %v", err)
				}
			} else {
				_, err := restore(newClockFS(t), home, first.Backup.ID, nil)
				var ce *app.ConflictError
				if !errors.As(err, &ce) || len(ce.Conflicts) != 2 {
					t.Fatalf("Restore(pinned) error = %v, want the two entries put since where the first install's go back", err)
				}
				res, err := restore(newClockFS(t), home, first.Backup.ID, consent)
				if err != nil || len(res.Undone) != 2 {
					t.Fatalf("Restore(pinned, --yes) = %+v, %v, want both installs undone", res, err)
				}
			}
			// Put since outside what the first install wiped, it stays.
			maps.Copy(want, atOrBelow(between, ".claude/new-tool"))
			diff(t, "home after the restore", homeEntries(t, home), want)
			diff(t, "the directory the links point at", entries(t, outside), outsideBefore)
		})
	}
}

// atOrBelow is the part of e at or below the slash path rel, as e has it.
func atOrBelow(e map[string]string, rel string) map[string]string {
	out := map[string]string{}
	for k, v := range e {
		if k == rel || strings.HasPrefix(k, rel+"/") {
			out[k] = v
		}
	}
	return out
}

func wipePaths(ws []app.WipePlan) []string {
	var out []string
	for _, w := range ws {
		out = append(out, w.Path)
	}
	return out
}

// TestPinnedReplaceBackupIsNeverPruned checks retention with the pinned
// first replace backup, the oldest of the replace installs the install
// state leads back to: after two isolated installs and a long run of
// replace installs, the pinned backup and the history back to it stay,
// where retention would prune them, while older backups are pruned as
// usual. A first replace install that failed and rolled back is not it,
// and one that was killed is, since its backup holds what it moved. Once
// a restore undoes it, the next replace install is pinned.
func TestPinnedReplaceBackupIsNeverPruned(t *testing.T) {
	for _, first := range []string{"succeeds", "fails", "is killed"} {
		t.Run("the first "+first, func(t *testing.T) {
			home, h := realHome(t)
			f := quiet(t)
			d := newClockFS(t)
			replace := func(d app.ExecuteFS) (app.InstallResult, error) {
				return app.InstallReplace(context.Background(), d, h, f.fetch, replaceRun(t), examplePack, home, app.InstallOptions{Variant: "tmux"})
			}
			var isolated []string
			for _, variant := range []string{"standalone", "tmux"} {
				res, err := app.InstallHost(context.Background(), d, h, f.fetch, replaceRun(t), examplePack, home, app.InstallOptions{Variant: variant})
				if err != nil || res.Backup == nil {
					t.Fatalf("Install(isolated %s) = %+v, %v", variant, res, err)
				}
				isolated = append(isolated, res.Backup.ID)
			}
			want := ""
			switch first {
			case "fails":
				if _, err := replace(&stepFS{clockFS: d, at: 1}); !errors.Is(err, errInjected) {
					t.Fatalf("Install(replace) error = %v, want the injected failure", err)
				}
				if got := app.PinnedBackup(t, home); got != "" {
					t.Errorf("pinned backup after a rolled back install = %q, want none", got)
				}
			case "is killed":
				func() {
					defer func() {
						if r := recover(); r != errCrash {
							t.Fatalf("recovered %v, want the crash", r)
						}
					}()
					_, _ = replace(&stepFS{clockFS: d, at: 3, kill: true})
				}()
				want = readJSON(t, at(home, ".dotai/state.json"), state.Decode).Pending.Backup
			}
			var replaces []string
			for i := range 12 {
				if i > 0 {
					writeFile(t, at(home, fmt.Sprintf(".claude/put-since-%d", i)), "x\n")
				}
				res, err := replace(d)
				if err != nil || res.Backup == nil {
					t.Fatalf("Install(replace %d) = %+v, %v", i, res, err)
				}
				replaces = append(replaces, res.Backup.ID)
			}
			if want == "" {
				want = replaces[0]
			}
			if got := app.PinnedBackup(t, home); got != want {
				t.Errorf("pinned backup = %q, want %q", got, want)
			}
			left := backups(t, home)
			for i, id := range append(replaces, want) {
				if !slices.Contains(left, id) {
					t.Errorf("replace backup %d %s was pruned, want the pinned one and the history back to it kept", i, id)
				}
			}
			if !slices.Contains(left, isolated[0]) || slices.Contains(left, isolated[1]) {
				t.Errorf("backups = %v, want the oldest kept and the isolated one after it pruned", left)
			}
			if _, err := restore(d, home, want, consent); err != nil {
				t.Fatalf("Restore(pinned) error = %v", err)
			}
			if got := app.PinnedBackup(t, home); got != "" {
				t.Errorf("pinned backup after restoring it = %q, want none", got)
			}
			res, err := replace(d)
			if err != nil || res.Backup == nil || app.PinnedBackup(t, home) != res.Backup.ID {
				t.Errorf("Install(replace) after restoring the pinned backup = %+v, %v, want it pinned", res, err)
			}
		})
	}
}

// moveStepFS fails, or with kill set panics, at the write number at of a
// restore, counting each entry moved and each file written other than the
// state; every other write works.
type moveStepFS struct {
	*clockFS
	at, n int
	kill  bool
}

func (f *moveStepFS) step() error {
	f.n++
	if f.n != f.at {
		return nil
	}
	if f.kill {
		panic(errCrash)
	}
	return errInjected
}

func (f *moveStepFS) MoveIn(root, from, to string) error {
	if err := f.step(); err != nil {
		return err
	}
	return f.clockFS.MoveIn(root, from, to)
}

func (f *moveStepFS) WriteIn(root, name string, data []byte, mode os.FileMode) error {
	if name != "state.json" && name != "manifest.json" {
		if err := f.step(); err != nil {
			return err
		}
	}
	return f.clockFS.WriteIn(root, name, data, mode)
}

// TestRestoreReplaceRollsBackAndSettles checks that a restore of a replace
// install that fails while it puts back ~/.claude.json or moves entries
// back moves everything back where it was, so that the home is as the
// install left it, and that a restore killed midway is settled by the next
// restore, which puts back the original configuration exactly.
func TestRestoreReplaceRollsBackAndSettles(t *testing.T) {
	for _, at := range []int{1, 2, 12, 21} {
		t.Run(fmt.Sprintf("fails at %d", at), func(t *testing.T) {
			home, h, _ := replaceHome(t)
			if _, err := installReplace(t, newClockFS(t), h, home); err != nil {
				t.Fatal(err)
			}
			installed := homeEntries(t, home)
			stateBefore, _ := os.ReadFile(filepath.Join(home, ".dotai", "state.json"))
			_, err := restore(&moveStepFS{clockFS: newClockFS(t), at: at}, home, "", nil)
			var re *app.RestoreError
			if !errors.As(err, &re) || !errors.Is(err, errInjected) || re.Rollback != nil {
				t.Fatalf("Restore() error = %v, want the injected failure rolled back", err)
			}
			diff(t, "home after the rollback", homeEntries(t, home), installed)
			if got, _ := os.ReadFile(filepath.Join(home, ".dotai", "state.json")); string(got) != string(stateBefore) {
				t.Errorf("state = %s, want it as the install left it", got)
			}
		})
	}
	for _, at := range []int{1, 2, 12, 21} {
		t.Run(fmt.Sprintf("killed at %d", at), func(t *testing.T) {
			home, h, outside := replaceHome(t)
			before, outsideBefore := homeEntries(t, home), entries(t, outside)
			if _, err := installReplace(t, newClockFS(t), h, home); err != nil {
				t.Fatal(err)
			}
			killRestore(t, &moveStepFS{clockFS: newClockFS(t), at: at, kill: true}, home, "")
			res, err := restore(newClockFS(t), home, "", nil)
			if err != nil {
				t.Fatalf("Restore() after the kill error = %v", err)
			}
			if !res.Uninstalled {
				t.Errorf("Restore() = %+v, want nothing dotai installed left", res)
			}
			diff(t, "home after the restore", homeEntries(t, home), before)
			diff(t, "the directory the links point at", entries(t, outside), outsideBefore)
		})
	}
}

// killInstall runs a replace install through d until d kills it.
func killInstall(t *testing.T, d app.ExecuteFS, h app.Host, home string) {
	t.Helper()
	defer func() {
		if r := recover(); r != errCrash {
			t.Fatalf("recovered %v, want the crash", r)
		}
	}()
	_, _ = installReplace(t, d, h, home)
}

// TestKilledReplaceIsSettled checks a replace install killed while it
// moves entries, after it rewrote ~/.claude.json or while it installs the
// pack: the journal is left, the next install settles it and a restore to
// the pinned backup then puts back the original configuration exactly, and
// a restore right after the kill does the same.
func TestKilledReplaceIsSettled(t *testing.T) {
	n := steps(t)
	for _, k := range []int{3, n - 6, n - 5, n - 2} {
		for _, next := range []string{"install", "restore"} {
			t.Run(fmt.Sprintf("killed at %d then %s", k, next), func(t *testing.T) {
				home, h, outside := replaceHome(t)
				before, outsideBefore := homeEntries(t, home), entries(t, outside)
				killInstall(t, &stepFS{clockFS: newClockFS(t), at: k, kill: true}, h, home)
				s := readJSON(t, at(home, ".dotai/state.json"), state.Decode)
				if s.Pending == nil || len(s.Pending.Wipes) == 0 || len(s.Pending.Splits) != 1 {
					t.Fatalf("state after the kill = %+v, want the journal with its wipes and ~/.claude.json", s)
				}
				pinned := s.Pending.Backup
				id := ""
				if next == "install" {
					if _, err := installAgain(t, newClockFS(t), h, home); err != nil {
						t.Fatalf("Install() after the kill error = %v", err)
					}
					p, err := app.PlanInstallHost(context.Background(), app.OSInstallFS{}, h, quiet(t).fetch, replaceRun(t), examplePack, home, app.InstallOptions{})
					if err != nil || len(p.Wipes) != 0 || len(p.WipeKeys) != 0 || len(p.Refusals) != 0 {
						t.Fatalf("plan after the settling install = %+v, %v, want nothing left to wipe", p, err)
					}
					for _, op := range p.Ops {
						if op.Action != "unchanged" {
							t.Errorf("op %+v, want every target in place", op)
						}
					}
					if got := app.PinnedBackup(t, home); got != pinned {
						t.Errorf("pinned backup = %q, want the killed install's %s, which holds what it moved", got, pinned)
					}
					id = pinned
				}
				res, err := restore(newClockFS(t), home, id, nil)
				if err != nil {
					t.Fatalf("Restore(%q) error = %v", id, err)
				}
				if !res.Uninstalled {
					t.Errorf("Restore() = %+v, want nothing dotai installed left", res)
				}
				diff(t, "home after the restore", homeEntries(t, home), before)
				diff(t, "the directory the links point at", entries(t, outside), outsideBefore)
			})
		}
	}
}

// TestRestoreReplacePutsBackAMissingSplitFile checks that a ~/.claude.json
// removed since the replace install comes back whole, from its copy, and
// that undoing that restore's write on a failure removes it again.
func TestRestoreReplacePutsBackAMissingSplitFile(t *testing.T) {
	home, h, _ := replaceHome(t)
	before := homeEntries(t, home)
	if _, err := installReplace(t, newClockFS(t), h, home); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(at(home, ".claude.json")); err != nil {
		t.Fatal(err)
	}
	gone := homeEntries(t, home)
	if _, err := restore(&moveStepFS{clockFS: newClockFS(t), at: 2}, home, "", nil); !errors.Is(err, errInjected) {
		t.Fatalf("Restore() error = %v, want the injected failure", err)
	}
	diff(t, "home after the rollback", homeEntries(t, home), gone)
	if _, err := restore(newClockFS(t), home, "", nil); err != nil {
		t.Fatalf("Restore() error = %v", err)
	}
	diff(t, "home after the restore", homeEntries(t, home), before)
}
