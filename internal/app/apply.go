package app

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"reflect"
	"slices"
	"strings"

	"github.com/lartweib/dotai/internal/backup"
	"github.com/lartweib/dotai/internal/plan"
	"github.com/lartweib/dotai/internal/state"
)

// A project apply (decision 0041) carries out the plan of PlanProject as
// an install carries out its own: under ~/.dotai/lock, after a backup of
// everything it overwrites or deletes and a journal in the project's
// record under the home directory, with each target checked again before
// it is written and everything rolled back when anything fails
// (project_journal.go). It never writes a file git tracks, one of the files
// that are the user's or the team's, or .dotai/packs/<pack>/.

// Warning codes of a project apply besides the plan's.
const (
	// CodeMCPKeysLost: the MCP servers dotai wrote to a harness's home file
	// were lost twice to a harness that rewrote the file meanwhile.
	CodeMCPKeysLost = "mcp-keys-lost"
	// CodeCleanupFailed: something failed after the apply itself had
	// succeeded, such as removing the temporary staging or the lock.
	CodeCleanupFailed = "cleanup-failed"
)

// ApplyResult is the outcome of a project apply: its plan, with the
// warnings of the apply after those of the plan, and the backup taken
// before the first write, nil when nothing was written.
type ApplyResult struct {
	ProjectPlan
	Backup *BackupRef `json:"backup"`
}

// ApplyRefusedError is a project apply whose plan has refusals, which no
// consent overrides; it wrote nothing.
type ApplyRefusedError struct {
	Plan ProjectPlan
}

func (e *ApplyRefusedError) Error() string {
	msgs := make([]string, len(e.Plan.Refusals))
	for i, r := range e.Plan.Refusals {
		msgs[i] = r.Message
	}
	return strings.Join(msgs, "; ") + "; the apply wrote nothing"
}

// ApplyConsentError is a project apply that would overwrite or delete
// what dotai does not own, or what was changed since dotai wrote it, and
// was not allowed to: Files files and Keys keys of files merged by key.
type ApplyConsentError struct {
	Plan        ProjectPlan
	Files, Keys int
}

func (e *ApplyConsentError) Error() string {
	return fmt.Sprintf("the apply would overwrite or delete what dotai does not own or what changed since dotai wrote it: %s and %s; "+
		"the apply wrote nothing: run it again with --yes to allow them, after reading the plan; the backup keeps what they hold",
		plural(e.Files, "file", "files"), plural(e.Keys, "key", "keys"))
}

// ApplyError is a project apply that stopped after it started writing:
// the Cause, the outcome of the rollback, nil when everything was put
// back, and the Backup directory, which is kept either way.
type ApplyError struct {
	Cause    error
	Rollback error
	Backup   string
}

func (e *ApplyError) Error() string {
	if e.Rollback == nil {
		return fmt.Sprintf("the apply stopped: %v; everything it wrote was rolled back from the backup %s, which is kept", e.Cause, e.Backup)
	}
	return fmt.Sprintf("the apply stopped: %v; the rollback failed: %v; the backup %s holds what was there before, "+
		"and the project record keeps the journal: the next apply or restore --project rolls it back", e.Cause, e.Rollback, e.Backup)
}

func (e *ApplyError) Unwrap() []error {
	if e.Rollback == nil {
		return []error{e.Cause}
	}
	return []error{e.Cause, e.Rollback}
}

// ApplyConsent is asked, with the plan, whether a project apply may
// overwrite or delete its conflicts.
type ApplyConsent = func(ProjectPlan) (bool, error)

// Apply applies the profiles of opts, from the pack in dir, to the project
// of the current directory cwd, for the user whose home directory is home,
// as PlanProject plans it. A journal an apply or a restore of the project
// left is rolled back first. A plan with diagnostics writes nothing; one
// with refusals is an *ApplyRefusedError, and one with conflicts, files or
// keys it would overwrite that dotai does not own or that changed since it
// wrote them, needs consent, asked once with the plan before the lock, or
// it is an *ApplyConsentError; a nil consent refuses. Then, under
// ~/.dotai/lock, with the record checked again, it backs up every target
// it overwrites or deletes, writes the journal into the record and carries
// out each op: a file written whole, a file merged by key, the keys of
// Claude Code's MCP servers in the .claude.json it reads, which it writes
// as the file is then and checks after writing, the marker, the block of
// the exclude file, the list of projects a replace install reads, and the
// record last. A failure rolls everything back. An apply whose plan writes
// nothing still puts back its exclude block and its entry in the list of
// projects when they are missing. Warnings that need telling are added to
// the plan's.
func Apply(ctx context.Context, cacheDir, dir, cwd, home string, opts ApplyOptions, progress Progress, consent ApplyConsent) (ApplyResult, error) {
	return Ports{}.Apply(ctx, cacheDir, dir, cwd, home, opts, progress, consent)
}

func applyProject(ctx context.Context, d executeFS, h host, g gitPort, fetch fetchFunc, run runFunc, cacheDir, dir, cwd, home string, opts ApplyOptions, progress Progress, consent ApplyConsent) (ApplyResult, error) {
	if !filepath.IsAbs(home) || !filepath.IsAbs(cwd) {
		return ApplyResult{}, fmt.Errorf("home directory %q and current directory %q must be absolute", home, cwd)
	}
	home, cwd = filepath.Clean(home), filepath.Clean(cwd)
	if err := settleProject(ctx, d, g, cwd, home); err != nil {
		return ApplyResult{}, err
	}
	var res ApplyResult
	err := withProjectPlan(ctx, d, h, g, fetch, run, cacheDir, dir, cwd, home, opts, progress, func(p projectPlanned) error {
		res = ApplyResult{ProjectPlan: p.plan}
		if len(p.plan.Diagnostics) > 0 {
			return nil
		}
		if len(p.plan.Refusals) > 0 {
			return &ApplyRefusedError{Plan: p.plan}
		}
		if files, keys := conflicts(p.plan); files+keys > 0 {
			ok := false
			if consent != nil {
				var err error
				if ok, err = consent(p.plan); err != nil {
					return err
				}
			}
			if !ok {
				return &ApplyConsentError{Plan: p.plan, Files: files, Keys: keys}
			}
		}
		a := applier{runner: runner{ctx: ctx, d: d, home: home, p: planned{ignoreExec: !d.ExecBit()}}, p: p}
		return a.run(&res)
	})
	if se, ok := errors.AsType[*stagingError](err); ok && se == err { // the apply itself succeeded
		res.Warnings = append(res.Warnings, plan.Warning{Code: CodeCleanupFailed, Path: se.dir, Message: se.Error()})
		err = nil
	}
	if err != nil {
		return ApplyResult{}, err
	}
	return res, nil
}

// conflicts counts the files and the keys of p that the apply overwrites
// or deletes although dotai does not own them as they are.
func conflicts(p ProjectPlan) (files, keys int) {
	for _, op := range p.Ops {
		if op.Action == plan.Conflict {
			files++
		}
		for _, k := range op.Keys {
			if k.Action == plan.Conflict {
				keys++
			}
		}
	}
	return files, keys
}

// applier carries out one project plan; its runner holds the lock and
// writes the targets, with the home directory as its home.
type applier struct {
	runner
	p projectPlanned
}

// run builds the next record and, when anything changes, the exclude
// block and the list of projects included, writes under the lock.
func (a applier) run(res *ApplyResult) error {
	next := a.nextRecord()
	var ex *write
	if next.Exclude.File != "" {
		var err error
		if ex, _, err = a.excludeWrite(next.Exclude.File, next.Project, next.Exclude.Entries, a.priorBlock()); err != nil {
			return err
		}
	}
	pj, err := a.projectsWrite(a.entry(next))
	if err != nil {
		return err
	}
	changes := slices.ContainsFunc(a.p.plan.Ops, func(op plan.ProjectOp) bool { return op.Action != plan.Unchanged })
	if a.p.prev != nil && !changes && ex == nil && pj == nil && sameRecord(*a.p.prev, next) {
		return nil
	}
	warnings := []string{}
	err = a.locked(&warnings, func() error { return a.write(res, next) })
	for _, w := range warnings {
		res.Warnings = append(res.Warnings, plan.Warning{Code: CodeCleanupFailed, Message: w})
	}
	return err
}

// priorBlock is the entries of the project's block in the exclude file as
// the record says it wrote them, nil for none.
func (a applier) priorBlock() []string {
	if p := a.p.prev; p != nil && p.Exclude.File != "" && !p.Exclude.Pending {
		return p.Exclude.Entries
	}
	return nil
}

// sameRecord reports whether a and b record the same apply, apart from
// when it ran and its backups.
func sameRecord(a, b state.Project) bool {
	b.AppliedAt, b.Backups, b.FirstApply = a.AppliedAt, a.Backups, a.FirstApply
	return reflect.DeepEqual(a, b)
}

// nextRecord is the record once the plan is carried out, without the
// backup of this apply.
func (a applier) nextRecord() state.Project {
	p := a.p
	r := state.Project{
		Schema: state.ProjectSchema, Pack: p.pack, Variant: p.plan.Variant, Profiles: slices.Clone(p.plan.Profiles), Harnesses: slices.Clone(p.plan.Harnesses),
		Project: p.plan.Project, ID: p.scope.id, MCPKey: p.scope.mcpKey, Backups: []string{}, Files: []state.ProjectFile{}, Dirs: []string{},
		Exclude: state.ProjectExclude{File: p.plan.Exclude.File, Pending: p.plan.Exclude.Pending, Entries: slices.Clone(p.plan.Exclude.Entries)},
	}
	if p.prev != nil {
		r.FirstApply, r.Backups, r.Dirs = p.prev.FirstApply, slices.Clone(p.prev.Backups), slices.Clone(p.prev.Dirs)
		if p.prev.Exclude.File == r.Exclude.File {
			r.Exclude.Created = p.prev.Exclude.Created
		}
	}
	for _, op := range p.plan.Ops {
		if op.Owns != nil && op.Action != plan.Remove && !op.Removal {
			r.Files = append(r.Files, state.ProjectFile{Target: op.Target, ProjectOwned: *op.Owns})
		}
	}
	for target, o := range p.kept {
		r.Files = append(r.Files, state.ProjectFile{Target: target, ProjectOwned: o})
	}
	slices.SortFunc(r.Files, func(x, y state.ProjectFile) int { return strings.Compare(x.Target, y.Target) })
	return r
}

// entry is the entry of the project in the list of projects once r is its
// record: the keys r owns in Claude Code's home file, with the objects and
// the file dotai created there, its own and those another project's entry
// handed over.
func (a applier) entry(r state.Project) state.ProjectEntry {
	file := a.p.homeMCP
	e := state.ProjectEntry{Project: r.Project, File: file}
	if prev := a.p.shared.Entry(r.Project); prev != nil && prev.File == file {
		e.Parents, e.Created = slices.Clone(prev.Parents), prev.Created
	}
	for _, f := range r.Files {
		if f.Target != file {
			continue
		}
		for _, k := range f.Keys {
			e.Keys = append(e.Keys, k.Path)
		}
		for _, q := range f.Parents {
			if !slices.ContainsFunc(e.Parents, func(p []string) bool { return slices.Equal(p, q) }) {
				e.Parents = append(e.Parents, q)
			}
		}
		e.Created = e.Created || f.Created
	}
	return e
}

// write carries out the plan under the lock towards the record next.
func (a applier) write(res *ApplyResult, next state.Project) error {
	p := a.p
	project := p.plan.Project
	cur, err := readRecord(a.ctx, a.d, p.scope)
	if err != nil {
		return err
	}
	if !reflect.DeepEqual(cur, p.prev) {
		return &ChangedError{Path: p.plan.Record}
	}
	root := projectBackups(a.home, project)
	if err := ensureDir(a.d, a.home, root, 0o700); err != nil {
		return fmt.Errorf("the backup failed, nothing was written: %w", err)
	}
	id, dir, now, err := newBackupDir(a.d, root, a.d.Now().UTC(), backup.OperationApply)
	if err != nil {
		return fmt.Errorf("the backup failed, nothing was written: %w", err)
	}
	writes, err := a.writes(&next)
	if err == nil {
		err = a.takeBackup(id, dir, project, now, writes, p.prev != nil)
	}
	if err != nil {
		return fmt.Errorf("the backup failed, nothing was written: %w", errors.Join(err, a.d.RemoveAll(dir)))
	}
	base := state.Project{Schema: state.ProjectSchema, Pack: next.Pack, Project: project, ID: next.ID, MCPKey: next.MCPKey, Profiles: []string{}, Harnesses: []string{},
		Backups: []string{}, Files: []state.ProjectFile{}, Dirs: []string{}, Exclude: state.ProjectExclude{Entries: []string{}}}
	if p.prev != nil {
		base = *p.prev
	}
	j := &state.ApplyJournal{Backup: id, Ops: []state.JournalOp{}}
	if p.prev != nil {
		j.Prior = priorCopy
	}
	for _, w := range writes {
		j.Ops = append(j.Ops, w.op)
	}
	base.Pending = j
	if err := a.writeRecord(base); err != nil {
		return fmt.Errorf("the apply journal could not be written, nothing was written: %w", errors.Join(err, a.d.RemoveAll(dir)))
	}
	next.Backups = append(next.Backups, id)
	// A file dotai writes over one it does not own is given back from this
	// backup by a restore.
	for i, f := range next.Files {
		if f.Hash != "" && !f.Created && f.Backup == "" && slices.ContainsFunc(p.plan.Ops, func(op plan.ProjectOp) bool { return op.Target == f.Target && op.Action == plan.Conflict }) {
			next.Files[i].Backup = id
		}
	}
	if next.FirstApply.IsZero() {
		next.FirstApply = now
	}
	next.AppliedAt = now
	done, lost, err := a.executeWrites(writes)
	for _, target := range lost {
		res.Warnings = append(res.Warnings, plan.Warning{Code: CodeMCPKeysLost, Harness: "claudecode", Path: target,
			Message: fmt.Sprintf("%s was rewritten while dotai wrote its MCP servers there, twice, and lost them: close Claude Code, then apply again", target)})
	}
	if err == nil {
		next.Dirs = a.pruneDirs(next.Dirs)
		err = a.writeRecord(next)
	}
	if err != nil {
		rb := a.undoWrites(project, dir, done)
		if rb == nil {
			rb = a.putRecordBack(project, dir, j.Prior)
		}
		return &ApplyError{Cause: err, Rollback: rb, Backup: dir}
	}
	res.Backup = &BackupRef{ID: id, Path: dir}
	return nil
}

// writes are the targets the apply writes, in order: the project's files,
// the marker and the home file of Claude Code's MCP servers in target
// order, then the exclude file and the list of projects, each read now,
// under the lock. next gets the directories the writes create in the
// project and whether the exclude file is created.
func (a applier) writes(next *state.Project) ([]write, error) {
	p := a.p
	var out []write
	dirs := map[string]bool{} // the directories planned to be created
	for _, op := range p.plan.Ops {
		if op.Action == plan.Unchanged {
			continue
		}
		if err := state.CheckProjectTarget(p.plan.Project, op.Target); err != nil {
			return nil, err
		}
		if p.in.Tracked[op.Target] {
			return nil, fmt.Errorf("%s is tracked by git, and an apply never writes it", op.Target)
		}
		root, name, ok := p.scope.rootOf(op.Target)
		if !ok {
			return nil, fmt.Errorf("%s is not a target of an apply of %s", op.Target, p.plan.Project)
		}
		if root != p.plan.Project {
			// The home file of Claude Code's MCP servers, which Claude Code
			// rewrites while it runs.
			if !samePath(root, a.home) {
				if err := ensureDir(a.d, a.home, root, 0o755); err != nil {
					return nil, err
				}
			}
			undo, err := undoEdits(p.in.Content[op.Target], op.Edits)
			if err != nil {
				return nil, fmt.Errorf("%s: %w", op.Target, err)
			}
			out = append(out, write{op: state.JournalOp{Target: op.Target, Root: root, Name: name, Undo: undo, RemoveIfEmpty: op.Before == ""}, edits: op.Edits, home: true})
			continue
		}
		w := write{op: state.JournalOp{Target: op.Target, Root: root, Name: name}, data: op.Data, exec: op.Owns != nil && op.Owns.Exec}
		if op.Before != "" {
			w.op.Before = &state.Content{SHA256: op.Before, Exec: p.in.Disk[op.Target].Exec}
		}
		switch {
		case op.Action == plan.Remove, op.Removal:
			w.remove = true
		case op.PutBack != "":
			data, exec, err := a.backupCopy(p.plan.Project, op.PutBack, op.Target)
			if err != nil {
				return nil, err
			}
			w.data, w.exec = data, exec
		}
		if !w.remove {
			w.op.After = &state.Content{SHA256: contentHash(w.data), Exec: w.exec}
			missing, err := a.missingDirs(root, name)
			if err != nil {
				return nil, err
			}
			for _, d := range missing {
				if !dirs[d] {
					dirs[d], w.op.Dirs = true, append(w.op.Dirs, d)
					if !slices.Contains(next.Dirs, d) && d != ".dotai" && !strings.HasPrefix(d, ".dotai/") {
						next.Dirs = append(next.Dirs, d)
					}
				}
			}
		}
		out = append(out, w)
	}
	slices.SortStableFunc(out, func(x, y write) int { return strings.Compare(x.op.Target, y.op.Target) })
	if file := next.Exclude.File; file != "" {
		ex, created, err := a.excludeWrite(file, next.Project, next.Exclude.Entries, a.priorBlock())
		if err != nil {
			return nil, err
		}
		if ex != nil {
			next.Exclude.Created = next.Exclude.Created || created
			out = append(out, *ex)
		}
	}
	pj, err := a.projectsWrite(a.entry(*next))
	if err != nil {
		return nil, err
	}
	if pj != nil {
		out = append(out, *pj)
	}
	return out, nil
}

// pruneDirs removes each of dirs, slash paths inside the project that
// dotai created, that is empty now, deepest first, and gives those left.
func (a applier) pruneDirs(dirs []string) []string {
	sorted := slices.Clone(dirs)
	slices.SortFunc(sorted, func(x, y string) int { return strings.Count(y, "/") - strings.Count(x, "/") })
	var left []string
	for _, d := range sorted {
		target := filepath.Join(a.p.plan.Project, filepath.FromSlash(d))
		if _, err := a.d.Lstat(target); errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if names, err := a.d.List(target); err == nil && len(names) == 0 && a.d.RemoveIn(a.p.plan.Project, d) == nil {
			continue
		}
		left = append(left, d)
	}
	slices.Sort(left)
	if left == nil {
		left = []string{}
	}
	return left
}
