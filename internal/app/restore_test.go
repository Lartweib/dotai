package app_test

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/lartweib/dotai/internal/app"
	"github.com/lartweib/dotai/internal/backup"
	"github.com/lartweib/dotai/internal/plan"
	"github.com/lartweib/dotai/internal/state"
)

func restore(d app.ExecuteFS, home, id string, overwrite func(app.RestoreResult) (bool, error)) (app.RestoreResult, error) {
	return app.RestoreWith(context.Background(), d, home, id, overwrite)
}

// snapshot is every file under home outside the backups, with its content
// and, where the file system keeps it, its exec bit.
func snapshot(t *testing.T, home string) map[string]string {
	t.Helper()
	out := map[string]string{}
	for _, rel := range installed(t, home) {
		name := at(home, rel)
		data, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		info, err := os.Stat(name)
		if err != nil {
			t.Fatal(err)
		}
		exec := runtime.GOOS != "windows" && info.Mode()&0o111 != 0
		out[rel] = fmt.Sprintf("exec=%v %s", exec, data)
	}
	return out
}

func manifest(t *testing.T, home, id string) backup.Manifest {
	t.Helper()
	return readJSON(t, at(home, ".dotai/backups/"+id+"/manifest.json"), backup.Decode)
}

func TestRestoreAfterAFreshInstallUninstalls(t *testing.T) {
	home, _ := installHome(t)
	first, err := quiet(t).install(newClockFS(t), examplePack, home)
	if err != nil {
		t.Fatal(err)
	}
	res, err := restore(newClockFS(t), home, "", nil)
	if err != nil {
		t.Fatalf("Restore() error = %v", err)
	}
	if got := installed(t, home); len(got) != 0 {
		t.Errorf("home = %v, want every installed file gone, and the state too", got)
	}
	if entries, _ := os.ReadDir(at(home, ".dotai")); len(entries) != 1 || entries[0].Name() != "backups" {
		t.Errorf("~/.dotai = %v, want only the backups, the empty directories removed", entries)
	}
	if !res.Uninstalled || res.State != at(home, ".dotai/state.json") || res.Backup == nil || !strings.HasSuffix(res.Backup.ID, "_restore") {
		t.Errorf("Restore() = %+v, want an uninstall with a restore backup", res)
	}
	if len(res.Undone) != 1 || res.Undone[0].ID != first.Backup.ID || res.Undone[0].Variant != "standalone" || res.Undone[0].Pack.Name != "minimal-pack" {
		t.Errorf("undone = %+v, want the install", res.Undone)
	}
	if len(res.Ops) != 5 || len(res.Conflicts) != 0 || !slices.Equal(res.Harnesses, []string{"claudecode", "antigravity-cli", "opencode"}) {
		t.Errorf("Restore() = %+v, want five deletes of three harnesses and no conflict", res)
	}
	for _, op := range res.Ops {
		if op.Action != plan.Remove || op.From != "" || op.After != "" || op.Conflict {
			t.Errorf("op %+v, want a delete", op)
		}
	}
	if len(res.Warnings) != 1 || !strings.Contains(res.Warnings[0], "close") {
		t.Errorf("warnings = %q, want only the one to close the harnesses", res.Warnings)
	}
	m := manifest(t, home, first.Backup.ID)
	if m.Restored == nil || m.Restored.Backup != res.Backup.ID {
		t.Errorf("install manifest restored = %+v, want it marked by %s", m.Restored, res.Backup.ID)
	}
	rm := manifest(t, home, res.Backup.ID)
	if rm.Operation != "restore" || len(rm.Ops) != 5 || rm.PriorState == nil || rm.PriorState.File != "state.json" {
		t.Errorf("restore manifest = %+v, want the five deletes and the state before", rm)
	}
	for _, op := range rm.Ops {
		if op.Action != plan.Remove || op.File == "" {
			t.Errorf("restore manifest op %+v, want a remove with a copy", op)
		}
	}
	if _, err := os.Stat(at(home, ".dotai/lock")); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("lock = %v, want it released", err)
	}
	if _, err := restore(newClockFS(t), home, "", nil); err == nil || !strings.Contains(err.Error(), "nothing to restore") {
		t.Errorf("Restore() again error = %v, want nothing to restore", err)
	}
}

// TestRestoreAfterAVariantSwitch checks that a restore puts back the files
// of the variant installed before, byte for byte and with their exec bits,
// and the state of that install.
func TestRestoreAfterAVariantSwitch(t *testing.T) {
	home, _ := installHome(t)
	dir := copyPack(t)
	d := newClockFS(t)
	if _, err := app.InstallWith(context.Background(), d, quiet(t).fetch, quiet(t).run, dir, "standalone", home); err != nil {
		t.Fatal(err)
	}
	before := snapshot(t, home)
	stateBefore, err := os.ReadFile(at(home, ".dotai/state.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(filepath.Join(dir, "base", "dotai", "tools", "hello.sh"), 0o644); err != nil {
		t.Fatal(err)
	}
	second, err := app.InstallWith(context.Background(), d, quiet(t).fetch, quiet(t).run, dir, "tmux", home)
	if err != nil {
		t.Fatal(err)
	}
	if reflect.DeepEqual(snapshot(t, home), before) {
		t.Fatal("the tmux variant installed the same files, so the test proves nothing")
	}
	res, err := restore(d, home, "", nil)
	if err != nil {
		t.Fatalf("Restore() error = %v", err)
	}
	if got := snapshot(t, home); !reflect.DeepEqual(got, before) {
		t.Errorf("home = %v, want %v", got, before)
	}
	if got, _ := os.ReadFile(at(home, ".dotai/state.json")); string(got) != string(stateBefore) {
		t.Errorf("state = %s, want %s", got, stateBefore)
	}
	if res.Uninstalled || len(res.Undone) != 1 || res.Undone[0].ID != second.Backup.ID {
		t.Errorf("Restore() = %+v, want the tmux install undone", res)
	}
	for _, op := range res.Ops {
		if op.Action == plan.Remove || op.From != second.Backup.ID {
			t.Errorf("op %+v, want each file put back from %s", op, second.Backup.ID)
		}
	}
	// Nothing is left to do: a plan of the standalone variant is in place.
	if err := os.Chmod(filepath.Join(dir, "base", "dotai", "tools", "hello.sh"), 0o755); err != nil {
		t.Fatal(err)
	}
	assertConverged(t, dir, "standalone", home)
}

// TestRestoreToAnOlderBackup checks that a restore to a backup undoes
// every install from the newest down to that one, and that restoring the
// first backup uninstalls everything.
func TestRestoreToAnOlderBackup(t *testing.T) {
	home, _ := installHome(t)
	dir := copyPack(t)
	tools := filepath.Join(dir, "base", "dotai", "tools")
	d := newClockFS(t)
	foreign := at(home, ".dotai/tools/mine.sh")
	writeFile(t, foreign, "echo mine\n")
	writeFile(t, filepath.Join(tools, "mine.sh"), "echo pack\n") // replaces a file of the user's
	a, err := quiet(t).install(d, dir, home)
	if err != nil {
		t.Fatal(err)
	}
	afterA := snapshot(t, home)
	stateA, _ := os.ReadFile(at(home, ".dotai/state.json"))
	writeFile(t, filepath.Join(tools, "hello.sh"), "echo b\n")
	writeFile(t, filepath.Join(tools, "lib", "b.sh"), "echo b\n")
	b, err := quiet(t).install(d, dir, home)
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(tools, "hello.sh"), "echo c\n")
	if err := os.RemoveAll(filepath.Join(tools, "lib")); err != nil {
		t.Fatal(err)
	}
	c, err := quiet(t).install(d, dir, home)
	if err != nil {
		t.Fatal(err)
	}

	res, err := restore(d, home, b.Backup.ID, nil)
	if err != nil {
		t.Fatalf("Restore(%s) error = %v", b.Backup.ID, err)
	}
	if ids := []string{res.Undone[0].ID, res.Undone[len(res.Undone)-1].ID}; len(res.Undone) != 2 || !slices.Equal(ids, []string{c.Backup.ID, b.Backup.ID}) {
		t.Errorf("undone = %+v, want %s then %s", res.Undone, c.Backup.ID, b.Backup.ID)
	}
	if got := snapshot(t, home); !reflect.DeepEqual(got, afterA) {
		t.Errorf("home = %v, want it as after the first install %v", got, afterA)
	}
	if got, _ := os.ReadFile(at(home, ".dotai/state.json")); string(got) != string(stateA) {
		t.Errorf("state = %s, want the first install's %s", got, stateA)
	}
	if _, err := os.Stat(at(home, ".dotai/tools/lib")); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("tools/lib = %v, want the directory b created removed", err)
	}
	for _, id := range []string{b.Backup.ID, c.Backup.ID} {
		if manifest(t, home, id).Restored == nil {
			t.Errorf("backup %s is not marked restored", id)
		}
	}
	if _, err := restore(d, home, c.Backup.ID, nil); err == nil || !strings.Contains(err.Error(), "already restored") {
		t.Errorf("Restore(%s) again error = %v, want it already restored", c.Backup.ID, err)
	}

	if _, err := restore(d, home, a.Backup.ID, nil); err != nil {
		t.Fatalf("Restore(%s) error = %v", a.Backup.ID, err)
	}
	if got := snapshot(t, home); !reflect.DeepEqual(got, map[string]string{".dotai/tools/mine.sh": "exec=false echo mine\n"}) {
		t.Errorf("home = %v, want only the user's own file, as it was", got)
	}
}

// TestRestoreToTheFirstBackupUninstallsEverything checks the same in one
// step from three installs.
func TestRestoreToTheFirstBackupUninstallsEverything(t *testing.T) {
	home, _ := installHome(t)
	dir := copyPack(t)
	d := newClockFS(t)
	var ids []string
	for i := range 3 {
		writeFile(t, filepath.Join(dir, "base", "dotai", "tools", "hello.sh"), fmt.Sprintf("echo %d\n", i))
		res, err := quiet(t).install(d, dir, home)
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, res.Backup.ID)
	}
	res, err := restore(d, home, ids[0], nil)
	if err != nil {
		t.Fatalf("Restore() error = %v", err)
	}
	if got := installed(t, home); len(got) != 0 || !res.Uninstalled || len(res.Undone) != 3 {
		t.Errorf("home = %v, Restore() = %+v, want everything uninstalled in three steps", got, res)
	}
}

func TestRestoreRefusesAChangedFileWithoutConsent(t *testing.T) {
	home, _ := installHome(t)
	if _, err := quiet(t).install(newClockFS(t), examplePack, home); err != nil {
		t.Fatal(err)
	}
	edited := at(home, ".dotai/tools/hello.sh")
	writeFile(t, edited, "echo mine\n")
	before, stateBefore := tree(t, home), snapshot(t, home)
	var asked app.RestoreResult
	for name, overwrite := range map[string]func(app.RestoreResult) (bool, error){
		"no one to ask": nil,
		"declined":      func(r app.RestoreResult) (bool, error) { asked = r; return false, nil },
	} {
		t.Run(name, func(t *testing.T) {
			_, err := restore(newClockFS(t), home, "", overwrite)
			var ce *app.ConflictError
			if !errors.As(err, &ce) || len(ce.Conflicts) != 1 || ce.Conflicts[0].Target != edited || !strings.Contains(err.Error(), "the file "+edited+" was changed") {
				t.Fatalf("Restore() error = %v, want a conflict naming %s", err, edited)
			}
			if got := tree(t, home); !reflect.DeepEqual(got, before) || !reflect.DeepEqual(snapshot(t, home), stateBefore) {
				t.Errorf("home = %v, want it unchanged %v", got, before)
			}
		})
	}
	if len(asked.Conflicts) != 1 || asked.Conflicts[0].Target != edited || len(asked.Ops) != 5 {
		t.Errorf("asked about %+v, want the plan with the conflict", asked)
	}
	for _, op := range asked.Ops {
		if op.Conflict != (op.Target == edited) {
			t.Errorf("op %+v, want only %s marked as a conflict", op, edited)
		}
	}
}

func TestRestoreWithConsentKeepsTheChangedFileInItsBackup(t *testing.T) {
	home, _ := installHome(t)
	if _, err := quiet(t).install(newClockFS(t), examplePack, home); err != nil {
		t.Fatal(err)
	}
	edited := at(home, ".dotai/tools/hello.sh")
	writeFile(t, edited, "echo mine\n")
	res, err := restore(newClockFS(t), home, "", func(app.RestoreResult) (bool, error) { return true, nil })
	if err != nil {
		t.Fatalf("Restore() error = %v", err)
	}
	if _, err := os.Stat(edited); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("hello.sh = %v, want it deleted", err)
	}
	copied, err := os.ReadFile(filepath.Join(res.Backup.Path, "files", ".dotai", "tools", "hello.sh"))
	if err != nil || string(copied) != "echo mine\n" {
		t.Errorf("restore backup copy = %q, %v, want the user's change", copied, err)
	}
	if len(res.Conflicts) != 1 || res.Conflicts[0].Target != edited {
		t.Errorf("conflicts = %+v, want %s", res.Conflicts, edited)
	}
}

// TestRestoreLeavesAFileTheUserRemoved checks that a file that is already
// as restore would leave it is neither a conflict nor written, and that a
// restore with nothing to write takes no backup.
func TestRestoreLeavesAFileTheUserRemoved(t *testing.T) {
	home, _ := installHome(t)
	first, err := quiet(t).install(newClockFS(t), examplePack, home)
	if err != nil {
		t.Fatal(err)
	}
	for _, op := range first.Ops {
		if err := os.Remove(op.Target); err != nil {
			t.Fatal(err)
		}
	}
	res, err := restore(newClockFS(t), home, "", nil)
	if err != nil || len(res.Ops) != 0 || len(res.Conflicts) != 0 || res.Backup != nil || !res.Uninstalled {
		t.Fatalf("Restore() = %+v, %v, want nothing to write, no backup and the state removed", res, err)
	}
	if got := backups(t, home); !slices.Equal(got, []string{first.Backup.ID}) || manifest(t, home, first.Backup.ID).Restored == nil {
		t.Errorf("backups = %v, want only the install's, marked restored", got)
	}
}

func TestRestoreRefusesACorruptCopy(t *testing.T) {
	for _, damage := range []string{"changed", "missing"} {
		t.Run(damage, func(t *testing.T) {
			home, _ := installHome(t)
			writeFile(t, at(home, ".dotai/tools/hello.sh"), "echo mine\n")
			res, err := quiet(t).install(newClockFS(t), examplePack, home)
			if err != nil {
				t.Fatal(err)
			}
			copyPath := filepath.Join(res.Backup.Path, "files", ".dotai", "tools", "hello.sh")
			if damage == "changed" {
				writeFile(t, copyPath, "echo corrupt\n")
			} else if err := os.Remove(copyPath); err != nil {
				t.Fatal(err)
			}
			before := snapshot(t, home)
			_, err = restore(newClockFS(t), home, "", func(app.RestoreResult) (bool, error) { return true, nil })
			var ce *app.CorruptBackupError
			if !errors.As(err, &ce) || ce.ID != res.Backup.ID || !strings.Contains(err.Error(), "files/.dotai/tools/hello.sh") {
				t.Fatalf("Restore() error = %v, want the corrupt copy", err)
			}
			if got := snapshot(t, home); !reflect.DeepEqual(got, before) || len(backups(t, home)) != 1 {
				t.Errorf("home = %v, backups %v, want nothing written", got, backups(t, home))
			}
		})
	}
}

// TestRestoreRefusesATamperedManifest checks that no manifest or prior
// state makes a restore write outside the isolated root and the tools
// directory, even with a copy that matches.
func TestRestoreRefusesATamperedManifest(t *testing.T) {
	for name, tamper := range map[string]func(home string, m *backup.Manifest, dir string){
		"restore never writes there: a target outside the dotai directories": func(home string, m *backup.Manifest, dir string) {
			writeFile(t, filepath.Join(dir, "files", ".ssh", "config"), "Host evil\n")
			m.Ops = append(m.Ops, backup.Op{Action: plan.Remove, Target: at(home, ".ssh/config"), File: "files/.ssh/config", Before: &backup.Content{SHA256: sum("Host evil\n")}})
		},
		"restore never writes there: a target in the isolated root itself": func(home string, m *backup.Manifest, dir string) {
			m.Ops = append(m.Ops, backup.Op{Action: plan.Create, Target: at(home, ".dotai/isolated/x"), After: &backup.Content{SHA256: sum("x")}})
		},
		"not under the home directory: a target outside it": func(home string, m *backup.Manifest, dir string) {
			m.Ops = append(m.Ops, backup.Op{Action: plan.Create, Target: filepath.Join(filepath.Dir(home), "x"), After: &backup.Content{SHA256: sum("x")}})
		},
		// Only a replace install writes in a harness's own directory.
		"restore never writes there: a harness's own directory in an isolated backup": func(home string, m *backup.Manifest, dir string) {
			m.Ops = append(m.Ops, backup.Op{Action: plan.Create, Target: at(home, ".claude/CLAUDE.md"), After: &backup.Content{SHA256: sum("x")}})
		},
		"restore never writes there: a target outside the harness directories in a replace backup": func(home string, m *backup.Manifest, dir string) {
			m.Mode = app.ModeReplace
			m.Ops = append(m.Ops, backup.Op{Action: plan.Create, Target: at(home, ".ssh/id"), After: &backup.Content{SHA256: sum("x")}})
		},
		"not a clean absolute path: a wiped entry that climbs out of an area": func(home string, m *backup.Manifest, dir string) {
			m.Mode = app.ModeReplace
			writeFile(t, filepath.Join(dir, "files", ".ssh", "authorized_keys"), "evil\n")
			sep := string(filepath.Separator)
			m.Wipes = []backup.Wipe{{Harness: "claudecode", Path: home + sep + ".claude" + sep + ".." + sep + ".ssh" + sep + "authorized_keys", Kind: backup.KindFile,
				File: "files/.ssh/authorized_keys", Items: []backup.Item{{Kind: backup.KindFile, SHA256: sum("evil\n")}}}}
		},
		"restore never writes there: a wiped entry no replace install wipes": func(home string, m *backup.Manifest, dir string) {
			m.Mode = app.ModeReplace
			writeFile(t, filepath.Join(dir, "files", ".ssh", "config"), "Host evil\n")
			m.Wipes = []backup.Wipe{{Harness: "claudecode", Path: at(home, ".ssh/config"), Kind: backup.KindFile, File: "files/.ssh/config",
				Items: []backup.Item{{Kind: backup.KindFile, SHA256: sum("Host evil\n")}}}}
		},
		"restore never writes there: a split file no harness has": func(home string, m *backup.Manifest, dir string) {
			m.Mode = app.ModeReplace
			writeFile(t, filepath.Join(dir, "files", ".ssh", "config.json"), "{}")
			m.Splits = []backup.Split{{Harness: "claudecode", Target: at(home, ".ssh/config.json"), File: "files/.ssh/config.json",
				Before: backup.Content{SHA256: sum("{}")}, After: backup.Content{SHA256: sum("{ }")}, Keys: [][]string{{"x"}}}}
		},
		"is damaged: a wiped entry that does not hold what its manifest records": func(home string, m *backup.Manifest, dir string) {
			m.Mode = app.ModeReplace
			writeFile(t, filepath.Join(dir, "files", "CLAUDE.md"), "changed\n")
			m.Wipes = []backup.Wipe{{Harness: "claudecode", Path: at(home, "CLAUDE.md"), Kind: backup.KindFile, File: "files/CLAUDE.md",
				Items: []backup.Item{{Kind: backup.KindFile, SHA256: sum("mine\n")}}}}
		},
		"is damaged: a split file whose copy is not what its manifest records": func(home string, m *backup.Manifest, dir string) {
			m.Mode = app.ModeReplace
			writeFile(t, filepath.Join(dir, "files", ".claude.json"), "{}")
			m.Splits = []backup.Split{{Harness: "claudecode", Target: at(home, ".claude.json"), File: "files/.claude.json",
				Before: backup.Content{SHA256: sum(`{"mcpServers":{}}`)}, After: backup.Content{SHA256: sum("{}")}, Keys: [][]string{{"mcpServers"}}}}
		},
		"owns a file restore never writes: a prior state with a file outside": func(home string, m *backup.Manifest, dir string) {
			s := state.State{Schema: state.Schema, Harnesses: []string{}, Files: []state.File{{Target: at(home, ".ssh/config"), SHA256: sum("x")}}}
			data, err := state.Encode(s)
			if err != nil {
				t.Fatal(err)
			}
			writeFile(t, filepath.Join(dir, "state.json"), string(data))
			m.PriorState = &backup.PriorState{File: "state.json", SHA256: sum(string(data))}
		},
	} {
		t.Run(name, func(t *testing.T) {
			home, _ := installHome(t)
			ssh := at(home, ".ssh/config")
			writeFile(t, ssh, "Host mine\n")
			res, err := quiet(t).install(newClockFS(t), examplePack, home)
			if err != nil {
				t.Fatal(err)
			}
			m := manifest(t, home, res.Backup.ID)
			tamper(home, &m, res.Backup.Path)
			data, err := backup.Encode(m)
			if err != nil {
				t.Fatal(err)
			}
			writeFile(t, filepath.Join(res.Backup.Path, "manifest.json"), string(data))
			before := snapshot(t, home)
			want, _, _ := strings.Cut(name, ":")
			if _, err := restore(newClockFS(t), home, "", func(app.RestoreResult) (bool, error) { return true, nil }); err == nil || !strings.Contains(err.Error(), want) {
				t.Fatalf("Restore() of a tampered backup error = %v, want %q", err, want)
			}
			if got := snapshot(t, home); !reflect.DeepEqual(got, before) {
				t.Errorf("home = %v, want nothing written %v", got, before)
			}
		})
	}
}

// TestRestoreUndoesAnInterruptedInstall checks that a restore settles the
// journal of an install that was killed by undoing it as the newest
// install, without the pack.
func TestRestoreUndoesAnInterruptedInstall(t *testing.T) {
	home, _ := installHome(t)
	dir := copyPack(t)
	tools := filepath.Join(dir, "base", "dotai", "tools")
	writeFile(t, at(home, ".dotai/tools/mine.sh"), "echo mine\n")
	writeFile(t, filepath.Join(tools, "mine.sh"), "echo pack\n")
	if _, err := quiet(t).install(newClockFS(t), dir, home); err != nil {
		t.Fatal(err)
	}
	afterA := snapshot(t, home)
	stateA, _ := os.ReadFile(at(home, ".dotai/state.json"))
	writeFile(t, filepath.Join(tools, "hello.sh"), "echo changed\n")
	writeFile(t, filepath.Join(tools, "a.sh"), "echo new\n")
	writeFile(t, filepath.Join(tools, "mine.sh"), "echo pack again\n")
	crash(t, &crashFS{clockFS: newClockFS(t), crashAt: 2}, dir, "", home)
	s := readJSON(t, at(home, ".dotai/state.json"), state.Decode)
	if s.Pending == nil {
		t.Fatal("the crash left no journal")
	}
	res, err := restore(newClockFS(t), home, "", nil)
	if err != nil {
		t.Fatalf("Restore() error = %v", err)
	}
	if len(res.Undone) != 1 || res.Undone[0].ID != s.Pending.Backup {
		t.Errorf("undone = %+v, want the interrupted install %s", res.Undone, s.Pending.Backup)
	}
	delete(afterA, ".dotai/state.json")
	if got := snapshot(t, home); !reflect.DeepEqual(withoutState(got), afterA) {
		t.Errorf("home = %v, want it as after the first install %v", got, afterA)
	}
	if got, _ := os.ReadFile(at(home, ".dotai/state.json")); string(got) != string(stateA) {
		t.Errorf("state = %s, want the first install's %s", got, stateA)
	}
}

func withoutState(m map[string]string) map[string]string {
	delete(m, ".dotai/state.json")
	return m
}

// TestRestoreUndoesAFirstInstallThatWasKilled checks the same in a fresh
// home, where a created file the user changed since is a conflict and is
// left unless allowed.
func TestRestoreUndoesAFirstInstallThatWasKilled(t *testing.T) {
	home, _ := installHome(t)
	crash(t, &crashFS{clockFS: newClockFS(t), crashAt: 3}, examplePack, "", home)
	s := readJSON(t, at(home, ".dotai/state.json"), state.Decode)
	written := ""
	for _, op := range s.Pending.Ops {
		if _, err := os.Stat(op.Target); err == nil {
			written = op.Target
		}
	}
	writeFile(t, written, "mine now\n")
	_, err := restore(newClockFS(t), home, "", nil)
	var ce *app.ConflictError
	if !errors.As(err, &ce) || len(ce.Conflicts) != 1 || ce.Conflicts[0].Target != written {
		t.Fatalf("Restore() error = %v, want the changed file %s reported", err, written)
	}
	if _, err := restore(newClockFS(t), home, "", func(app.RestoreResult) (bool, error) { return true, nil }); err != nil {
		t.Fatalf("Restore() error = %v", err)
	}
	if got := installed(t, home); len(got) != 0 {
		t.Errorf("home = %v, want everything the killed install wrote gone", got)
	}
}

// restoreCrashFS kills a restore at its target write number crashAt.
type restoreCrashFS struct {
	*clockFS
	crashAt, writes int
}

func (f *restoreCrashFS) WriteIn(root, name string, data []byte, mode os.FileMode) error {
	if name != "state.json" {
		f.writes++
		if f.writes == f.crashAt {
			panic(errCrash)
		}
	}
	return f.clockFS.WriteIn(root, name, data, mode)
}

func (f *restoreCrashFS) RemoveIn(root, name string) error {
	if name != "state.json" {
		f.writes++
		if f.writes == f.crashAt {
			panic(errCrash)
		}
	}
	return f.clockFS.RemoveIn(root, name)
}

// killRestore runs a restore through d until d kills it.
func killRestore(t *testing.T, d app.ExecuteFS, home, id string) {
	t.Helper()
	defer func() {
		if r := recover(); r != errCrash {
			t.Fatalf("recovered %v, want the crash", r)
		}
	}()
	_, _ = restore(d, home, id, nil)
}

// TestRestoreSettlesARestoreThatWasKilled checks that the next restore
// rolls a killed restore back and then undoes what it was undoing, and
// that an install settles it too, as before.
func TestRestoreSettlesARestoreThatWasKilled(t *testing.T) {
	for _, next := range []string{"restore", "install"} {
		t.Run(next, func(t *testing.T) {
			home, _ := installHome(t)
			dir := copyPack(t)
			writeFile(t, at(home, ".dotai/tools/hello.sh"), "echo mine\n")
			first, err := quiet(t).install(newClockFS(t), dir, home)
			if err != nil {
				t.Fatal(err)
			}
			killRestore(t, &restoreCrashFS{clockFS: newClockFS(t), crashAt: 3}, home, "")
			s := readJSON(t, at(home, ".dotai/state.json"), state.Decode)
			if s.Pending == nil || !strings.HasSuffix(s.Pending.Backup, "_restore") {
				t.Fatalf("state = %+v, want the restore's journal", s)
			}
			if next == "install" {
				if _, err := quiet(t).install(newClockFS(t), dir, home); err != nil {
					t.Fatalf("Install() after the killed restore error = %v", err)
				}
				assertConverged(t, dir, "", home)
				if _, err := restore(newClockFS(t), home, "", nil); err != nil { // the install that settled it
					t.Fatal(err)
				}
			}
			res, err := restore(newClockFS(t), home, "", nil)
			if err != nil {
				t.Fatalf("Restore() error = %v", err)
			}
			if last := res.Undone[len(res.Undone)-1]; last.ID != first.Backup.ID || !res.Uninstalled {
				t.Errorf("Restore() = %+v, want the first install undone and nothing left", res)
			}
			if got := snapshot(t, home); !reflect.DeepEqual(got, map[string]string{".dotai/tools/hello.sh": "exec=false echo mine\n"}) {
				t.Errorf("home = %v, want only the user's own file, as it was", got)
			}
		})
	}
}

// TestRestoreAsksBeforeItLocks checks that the question is asked without
// the lock held, and that a restore whose files changed while it asked is
// refused under the lock without writing.
func TestRestoreAsksBeforeItLocks(t *testing.T) {
	home, _ := installHome(t)
	if _, err := quiet(t).install(newClockFS(t), examplePack, home); err != nil {
		t.Fatal(err)
	}
	writeFile(t, at(home, ".dotai/isolated/claude/settings.json"), "{\"model\": \"haiku\"}\n")
	before := snapshot(t, home)
	_, err := restore(newClockFS(t), home, "", func(app.RestoreResult) (bool, error) {
		if _, err := os.Stat(at(home, ".dotai/lock")); !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("lock while asking = %v, want none", err)
		}
		writeFile(t, at(home, ".dotai/tools/hello.sh"), "echo changed meanwhile\n")
		return true, nil
	})
	var se *app.StaleError
	if !errors.As(err, &se) || !strings.Contains(err.Error(), "run restore again") {
		t.Fatalf("Restore() error = %v, want a stale plan", err)
	}
	before[".dotai/tools/hello.sh"] = snapshot(t, home)[".dotai/tools/hello.sh"]
	if got := snapshot(t, home); !reflect.DeepEqual(got, before) || len(backups(t, home)) != 1 {
		t.Errorf("home = %v, backups %v, want nothing written", got, backups(t, home))
	}
}

// TestRestoreLeavesWhatAHarnessKeeps checks that an uninstall removes the
// empty isolated and tools directories, and keeps a harness directory that
// holds a file dotai never installed, such as a login.
func TestRestoreLeavesWhatAHarnessKeeps(t *testing.T) {
	home, _ := installHome(t)
	if _, err := quiet(t).install(newClockFS(t), examplePack, home); err != nil {
		t.Fatal(err)
	}
	login := at(home, ".dotai/isolated/claude/.credentials.json")
	writeFile(t, login, "{}\n")
	if _, err := restore(newClockFS(t), home, "", nil); err != nil {
		t.Fatal(err)
	}
	if got := tree(t, at(home, ".dotai/isolated")); !reflect.DeepEqual(got, []string{"claude/.credentials.json"}) {
		t.Errorf("isolated = %v, want only the login", got)
	}
	for _, gone := range []string{".dotai/isolated/gemini", ".dotai/isolated/opencode", ".dotai/tools"} {
		if _, err := os.Stat(at(home, gone)); !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("%s = %v, want it removed", gone, err)
		}
	}
}

func TestRestoreRefusesWhileAnotherHoldsTheLock(t *testing.T) {
	home, _ := installHome(t)
	if _, err := quiet(t).install(newClockFS(t), examplePack, home); err != nil {
		t.Fatal(err)
	}
	writeFile(t, at(home, ".dotai/lock"), "pid 4242\n")
	before := snapshot(t, home)
	_, err := restore(newClockFS(t), home, "", nil)
	var le *app.LockedError
	if !errors.As(err, &le) || le.PID != 4242 {
		t.Fatalf("Restore() error = %v, want the held lock", err)
	}
	if got := snapshot(t, home); !reflect.DeepEqual(got, before) {
		t.Errorf("home = %v, want it unchanged, with the lock left", got)
	}
}

// TestRestoreRollsBackWhenCancelled checks that Ctrl-C midway puts back
// what the restore changed, and the state.
func TestRestoreRollsBackWhenCancelled(t *testing.T) {
	home, _ := installHome(t)
	writeFile(t, at(home, ".dotai/tools/hello.sh"), "echo mine\n")
	first, err := quiet(t).install(newClockFS(t), examplePack, home)
	if err != nil {
		t.Fatal(err)
	}
	before := snapshot(t, home)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	_, err = app.RestoreWith(ctx, &cancelAnyFS{clockFS: newClockFS(t), cancel: cancel}, home, "", nil)
	var re *app.RestoreError
	if !errors.Is(err, context.Canceled) || !errors.As(err, &re) || re.Rollback != nil || !strings.Contains(err.Error(), re.Backup) {
		t.Fatalf("Restore() error = %v, want the cancellation and a rollback that worked", err)
	}
	if got := snapshot(t, home); !reflect.DeepEqual(got, before) {
		t.Errorf("home = %v, want it as before the restore %v", got, before)
	}
	if manifest(t, home, first.Backup.ID).Restored != nil {
		t.Error("the install backup is marked restored after a restore that was rolled back")
	}
}

// cancelAnyFS cancels after the first target it writes or removes.
type cancelAnyFS struct {
	*clockFS
	cancel context.CancelFunc
}

func (f *cancelAnyFS) WriteIn(root, name string, data []byte, mode os.FileMode) error {
	err := f.clockFS.WriteIn(root, name, data, mode)
	if name != "state.json" {
		f.cancel()
	}
	return err
}

func (f *cancelAnyFS) RemoveIn(root, name string) error {
	err := f.clockFS.RemoveIn(root, name)
	f.cancel()
	return err
}

func TestRestoreRefusesWhatItCannotUndo(t *testing.T) {
	home, _ := installHome(t)
	if _, err := restore(newClockFS(t), home, "", nil); err == nil || !strings.Contains(err.Error(), "nothing to restore") {
		t.Errorf("Restore() without a state error = %v, want nothing to restore", err)
	}
	if entries, _ := os.ReadDir(home); len(entries) != 0 {
		t.Errorf("home = %v, want nothing created", entries)
	}
	first, err := quiet(t).install(newClockFS(t), examplePack, home)
	if err != nil {
		t.Fatal(err)
	}
	var usage *app.UsageError
	if _, err := restore(newClockFS(t), home, "latest", nil); !errors.As(err, &usage) {
		t.Errorf("Restore(latest) error = %v, want a usage error", err)
	}
	for id, want := range map[string]string{
		"20261029T000000.000Z_install": "no backup",
		"20261029T000000.000Z_restore": "not one an install took",
	} {
		if id == "20261029T000000.000Z_restore" {
			if err := os.MkdirAll(at(home, ".dotai/backups/"+id), 0o700); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := restore(newClockFS(t), home, id, nil); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("Restore(%s) error = %v, want %q", id, err, want)
		}
	}
	// A backup taken before backups recorded the state: refused, named.
	m := manifest(t, home, first.Backup.ID)
	m.PriorState = nil
	data, err := backup.Encode(m)
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(first.Backup.Path, "manifest.json"), string(data))
	before := snapshot(t, home)
	if _, err := restore(newClockFS(t), home, "", nil); err == nil || !strings.Contains(err.Error(), "older dotai") || !strings.Contains(err.Error(), first.Backup.ID) {
		t.Errorf("Restore() of an older backup error = %v, want it refused and named", err)
	}
	if got := snapshot(t, home); !reflect.DeepEqual(got, before) {
		t.Errorf("home = %v, want nothing written", got)
	}
}

// TestRestoreStopsAtABackupThatIsGone checks that a restore that would
// pass a pruned backup, or follow a state edited to lead outside the
// backups, writes nothing.
func TestRestoreStopsAtABackupThatIsGone(t *testing.T) {
	home, _ := installHome(t)
	dir := copyPack(t)
	d := newClockFS(t)
	var ids []string
	for i := range 3 {
		writeFile(t, filepath.Join(dir, "base", "dotai", "tools", "hello.sh"), fmt.Sprintf("echo %d\n", i))
		res, err := quiet(t).install(d, dir, home)
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, res.Backup.ID)
	}
	if err := os.RemoveAll(at(home, ".dotai/backups/"+ids[1])); err != nil {
		t.Fatal(err)
	}
	before := snapshot(t, home)
	if _, err := restore(d, home, ids[0], nil); err == nil || !strings.Contains(err.Error(), ids[1]) || !strings.Contains(err.Error(), "pruned") {
		t.Errorf("Restore() error = %v, want the missing backup %s", err, ids[1])
	}
	s := readJSON(t, at(home, ".dotai/state.json"), state.Decode)
	s.Backup = "../../.ssh"
	data, err := state.Encode(s)
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, at(home, ".dotai/state.json"), string(data))
	before[".dotai/state.json"] = "exec=false " + string(data)
	if _, err := restore(d, home, "", nil); err == nil || !strings.Contains(err.Error(), "not a backup id") {
		t.Errorf("Restore() error = %v, want the edited state refused", err)
	}
	if got := snapshot(t, home); !reflect.DeepEqual(got, before) {
		t.Errorf("home = %v, want nothing written", got)
	}
}

// TestRetentionKeepsTheHistory checks that restores do not prune a backup
// the install state still leads to: after eight installs and four
// restores, the twelve backups lose a restored one, and a restore of the
// first install still puts back the disk as it was.
func TestRetentionKeepsTheHistory(t *testing.T) {
	home, _ := installHome(t)
	dir := copyPack(t)
	writeFile(t, at(home, ".dotai/tools/hello.sh"), "echo mine\n")
	original := snapshot(t, home)
	d := newClockFS(t)
	var ids []string
	for i := range 8 {
		writeFile(t, filepath.Join(dir, "base", "dotai", "tools", "hello.sh"), fmt.Sprintf("echo %d\n", i))
		res, err := quiet(t).install(d, dir, home)
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, res.Backup.ID)
	}
	for range 4 {
		if _, err := restore(d, home, "", nil); err != nil {
			t.Fatal(err)
		}
	}
	got := backups(t, home)
	if len(got) != 11 || slices.Contains(got, ids[4]) {
		t.Errorf("backups = %v, want eleven, without the oldest restored install %s", got, ids[4])
	}
	for _, id := range ids[:4] {
		if !slices.Contains(got, id) {
			t.Errorf("backup %s of the history was pruned", id)
		}
	}
	if _, err := restore(d, home, ids[0], nil); err != nil {
		t.Fatalf("Restore(%s) error = %v", ids[0], err)
	}
	if got := snapshot(t, home); !reflect.DeepEqual(got, original) {
		t.Errorf("home = %v, want the original %v", got, original)
	}
}
