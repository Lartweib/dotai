package app_test

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/lartweib/dotai/internal/app"
	"github.com/lartweib/dotai/internal/plan"
	"github.com/lartweib/dotai/internal/state"
)

// errCrash is the panic of crashFS, which stands for the process being
// killed: no rollback runs.
var errCrash = errors.New("killed")

// crashFS kills the install at its target write number crashAt, before
// that write, so that crashAt-1 targets are written.
type crashFS struct {
	*clockFS
	crashAt, writes int
}

func (f *crashFS) WriteIn(root, name string, data []byte, mode os.FileMode) error {
	if name != "state.json" {
		f.writes++
		if f.writes == f.crashAt {
			panic(errCrash)
		}
	}
	return f.clockFS.WriteIn(root, name, data, mode)
}

func crash(t *testing.T, d app.ExecuteFS, dir, variant, home string) {
	t.Helper()
	defer func() {
		if r := recover(); r != errCrash {
			t.Fatalf("recovered %v, want the crash", r)
		}
	}()
	_, _ = app.InstallWith(context.Background(), d, quiet(t).fetch, quiet(t).run, dir, variant, home)
}

// assertConverged checks that a plan of home has nothing left to write and
// that the state has no journal.
func assertConverged(t *testing.T, dir, variant, home string) {
	t.Helper()
	p, err := app.PlanInstallWith(context.Background(), app.OSInstallFS{}, quiet(t).fetch, quiet(t).run, dir, variant, home)
	if err != nil {
		t.Fatal(err)
	}
	for _, op := range p.Ops {
		if op.Action != plan.Unchanged {
			t.Errorf("op %+v, want every target in place", op)
		}
	}
	if s := readJSON(t, at(home, ".dotai/state.json"), state.Decode); s.Pending != nil || s.Variant != p.Variant {
		t.Errorf("state = %+v, want the %s install without a journal", s, p.Variant)
	}
}

// TestInstallRecoversFromACrash checks that an install killed after some
// writes leaves a journal, and that the next install owns what the killed
// one wrote instead of refusing it as edited.
func TestInstallRecoversFromACrash(t *testing.T) {
	for _, k := range []int{1, 3} {
		t.Run("after "+string(rune('0'+k-1))+" writes", func(t *testing.T) {
			home, _ := installHome(t)
			crash(t, &crashFS{clockFS: newClockFS(t), crashAt: k}, examplePack, "", home)
			s := readJSON(t, at(home, ".dotai/state.json"), state.Decode)
			if s.Pending == nil || len(s.Pending.Ops) != 5 || len(s.Files) != 0 {
				t.Fatalf("state after the crash = %+v, want the journal of five creates", s)
			}
			// What a write the kill cut short leaves, and a file of the user's.
			left := at(home, ".dotai/tools/.hello.sh.dotai-0123456789abcdef")
			mine := at(home, ".dotai/tools/.hello.sh.dotai-notes")
			writeFile(t, left, "partial")
			writeFile(t, mine, "mine")
			res, err := quiet(t).install(newClockFS(t), examplePack, home)
			if _, err := os.Stat(left); !errors.Is(err, fs.ErrNotExist) {
				t.Errorf("temporary file = %v, want it removed", err)
			}
			if _, err := os.Stat(mine); err != nil {
				t.Errorf("user file = %v, want it kept", err)
			}
			_ = os.Remove(mine)
			if err != nil {
				t.Fatalf("Install() after the crash error = %v", err)
			}
			var created int
			for _, op := range res.Ops {
				if op.Action == plan.Create {
					created++
				}
			}
			if created != 5-(k-1) {
				t.Errorf("creates = %d, want the %d the crash left", created, 5-(k-1))
			}
			assertConverged(t, examplePack, "", home)
		})
	}
}

// TestInstallRecoversFromACrashOverAnInstall checks the same over an
// earlier install, with a create, a replace and a remove of owned files,
// killed before the replace or after it.
func TestInstallRecoversFromACrashOverAnInstall(t *testing.T) {
	for _, created := range []string{"b.sh", "z.sh"} { // in target order before hello.sh, or after it and lib/a.sh
		t.Run(created, func(t *testing.T) {
			home, _ := installHome(t)
			dir := copyPack(t)
			tools := filepath.Join(dir, "base", "dotai", "tools")
			writeFile(t, filepath.Join(tools, "lib", "a.sh"), "echo a\n")
			if _, err := quiet(t).install(newClockFS(t), dir, home); err != nil {
				t.Fatal(err)
			}
			writeFile(t, filepath.Join(tools, "hello.sh"), "echo changed\n")
			writeFile(t, filepath.Join(tools, created), "echo new\n")
			if err := os.RemoveAll(filepath.Join(tools, "lib")); err != nil {
				t.Fatal(err)
			}
			crash(t, &crashFS{clockFS: newClockFS(t), crashAt: 2}, dir, "", home)
			if s := readJSON(t, at(home, ".dotai/state.json"), state.Decode); s.Pending == nil || len(s.Pending.Ops) != 3 {
				t.Fatalf("state after the crash = %+v, want the journal of three operations", s)
			}
			if _, err := quiet(t).install(newClockFS(t), dir, home); err != nil {
				t.Fatalf("Install() after the crash error = %v", err)
			}
			assertConverged(t, dir, "", home)
			if got, _ := os.ReadFile(at(home, ".dotai/tools/hello.sh")); string(got) != "echo changed\n" {
				t.Errorf("hello.sh = %q, want the new content", got)
			}
			if _, err := os.Stat(at(home, ".dotai/tools/lib")); !errors.Is(err, fs.ErrNotExist) {
				t.Errorf("tools/lib = %v, want it removed", err)
			}
		})
	}
}

// cancelFS cancels the install after its first target write, as Ctrl-C
// would.
type cancelFS struct {
	*clockFS
	cancel context.CancelFunc
}

func (f *cancelFS) WriteIn(root, name string, data []byte, mode os.FileMode) error {
	err := f.clockFS.WriteIn(root, name, data, mode)
	if name != "state.json" {
		f.cancel()
	}
	return err
}

func TestInstallRollsBackWhenCancelled(t *testing.T) {
	home, _ := installHome(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	_, err := app.InstallWith(ctx, &cancelFS{newClockFS(t), cancel}, quiet(t).fetch, quiet(t).run, examplePack, "", home)
	var ie *app.InstallError
	if !errors.Is(err, context.Canceled) || !errors.As(err, &ie) || ie.Rollback != nil {
		t.Fatalf("Install() error = %v, want the cancellation and a rollback that worked", err)
	}
	if got := installed(t, home); len(got) != 0 {
		t.Errorf("home = %v, want what the install created removed, and no state", got)
	}
}

// warnFS fails the cleanups after an install: the staging, the pruning of
// the backups and the lock.
type warnFS struct {
	*clockFS
	staging, home string
	fail          string
}

func (f *warnFS) StagingDir() (string, error) {
	dir, err := f.clockFS.StagingDir()
	f.staging = dir
	return dir, err
}

func (f *warnFS) RemoveAll(name string) error {
	if f.fail == "staging" && name == f.staging {
		_ = f.clockFS.RemoveAll(name)
		return errors.New("busy")
	}
	return f.clockFS.RemoveAll(name)
}

func (f *warnFS) List(dir string) ([]string, error) {
	if f.fail == "prune" && dir == at(f.home, ".dotai/backups") {
		return nil, errors.New("busy")
	}
	return f.clockFS.List(dir)
}

func (f *warnFS) Lock(name string) (func() error, error) {
	unlock, err := f.clockFS.Lock(name)
	if err != nil || f.fail != "lock" {
		return unlock, err
	}
	return func() error {
		_ = unlock()
		return errors.New("busy")
	}, nil
}

func TestInstallWarnsAboutCleanupsThatFailed(t *testing.T) {
	for _, tt := range []struct{ fail, want string }{
		{"staging", "staging"},
		{"prune", "older backups could not be pruned"},
		{"lock", "could not be removed"},
	} {
		t.Run(tt.fail, func(t *testing.T) {
			home, _ := installHome(t)
			res, err := quiet(t).install(&warnFS{clockFS: newClockFS(t), home: home, fail: tt.fail}, examplePack, home)
			if err != nil || res.Backup == nil || res.State == "" {
				t.Fatalf("Install() = %+v, %v, want the install to succeed", res, err)
			}
			if len(res.Warnings) != 1 || !strings.Contains(res.Warnings[0], tt.want) || !strings.Contains(res.Warnings[0], "busy") {
				t.Errorf("warnings = %q, want one about %s", res.Warnings, tt.want)
			}
		})
	}
	home, _ := installHome(t)
	if res, err := quiet(t).install(newClockFS(t), examplePack, home); err != nil || res.Warnings == nil || len(res.Warnings) != 0 {
		t.Errorf("Install() = %+v, %v, want no warnings", res, err)
	}
}

// lockFS writes another install's state as it takes the lock, as an
// install that ran between this one's plan and its lock would.
type lockFS struct {
	*clockFS
	home string
}

func (f *lockFS) Lock(name string) (func() error, error) {
	writeFile(f.t, at(f.home, ".dotai/state.json"), `{"schema": "dotai.state/v1", "files": []}`)
	return f.clockFS.Lock(name)
}

func TestInstallStopsWhenTheStateChangedBeforeTheLock(t *testing.T) {
	home, _ := installHome(t)
	_, err := quiet(t).install(&lockFS{newClockFS(t), home}, examplePack, home)
	var changed *app.ChangedError
	if !errors.As(err, &changed) || changed.Path != at(home, ".dotai/state.json") {
		t.Fatalf("Install() error = %v, want the changed state", err)
	}
	if got := installed(t, home); !reflect.DeepEqual(got, []string{".dotai/state.json"}) || len(backups(t, home)) != 0 {
		t.Errorf("home = %v, backups %v, want only the other state", got, backups(t, home))
	}
}

func TestInstallNamesTheProcessThatHoldsTheLock(t *testing.T) {
	home, _ := installHome(t)
	writeFile(t, at(home, ".dotai/lock"), "pid 4242\n")
	_, err := quiet(t).install(app.OSInstallFS{}, examplePack, home)
	var le *app.LockedError
	if !errors.As(err, &le) || le.PID != 4242 || le.Age < 0 || !strings.Contains(err.Error(), "process 4242") || !strings.Contains(err.Error(), "ago") {
		t.Errorf("Install() error = %v, want the process and the age of the lock", err)
	}
}

// syncFS records the order of the backup's syncs and the first write of a
// target.
type syncFS struct {
	*clockFS
	events []string
}

func (f *syncFS) WriteSynced(dir, name string, data []byte, perm os.FileMode) error {
	f.events = append(f.events, "write "+name)
	return f.clockFS.WriteSynced(dir, name, data, perm)
}

func (f *syncFS) SyncDir(name string) error {
	f.events = append(f.events, "sync "+filepath.Base(name))
	return f.clockFS.SyncDir(name)
}

func (f *syncFS) WriteIn(root, name string, data []byte, mode os.FileMode) error {
	f.events = append(f.events, "target "+name)
	return f.clockFS.WriteIn(root, name, data, mode)
}

func TestInstallSyncsTheBackupBeforeTheFirstWrite(t *testing.T) {
	home, _ := installHome(t)
	writeFile(t, at(home, ".dotai/tools/hello.sh"), "old\n")
	d := &syncFS{clockFS: newClockFS(t)}
	res, err := quiet(t).install(d, examplePack, home)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"write files/.dotai/tools/hello.sh", "sync tools", "sync .dotai", "sync files",
		"write manifest.json", "sync " + res.Backup.ID, "sync backups",
		"target state.json", // the journal
	}
	if got := d.events[:len(want)]; !reflect.DeepEqual(got, want) {
		t.Errorf("events = %q, want %q", got, want)
	}
}

func TestInstallRemovesTheDirectoriesARemoveEmptied(t *testing.T) {
	home, _ := installHome(t)
	dir := copyPack(t)
	writeFile(t, filepath.Join(dir, "base", "dotai", "tools", "lib", "deep", "util.sh"), "echo util\n")
	if _, err := quiet(t).install(newClockFS(t), dir, home); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(filepath.Join(dir, "base", "dotai", "tools", "lib")); err != nil {
		t.Fatal(err)
	}
	if _, err := quiet(t).install(newClockFS(t), dir, home); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(at(home, ".dotai/tools/lib")); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("tools/lib = %v, want it removed with the file", err)
	}
	if _, err := os.Stat(at(home, ".dotai/tools/hello.sh")); err != nil {
		t.Errorf("tools/hello.sh = %v, want the tools directory kept", err)
	}
}

func TestInstallKeepsPermissionsPrivate(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows files have no permission bits")
	}
	home, _ := installHome(t)
	private := at(home, ".dotai/isolated/claude/settings.json")
	writeFile(t, private, "{}\n")
	if err := os.Chmod(private, 0o600); err != nil {
		t.Fatal(err)
	}
	writeFile(t, at(home, ".dotai/tools/hello.sh"), "old\n")
	res, err := quiet(t).install(newClockFS(t), examplePack, home)
	if err != nil {
		t.Fatal(err)
	}
	for name, want := range map[string]fs.FileMode{
		private:                           0o600,
		at(home, ".dotai/tools/hello.sh"): 0o755,
		at(home, ".dotai/backups"):        0o700 | fs.ModeDir,
		res.Backup.Path:                   0o700 | fs.ModeDir,
		filepath.Join(res.Backup.Path, "manifest.json"):                        0o600,
		filepath.Join(res.Backup.Path, "files", ".dotai", "tools", "hello.sh"): 0o600,
		res.State: 0o600,
	} {
		if info, err := os.Lstat(name); err != nil || info.Mode() != want {
			t.Errorf("%s mode = %v, %v, want %v", name, info.Mode(), err, want)
		}
	}
}

func TestNarrowMode(t *testing.T) {
	for _, tt := range []struct{ mode, cur, want fs.FileMode }{
		{0o644, 0o644, 0o644},
		{0o644, 0o600, 0o600},
		{0o755, 0o600, 0o700},
		{0o755, 0o644, 0o755},
		{0o644, 0o755, 0o644},
		{0o600, 0o644, 0o600},
	} {
		if got := app.NarrowMode(tt.mode, tt.cur); got != tt.want {
			t.Errorf("NarrowMode(%v, %v) = %v, want %v", tt.mode, tt.cur, got, tt.want)
		}
	}
}

func TestRetry(t *testing.T) {
	busy := errors.New("busy")
	transient := func(err error) bool { return err == busy }
	var waits []time.Duration
	sleep := func(d time.Duration) { waits = append(waits, d) }
	calls := 0
	renameBusyTwice := func() error {
		calls++
		if calls < 3 {
			return busy
		}
		return nil
	}
	if err := app.RetryWith(renameBusyTwice, transient, 5, time.Millisecond, sleep); err != nil || calls != 3 || !reflect.DeepEqual(waits, []time.Duration{time.Millisecond, 2 * time.Millisecond}) {
		t.Errorf("RetryWith() = %v after %d calls and waits %v, want success on the third", err, calls, waits)
	}
	calls, waits = 0, nil
	alwaysBusy := func() error { calls++; return busy }
	if err := app.RetryWith(alwaysBusy, transient, 5, time.Millisecond, sleep); err != busy || calls != 5 {
		t.Errorf("RetryWith() = %v after %d calls, want the error after five", err, calls)
	}
	calls = 0
	gone := errors.New("gone")
	if err := app.RetryWith(func() error { calls++; return gone }, transient, 5, time.Millisecond, sleep); err != gone || calls != 1 {
		t.Errorf("RetryWith() = %v after %d calls, want a lasting error at once", err, calls)
	}
	// ERROR_ACCESS_DENIED and ERROR_SHARING_VIOLATION are transient on
	// Windows only; ERROR_FILE_NOT_FOUND never is.
	win := runtime.GOOS == "windows"
	for errno, want := range map[syscall.Errno]bool{5: win, 32: win, 2: false} {
		if got := app.Transient(&os.LinkError{Op: "rename", Err: errno}); got != want {
			t.Errorf("Transient(%d) = %v, want %v", errno, got, want)
		}
	}
}

// TestWriteInStaysInsideTheRoot checks that a link inside a root cannot
// lead a write outside it.
func TestWriteInStaysInsideTheRoot(t *testing.T) {
	root, outside := t.TempDir(), t.TempDir()
	if err := os.Symlink(outside, filepath.Join(root, "out")); err != nil {
		t.Skipf("symbolic links not supported here: %v", err)
	}
	if err := (app.OSInstallFS{}).WriteIn(root, "out/x", []byte("x"), 0o644); err == nil {
		t.Error("WriteIn() through a link out of the root succeeded")
	}
	if entries, _ := os.ReadDir(outside); len(entries) != 0 {
		t.Errorf("outside = %v, want nothing written", entries)
	}
	if entries, _ := os.ReadDir(root); !slices.EqualFunc(entries, []string{"out"}, func(e os.DirEntry, n string) bool { return e.Name() == n }) {
		t.Errorf("root = %v, want only the link, no temporary file", entries)
	}
}

// TestWriteInDoesNotFollowAJunction checks the same for a Windows
// junction, which a directory swapped after the checks could be.
func TestWriteInDoesNotFollowAJunction(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("junctions are a Windows file system feature")
	}
	root, outside := t.TempDir(), t.TempDir()
	if out, err := exec.Command("cmd", "/c", "mklink", "/J", filepath.Join(root, "out"), outside).CombinedOutput(); err != nil {
		t.Skipf("cannot create a junction: %v: %s", err, out)
	}
	if err := (app.OSInstallFS{}).WriteIn(root, "out/x", []byte("x"), 0o644); err == nil {
		t.Error("WriteIn() through a junction out of the root succeeded")
	}
	if entries, _ := os.ReadDir(outside); len(entries) != 0 {
		t.Errorf("outside = %v, want nothing written", entries)
	}
}

// TestInstallReplacesAReadOnlyFile checks that a read-only target, which
// Windows would not let a move replace, is replaced, and stays read-only.
func TestInstallReplacesAReadOnlyFile(t *testing.T) {
	home, _ := installHome(t)
	target := at(home, ".dotai/tools/hello.sh")
	writeFile(t, target, "old\n")
	if err := os.Chmod(target, 0o444); err != nil {
		t.Fatal(err)
	}
	if _, err := quiet(t).install(newClockFS(t), examplePack, home); err != nil {
		t.Fatalf("Install() error = %v", err)
	}
	info, err := os.Stat(target)
	if got, _ := os.ReadFile(target); err != nil || string(got) == "old\n" || info.Mode().Perm()&0o200 != 0 {
		t.Errorf("hello.sh = %q, mode %v, %v, want the new content, still read-only", got, info.Mode(), err)
	}
	_ = os.Chmod(target, 0o644) // so that the temporary directory can be removed
}
