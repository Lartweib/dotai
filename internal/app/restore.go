package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/lartweib/dotai/internal/backup"
	"github.com/lartweib/dotai/internal/plan"
	"github.com/lartweib/dotai/internal/state"
)

// RestoreResult is the outcome of a restore, or, given to the overwrite
// function of Restore, its plan. Undone lists the installs it undoes,
// newest first; Ops what it writes, in the order it writes them; Conflicts
// the files that changed after an install wrote them; Harnesses those
// whose files it writes, which must not be running. Backup is the backup
// of what it overwrote or deleted, nil when it wrote no file; State is the
// path of the install state, which Uninstalled says was removed because
// no install is left. Lists are never nil.
type RestoreResult struct {
	Undone      []UndoneInstall   `json:"undone"`
	Ops         []RestoreOp       `json:"ops"`
	Conflicts   []RestoreConflict `json:"conflicts"`
	Harnesses   []string          `json:"harnesses"`
	Backup      *BackupRef        `json:"backup"`
	State       string            `json:"state"`
	Uninstalled bool              `json:"uninstalled"`
	Warnings    []string          `json:"warnings"`
}

// UndoneInstall is an install a restore undoes: its backup's id and
// directory, when it was taken, and what that install installed.
type UndoneInstall struct {
	ID        string      `json:"id"`
	Path      string      `json:"path"`
	CreatedAt time.Time   `json:"createdAt"`
	Pack      backup.Pack `json:"pack"`
	Variant   string      `json:"variant"`
	Harnesses []string    `json:"harnesses"`
}

// RestoreOp is one file a restore writes: a create puts back a file that
// is missing, a replace puts back the file over what is there and a remove
// deletes it. From is the backup whose copy is put back, "" for a remove;
// Before and BeforeExec are what the target holds, "" and false when it
// is missing; After and Exec what it is given, "" and false for a remove.
// Conflict marks a target that changed after the install wrote it.
type RestoreOp struct {
	Action     plan.Action `json:"action"`
	Harness    string      `json:"harness"`
	Target     string      `json:"target"`
	From       string      `json:"from"`
	Exec       bool        `json:"exec"`
	Before     string      `json:"before"`
	BeforeExec bool        `json:"beforeExec"`
	After      string      `json:"after"`
	Conflict   bool        `json:"conflict"`
}

// RestoreConflict is a target that holds neither what the install of
// Backup wrote nor what the restore puts there: the user or a harness
// changed it after that install.
type RestoreConflict struct {
	Target  string `json:"target"`
	Harness string `json:"harness"`
	Backup  string `json:"backup"`
}

// ConflictError is a restore that would overwrite or delete files that
// changed after dotai installed them, and was not allowed to.
type ConflictError struct {
	Conflicts []RestoreConflict
}

func (e *ConflictError) Error() string {
	targets := make([]string, len(e.Conflicts))
	for i, c := range e.Conflicts {
		targets[i] = c.Target
	}
	if len(targets) == 1 {
		return "the file " + targets[0] + " was changed after dotai installed it, and the restore would overwrite or delete it: " +
			"nothing was written; confirm the restore (--yes) to overwrite it, after it is copied into a new backup"
	}
	return "the files " + strings.Join(targets, ", ") + " were changed after dotai installed them, and the restore would overwrite or delete them: " +
		"nothing was written; confirm the restore (--yes) to overwrite them, after they are copied into a new backup"
}

// CorruptBackupError is a copy in a backup that is missing or does not
// hold what its manifest records, found before anything was written.
type CorruptBackupError struct {
	ID, File string
	Err      error // why it cannot be read, nil when it reads but differs
}

func (e *CorruptBackupError) Error() string {
	if e.Err != nil {
		return fmt.Sprintf("the backup %s is damaged: %s cannot be read: %v; nothing was written", e.ID, e.File, e.Err)
	}
	return fmt.Sprintf("the backup %s is damaged: %s does not hold what its manifest records; nothing was written", e.ID, e.File)
}

func (e *CorruptBackupError) Unwrap() error { return e.Err }

// PendingError is an install state with a journal: an install or a
// restore that did not finish, whose files are not all as either side
// records.
type PendingError struct {
	State   string
	Pack    state.Pack
	Variant string
}

func (e *PendingError) Error() string {
	return fmt.Sprintf("an install or a restore did not finish: %s still holds its journal, so restore cannot tell what is on disk; "+
		"run dotai install %s --variant %s to settle it, then restore again; nothing was written", e.State, e.Pack.Path, e.Variant)
}

// RestoreError is a restore that stopped after it started writing: the
// Cause, the outcome of putting back what it wrote, nil when it all was,
// and the Backup of what it overwrote or deleted, which is kept either way.
type RestoreError struct {
	Cause    error
	Rollback error
	Backup   string
}

func (e *RestoreError) Error() string {
	if e.Rollback == nil {
		return fmt.Sprintf("the restore stopped: %v; everything it wrote was put back from its backup %s, which is kept", e.Cause, e.Backup)
	}
	return fmt.Sprintf("the restore stopped: %v; putting back what it wrote failed: %v; the backup %s holds what was there before, "+
		"and the install state keeps the restore's journal: run dotai install to settle it", e.Cause, e.Rollback, e.Backup)
}

func (e *RestoreError) Unwrap() []error {
	if e.Rollback == nil {
		return []error{e.Cause}
	}
	return []error{e.Cause, e.Rollback}
}

// Restore undoes installs from their backups in the absolute home
// directory home, as decisions 0018, 0032 and 0033 describe. Without an
// id it undoes the install the install state names, the last one that
// wrote files; with one, every install from that one down to the install
// that took the backup id, newest first, so that the disk ends as it was
// before it. The installs are found from the state, through the state
// before each one that its backup keeps.
//
// Everything is checked under ~/.dotai/lock before the first write: the
// state has no journal, each manifest is one an install wrote for home
// with every target inside a harness's isolated directory or the tools
// directory, each copy holds what its manifest records, and each target
// holds what the install wrote. A target that holds something else, and
// not what the restore puts there either, is a conflict: overwrite is
// asked once with the plan, and the restore writes nothing unless it
// answers true; a nil overwrite refuses. Every file the restore then
// replaces or deletes is copied into a backup of its own first, so that
// nothing is lost, and a journal is written into the state; a failure or
// a cancellation rolls back from that backup, as an install does. The
// state is then the one from before the oldest install undone, or none,
// and the backups undone are marked restored. Failures after the restore
// succeeded are warnings.
func Restore(ctx context.Context, home, id string, overwrite func(RestoreResult) (bool, error)) (RestoreResult, error) {
	return restore(ctx, osInstallFS{}, home, id, overwrite)
}

func restore(ctx context.Context, d executeFS, home, id string, overwrite func(RestoreResult) (bool, error)) (RestoreResult, error) {
	if !filepath.IsAbs(home) {
		return RestoreResult{}, fmt.Errorf("home directory %q is not absolute", home)
	}
	home = filepath.Clean(home)
	if _, ok := backup.ParseID(id); id != "" && !ok {
		return RestoreResult{}, &UsageError{fmt.Sprintf("%q is not a backup id, such as 20261029T101500.000Z_install", id)}
	}
	s, err := readState(d, home)
	if err != nil {
		return RestoreResult{}, err
	}
	statePath := filepath.Join(home, dotaiDir, stateFile)
	if s == nil {
		return RestoreResult{}, fmt.Errorf("nothing to restore: there is no install state %s", statePath)
	}
	r := restorer{runner: runner{ctx: ctx, d: d, home: home, p: planned{
		prev: s, tree: filepath.Join(home, dotaiDir, backupsDir), ignoreExec: !d.ExecBit(),
	}}}
	res := RestoreResult{Warnings: []string{}}
	err = r.locked(&res.Warnings, func() error {
		if err := r.recheckState(statePath); err != nil {
			return err
		}
		return r.run(&res, id, overwrite)
	})
	if err != nil {
		return RestoreResult{}, err
	}
	return res, nil
}

// restorer carries out a restore; its planned holds the state it starts
// from as prev, the backups directory as the tree its sources are in, and
// the operations once they are planned.
type restorer struct {
	runner
}

// undone is an install backup a restore undoes: its manifest, directory
// and the state before its install, nil when there was none.
type undone struct {
	m     backup.Manifest
	dir   string
	prior *state.State
}

// vfile is what a target holds at one step of a restore: nothing, or
// content, from the disk or, with src set, from the copy at that slash
// path under the backups directory.
type vfile struct {
	exists bool
	cur    plan.Current
	src    string
	from   string // the backup that holds src
}

// run plans the restore of id into res, asks overwrite when there are
// conflicts, and carries it out.
func (r *restorer) run(res *RestoreResult, id string, overwrite func(RestoreResult) (bool, error)) error {
	s := *r.p.prev
	statePath := filepath.Join(r.home, dotaiDir, stateFile)
	if s.Pending != nil {
		return &PendingError{State: statePath, Pack: s.Pack, Variant: s.Variant}
	}
	chain, err := r.chain(s, id)
	if err != nil {
		return err
	}
	if err := r.plan(res, s, chain); err != nil {
		return err
	}
	res.State = statePath
	if len(res.Conflicts) > 0 {
		ok := false
		if overwrite != nil {
			if ok, err = overwrite(*res); err != nil {
				return err
			}
		}
		if !ok {
			return &ConflictError{Conflicts: res.Conflicts}
		}
	}
	prior := chain[len(chain)-1].prior
	finish := func() error {
		if prior == nil {
			res.Uninstalled = true
			return r.restoreState(false, state.State{})
		}
		return r.writeState(*prior)
	}

	ref := (*BackupRef)(nil)
	if len(r.p.ops) > 0 {
		info := backup.Info{Mode: ModeIsolated, Harnesses: []string{}}
		if prior != nil {
			info = backup.Info{Mode: prior.Mode, Pack: backup.Pack(prior.Pack), Variant: prior.Variant, Harnesses: prior.Harnesses}
		}
		if ref, err = r.backup(backup.OperationRestore, info, &s, r.p.ops); err != nil {
			return fmt.Errorf("the backup failed, nothing was written: %w", err)
		}
		if err := r.writeState(state.Journal(s, ref.ID, r.p.ops)); err != nil {
			return fmt.Errorf("the restore journal could not be written, nothing was written: %w", errors.Join(err, r.d.RemoveAll(ref.Path)))
		}
		done, err := r.execute(r.p.ops)
		if err == nil {
			err = finish()
		}
		if err != nil {
			rb := r.rollback(ref.Path, done)
			if rb == nil {
				rb = r.restoreState(true, s)
			}
			return &RestoreError{Cause: err, Rollback: rb, Backup: ref.Path}
		}
	} else if err := finish(); err != nil {
		return err
	}
	res.Backup = ref

	for _, u := range chain {
		if err := r.mark(u, ref); err != nil {
			res.Warnings = append(res.Warnings, fmt.Sprintf("the backup %s could not be marked restored: %v; restore no longer reaches it, since the install state no longer leads to it", u.m.ID, err))
		}
	}
	if ref != nil {
		protect := []string{ref.ID}
		if prior != nil && prior.Backup != "" {
			protect = append(protect, prior.Backup)
		}
		if err := r.prune(protect...); err != nil {
			res.Warnings = append(res.Warnings, fmt.Sprintf("older backups could not be pruned: %v", err))
		}
	}
	return nil
}

// chain gives the installs to undo, newest first: the one s names, then
// the one before it, as the state each backup keeps names it, down to the
// backup id, or only the first without an id.
func (r *restorer) chain(s state.State, id string) ([]undone, error) {
	if s.Backup == "" {
		return nil, fmt.Errorf("nothing to restore: no install recorded in %s wrote files", filepath.Join(r.home, dotaiDir, stateFile))
	}
	if id != "" {
		if err := r.checkTarget(id); err != nil {
			return nil, err
		}
	}
	var chain []undone
	for next := s.Backup; ; {
		u, err := r.load(next)
		if err != nil {
			return nil, err
		}
		chain = append(chain, u)
		if id == "" || next == id {
			return chain, nil
		}
		if u.prior == nil || u.prior.Backup == "" {
			return nil, fmt.Errorf("the backup %s is not one restore can undo: it is not in the history the install state leads to (%s)", id, strings.Join(ids(chain), ", "))
		}
		if slices.ContainsFunc(chain, func(c undone) bool { return c.m.ID == u.prior.Backup }) {
			return nil, fmt.Errorf("the backups %s name each other as the install before: they were edited, so restore stops", strings.Join(ids(chain), ", "))
		}
		next = u.prior.Backup
	}
}

func ids(chain []undone) []string {
	out := make([]string, len(chain))
	for i, u := range chain {
		out[i] = u.m.ID
	}
	return out
}

// checkTarget gives a reason the backup id that a restore goes back to
// cannot be undone, found before the chain is followed.
func (r *restorer) checkTarget(id string) error {
	dir := filepath.Join(r.p.tree, id)
	if err := checkDir(r.d, dir); errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("there is no backup %s in %s", id, r.p.tree)
	} else if err != nil {
		return err
	}
	if op, _ := backup.Operation(id); op != backup.OperationInstall {
		return fmt.Errorf("the backup %s is not one an install took: restore undoes installs", id)
	}
	data, err := r.d.ReadFile(dir, backup.ManifestName)
	if err != nil {
		return fmt.Errorf("the backup %s has no manifest it can read: %w", id, err)
	}
	if m, err := backup.Decode(data); err == nil && m.Restored != nil {
		return fmt.Errorf("the backup %s is already restored: a restore undid its install at %s", id, m.Restored.At.Format(time.RFC3339))
	}
	return nil
}

// load reads and checks the backup id of an install to undo: its
// manifest, every copy it holds and the state before its install.
func (r *restorer) load(id string) (undone, error) {
	if _, ok := backup.ParseID(id); !ok {
		return undone{}, fmt.Errorf("the install state leads to %q, which is not a backup id: it was edited, so restore stops", id)
	}
	dir := filepath.Join(r.p.tree, id)
	for _, d := range []string{filepath.Join(r.home, dotaiDir), r.p.tree, dir} {
		if err := checkDir(r.d, d); errors.Is(err, fs.ErrNotExist) {
			return undone{}, fmt.Errorf("the backup %s is missing from %s: it was pruned or removed, so restore cannot go back past it", id, r.p.tree)
		} else if err != nil {
			return undone{}, err
		}
	}
	data, err := r.d.ReadFile(dir, backup.ManifestName)
	if err != nil {
		return undone{}, fmt.Errorf("the backup %s has no manifest it can read: %w", id, err)
	}
	m, err := backup.Decode(data)
	if err != nil {
		return undone{}, fmt.Errorf("%s: %w", filepath.Join(dir, backup.ManifestName), err)
	}
	if err := backup.Check(m, id, r.home); err != nil {
		return undone{}, err
	}
	switch {
	case m.Operation != backup.OperationInstall:
		return undone{}, fmt.Errorf("the backup %s is not one an install took: restore undoes installs", id)
	case m.Restored != nil:
		return undone{}, fmt.Errorf("the backup %s is already restored, yet the install state leads to it: it was edited, so restore stops", id)
	case m.PriorState == nil:
		return undone{}, fmt.Errorf("the backup %s was taken by an older dotai, which did not keep the install state from before it, so restore cannot put that state back: "+
			"its files/ holds the copies to put back by hand", id)
	}
	for _, op := range m.Ops {
		if _, _, err := installRoot(r.home, op.Target); err != nil {
			return undone{}, fmt.Errorf("the backup %s: %w; restore never writes there", id, err)
		}
		if op.File == "" {
			continue
		}
		if err := r.verify(id, dir, op.File, op.Before.SHA256); err != nil {
			return undone{}, err
		}
	}
	u := undone{m: m, dir: dir}
	if m.PriorState.None {
		return u, nil
	}
	if err := r.verify(id, dir, m.PriorState.File, m.PriorState.SHA256); err != nil {
		return undone{}, err
	}
	data, err = r.d.ReadFile(dir, m.PriorState.File)
	if err != nil {
		return undone{}, err
	}
	prior, err := state.Decode(data)
	if err != nil {
		return undone{}, fmt.Errorf("%s: %w", filepath.Join(dir, m.PriorState.File), err)
	}
	if prior.Pending != nil {
		return undone{}, fmt.Errorf("the backup %s keeps a state with a journal: it was edited, so restore stops", id)
	}
	for _, f := range prior.Files {
		if _, _, err := installRoot(r.home, f.Target); err != nil {
			return undone{}, fmt.Errorf("the state the backup %s keeps owns a file restore never writes: %w", id, err)
		}
	}
	u.prior = &prior
	return u, nil
}

// verify checks that the file name in the backup directory dir holds hash.
func (r *restorer) verify(id, dir, name, hash string) error {
	data, err := r.d.ReadFile(dir, name)
	if err != nil {
		return &CorruptBackupError{ID: id, File: name, Err: err}
	}
	if h := sha256.Sum256(data); hex.EncodeToString(h[:]) != hash {
		return &CorruptBackupError{ID: id, File: name}
	}
	return nil
}

// plan fills res with what undoing chain from s does, and r.p.ops with the
// operations to carry it out. It follows each target from the disk
// through each op, newest first: an op expects its target to hold what it
// wrote (nothing for a remove) and leaves it with what it found (nothing
// for a create). A target that holds anything else, which is also not
// what it ends with, is a conflict. Each target whose end differs from
// the disk is written.
func (r *restorer) plan(res *RestoreResult, s state.State, chain []undone) error {
	harnessOf := map[string]string{}
	for _, st := range append([]*state.State{&s}, priors(chain)...) {
		for _, f := range st.Files {
			if _, ok := harnessOf[f.Target]; !ok {
				harnessOf[f.Target] = f.Harness
			}
		}
	}
	type check struct {
		target, backup string
		expect         *backup.Content
		seen           vfile
	}
	var checks []check
	var order []string
	disk, virtual := map[string]vfile{}, map[string]vfile{}
	dirs := map[string]error{}
	for _, u := range chain {
		res.Undone = append(res.Undone, UndoneInstall{
			ID: u.m.ID, Path: u.dir, CreatedAt: u.m.CreatedAt, Pack: u.m.Pack, Variant: u.m.Variant, Harnesses: u.m.Harnesses,
		})
		for _, op := range slices.Backward(u.m.Ops) {
			v, ok := virtual[op.Target]
			if !ok {
				cur, exists, err := inspectTarget(r.ctx, r.d, r.home, op.Target, dirs)
				if err != nil {
					return err
				}
				v = vfile{exists: exists, cur: cur}
				disk[op.Target] = v
				order = append(order, op.Target)
			}
			checks = append(checks, check{op.Target, u.m.ID, op.After, v})
			virtual[op.Target] = vfile{}
			if op.Before != nil {
				virtual[op.Target] = vfile{exists: true, cur: plan.Current{Hash: op.Before.SHA256, Exec: op.Before.Exec}, src: u.m.ID + "/" + op.File, from: u.m.ID}
			}
		}
	}

	conflicted := map[string]bool{}
	res.Conflicts = []RestoreConflict{}
	for _, c := range checks {
		end := virtual[c.target]
		if !c.seen.exists || conflicted[c.target] ||
			c.expect != nil && r.same(c.seen.cur, c.expect.SHA256, c.expect.Exec) || end.exists && r.same(c.seen.cur, end.cur.Hash, end.cur.Exec) {
			continue
		}
		conflicted[c.target] = true
		res.Conflicts = append(res.Conflicts, RestoreConflict{Target: c.target, Harness: harnessOf[c.target], Backup: c.backup})
	}

	res.Ops = []RestoreOp{}
	r.p.ops = nil
	for _, target := range order {
		cur, end := disk[target], virtual[target]
		if !cur.exists && !end.exists || cur.exists && end.exists && r.same(cur.cur, end.cur.Hash, end.cur.Exec) {
			continue
		}
		op := plan.Op{Target: target, Harness: harnessOf[target]}
		if cur.exists {
			op.Before, op.BeforeExec = cur.cur.Hash, cur.cur.Exec
		}
		switch {
		case !end.exists:
			op.Action = plan.Remove
		case !cur.exists:
			op.Action = plan.Create
		default:
			op.Action = plan.Replace
		}
		if end.exists {
			op.Source, op.After, op.Exec = end.src, end.cur.Hash, end.cur.Exec
		}
		r.p.ops = append(r.p.ops, op)
		res.Ops = append(res.Ops, RestoreOp{
			Action: op.Action, Harness: op.Harness, Target: target, From: end.from, Exec: op.Exec,
			Before: op.Before, BeforeExec: op.BeforeExec, After: op.After, Conflict: conflicted[target],
		})
	}
	res.Harnesses = []string{}
	for _, h := range slices.Concat(s.Harnesses, harnessesOf(chain)) {
		if !slices.Contains(res.Harnesses, h) && slices.ContainsFunc(res.Ops, func(op RestoreOp) bool { return op.Harness == h }) {
			res.Harnesses = append(res.Harnesses, h)
		}
	}
	if len(res.Harnesses) > 0 {
		res.Warnings = append(res.Warnings, fmt.Sprintf("close %s before restoring: a harness that is running may rewrite its files while they are put back", strings.Join(res.Harnesses, ", ")))
	}
	return nil
}

func priors(chain []undone) []*state.State {
	var out []*state.State
	for _, u := range chain {
		if u.prior != nil {
			out = append(out, u.prior)
		}
	}
	return out
}

func harnessesOf(chain []undone) []string {
	var out []string
	for _, u := range chain {
		out = append(out, u.m.Harnesses...)
		if u.prior != nil {
			out = append(out, u.prior.Harnesses...)
		}
	}
	return out
}

// mark records in the manifest of u that a restore undid it, with the
// restore's backup ref, nil when it wrote no file.
func (r *restorer) mark(u undone, ref *BackupRef) error {
	m := u.m
	m.Restored = &backup.Restored{At: r.d.Now().UTC()}
	if ref != nil {
		m.Restored.Backup = ref.ID
	}
	data, err := backup.Encode(m)
	if err != nil {
		return err
	}
	return r.d.WriteIn(u.dir, backup.ManifestName, data, 0o600)
}
