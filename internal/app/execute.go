package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/lartweib/dotai/internal/backup"
	"github.com/lartweib/dotai/internal/plan"
	"github.com/lartweib/dotai/internal/state"
)

// keepBackups is how many of the latest backups an install keeps, besides
// the first one (decision 0018).
const keepBackups = 10

// InstallResult is the outcome of an install: its plan, the backup taken
// before the first write, nil when nothing was written, and the path of
// the install state, "" when the pack has diagnostics.
type InstallResult struct {
	InstallPlan
	Backup *BackupRef `json:"backup"`
	State  string     `json:"state"`
}

// BackupRef is a backup's id and absolute directory.
type BackupRef struct {
	ID   string `json:"id"`
	Path string `json:"path"`
}

// ChangedError is a target that is no longer as the plan found it: another
// program changed it after planning, so the install stopped there.
type ChangedError struct {
	Path string
}

func (e *ChangedError) Error() string {
	return fmt.Sprintf("%s changed after the plan was made", e.Path)
}

// InstallError is an install that stopped after it started writing: the
// Cause, the outcome of the rollback from the backup, nil when everything
// was put back, and the Backup directory, which is kept either way.
type InstallError struct {
	Cause    error
	Rollback error
	Backup   string
}

func (e *InstallError) Error() string {
	if e.Rollback == nil {
		return fmt.Sprintf("the install stopped: %v; everything it wrote was rolled back from the backup %s, which is kept", e.Cause, e.Backup)
	}
	return fmt.Sprintf("the install stopped: %v; the rollback failed: %v; the backup %s holds what was there before", e.Cause, e.Rollback, e.Backup)
}

func (e *InstallError) Unwrap() []error {
	if e.Rollback == nil {
		return []error{e.Cause}
	}
	return []error{e.Cause, e.Rollback}
}

// LockedError is a lock file that another dotai holds, or that one which
// stopped left behind.
type LockedError struct {
	Path string
}

func (e *LockedError) Error() string {
	return fmt.Sprintf("another dotai is running: %s exists; if no dotai is running, one that stopped left it there: remove it and try again", e.Path)
}

// executeFS is the port Install writes through, besides what it plans
// through.
type executeFS interface {
	installFS
	// Read reads the regular file name, which is absolute, as Hash does,
	// and gives its content with its state.
	Read(ctx context.Context, name string) ([]byte, plan.Current, error)
	// WriteAtomic writes data to name, which is absolute, through a
	// temporary file in its directory moved over it, executable when exec
	// is set and the file system has an exec bit.
	WriteAtomic(name string, data []byte, exec bool) error
	Remove(name string) error   // a file or an empty directory
	Mkdir(name string) error    // one directory; an fs.ErrExist error when it is there
	MkdirAll(name string) error // a directory and the missing ones above it
	// Lock creates the file name only if it is missing, and gives the
	// function that removes it; an fs.ErrExist error when it is there.
	Lock(name string) (unlock func() error, err error)
	Now() time.Time
}

// Install installs the user scope of the pack in dir, with variant or the
// pack's default, isolated into the absolute home directory home, as
// decisions 0018, 0028 and 0032 describe. It plans as PlanInstall does,
// including the removal of the files the previous install owned and the
// pack no longer stages, and holds ~/.dotai/lock throughout. A pack with
// problems gives diagnostics and installs nothing. Before the first write
// it backs up every target it replaces or removes, and records the ones it
// creates; a failed backup writes nothing. It then checks each target
// again and writes it; a target that changed after planning, or any other
// failure, stops the install and rolls back what it wrote from the
// backup. The install state is written last, and older backups are pruned
// after it. An install that writes no file takes no backup, and leaves
// the state as it was when it records the same install.
func Install(ctx context.Context, cacheDir, dir, variant, home string, progress Progress) (InstallResult, error) {
	return install(ctx, osInstallFS{}, download, generate, cacheDir, dir, variant, home, progress)
}

func install(ctx context.Context, d executeFS, fetch fetchFunc, run runFunc, cacheDir, dir, variant, home string, progress Progress) (res InstallResult, err error) {
	if !filepath.IsAbs(home) {
		return InstallResult{}, fmt.Errorf("home directory %q is not absolute", home)
	}
	packPath, err := filepath.Abs(dir)
	if err != nil {
		return InstallResult{}, err
	}
	dotai := filepath.Join(home, dotaiDir)
	if err := ensureDir(d, home, dotai); err != nil {
		return InstallResult{}, err
	}
	lock := filepath.Join(dotai, lockFile)
	unlock, err := d.Lock(lock)
	if errors.Is(err, fs.ErrExist) {
		return InstallResult{}, &LockedError{Path: lock}
	}
	if err != nil {
		return InstallResult{}, err
	}
	defer func() {
		if uErr := unlock(); uErr != nil {
			err = errors.Join(err, fmt.Errorf("the lock %s could not be removed: %w", lock, uErr))
		}
	}()
	err = withPlan(ctx, d, fetch, run, cacheDir, dir, variant, home, progress, func(p planned) error {
		res = InstallResult{InstallPlan: p.plan}
		if len(p.plan.Diagnostics) > 0 {
			return nil
		}
		r := runner{ctx: ctx, d: d, home: home, p: p, pack: state.Pack{Name: p.plan.Pack.Name, Version: p.plan.Pack.Version, Path: packPath}}
		ref, statePath, err := r.run()
		res.Backup, res.State = ref, statePath
		return err
	})
	if err != nil {
		return InstallResult{}, err
	}
	return res, nil
}

// runner carries out one install plan.
type runner struct {
	ctx  context.Context
	d    executeFS
	home string
	p    planned
	pack state.Pack
}

// applied is an operation carried out, with the directories it created,
// outermost first.
type applied struct {
	op   plan.Op
	dirs []string
}

// run backs up, executes, writes the state and prunes the backups. It
// gives the backup, nil when nothing was written, and the state path.
func (r runner) run() (*BackupRef, string, error) {
	statePath := filepath.Join(r.home, dotaiDir, stateFile)
	var writes []plan.Op
	for _, op := range r.p.ops {
		if op.Action != plan.Unchanged {
			writes = append(writes, op)
		}
	}
	next := state.New(r.pack, r.p.plan.Variant, ModeIsolated, time.Time{}, r.p.harnesses, "", r.p.ops)
	if len(writes) == 0 {
		if r.p.prev != nil && r.p.prev.SameInstall(next) {
			return nil, statePath, nil
		}
		if r.p.prev != nil {
			next.Backup = r.p.prev.Backup
		}
		return nil, statePath, r.writeState(statePath, next)
	}
	ref, err := r.backup(writes)
	if err != nil {
		return nil, "", fmt.Errorf("the backup failed, nothing was written: %w", err)
	}
	next.Backup = ref.ID
	done, err := r.execute(writes)
	if err == nil {
		err = r.writeState(statePath, next)
	}
	if err != nil {
		return nil, "", &InstallError{Cause: err, Rollback: r.rollback(ref.Path, done), Backup: ref.Path}
	}
	if err := r.prune(); err != nil {
		return nil, "", fmt.Errorf("installed, with the backup %s and the state %s, but older backups could not be pruned: %w", ref.Path, statePath, err)
	}
	return ref, statePath, nil
}

// backup writes a new backup of writes: a copy of each target that is
// replaced or removed, checked against the plan, and then the manifest. A
// backup that fails is removed.
func (r runner) backup(writes []plan.Op) (ref *BackupRef, err error) {
	root := filepath.Join(r.home, dotaiDir, backupsDir)
	if err := ensureDir(r.d, r.home, root); err != nil {
		return nil, err
	}
	t := r.d.Now()
	var id, dir string
	for range 1000 { // ids have milliseconds; another backup in the same one moves this one on
		id = backup.ID(t, backup.OperationInstall)
		dir = filepath.Join(root, id)
		if err = r.d.Mkdir(dir); !errors.Is(err, fs.ErrExist) {
			break
		}
		t = t.Add(time.Millisecond)
	}
	if err != nil {
		return nil, err
	}
	defer func() {
		if err != nil {
			err = errors.Join(err, r.d.RemoveAll(dir))
		}
	}()
	info := backup.Info{
		Mode: ModeIsolated, Pack: backup.Pack{Name: r.pack.Name, Version: r.pack.Version, Path: r.pack.Path},
		Variant: r.p.plan.Variant, Harnesses: r.p.harnesses,
	}
	m, err := backup.New(id, backup.OperationInstall, t, r.home, info, writes)
	if err != nil {
		return nil, err
	}
	for i, op := range m.Ops { // m.Ops are writes, in order
		if op.File == "" {
			continue
		}
		data, cur, err := r.d.Read(r.ctx, op.Target)
		if errors.Is(err, fs.ErrNotExist) || err == nil && !r.same(cur, writes[i].Before, writes[i].BeforeExec) {
			return nil, &ChangedError{Path: op.Target}
		}
		if err != nil {
			return nil, err
		}
		if err := r.d.WriteFile(dir, op.File, data, perm(cur.Exec)); err != nil {
			return nil, err
		}
	}
	data, err := backup.Encode(m)
	if err != nil {
		return nil, err
	}
	if err := r.d.WriteFile(dir, backup.ManifestName, data, 0o644); err != nil {
		return nil, err
	}
	return &BackupRef{ID: id, Path: dir}, nil
}

// execute carries out writes in order and stops at the first failure. It
// gives the operations carried out, and the one that failed with the
// directories it created, so that they can be rolled back.
func (r runner) execute(writes []plan.Op) ([]applied, error) {
	var done []applied
	for _, op := range writes {
		a, err := r.apply(op)
		if err != nil {
			if len(a.dirs) > 0 {
				done = append(done, applied{dirs: a.dirs})
			}
			return done, err
		}
		done = append(done, a)
	}
	return done, nil
}

// apply carries out one operation once the directories above its target
// are real directories and the target is still as the plan found it.
func (r runner) apply(op plan.Op) (applied, error) {
	a := applied{op: op}
	if err := r.ctx.Err(); err != nil {
		return a, err
	}
	missing, err := r.parents(op.Target)
	if err != nil {
		return a, err
	}
	if len(missing) > 0 && op.Action == plan.Create {
		if err := r.d.MkdirAll(filepath.Dir(op.Target)); err != nil {
			return a, err
		}
		a.dirs = missing
	}
	cur, err := r.d.Hash(r.ctx, op.Target)
	switch {
	case op.Action == plan.Create && errors.Is(err, fs.ErrNotExist):
	case op.Action == plan.Create && err == nil, op.Action != plan.Create && errors.Is(err, fs.ErrNotExist):
		return a, &ChangedError{Path: op.Target}
	case err != nil:
		return a, err
	case !r.same(cur, op.Before, op.BeforeExec):
		return a, &ChangedError{Path: op.Target}
	}
	if op.Action == plan.Remove {
		return a, r.d.Remove(op.Target)
	}
	data, err := r.d.ReadFile(r.p.tree, op.Source)
	if err != nil {
		return a, err
	}
	if h := sha256.Sum256(data); hex.EncodeToString(h[:]) != op.After {
		return a, fmt.Errorf("the staged %s changed after the plan was made", op.Source)
	}
	return a, r.d.WriteAtomic(op.Target, data, op.Exec)
}

// parents checks each directory between the home directory and target, as
// the plan does, and gives the missing ones, outermost first.
func (r runner) parents(target string) ([]string, error) {
	rel, err := filepath.Rel(r.home, filepath.Dir(target))
	if err != nil || !filepath.IsLocal(rel) && rel != "." {
		return nil, fmt.Errorf("target %s is not under the home directory %s", target, r.home)
	}
	var missing []string
	dir := r.home
	if rel == "." {
		return nil, nil
	}
	for seg := range strings.SplitSeq(rel, string(filepath.Separator)) {
		dir = filepath.Join(dir, seg)
		if len(missing) > 0 {
			missing = append(missing, dir)
			continue
		}
		err := checkDir(r.d, dir)
		if errors.Is(err, fs.ErrNotExist) {
			missing = append(missing, dir)
			continue
		}
		if err != nil {
			return nil, err
		}
	}
	return missing, nil
}

// rollback undoes done, last first, from the backup in dir: a created
// target is removed unless something else changed it since, a replaced or
// removed one is written back from its copy, and each directory the
// install created is removed when it is empty.
func (r runner) rollback(dir string, done []applied) error {
	var errs []error
	for _, a := range slices.Backward(done) {
		if a.op.Target != "" {
			if err := r.undo(dir, a.op); err != nil {
				errs = append(errs, err)
			}
		}
		for _, d := range slices.Backward(a.dirs) {
			_ = r.d.Remove(d) // only an empty directory goes; one that is not is left
		}
	}
	return errors.Join(errs...)
}

func (r runner) undo(dir string, op plan.Op) error {
	if op.Action == plan.Create {
		cur, err := r.d.Hash(r.ctx, op.Target)
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		if err != nil {
			return err
		}
		if cur.Hash != op.After {
			return fmt.Errorf("%s was left: it changed after the install wrote it", op.Target)
		}
		return r.d.Remove(op.Target)
	}
	rel, err := backup.Rel(r.home, op.Target)
	if err != nil {
		return err
	}
	data, err := r.d.ReadFile(dir, backup.FilesDir+"/"+rel)
	if err != nil {
		return err
	}
	if h := sha256.Sum256(data); hex.EncodeToString(h[:]) != op.Before {
		return fmt.Errorf("the backup copy of %s does not match what was there", op.Target)
	}
	return r.d.WriteAtomic(op.Target, data, op.BeforeExec)
}

// writeState writes s to name, atomically, installed now.
func (r runner) writeState(name string, s state.State) error {
	s.InstalledAt = r.d.Now().UTC()
	data, err := state.Encode(s)
	if err != nil {
		return err
	}
	return r.d.WriteAtomic(name, data, false)
}

// prune removes the backups retention does not keep.
func (r runner) prune() error {
	root := filepath.Join(r.home, dotaiDir, backupsDir)
	names, err := r.d.List(root)
	if err != nil {
		return err
	}
	var errs []error
	for _, id := range backup.Prune(names, keepBackups) {
		errs = append(errs, r.d.RemoveAll(filepath.Join(root, id)))
	}
	return errors.Join(errs...)
}

// same reports whether cur is the content hash and exec bit, leaving the
// exec bit out where the file system has none.
func (r runner) same(cur plan.Current, hash string, exec bool) bool {
	return cur.Hash == hash && (r.p.ignoreExec || cur.Exec == exec)
}

// ensureDir checks each directory from home down to dir as the plan does,
// and creates the missing ones.
func ensureDir(d executeFS, home, dir string) error {
	rel, err := filepath.Rel(home, dir)
	if err != nil || !filepath.IsLocal(rel) {
		return fmt.Errorf("%s is not under the home directory %s", dir, home)
	}
	at := home
	for seg := range strings.SplitSeq(rel, string(filepath.Separator)) {
		at = filepath.Join(at, seg)
		err := checkDir(d, at)
		if errors.Is(err, fs.ErrNotExist) {
			return d.MkdirAll(dir)
		}
		if err != nil {
			return err
		}
	}
	return nil
}

// perm is the mode of a written file.
func perm(exec bool) fs.FileMode {
	if exec {
		return 0o755
	}
	return 0o644
}

func (osInstallFS) Read(ctx context.Context, name string) ([]byte, plan.Current, error) {
	f, info, err := openRegular(name)
	if err != nil {
		return nil, plan.Current{}, err
	}
	defer f.Close()
	data, err := io.ReadAll(ctxReader{ctx, f})
	if err != nil {
		return nil, plan.Current{}, fmt.Errorf("%s: %w", name, err)
	}
	h := sha256.Sum256(data)
	return data, plan.Current{Hash: hex.EncodeToString(h[:]), Exec: info.Mode()&0o111 != 0}, nil
}

// WriteAtomic leaves no temporary file behind when it fails. The file is
// synced before the move, so that a crash leaves the old or the new file.
func (o osInstallFS) WriteAtomic(name string, data []byte, exec bool) (err error) {
	f, err := os.CreateTemp(filepath.Dir(name), "."+filepath.Base(name)+".dotai-*")
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			_ = f.Close()
			_ = os.Remove(f.Name())
		}
	}()
	if _, err := f.Write(data); err != nil {
		return err
	}
	if err := f.Chmod(perm(exec && o.ExecBit())); err != nil && o.ExecBit() {
		return err
	}
	if err := f.Sync(); err != nil {
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(f.Name(), name)
}

func (osInstallFS) Remove(name string) error   { return os.Remove(name) }
func (osInstallFS) Mkdir(name string) error    { return os.Mkdir(name, 0o755) }
func (osInstallFS) MkdirAll(name string) error { return os.MkdirAll(name, 0o755) }
func (osInstallFS) Now() time.Time             { return time.Now() }

// Lock writes the process id into the lock, for whoever finds it.
func (osInstallFS) Lock(name string) (func() error, error) {
	f, err := os.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return nil, err
	}
	_, wErr := fmt.Fprintf(f, "pid %d\n", os.Getpid())
	if err := errors.Join(wErr, f.Close()); err != nil {
		return nil, errors.Join(err, os.Remove(name))
	}
	return func() error { return os.Remove(name) }, nil
}
