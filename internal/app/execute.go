package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"time"

	"github.com/lartweib/dotai/internal/backup"
	"github.com/lartweib/dotai/internal/harness"
	"github.com/lartweib/dotai/internal/merge"
	"github.com/lartweib/dotai/internal/plan"
	"github.com/lartweib/dotai/internal/state"
)

// keepBackups is how many of the latest backups an install keeps, besides
// the first one (decision 0018).
const keepBackups = 10

// rollbackTimeout bounds a rollback, which runs even when the install was
// cancelled.
const rollbackTimeout = time.Minute

// InstallResult is the outcome of an install: its plan, the backup taken
// before the first write, nil when nothing was written, the path of the
// install state, "" when the pack has diagnostics, and what went wrong
// after the install itself had succeeded, such as old backups that could
// not be pruned. Warnings is never nil.
type InstallResult struct {
	InstallPlan
	Backup   *BackupRef `json:"backup"`
	State    string     `json:"state"`
	Warnings []string   `json:"warnings"`
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
	return fmt.Sprintf("the install stopped: %v; the rollback failed: %v; the backup %s holds what was there before, "+
		"and the next install starts from what is on disk", e.Cause, e.Rollback, e.Backup)
}

func (e *InstallError) Unwrap() []error {
	if e.Rollback == nil {
		return []error{e.Cause}
	}
	return []error{e.Cause, e.Rollback}
}

// LockedError is a lock file that another dotai holds, or that one which
// stopped left behind: the process id written in it, 0 when unknown, and
// how long ago it was taken, negative when unknown.
type LockedError struct {
	Path string
	PID  int
	Age  time.Duration
}

func (e *LockedError) Error() string {
	var held []string
	if e.PID > 0 {
		held = append(held, fmt.Sprintf("by process %d", e.PID))
	}
	if e.Age >= 0 {
		held = append(held, fmt.Sprintf("taken %s ago", e.Age))
	}
	detail := ""
	if len(held) > 0 {
		detail = " (" + strings.Join(held, ", ") + ")"
	}
	return fmt.Sprintf("another dotai is running: %s exists%s; if no dotai is running, one that stopped left it there: remove it and try again", e.Path, detail)
}

// stagingError is a temporary staging that could not be removed. Alone,
// after an install that succeeded, it is a warning.
type stagingError struct {
	dir string
	err error
}

func (e *stagingError) Error() string {
	return fmt.Sprintf("the temporary staging %s could not be removed: %v", e.dir, e.err)
}

func (e *stagingError) Unwrap() error { return e.err }

// executeFS is the port Install writes through, besides what it plans
// through. The writes named ...In are confined to root, an absolute real
// directory, with name a slash path inside it: nothing outside root is
// written, not even through a link or a junction.
type executeFS interface {
	installFS
	// WriteIn writes data through a temporary file next to name moved over
	// it, with at most the permissions of mode and of a file already there,
	// and the exec bits of mode where the file system has them.
	WriteIn(root, name string, data []byte, mode fs.FileMode) error
	RemoveIn(root, name string) error // a file, a link or an empty directory
	MkdirIn(root, name string) error  // with the missing ones above it
	// MoveIn renames the entry from to the missing to, a file, a directory
	// or a link, never followed (decision 0038).
	MoveIn(root, from, to string) error
	// CopyIn copies the entry from to the missing to, a file, a directory
	// with everything in it or a link, never followed, keeping permissions
	// and modification times, synced, until ctx is done; it gives the bytes
	// copied. A move across volumes falls back to it.
	CopyIn(ctx context.Context, root, from, to string) (int64, error)
	// RemoveAllIn removes name and everything in it, never following a
	// link.
	RemoveAllIn(root, name string) error
	// LinkIn creates the link name to the absolute dest, a junction on
	// Windows and a symbolic link elsewhere; name must be missing.
	LinkIn(root, name, dest string) error
	// WriteSynced writes a new file inside dir, as WriteFile does, and
	// syncs it to the disk.
	WriteSynced(dir, name string, data []byte, perm fs.FileMode) error
	SyncDir(name string) error // syncs a directory where the OS can
	Remove(name string) error  // an empty directory above a root
	Mkdir(name string, perm fs.FileMode) error
	MkdirAll(name string, perm fs.FileMode) error
	// Lock creates the file name only if it is missing, and gives the
	// function that removes it; an fs.ErrExist error when it is there.
	Lock(name string) (unlock func() error, err error)
	ModTime(name string) (time.Time, error)
	Now() time.Time
}

// Install installs the user scope of the pack in dir, with the options
// opts as PlanInstall reads them, into the absolute home directory home,
// as decisions 0018, 0028 and 0032 describe. It plans as PlanInstall does,
// including the removal of the files the previous install owned and the
// pack no longer stages. A pack with problems gives diagnostics and
// writes nothing.
//
// In the replace mode (decision 0038), nothing is written when the plan
// has refusals, a *RefusedError; while a harness it resets is running, a
// *RunningError, unless opts.IgnoreRunning; or when it wipes or
// overwrites anything dotai does not own and consent, asked once with the
// plan before the lock, does not answer true, a *ConsentError (a nil
// consent refuses). Each wiped entry is then moved into the backup and
// each split file rewritten by key before the pack's files are written.
//
// Only then, when there is something to write, it takes ~/.dotai/lock and
// checks that the install state did not change since it planned. Before
// the first write it backs up every target it replaces or removes, and
// records the ones it creates; a failed backup writes nothing. It then
// writes a journal of the operations into the state, checks each target
// again and writes it; a target that changed after planning, a
// cancellation or any other failure stops the install and rolls back what
// it wrote from the backup, even when ctx is done. The final state is
// written last, and older backups are pruned after it. An install that
// stops without its rollback, such as when it is killed, leaves the
// journal, so that the next install owns what it wrote. An install that
// writes no file takes no backup, and leaves the state as it was when it
// records the same install. Failures after the install succeeded are
// warnings. A warning the user must read before the install writes, such
// as to close a harness, is also given to warn, when it is not nil, once
// the plan is known and before the lock, the backup or any write.
func Install(ctx context.Context, cacheDir, dir, home string, opts InstallOptions, progress Progress, warn Warn, consent Consent) (InstallResult, error) {
	return Ports{}.Install(ctx, cacheDir, dir, home, opts, progress, warn, consent)
}

// Consent is asked, with the plan, whether a replace install may wipe or
// overwrite what dotai does not own (decision 0038).
type Consent = func(InstallPlan) (bool, error)

// Warn receives a warning, text for people, that must be read before
// dotai writes, as soon as it is known.
type Warn = func(text string)

func install(ctx context.Context, d executeFS, h host, fetch fetchFunc, run runFunc, cacheDir, dir, home string, opts InstallOptions, progress Progress, warn Warn, consent Consent) (InstallResult, error) {
	if !filepath.IsAbs(home) {
		return InstallResult{}, fmt.Errorf("home directory %q is not absolute", home)
	}
	home = filepath.Clean(home)
	packPath, err := filepath.Abs(dir)
	if err != nil {
		return InstallResult{}, err
	}
	if err := settleCrossings(runner{ctx: ctx, d: d, home: home}); err != nil {
		return InstallResult{}, err
	}
	var res InstallResult
	err = withPlan(ctx, d, h, fetch, run, cacheDir, dir, home, opts, progress, func(p planned) error {
		res = InstallResult{InstallPlan: p.plan, Warnings: []string{}}
		if len(p.plan.Diagnostics) > 0 {
			return nil
		}
		if len(p.plan.Refusals) > 0 {
			return &RefusedError{Refusals: p.plan.Refusals, Plan: p.plan}
		}
		if p.plan.Mode == ModeReplace && !opts.IgnoreRunning {
			if err := running(h, "install", p.harnesses); err != nil {
				return err
			}
		}
		entries, keys, files, err := foreign(d, p)
		if err != nil {
			return err
		}
		if entries+keys+files > 0 {
			ok := false
			if consent != nil {
				if ok, err = consent(p.plan); err != nil {
					return err
				}
			}
			if !ok {
				return &ConsentError{Plan: p.plan, Entries: entries, Keys: keys, Files: files}
			}
		}
		for _, w := range append(sharedWarnings(p.plan.SharedSkills), settingsWarning(p)) {
			if w == "" {
				continue
			}
			res.Warnings = append(res.Warnings, w)
			if warn != nil {
				warn(w)
			}
		}
		r := runner{ctx: ctx, d: d, home: home, p: p, pack: state.Pack{Name: p.plan.Pack.Name, Version: p.plan.Pack.Version, Path: packPath}, layouts: p.layouts(),
			warnings: &res.Warnings}
		return r.run(&res)
	})
	if se, ok := err.(*stagingError); ok { // the install itself succeeded
		res.Warnings = append(res.Warnings, se.Error())
		err = nil
	}
	if err != nil {
		return InstallResult{}, err
	}
	return res, nil
}

// settingsWarning asks to close each harness whose settings file the plan
// p replaces or removes, since a harness that is running writes there too
// (decision 0035), or is "" when there is none. A settings file the plan
// creates was missing, so no harness was writing it. dotai does not detect
// a running harness (decision 0018, rule 5).
func settingsWarning(p planned) string {
	var harnesses []string
	for _, op := range p.ops {
		if op.Settings && (op.Action == plan.Replace || op.Action == plan.Remove) && !slices.Contains(harnesses, op.Harness) {
			harnesses = append(harnesses, op.Harness)
		}
	}
	if len(harnesses) == 0 {
		return ""
	}
	return fmt.Sprintf("close %s before installing: a harness that is running may rewrite its settings file while dotai writes its keys there", strings.Join(harnesses, ", "))
}

// runner carries out one install plan. layouts are the layouts its
// targets may be in, the isolated one when empty.
type runner struct {
	ctx     context.Context
	d       executeFS
	home    string
	p       planned
	pack    state.Pack
	layouts []harness.Mode
	// warnings, when set, gets what went right but needs telling, such as
	// an entry copied across volumes instead of moved.
	warnings *[]string
}

// applied is an operation carried out, or, without a target, one that
// failed after it created directories, with the root its target is
// written in and the directories it created, outermost first.
type applied struct {
	op   plan.Op
	root string
	dirs []string
}

// run locks, backs up, journals, executes, writes the state and prunes
// the backups, and fills res with the backup, the state path and the
// warnings.
func (r runner) run(res *InstallResult) error {
	statePath := filepath.Join(r.home, dotaiDir, stateFile)
	var writes []plan.Op
	for _, op := range r.p.ops {
		if op.Action != plan.Unchanged {
			writes = append(writes, op)
		}
	}
	prev := r.p.prev
	next := state.New(r.pack, r.p.plan.Variant, r.p.plan.Mode, time.Time{}, r.p.harnesses, "", r.p.ops)
	next.SharedSkills, next.Selection = r.p.shared, r.p.choice
	if len(writes) == 0 && !r.p.resets() && prev != nil && prev.SameInstall(next) {
		res.State = statePath
		return nil
	}
	return r.locked(&res.Warnings, func() error { return r.write(res, writes, next) })
}

// write carries out writes under the lock, as run describes, towards the
// state next.
func (r runner) write(res *InstallResult, writes []plan.Op, next state.State) error {
	statePath := filepath.Join(r.home, dotaiDir, stateFile)
	prev := r.p.prev
	if err := r.recheckState(statePath); err != nil {
		return err
	}
	if prev != nil && prev.Pending != nil {
		r.removeTemps(prev.Pending)
	}

	if len(writes) == 0 && !r.p.resets() {
		if prev != nil {
			next.Backup = settledBackup(*prev)
		}
		next.InstalledAt = r.d.Now().UTC()
		if err := r.writeState(next); err != nil {
			return err
		}
		res.State = statePath
		return nil
	}

	base := state.State{Schema: state.Schema, Pack: next.Pack, Variant: next.Variant, Mode: next.Mode, Harnesses: next.Harnesses, Files: []state.File{}}
	var prior *state.State // what the backup records as the state before
	if prev != nil {
		base = prev.Settled(r.p.owned, r.p.keys)
		base.Backup = settledBackup(*prev)
		prior = &base
	}
	info := backup.Info{
		Mode: r.p.plan.Mode, Pack: backup.Pack{Name: r.pack.Name, Version: r.pack.Version, Path: r.pack.Path},
		Variant: r.p.plan.Variant, Harnesses: r.p.harnesses,
	}
	ex, err := r.resetWork()
	if err != nil {
		return fmt.Errorf("the backup failed, nothing was written: %w", err)
	}
	ref, err := r.backup(backup.OperationInstall, info, prior, writes, ex)
	if err != nil {
		return fmt.Errorf("the backup failed, nothing was written: %w", err)
	}
	// A replace install wipes first, so that the pack installs on a clean
	// base (decision 0038). Its journal has no ops until every wipe is
	// done: an install killed before then wrote none of the pack's files,
	// and the user's own entries it had not moved yet are not dotai's.
	resets := len(ex.wipes) > 0 || len(ex.splits) > 0
	ops := writes
	if resets {
		ops = nil
	}
	if err := r.writeState(journal(base, ref.ID, ops, ex)); err != nil {
		return fmt.Errorf("the install journal could not be written, nothing was written: %w", errors.Join(err, r.d.RemoveAll(ref.Path)))
	}
	next.Backup = ref.ID
	wiped, err := r.wipe(ref.Path, ex.wipes)
	var split []splitWrite
	if err == nil {
		split, err = r.writeSplits(ex.splits)
	}
	if err == nil && resets {
		err = r.recordItems(ref.Path, ex)
	}
	if err == nil && resets {
		err = r.writeState(journal(base, ref.ID, writes, ex))
	}
	var done []applied
	if err == nil {
		done, err = r.execute(writes)
	}
	if err == nil {
		next.InstalledAt = r.d.Now().UTC()
		err = r.writeState(next)
	}
	if err != nil {
		rb := errors.Join(r.rollback(ref.Path, done), r.unsplit(split), r.unmove(wiped))
		if rb == nil {
			rb = r.restoreState(prev != nil, base)
		}
		return &InstallError{Cause: err, Rollback: rb, Backup: ref.Path}
	}
	res.Backup, res.State = ref, statePath
	if err := r.prune(ref.ID, ref.ID); err != nil {
		res.Warnings = append(res.Warnings, fmt.Sprintf("older backups could not be pruned: %v", err))
	}
	return nil
}

// settledBackup is the backup that undoes what dotai owns once the journal
// of s, if any, is settled: that of the install the journal records, which
// may have written its files, or else the one s names. A restore's journal
// changes nothing there, since what it wrote is undone by the install
// before it.
func settledBackup(s state.State) string {
	if s.Pending != nil {
		if op, _ := backup.Operation(s.Pending.Backup); op == backup.OperationInstall {
			return s.Pending.Backup
		}
	}
	return s.Backup
}

// locked runs f holding ~/.dotai/lock. A lock that cannot be removed
// afterwards is added to warnings when f worked, and to its error when it
// did not.
func (r runner) locked(warnings *[]string, f func() error) (err error) {
	unlock, err := r.lock()
	if err != nil {
		return err
	}
	defer func() {
		if uErr := unlock(); uErr != nil {
			w := fmt.Sprintf("the lock %s could not be removed: %v; remove it before dotai runs again", filepath.Join(r.home, dotaiDir, lockFile), uErr)
			if err != nil {
				err = errors.Join(err, errors.New(w))
			} else {
				*warnings = append(*warnings, w)
			}
		}
	}()
	return f()
}

// lock takes ~/.dotai/lock, creating ~/.dotai when it is missing.
func (r runner) lock() (func() error, error) {
	dotai := filepath.Join(r.home, dotaiDir)
	if err := ensureDir(r.d, r.home, dotai, 0o755); err != nil {
		return nil, err
	}
	name := filepath.Join(dotai, lockFile)
	unlock, err := r.d.Lock(name)
	if !errors.Is(err, fs.ErrExist) {
		return unlock, err
	}
	le := &LockedError{Path: name, Age: -1}
	if data, _, err := r.d.Read(r.ctx, name); err == nil {
		_, _ = fmt.Sscanf(string(data), "pid %d", &le.PID)
	}
	if t, err := r.d.ModTime(name); err == nil {
		if age := r.d.Now().Sub(t).Round(time.Second); age >= 0 {
			le.Age = age
		}
	}
	return nil, le
}

// removeTemps removes the temporary files an install that stopped midway
// left next to the targets of its journal and next to the state, as
// WriteIn names them. It does its best: a file it cannot remove stays.
func (r runner) removeTemps(p *state.Pending) {
	targets := []string{filepath.Join(r.home, dotaiDir, stateFile)}
	for _, op := range p.Ops {
		targets = append(targets, op.Target)
	}
	for _, target := range targets {
		root, name, err := installRoot(r.home, target, r.layouts...)
		if target == targets[0] {
			root, name, err = filepath.Join(r.home, dotaiDir), stateFile, nil
		}
		if err != nil {
			continue
		}
		names, err := r.d.List(filepath.Dir(target))
		if err != nil {
			continue
		}
		for _, n := range names {
			if isTempOf(n, path.Base(name)) {
				_ = r.d.RemoveIn(root, path.Join(path.Dir(name), n))
			}
		}
	}
	for _, target := range p.Splits { // a split file is written in the directory that holds it
		names, err := r.d.List(filepath.Dir(target))
		if err != nil {
			continue
		}
		for _, n := range names {
			if isTempOf(n, filepath.Base(target)) {
				_ = r.d.RemoveIn(filepath.Dir(target), n)
			}
		}
	}
}

// recheckState checks, under the lock, that the install state is still
// the one the plan read, so that an install that ran in between is not
// undone.
func (r runner) recheckState(statePath string) error {
	cur, err := readState(r.d, r.home)
	if err != nil {
		return err
	}
	if (cur == nil) != (r.p.prev == nil) || cur != nil && !reflect.DeepEqual(*cur, *r.p.prev) {
		return &ChangedError{Path: statePath}
	}
	return nil
}

// backup writes a new backup of operation, which writes writes, what ex
// names besides, and installs what info names: a copy of each target that
// is replaced or removed, checked against the plan, a copy of each split
// file of ex and the record of each entry it moves into the backup
// (backupExtra), a copy of prior, the install state before the operation
// (nil for none), and then the manifest, each synced to the disk with its
// directories. A backup that fails is removed.
func (r runner) backup(operation string, info backup.Info, prior *state.State, writes []plan.Op, ex *extra) (ref *BackupRef, err error) {
	root := filepath.Join(r.home, dotaiDir, backupsDir)
	if err := ensureDir(r.d, r.home, root, 0o700); err != nil {
		return nil, err
	}
	t := r.d.Now()
	var id, dir string
	for range 1000 { // ids have milliseconds; another backup in the same one moves this one on
		id = backup.ID(t, operation)
		dir = filepath.Join(root, id)
		if err = r.d.Mkdir(dir, 0o700); !errors.Is(err, fs.ErrExist) {
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
	m, err := backup.New(id, operation, t, r.home, info, writes)
	if err != nil {
		return nil, err
	}
	dirs := map[string]bool{}  // the directories under dir that hold copies
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
		if err := r.d.WriteSynced(dir, op.File, data, 0o600); err != nil {
			return nil, err
		}
		for d := path.Dir(op.File); d != "."; d = path.Dir(d) {
			dirs[d] = true
		}
	}
	more, err := r.backupExtra(dir, &m, ex)
	if err != nil {
		return nil, err
	}
	maps.Copy(dirs, more)
	// Deepest first, so that each directory is synced after what it holds.
	for _, d := range slices.Backward(slices.Sorted(maps.Keys(dirs))) {
		if err := r.d.SyncDir(filepath.Join(dir, filepath.FromSlash(d))); err != nil {
			return nil, err
		}
	}
	m.PriorState = &backup.PriorState{None: true}
	if prior != nil {
		data, err := state.Encode(*prior)
		if err != nil {
			return nil, err
		}
		if err := r.d.WriteSynced(dir, backup.StateName, data, 0o600); err != nil {
			return nil, err
		}
		h := sha256.Sum256(data)
		m.PriorState = &backup.PriorState{File: backup.StateName, SHA256: hex.EncodeToString(h[:])}
	}
	data, err := backup.Encode(m)
	if err != nil {
		return nil, err
	}
	if err := r.d.WriteSynced(dir, backup.ManifestName, data, 0o600); err != nil {
		return nil, err
	}
	if err := errors.Join(r.d.SyncDir(dir), r.d.SyncDir(root)); err != nil {
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
				done = append(done, applied{root: a.root, dirs: a.dirs})
			}
			return done, err
		}
		done = append(done, a)
	}
	return done, nil
}

// isLink reports an operation on a link (decision 0036).
func isLink(op plan.Op) bool { return op.Link != "" || op.BeforeLink != "" }

// current gives the state of target, a link when link is set, without
// following it.
func (r runner) current(target string, link bool) (plan.Current, error) {
	if link {
		return r.d.Link(target)
	}
	return r.d.Hash(r.ctx, target)
}

// link makes the link name in root, at target, to dest, once dest is
// checked again to be the shared skill of its name (decision 0036), so
// that an edited record never makes a link anywhere else.
func (r runner) link(root, name, target, dest string) error {
	if err := plan.CheckLinkDest(r.home, target, dest, foldCase); err != nil {
		return err
	}
	return r.d.LinkIn(root, name, dest)
}

// apply carries out one operation once the directories above its target
// are real directories and the target is still as the plan found it. A
// remove also removes the directories it leaves empty, up to the root. A
// link is replaced by removing it and making the new one.
func (r runner) apply(op plan.Op) (applied, error) {
	a := applied{op: op}
	if err := r.ctx.Err(); err != nil {
		return a, err
	}
	root, name, err := installRoot(r.home, op.Target, r.layouts...)
	if err != nil {
		return a, err
	}
	a.root = root
	missing, err := r.parents(op.Target)
	if err != nil {
		return a, err
	}
	if len(missing) > 0 && op.Action == plan.Create {
		a.dirs = missing
		if err := ensureDir(r.d, r.home, root, 0o755); err != nil {
			return a, err
		}
		if dir := path.Dir(name); dir != "." {
			if err := r.d.MkdirIn(root, dir); err != nil {
				return a, err
			}
		}
	}
	if op.Link != "" && op.Action == plan.Create {
		// A directory where the link goes, which the plan found holding
		// only files it removes, or that an install killed before it wrote
		// there left: empty now, it gives way.
		if mode, err := r.d.Lstat(op.Target); err == nil && mode.IsDir() && mode&(fs.ModeSymlink|fs.ModeIrregular) == 0 {
			if err := r.d.RemoveIn(root, name); err != nil {
				return a, &ChangedError{Path: op.Target}
			}
		}
	}
	cur, err := r.current(op.Target, isLink(op))
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
		if err := r.d.RemoveIn(root, name); err != nil {
			return a, err
		}
		for dir := path.Dir(name); dir != "."; dir = path.Dir(dir) {
			if r.d.RemoveIn(root, dir) != nil {
				break // not empty
			}
		}
		return a, nil
	}
	if op.Link != "" {
		if op.Action == plan.Replace {
			if err := r.d.RemoveIn(root, name); err != nil {
				return a, err
			}
		}
		return a, r.link(root, name, op.Target, op.Link)
	}
	data := op.Data // a settings file with dotai's keys written into it
	if data == nil {
		if data, err = r.d.ReadFile(r.p.tree, op.Source); err != nil {
			return a, err
		}
	}
	if h := sha256.Sum256(data); hex.EncodeToString(h[:]) != op.After {
		return a, fmt.Errorf("%s changed after the plan was made", filepath.Join(r.p.tree, filepath.FromSlash(op.Source)))
	}
	return a, r.d.WriteIn(root, name, data, perm(op.Exec))
}

// installRoot is the directory target is written in, the tools directory
// or a harness's directory in one of layouts, the isolated one when there
// is none, and target's slash path inside it. In the isolated layout that
// directory is the harness's isolated directory; in the replace layout,
// its own configuration directory (decision 0038).
func installRoot(home, target string, layouts ...harness.Mode) (root, name string, err error) {
	rel, err := backup.Rel(home, target)
	if err != nil {
		return "", "", err
	}
	if rest, ok := strings.CutPrefix(rel, merge.ToolsDir+"/"); ok {
		return filepath.Join(home, filepath.FromSlash(merge.ToolsDir)), rest, nil
	}
	if len(layouts) == 0 {
		layouts = []harness.Mode{harness.Isolated}
	}
	for _, layout := range layouts {
		if layout == harness.Replace {
			for _, a := range harness.All() {
				dir := a.Root(harness.Replace)
				if rest, ok := strings.CutPrefix(rel, dir+"/"); ok && rest != "" {
					return filepath.Join(home, filepath.FromSlash(dir)), rest, nil
				}
			}
			continue
		}
		if rest, ok := strings.CutPrefix(rel, harness.IsolatedDir+"/"); ok {
			if dir, rest, ok := strings.Cut(rest, "/"); ok && rest != "" {
				return filepath.Join(home, filepath.FromSlash(harness.IsolatedDir), dir), rest, nil
			}
		}
	}
	return "", "", fmt.Errorf("target %s is not inside a harness directory or the tools directory", target)
}

// parents checks each directory between the home directory and target, as
// the plan does, and gives the missing ones, outermost first.
func (r runner) parents(target string) ([]string, error) {
	rel, err := filepath.Rel(r.home, filepath.Dir(target))
	if err != nil || !filepath.IsLocal(rel) && rel != "." {
		return nil, fmt.Errorf("target %s is not under the home directory %s", target, r.home)
	}
	if rel == "." {
		return nil, nil
	}
	var missing []string
	dir := r.home
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

// detached is r with a context of its own, bounded by rollbackTimeout, so
// that what a rollback puts back is put back even when the operation was
// cancelled.
func (r runner) detached() (runner, context.CancelFunc) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(r.ctx), rollbackTimeout)
	r.ctx = ctx
	return r, cancel
}

// rollback undoes done, last first, from the backup in dir, with a context
// of its own, so that a cancelled install is still rolled back: a created
// target is removed and a replaced or removed one is written back from its
// copy, unless something else changed it since, which is left and
// reported. Each directory the install created is removed when it is
// empty.
func (r runner) rollback(dir string, done []applied) error {
	r, cancel := r.detached()
	defer cancel()
	var errs []error
	for _, a := range slices.Backward(done) {
		if a.op.Target != "" {
			if err := r.undo(dir, a); err != nil {
				errs = append(errs, err)
			}
		}
		for _, d := range slices.Backward(a.dirs) {
			// Only an empty directory goes; one that is not is left.
			if rel, err := filepath.Rel(a.root, d); err == nil && rel != "." && filepath.IsLocal(rel) {
				_ = r.d.RemoveIn(a.root, filepath.ToSlash(rel))
			} else {
				_ = r.d.Remove(d)
			}
		}
	}
	return errors.Join(errs...)
}

func (r runner) undo(dir string, a applied) error {
	op := a.op
	_, name, err := installRoot(r.home, op.Target, r.layouts...)
	if err != nil {
		return err
	}
	cur, err := r.current(op.Target, isLink(op))
	exists := err == nil
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	changed := fmt.Errorf("%s was left as it is: it changed after the install wrote it", op.Target)
	if op.Action == plan.Create {
		switch {
		case !exists:
			return nil
		case !r.same(cur, op.After, op.Exec):
			return changed
		}
		return r.d.RemoveIn(a.root, name)
	}
	switch {
	case exists && r.same(cur, op.Before, op.BeforeExec):
		return nil // already what was there
	case exists && (op.Action == plan.Remove || !r.same(cur, op.After, op.Exec)):
		return changed
	}
	if op.BeforeLink != "" { // a link has no copy: it is made again
		if exists {
			if err := r.d.RemoveIn(a.root, name); err != nil {
				return err
			}
		}
		if d := path.Dir(name); d != "." {
			if err := r.d.MkdirIn(a.root, d); err != nil {
				return err
			}
		}
		return r.link(a.root, name, op.Target, op.BeforeLink)
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
	if d := path.Dir(name); d != "." {
		if err := r.d.MkdirIn(a.root, d); err != nil {
			return err
		}
	}
	return r.d.WriteIn(a.root, name, data, perm(op.BeforeExec))
}

// restoreState puts back the state the install started from after a
// rollback: base, or no state when there was none.
func (r runner) restoreState(had bool, base state.State) error {
	if had {
		return r.writeState(base)
	}
	err := r.d.RemoveIn(filepath.Join(r.home, dotaiDir), stateFile)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	return err
}

// writeState writes s to the state file, atomically, readable by the user
// only.
func (r runner) writeState(s state.State) error {
	data, err := state.Encode(s)
	if err != nil {
		return err
	}
	return r.d.WriteIn(filepath.Join(r.home, dotaiDir), stateFile, data, 0o600)
}

// prune removes the backups retention does not keep, never the backups in
// protect, with head the backup the install state now names ("" for
// none), from which the history is followed. The pinned first replace
// backup is never removed either, nor the history from head back to it
// (pinnedChain, decision 0038). A backup whose manifest cannot be read or
// decoded is left alone; one without a manifest is incomplete.
func (r runner) prune(head string, protect ...string) error {
	root := filepath.Join(r.home, dotaiDir, backupsDir)
	names, err := r.d.List(root)
	if err != nil {
		return err
	}
	order := r.historyOrder(head)
	chain := map[string]bool{}
	for _, id := range order {
		chain[id] = true
	}
	protect = append(slices.Clone(protect), r.pinnedChain(order)...)
	var entries []backup.Entry
	for _, name := range names {
		if _, ok := backup.ParseID(name); !ok {
			continue
		}
		data, err := r.d.ReadFile(filepath.Join(root, name), backup.ManifestName)
		if errors.Is(err, fs.ErrNotExist) {
			entries = append(entries, backup.Entry{ID: name})
			continue
		}
		if err != nil {
			continue
		}
		m, err := backup.Decode(data)
		if err != nil {
			continue
		}
		entries = append(entries, backup.Entry{ID: name, CreatedAt: m.CreatedAt, Complete: true, Chain: chain[name]})
	}
	var errs []error
	for _, id := range backup.Prune(entries, keepBackups, protect...) {
		errs = append(errs, r.d.RemoveAll(filepath.Join(root, id)))
	}
	return errors.Join(errs...)
}

// historyOrder is the backups the install state leads to, newest first:
// head, then the backup the state kept in it names, and so on, as far as
// each one can be read.
func (r runner) historyOrder(head string) []string {
	root := filepath.Join(r.home, dotaiDir, backupsDir)
	seen := map[string]bool{}
	var order []string
	for id := head; id != "" && !seen[id]; {
		if _, ok := backup.ParseID(id); !ok {
			break
		}
		seen[id] = true
		order = append(order, id)
		dir := filepath.Join(root, id)
		data, err := r.d.ReadFile(dir, backup.ManifestName)
		if err != nil {
			break
		}
		m, err := backup.Decode(data)
		if err != nil || m.PriorState == nil || m.PriorState.None {
			break
		}
		if data, err = r.d.ReadFile(dir, m.PriorState.File); err != nil {
			break
		}
		prior, err := state.Decode(data)
		if err != nil {
			break
		}
		id = prior.Backup
	}
	return order
}

// same reports whether cur is the content hash and exec bit, leaving the
// exec bit out where the file system has none.
func (r runner) same(cur plan.Current, hash string, exec bool) bool {
	return cur.Hash == hash && (r.p.ignoreExec || cur.Exec == exec)
}

// ensureDir checks each directory from home down to dir as the plan does,
// and creates the missing ones with perm.
func ensureDir(d executeFS, home, dir string, perm fs.FileMode) error {
	rel, err := filepath.Rel(home, dir)
	if err != nil || !filepath.IsLocal(rel) {
		return fmt.Errorf("%s is not under the home directory %s", dir, home)
	}
	at := home
	for seg := range strings.SplitSeq(rel, string(filepath.Separator)) {
		at = filepath.Join(at, seg)
		err := checkDir(d, at)
		if errors.Is(err, fs.ErrNotExist) {
			return d.MkdirAll(dir, perm)
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

// Lock writes the process id into the lock, for whoever finds it.
func (osInstallFS) Lock(name string) (func() error, error) {
	f, err := os.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return nil, err
	}
	_, wErr := fmt.Fprintf(f, "pid %d\n", os.Getpid())
	if err := errors.Join(wErr, f.Close()); err != nil {
		return nil, errors.Join(err, os.Remove(name))
	}
	return func() error { return os.Remove(name) }, nil
}

func (osInstallFS) ModTime(name string) (time.Time, error) {
	info, err := os.Lstat(name)
	if err != nil {
		return time.Time{}, err
	}
	return info.ModTime(), nil
}
