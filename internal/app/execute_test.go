package app_test

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/lartweib/dotai/internal/app"
	"github.com/lartweib/dotai/internal/backup"
	"github.com/lartweib/dotai/internal/plan"
	"github.com/lartweib/dotai/internal/state"
)

// clockFS is the real file system port with a clock that advances one
// minute on each reading, so that each backup gets its own id.
type clockFS struct {
	app.OSInstallFS
	t   *testing.T
	now time.Time
}

func newClockFS(t *testing.T) *clockFS {
	return &clockFS{t: t, now: time.Date(2026, 10, 29, 10, 0, 0, 0, time.UTC)}
}

func (c *clockFS) Now() time.Time {
	c.now = c.now.Add(time.Minute)
	return c.now
}

func (f *fakeRulesync) install(d app.ExecuteFS, dir, home string) (app.InstallResult, error) {
	return app.InstallWith(context.Background(), d, f.fetch, f.run, dir, "", home)
}

// copyPack copies the example pack into a temporary directory, so that a
// test can change it between installs.
func copyPack(t *testing.T) string {
	t.Helper()
	dst := filepath.Join(t.TempDir(), "pack")
	err := filepath.WalkDir(examplePack, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, err := filepath.Rel(examplePack, p)
		if err != nil {
			return err
		}
		data, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Join(dst, filepath.Dir(rel)), 0o755); err != nil {
			return err
		}
		return os.WriteFile(filepath.Join(dst, rel), data, info.Mode().Perm())
	})
	if err != nil {
		t.Fatal(err)
	}
	return dst
}

// copyPackWithoutToolCommand is copyPack with agy's statusLine running
// hello.sh by its path instead of as a tool command (decision 0037), so
// that a test can drop hello.sh from the pack.
func copyPackWithoutToolCommand(t *testing.T) string {
	t.Helper()
	dir := copyPack(t)
	name := filepath.Join(dir, "base", "dotai", "settings.yaml")
	data, err := os.ReadFile(name)
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, name, strings.ReplaceAll(string(data), "${DOTAI_TOOL:hello}", "${DOTAI_TOOLS}/hello.sh"))
	return dir
}

func readJSON[T any](t *testing.T, name string, decode func([]byte) (T, error)) T {
	t.Helper()
	data, err := os.ReadFile(name)
	if err != nil {
		t.Fatal(err)
	}
	v, err := decode(data)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

// backups lists the backup directories under home.
func backups(t *testing.T, home string) []string {
	t.Helper()
	entries, err := os.ReadDir(at(home, ".dotai/backups"))
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	return names
}

// installed lists the files under home outside the backups.
func installed(t *testing.T, home string) []string {
	t.Helper()
	var out []string
	for _, f := range tree(t, home) {
		if !strings.HasPrefix(f, ".dotai/backups/") {
			out = append(out, f)
		}
	}
	return out
}

func TestInstallInAFreshHomeWritesEveryFile(t *testing.T) {
	home, tmp := installHome(t)
	d := newClockFS(t)
	res, err := (&fakeRulesync{t: t}).install(d, examplePack, home)
	if err != nil || len(res.Diagnostics) != 0 {
		t.Fatalf("Install() = %+v, %v", res, err)
	}
	if len(res.Ops) != 6 || res.Backup == nil || res.State != at(home, ".dotai/state.json") {
		t.Fatalf("Install() = %+v, want six ops, a backup and the state path", res)
	}
	for _, op := range res.Ops {
		if op.Action != plan.Create {
			t.Errorf("op %+v, want create", op)
		}
		data, err := os.ReadFile(op.Target)
		if err != nil || sum(string(data)) != op.After {
			t.Errorf("%s = %q, %v, want the staged content", op.Target, data, err)
		}
		info, err := os.Stat(op.Target)
		if err == nil && runtime.GOOS != "windows" && (info.Mode()&0o111 != 0) != op.Exec {
			t.Errorf("%s mode = %v, want exec %v", op.Target, info.Mode(), op.Exec)
		}
	}
	if got := at(home, ".dotai/tools/hello.sh"); runtime.GOOS != "windows" && !res.Ops[5].Exec || res.Ops[5].Target != got {
		t.Errorf("last op = %+v, want the executable tool %s", res.Ops[5], got)
	}

	id := "20261029T100100.000Z_install"
	if res.Backup.ID != id || res.Backup.Path != at(home, ".dotai/backups/"+id) {
		t.Errorf("backup = %+v, want %s", res.Backup, id)
	}
	m := readJSON(t, filepath.Join(res.Backup.Path, "manifest.json"), backup.Decode)
	if m.ID != id || m.Operation != "install" || m.Mode != "isolated" || m.Pack.Name != "minimal-pack" || m.Pack.Version != "0.1.0" ||
		!filepath.IsAbs(m.Pack.Path) || m.Variant != "standalone" || len(m.Harnesses) != 3 || m.Home != home || len(m.Ops) != 6 {
		t.Errorf("manifest = %+v, want the install of minimal-pack with six ops", m)
	}
	for i, op := range m.Ops {
		if op.Action != plan.Create || op.Target != res.Ops[i].Target || op.File != "" || op.Before != nil || op.After == nil || op.After.SHA256 != res.Ops[i].After {
			t.Errorf("manifest op %d = %+v, want a create without a copy", i, op)
		}
	}
	if _, err := os.Stat(filepath.Join(res.Backup.Path, "files")); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("backup files/ = %v, want none for a fresh install", err)
	}

	s := readJSON(t, res.State, state.Decode)
	if s.Pack.Name != "minimal-pack" || s.Pack.Version != "0.1.0" || s.Pack.Path != m.Pack.Path || s.Variant != "standalone" || s.Mode != "isolated" ||
		!s.InstalledAt.Equal(time.Date(2026, 10, 29, 10, 2, 0, 0, time.UTC)) || s.Backup != id || !reflect.DeepEqual(s.Harnesses, []string{"claudecode", "antigravity-cli", "opencode"}) {
		t.Errorf("state = %+v, want minimal-pack installed after its backup", s)
	}
	if len(s.Files) != 6 {
		t.Fatalf("state files = %+v, want six", s.Files)
	}
	for i, f := range s.Files {
		if f.Target != res.Ops[i].Target || f.Harness != res.Ops[i].Harness || f.SHA256 != res.Ops[i].After || f.Exec != res.Ops[i].Exec {
			t.Errorf("state file %d = %+v, want op %+v", i, f, res.Ops[i])
		}
	}
	if _, err := os.Stat(at(home, ".dotai/lock")); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("lock = %v, want it released", err)
	}
	assertNoStaging(t, tmp)
}

func TestInstallAgainWithNoChangeWritesNothing(t *testing.T) {
	home, _ := installHome(t)
	d := newClockFS(t)
	first, err := quiet(t).install(d, examplePack, home)
	if err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(first.State)
	if err != nil {
		t.Fatal(err)
	}
	res, err := quiet(t).install(d, examplePack, home)
	if err != nil {
		t.Fatal(err)
	}
	for _, op := range res.Ops {
		if op.Action != plan.Unchanged {
			t.Errorf("op %+v, want unchanged", op)
		}
	}
	if res.Backup != nil || res.State != first.State {
		t.Errorf("Install() backup = %+v, state %q, want no backup and the state path", res.Backup, res.State)
	}
	if got := backups(t, home); len(got) != 1 {
		t.Errorf("backups = %v, want only the first", got)
	}
	if after, _ := os.ReadFile(first.State); string(after) != string(before) {
		t.Errorf("state = %s, want it as the first install wrote it", after)
	}
}

func TestInstallReplacesAChangedFileWithABackup(t *testing.T) {
	home, _ := installHome(t)
	dir := copyPack(t)
	d := newClockFS(t)
	if _, err := quiet(t).install(d, dir, home); err != nil {
		t.Fatal(err)
	}
	target := at(home, ".dotai/tools/hello.sh")
	old, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	src := filepath.Join(dir, "base", "dotai", "tools", "hello.sh")
	data, err := os.ReadFile(src)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(src, append(data, "echo again\n"...), 0o755); err != nil {
		t.Fatal(err)
	}
	res, err := quiet(t).install(d, dir, home)
	if err != nil {
		t.Fatal(err)
	}
	var op app.InstallOp
	for _, o := range res.Ops {
		if o.Target == target {
			op = o
		} else if o.Action != plan.Unchanged {
			t.Errorf("op %+v, want unchanged", o)
		}
	}
	if op.Action != plan.Replace || op.Before != sum(string(old)) {
		t.Fatalf("hello.sh op = %+v, want replace", op)
	}
	if got, _ := os.ReadFile(target); sum(string(got)) != op.After || !strings.HasSuffix(string(got), "echo again\n") {
		t.Errorf("hello.sh = %q, want the new content", got)
	}
	m := readJSON(t, filepath.Join(res.Backup.Path, "manifest.json"), backup.Decode)
	if len(m.Ops) != 1 || m.Ops[0].Action != plan.Replace || m.Ops[0].File != "files/.dotai/tools/hello.sh" ||
		*m.Ops[0].Before != (backup.Content{SHA256: op.Before, Exec: op.BeforeExec}) || *m.Ops[0].After != (backup.Content{SHA256: op.After, Exec: op.Exec}) {
		t.Errorf("manifest ops = %+v, want the replace of hello.sh", m.Ops)
	}
	if copied, _ := os.ReadFile(filepath.Join(res.Backup.Path, "files", ".dotai", "tools", "hello.sh")); string(copied) != string(old) {
		t.Errorf("backup copy = %q, want %q", copied, old)
	}
	s := readJSON(t, res.State, state.Decode)
	if s.Backup != res.Backup.ID || s.Files[len(s.Files)-1].SHA256 != op.After {
		t.Errorf("state = %+v, want the new backup and hash", s)
	}
}

func TestInstallRemovesAFileThePackDropped(t *testing.T) {
	home, _ := installHome(t)
	dir := copyPackWithoutToolCommand(t)
	d := newClockFS(t)
	if _, err := quiet(t).install(d, dir, home); err != nil {
		t.Fatal(err)
	}
	target := at(home, ".dotai/tools/hello.sh")
	old, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(dir, "base", "dotai", "tools", "hello.sh")); err != nil {
		t.Fatal(err)
	}
	res, err := quiet(t).install(d, dir, home)
	if err != nil {
		t.Fatal(err)
	}
	last := res.Ops[len(res.Ops)-1]
	if last.Action != plan.Remove || last.Target != target || last.Source != "" || last.Before != sum(string(old)) {
		t.Errorf("last op = %+v, want the remove of %s", last, target)
	}
	if _, err := os.Stat(target); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("hello.sh = %v, want it removed", err)
	}
	m := readJSON(t, filepath.Join(res.Backup.Path, "manifest.json"), backup.Decode)
	if len(m.Ops) != 1 || m.Ops[0].Action != plan.Remove || m.Ops[0].After != nil || m.Ops[0].File != "files/.dotai/tools/hello.sh" {
		t.Errorf("manifest ops = %+v, want the remove of hello.sh", m.Ops)
	}
	if copied, _ := os.ReadFile(filepath.Join(res.Backup.Path, "files", ".dotai", "tools", "hello.sh")); string(copied) != string(old) {
		t.Errorf("backup copy = %q, want %q", copied, old)
	}
	s := readJSON(t, res.State, state.Decode)
	for _, f := range s.Files {
		if f.Target == target {
			t.Errorf("state still owns %s", target)
		}
	}
}

func TestInstallRefusesAnOwnedFileTheUserEdited(t *testing.T) {
	home, _ := installHome(t)
	dir := copyPackWithoutToolCommand(t)
	d := newClockFS(t)
	first, err := (&fakeRulesync{t: t}).install(d, dir, home)
	if err != nil {
		t.Fatal(err)
	}
	edited := at(home, ".dotai/isolated/claude/CLAUDE.md")
	writeFile(t, edited, "# mine\n")
	stateBefore, _ := os.ReadFile(first.State)
	// The pack changes too, so that the install has something to write.
	if err := os.Remove(filepath.Join(dir, "base", "dotai", "tools", "hello.sh")); err != nil {
		t.Fatal(err)
	}
	filesBefore := tree(t, home)
	_, err = (&fakeRulesync{t: t}).install(d, dir, home)
	var e *plan.EditedError
	if !errors.As(err, &e) || !reflect.DeepEqual(e.Targets, []string{edited}) || !strings.Contains(err.Error(), edited) {
		t.Fatalf("Install() error = %v, want an edited error naming %s", err, edited)
	}
	if got := tree(t, home); !reflect.DeepEqual(got, filesBefore) {
		t.Errorf("home = %v, want %v", got, filesBefore)
	}
	if got, _ := os.ReadFile(edited); string(got) != "# mine\n" {
		t.Errorf("CLAUDE.md = %q, want the user's edit", got)
	}
	if got, _ := os.ReadFile(first.State); string(got) != string(stateBefore) {
		t.Errorf("state = %s, want it unchanged", got)
	}
}

func TestInstallTakesOwnershipOfAFileThatAlreadyMatches(t *testing.T) {
	home, _ := installHome(t)
	writeFile(t, at(home, ".dotai/isolated/claude/CLAUDE.md"), "# generated\n")
	res, err := (&fakeRulesync{t: t}).install(newClockFS(t), examplePack, home)
	if err != nil {
		t.Fatal(err)
	}
	s := readJSON(t, res.State, state.Decode)
	if len(s.Files) != 6 || s.Files[0].Target != at(home, ".dotai/isolated/claude/CLAUDE.md") {
		t.Errorf("state files = %+v, want every planned file, unchanged ones too", s.Files)
	}
}

func TestInstallRecordsAFirstInstallThatWritesNothing(t *testing.T) {
	home, _ := installHome(t)
	d := newClockFS(t)
	first, err := quiet(t).install(d, examplePack, home)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(first.State); err != nil {
		t.Fatal(err)
	}
	res, err := quiet(t).install(d, examplePack, home)
	if err != nil {
		t.Fatal(err)
	}
	if res.Backup != nil {
		t.Errorf("backup = %+v, want none when nothing is written", res.Backup)
	}
	if s := readJSON(t, res.State, state.Decode); len(s.Files) != 5 || s.Backup != "" {
		t.Errorf("state = %+v, want the files and no backup", s)
	}
}

// tamperFS counts the writes of targets, not of the install state. At the
// first one, after the backup, it changes target on disk as another
// program would between plan and execution; it fails the write number
// failAt.
type tamperFS struct {
	*clockFS
	target string
	failAt int
	writes int
}

func (f *tamperFS) WriteIn(root, name string, data []byte, mode os.FileMode) error {
	if name != "state.json" {
		f.writes++
		if f.writes == 1 && f.target != "" {
			writeFile(f.t, f.target, "tampered\n")
		}
		if f.writes == f.failAt {
			return errDisk
		}
	}
	return f.clockFS.WriteIn(root, name, data, mode)
}

func TestInstallStopsWhenATargetChangedAfterPlanning(t *testing.T) {
	home, _ := installHome(t)
	tampered := at(home, ".dotai/tools/hello.sh")
	d := &tamperFS{clockFS: newClockFS(t), target: tampered}
	_, err := (&fakeRulesync{t: t}).install(d, examplePack, home)
	var changed *app.ChangedError
	var ie *app.InstallError
	if !errors.As(err, &changed) || changed.Path != tampered || !errors.As(err, &ie) || ie.Rollback != nil || ie.Backup == "" {
		t.Fatalf("Install() error = %v, want a changed target, a rollback that worked and the backup", err)
	}
	if !strings.Contains(err.Error(), tampered) || !strings.Contains(err.Error(), "rolled back") || !strings.Contains(err.Error(), ie.Backup) {
		t.Errorf("Install() error = %q, want the target, the rollback and the backup", err)
	}
	if got := installed(t, home); !reflect.DeepEqual(got, []string{".dotai/tools/hello.sh"}) {
		t.Errorf("home = %v, want only the tampered file, and no state", got)
	}
	if got, _ := os.ReadFile(tampered); string(got) != "tampered\n" {
		t.Errorf("hello.sh = %q, want what the other program wrote", got)
	}
	if _, err := os.Stat(filepath.Join(ie.Backup, "manifest.json")); err != nil {
		t.Errorf("backup manifest: %v, want the backup kept", err)
	}
	if _, err := os.Stat(at(home, ".dotai/isolated")); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("isolated root = %v, want the directories the install created removed", err)
	}
}

func TestInstallRollsBackFromTheBackupWhenAWriteFails(t *testing.T) {
	home, _ := installHome(t)
	foreign := at(home, ".dotai/isolated/claude/CLAUDE.md")
	writeFile(t, foreign, "# there before\n")
	d := &tamperFS{clockFS: newClockFS(t), failAt: 3}
	_, err := (&fakeRulesync{t: t}).install(d, examplePack, home)
	var ie *app.InstallError
	if !errors.Is(err, errDisk) || !errors.As(err, &ie) || ie.Rollback != nil {
		t.Fatalf("Install() error = %v, want the write failure and a rollback that worked", err)
	}
	if got := installed(t, home); !reflect.DeepEqual(got, []string{".dotai/isolated/claude/CLAUDE.md"}) {
		t.Errorf("home = %v, want only the file that was there, and no state", got)
	}
	if got, _ := os.ReadFile(foreign); string(got) != "# there before\n" {
		t.Errorf("CLAUDE.md = %q, want it restored from the backup", got)
	}
	if copied, _ := os.ReadFile(filepath.Join(ie.Backup, "files", ".dotai", "isolated", "claude", "CLAUDE.md")); string(copied) != "# there before\n" {
		t.Errorf("backup copy = %q, want the file as it was", copied)
	}
}

// overwriteFS writes other content over target right after the install
// wrote it, as another program would, and then fails the next write.
type overwriteFS struct {
	*clockFS
	target  string
	written bool
}

func (f *overwriteFS) WriteIn(root, name string, data []byte, mode os.FileMode) error {
	if f.written && name != "state.json" {
		return errDisk
	}
	if err := f.clockFS.WriteIn(root, name, data, mode); err != nil {
		return err
	}
	if filepath.Join(root, filepath.FromSlash(name)) == f.target {
		f.written = true
		writeFile(f.t, f.target, "someone else's\n")
	}
	return nil
}

// TestInstallRollbackLeavesWhatAnotherProgramWrote checks that a rollback
// does not overwrite a replaced target that is neither what the install
// wrote nor what was there, and reports it.
func TestInstallRollbackLeavesWhatAnotherProgramWrote(t *testing.T) {
	home, _ := installHome(t)
	target := at(home, ".dotai/isolated/claude/CLAUDE.md")
	writeFile(t, target, "# there before\n")
	_, err := (&fakeRulesync{t: t}).install(&overwriteFS{clockFS: newClockFS(t), target: target}, examplePack, home)
	var ie *app.InstallError
	if !errors.As(err, &ie) || ie.Rollback == nil || !strings.Contains(ie.Rollback.Error(), target) || !strings.Contains(ie.Rollback.Error(), "left as it is") {
		t.Fatalf("Install() error = %v, want a rollback that reports %s", err, target)
	}
	if got, _ := os.ReadFile(target); string(got) != "someone else's\n" {
		t.Errorf("CLAUDE.md = %q, want what the other program wrote", got)
	}
}

// failRemoveFS fails every removal inside a root, so that a rollback
// cannot delete what the install created.
type failRemoveFS struct {
	*tamperFS
}

func (failRemoveFS) RemoveIn(string, string) error { return errors.New("locked by another program") }

func TestInstallReportsARollbackThatFailed(t *testing.T) {
	home, _ := installHome(t)
	d := failRemoveFS{&tamperFS{clockFS: newClockFS(t), failAt: 2}}
	_, err := quiet(t).install(d, examplePack, home)
	var ie *app.InstallError
	if !errors.As(err, &ie) || ie.Rollback == nil || !strings.Contains(err.Error(), "locked by another program") || !strings.Contains(err.Error(), ie.Backup) {
		t.Errorf("Install() error = %v, want the cause, the rollback failure and the backup", err)
	}
	// The journal stays, so that the next install owns what was written.
	s := readJSON(t, at(home, ".dotai/state.json"), state.Decode)
	if s.Pending == nil || s.Pending.Backup != filepath.Base(ie.Backup) {
		t.Errorf("state = %+v, want the journal of the install", s)
	}
	if res, err := quiet(t).install(newClockFS(t), examplePack, home); err != nil || res.Backup == nil {
		t.Errorf("next Install() = %+v, %v, want it to finish the install", res, err)
	}
}

// failBackupFS fails to write the manifest of a backup.
type failBackupFS struct {
	*clockFS
}

func (f failBackupFS) WriteSynced(dir, name string, data []byte, perm os.FileMode) error {
	if name == "manifest.json" {
		return errDisk
	}
	return f.clockFS.WriteSynced(dir, name, data, perm)
}

func TestInstallWritesNothingWhenTheBackupFails(t *testing.T) {
	home, _ := installHome(t)
	writeFile(t, at(home, ".dotai/isolated/claude/CLAUDE.md"), "# there before\n")
	before := tree(t, home)
	_, err := (&fakeRulesync{t: t}).install(failBackupFS{newClockFS(t)}, examplePack, home)
	if !errors.Is(err, errDisk) || !strings.Contains(err.Error(), "nothing was written") {
		t.Fatalf("Install() error = %v, want the backup failure", err)
	}
	if got := tree(t, home); !reflect.DeepEqual(got, before) {
		t.Errorf("home = %v, want %v", got, before)
	}
	if got := backups(t, home); len(got) != 0 {
		t.Errorf("backups = %v, want the incomplete backup removed", got)
	}
}

func TestInstallRefusesWhileAnotherHoldsTheLock(t *testing.T) {
	home, _ := installHome(t)
	lock := at(home, ".dotai/lock")
	writeFile(t, lock, "")
	_, err := quiet(t).install(newClockFS(t), examplePack, home)
	var le *app.LockedError
	if !errors.As(err, &le) || le.Path != lock || !strings.Contains(err.Error(), "another dotai") || !strings.Contains(err.Error(), lock) {
		t.Fatalf("Install() error = %v, want a held lock naming %s", err, lock)
	}
	if got := tree(t, home); !reflect.DeepEqual(got, []string{".dotai/lock"}) {
		t.Errorf("home = %v, want only the lock, left in place", got)
	}
}

func TestInstallRefusesALinkAtTheDotaiDirectory(t *testing.T) {
	home, _ := installHome(t)
	real := t.TempDir()
	if err := os.Symlink(real, at(home, ".dotai")); err != nil {
		t.Skipf("symbolic links not supported here: %v", err)
	}
	_, err := quiet(t).install(newClockFS(t), examplePack, home)
	var nr *app.NotRegularError
	if !errors.As(err, &nr) || nr.Path != at(home, ".dotai") {
		t.Errorf("Install() error = %v, want a not-regular error naming the dotai directory", err)
	}
	if got := tree(t, real); len(got) != 0 {
		t.Errorf("link target holds %v, want nothing written through the link", got)
	}
}

func TestInstallInvalidPackWritesNothing(t *testing.T) {
	home, _ := installHome(t)
	res, err := app.InstallWith(context.Background(), newClockFS(t), quiet(t).fetch, quiet(t).run, examplePack, "nope", home)
	if err != nil || len(res.Diagnostics) != 1 || res.Backup != nil || res.State != "" {
		t.Fatalf("Install() = %+v, %v, want one diagnostic and nothing installed", res, err)
	}
	if entries, _ := os.ReadDir(home); len(entries) != 0 {
		t.Errorf("home = %v, want nothing, not even the dotai directory", entries)
	}
}

func TestInstallKeepsTheFirstAndTheLastTenBackups(t *testing.T) {
	home, _ := installHome(t)
	d := newClockFS(t)
	var ids []string
	for i := range 13 {
		if i > 0 { // a file the install owns is gone, so it is written again
			if err := os.Remove(at(home, ".dotai/tools/hello.sh")); err != nil {
				t.Fatal(err)
			}
		}
		res, err := quiet(t).install(d, examplePack, home)
		if err != nil || res.Backup == nil {
			t.Fatalf("install %d = %+v, %v, want a backup", i, res, err)
		}
		ids = append(ids, res.Backup.ID)
	}
	want := slices.Concat(ids[:1], ids[3:])
	if got := backups(t, home); !reflect.DeepEqual(got, want) {
		t.Errorf("backups = %v, want the first and the last ten %v", got, want)
	}
}

// TestPlanInstallAfterAnInstall checks that the dry run reads the
// ownership record as the install does, and still writes nothing.
func TestPlanInstallAfterAnInstall(t *testing.T) {
	home, _ := installHome(t)
	dir := copyPackWithoutToolCommand(t)
	if _, err := quiet(t).install(newClockFS(t), dir, home); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(dir, "base", "dotai", "tools", "hello.sh")); err != nil {
		t.Fatal(err)
	}
	before := tree(t, home)
	p, err := quiet(t).planInstall(dir, "", home)
	if err != nil {
		t.Fatal(err)
	}
	if last := p.Ops[len(p.Ops)-1]; last.Action != plan.Remove || last.Target != at(home, ".dotai/tools/hello.sh") {
		t.Errorf("last op = %+v, want the remove of hello.sh", last)
	}
	writeFile(t, at(home, ".dotai/isolated/claude/settings.json"), "{\"model\": \"haiku\"}\n")
	if _, err := quiet(t).planInstall(dir, "", home); !errors.As(err, new(*plan.EditedError)) {
		t.Errorf("PlanInstall() error = %v, want the edited key", err)
	}
	if got := tree(t, home); !reflect.DeepEqual(got, before) {
		t.Errorf("home = %v, want %v", got, before)
	}
}

func TestInstallRefusesAStateItCannotRead(t *testing.T) {
	home, _ := installHome(t)
	name := at(home, ".dotai/state.json")
	writeFile(t, name, `{"schema": "dotai.state/v9"}`)
	_, err := quiet(t).install(newClockFS(t), examplePack, home)
	if err == nil || !strings.Contains(err.Error(), name) || !strings.Contains(err.Error(), "dotai.state/v9") {
		t.Errorf("Install() error = %v, want the state file and its schema", err)
	}
	if got := tree(t, home); !reflect.DeepEqual(got, []string{".dotai/state.json"}) {
		t.Errorf("home = %v, want only the state", got)
	}
}

func TestInstallCleansTheHomeDirectory(t *testing.T) {
	home, _ := installHome(t)
	res, err := quiet(t).install(newClockFS(t), examplePack, home+"/./")
	if err != nil {
		t.Fatal(err)
	}
	m := readJSON(t, filepath.Join(res.Backup.Path, "manifest.json"), backup.Decode)
	if m.Home != home || res.State != at(home, ".dotai/state.json") || res.Root != at(home, ".dotai/isolated") {
		t.Errorf("home = %q, state %q, root %q, want them under %s", m.Home, res.State, res.Root, home)
	}
}

// TestInstallPrunesByTheRecordedTime checks retention with a clock that
// went back: the backup just taken stays, and so do incomplete backups'
// neighbours, while an incomplete backup left by an install that stopped
// is deleted.
func TestInstallPrunesByTheRecordedTime(t *testing.T) {
	home, _ := installHome(t)
	d := newClockFS(t)
	var ids []string
	for i := range 12 {
		if i > 0 {
			if err := os.Remove(at(home, ".dotai/tools/hello.sh")); err != nil {
				t.Fatal(err)
			}
		}
		res, err := quiet(t).install(d, examplePack, home)
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, res.Backup.ID)
	}
	incomplete := at(home, ".dotai/backups/20261029T095900.000Z_install")
	if err := os.MkdirAll(filepath.Join(incomplete, "files"), 0o700); err != nil {
		t.Fatal(err)
	}
	d.now = time.Date(2026, 10, 29, 9, 0, 0, 0, time.UTC) // the clock goes back
	if err := os.Remove(at(home, ".dotai/tools/hello.sh")); err != nil {
		t.Fatal(err)
	}
	res, err := quiet(t).install(d, examplePack, home)
	if err != nil {
		t.Fatal(err)
	}
	// The new backup is now the oldest by its recorded time, so it is the
	// protected first one; the ten latest stay, the two oldest go.
	want := slices.Concat([]string{res.Backup.ID}, ids[2:])
	slices.Sort(want)
	if got := backups(t, home); !reflect.DeepEqual(got, want) {
		t.Errorf("backups = %v, want %v", got, want)
	}
}

// TestInstallKeepsThePriorStateInItsBackup checks that each backup records
// the install state from before its install, so that a restore can put it
// back: none before the first install, and a synced copy after it.
func TestInstallKeepsThePriorStateInItsBackup(t *testing.T) {
	home, _ := installHome(t)
	dir := copyPack(t)
	d := newClockFS(t)
	first, err := quiet(t).install(d, dir, home)
	if err != nil {
		t.Fatal(err)
	}
	m := readJSON(t, filepath.Join(first.Backup.Path, "manifest.json"), backup.Decode)
	if m.PriorState == nil || !m.PriorState.None {
		t.Errorf("first prior state = %+v, want none", m.PriorState)
	}
	if _, err := os.Stat(filepath.Join(first.Backup.Path, "state.json")); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("first backup state.json = %v, want none", err)
	}
	before, err := os.ReadFile(first.State)
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(dir, "base", "dotai", "tools", "hello.sh"), "echo changed\n")
	second, err := quiet(t).install(d, dir, home)
	if err != nil {
		t.Fatal(err)
	}
	m = readJSON(t, filepath.Join(second.Backup.Path, "manifest.json"), backup.Decode)
	copied, err := os.ReadFile(filepath.Join(second.Backup.Path, "state.json"))
	if err != nil || string(copied) != string(before) {
		t.Fatalf("second backup state.json = %q, %v, want the state of the first install", copied, err)
	}
	if m.PriorState == nil || m.PriorState.None || m.PriorState.File != "state.json" || m.PriorState.SHA256 != sum(string(before)) {
		t.Errorf("second prior state = %+v, want the copy and its hash", m.PriorState)
	}
	if runtime.GOOS != "windows" {
		if info, err := os.Stat(filepath.Join(second.Backup.Path, "state.json")); err != nil || info.Mode().Perm() != 0o600 {
			t.Errorf("state.json copy mode = %v, %v, want 0600", info.Mode(), err)
		}
	}
}

// TestInstallAfterACrashChainsToTheCrashedBackup checks that the state an
// install starts from after a crash names the backup of the crashed
// install, so that a restore undoes that one next.
func TestInstallAfterACrashChainsToTheCrashedBackup(t *testing.T) {
	home, _ := installHome(t)
	dir := copyPack(t)
	if _, err := quiet(t).install(newClockFS(t), dir, home); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(dir, "base", "dotai", "tools", "hello.sh"), "echo changed\n")
	writeFile(t, filepath.Join(dir, "base", "dotai", "tools", "new.sh"), "echo new\n")
	crash(t, &crashFS{clockFS: newClockFS(t), crashAt: 2}, dir, "", home)
	crashed := readJSON(t, at(home, ".dotai/state.json"), state.Decode).Pending.Backup
	writeFile(t, filepath.Join(dir, "base", "dotai", "tools", "other.sh"), "echo other\n")
	res, err := quiet(t).install(newClockFS(t), dir, home)
	if err != nil || res.Backup == nil {
		t.Fatalf("Install() = %+v, %v, want a backup", res, err)
	}
	prior := readJSON(t, filepath.Join(res.Backup.Path, "state.json"), state.Decode)
	if prior.Backup != crashed || prior.Pending != nil {
		t.Errorf("prior state = %+v, want the crashed backup %s and no journal", prior, crashed)
	}
}
