package app

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"path"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"time"

	"github.com/lartweib/dotai/internal/backup"
	"github.com/lartweib/dotai/internal/plan"
	"github.com/lartweib/dotai/internal/state"
)

// dotai restore --project undoes the applies of a project from its record
// and their backups: every file dotai created goes, every file it wrote
// over another gets that one back, every key it set gets back the value it
// had before the first apply, its block leaves the exclude file, its keys
// leave the list of projects, and .dotai/ goes, the pack's own directory
// included. What someone else put there since is kept; what dotai wrote
// that someone changed since is a conflict, overwritten only with consent.

// ProjectRestoreResult is the outcome of a restore of a project, or, given
// to the overwrite function of RestoreProject, its plan: the project and
// its record, each target it writes, in order, the exclude file it takes
// dotai's block out of, the targets that changed since dotai wrote them,
// the backup of what it overwrote or deleted, nil when it wrote nothing,
// and what went wrong after it succeeded. Lists are never nil.
type ProjectRestoreResult struct {
	Project   string             `json:"project"`
	Record    string             `json:"record"`
	Ops       []ProjectRestoreOp `json:"ops"`
	Exclude   ExcludeRestore     `json:"exclude"`
	Conflicts []RestoreConflict  `json:"conflicts"`
	Backup    *BackupRef         `json:"backup"`
	Warnings  []string           `json:"warnings"`
}

// ProjectRestoreOp is one target a restore of a project writes: a remove
// deletes what dotai created, a replace puts back the copy of what was
// there from the backup From, and a merge gives back the keys dotai set in
// a file merged by key, each Keys with what it does to the key. Conflict
// marks a target, or a key, that changed since dotai wrote it.
type ProjectRestoreOp struct {
	Action   plan.Action       `json:"action"`
	Harness  string            `json:"harness"`
	Kind     string            `json:"kind"`
	Target   string            `json:"target"`
	From     string            `json:"from"`
	Conflict bool              `json:"conflict"`
	Keys     []plan.ProjectKey `json:"keys"`
}

// ExcludeRestore is the exclude file a restore of a project takes dotai's
// block out of, "" when there is none, and whether it does.
type ExcludeRestore struct {
	File    string `json:"file"`
	Removed bool   `json:"removed"`
}

// RestoreProject undoes the applies of the project of the current
// directory cwd, found as Apply finds it, for the user whose home
// directory is home, so that the project, its exclude file and the keys
// dotai owns in a harness's home file are as before its first apply. A
// journal an apply or a restore left is rolled back first. Everything is
// planned before the first write; a target or a key that changed since
// dotai wrote it is a conflict: overwrite is asked once with the plan, and
// the restore writes nothing unless it answers true, a *ConflictError (a
// nil overwrite refuses). Under ~/.dotai/lock, with the record checked
// again, every file it overwrites or deletes, everything in .dotai/
// included, is copied into a backup of its own, and a journal is written
// into the record; a failure rolls back from that backup. The record goes
// last, then .dotai/.
func RestoreProject(ctx context.Context, cwd, home string, overwrite func(ProjectRestoreResult) (bool, error)) (ProjectRestoreResult, error) {
	return restoreProject(ctx, osInstallFS{}, osGit{}, cwd, home, overwrite)
}

func restoreProject(ctx context.Context, d executeFS, g gitPort, cwd, home string, overwrite func(ProjectRestoreResult) (bool, error)) (ProjectRestoreResult, error) {
	if !filepath.IsAbs(home) || !filepath.IsAbs(cwd) {
		return ProjectRestoreResult{}, fmt.Errorf("home directory %q and current directory %q must be absolute", home, cwd)
	}
	home, cwd = filepath.Clean(home), filepath.Clean(cwd)
	if err := settleProject(ctx, d, g, cwd, home); err != nil {
		return ProjectRestoreResult{}, err
	}
	project, _, _, err := findProject(ctx, g, cwd, home)
	if err != nil {
		return ProjectRestoreResult{}, err
	}
	record := filepath.Join(project, filepath.FromSlash(plan.RecordFile))
	r, err := readRecord(ctx, d, project)
	if err != nil {
		return ProjectRestoreResult{}, err
	}
	if r == nil {
		return ProjectRestoreResult{}, fmt.Errorf("nothing to restore: %s has no project record %s", project, record)
	}
	ru := runner{ctx: ctx, d: d, home: home, p: planned{ignoreExec: !d.ExecBit()}}
	res, _, err := ru.planProjectRestore(*r)
	if err != nil {
		return ProjectRestoreResult{}, err
	}
	if len(res.Conflicts) > 0 {
		ok := false
		if overwrite != nil {
			if ok, err = overwrite(res); err != nil {
				return ProjectRestoreResult{}, err
			}
		}
		if !ok {
			return ProjectRestoreResult{}, &ConflictError{Conflicts: res.Conflicts}
		}
	}
	err = ru.locked(&res.Warnings, func() error {
		again, err := readRecord(ctx, d, project)
		if err != nil {
			return err
		}
		if !reflect.DeepEqual(again, r) {
			return &StaleError{What: "the project record " + record}
		}
		check, writes, err := ru.planProjectRestore(*r)
		if err != nil {
			return err
		}
		if !reflect.DeepEqual(check.Ops, res.Ops) || !reflect.DeepEqual(check.Conflicts, res.Conflicts) {
			return &StaleError{What: "a file the restore writes"}
		}
		return ru.writeProjectRestore(&res, *r, writes)
	})
	if err != nil {
		return ProjectRestoreResult{}, err
	}
	return res, nil
}

// planProjectRestore plans the restore of the project of the record r, as
// RestoreProject describes, and gives the writes that carry it out.
func (a runner) planProjectRestore(r state.Project) (ProjectRestoreResult, []write, error) {
	project := r.Project
	res := ProjectRestoreResult{Project: project, Record: filepath.Join(project, filepath.FromSlash(plan.RecordFile)),
		Ops: []ProjectRestoreOp{}, Conflicts: []RestoreConflict{}, Warnings: []string{}, Exclude: ExcludeRestore{File: r.Exclude.File}}
	shared, err := readProjects(a.d, a.home)
	if err != nil {
		return ProjectRestoreResult{}, nil, err
	}
	var writes []write
	for _, f := range r.Files {
		op, w, err := a.restoreFile(r, f, shared)
		if err != nil {
			return ProjectRestoreResult{}, nil, err
		}
		if w == nil {
			continue
		}
		res.Ops = append(res.Ops, op)
		writes = append(writes, *w)
		for _, k := range op.Keys {
			if k.Action == plan.Conflict {
				res.Conflicts = append(res.Conflicts, RestoreConflict{Target: f.Target, Harness: f.Harness, Key: plan.OwnedKey{Path: k.Path}.Name()})
			}
		}
		if op.Conflict && len(op.Keys) == 0 {
			res.Conflicts = append(res.Conflicts, RestoreConflict{Target: f.Target, Harness: f.Harness, Expected: f.Hash, Current: w.op.Before.SHA256})
		}
	}
	if r.Exclude.File != "" {
		w, err := a.excludeRestore(r)
		if err != nil {
			return ProjectRestoreResult{}, nil, err
		}
		if w != nil {
			res.Exclude.Removed = true
			writes = append(writes, *w)
		}
	}
	files := map[string]bool{}
	for _, f := range r.Files {
		if _, ok := relIn(project, f.Target); !ok {
			files[f.Target] = true
		}
	}
	for _, file := range slices.Sorted(func(yield func(string) bool) {
		for f := range files {
			if !yield(f) {
				return
			}
		}
	}) {
		w, err := a.projectsWriteWith(project, file, nil)
		if err != nil {
			return ProjectRestoreResult{}, nil, err
		}
		if w != nil {
			writes = append(writes, *w)
		}
	}
	more, err := a.dotaiRemovals(project)
	if err != nil {
		return ProjectRestoreResult{}, nil, err
	}
	return res, append(writes, more...), nil
}

// restoreFile plans the restore of f, an owned target of the record r,
// with shared the keys other projects own in a home file, and gives the
// op and its write, nil when there is nothing to do.
func (a runner) restoreFile(r state.Project, f state.ProjectFile, shared state.Projects) (ProjectRestoreOp, *write, error) {
	project := r.Project
	op := ProjectRestoreOp{Harness: f.Harness, Kind: f.Kind, Target: f.Target, Keys: []plan.ProjectKey{}}
	rel, inProject := relIn(project, f.Target)
	base := project
	if !inProject {
		base = a.home
	}
	data, cur, exists, err := readTarget(a.ctx, a.d, base, f.Target)
	if err != nil {
		return op, nil, err
	}
	if f.Hash != "" {
		w := &write{op: state.JournalOp{Target: f.Target, Root: project, Name: rel}}
		if !inProject {
			return op, nil, fmt.Errorf("the project record owns %s whole, outside the project", f.Target)
		}
		if exists {
			w.op.Before = &state.Content{SHA256: cur.Hash, Exec: cur.Exec}
			op.Conflict = cur.Hash != f.Hash
		}
		switch {
		case f.Created && !exists:
			return op, nil, nil
		case f.Created:
			op.Action, w.remove = plan.Remove, true
		default:
			copied, exec, err := a.projectBackupCopy(project, f.Backup, f.Target)
			if err != nil {
				return op, nil, err
			}
			if exists && contentHash(copied) == cur.Hash {
				return op, nil, nil
			}
			op.Action, op.From, w.data, w.exec = plan.Replace, f.Backup, copied, exec
			w.op.After = &state.Content{SHA256: contentHash(copied), Exec: exec}
			missing, err := a.missingDirs(project, rel)
			if err != nil {
				return op, nil, err
			}
			w.op.Dirs = missing
		}
		return op, w, nil
	}
	if !exists {
		return op, nil, nil
	}
	var edits []plan.KeyEdit
	for _, k := range f.Keys {
		if !inProject && slices.ContainsFunc(shared.Projects, func(e state.ProjectEntry) bool {
			return e.Project != project && e.File == f.Target && slices.ContainsFunc(e.Keys, func(o []string) bool { return slices.Equal(o, k.Path) })
		}) {
			continue // another project's apply owns it too
		}
		have, present, err := plan.GetKey(data, k.Path)
		if err != nil {
			return op, nil, fmt.Errorf("%s: not a JSON object dotai can edit key by key: %w; fix it or move it away, then restore again", f.Target, err)
		}
		if !present && k.Before == nil {
			continue
		}
		pk := plan.ProjectKey{Path: k.Path, Action: plan.Remove}
		if k.Before != nil {
			pk.Action = plan.Merge
		}
		if h, err := plan.ValueHash(have); present && (err != nil || h != k.SHA256) {
			pk.Action, op.Conflict = plan.Conflict, true
		}
		op.Keys = append(op.Keys, pk)
		edits = append(edits, plan.KeyEdit{Path: k.Path, Value: k.Before})
	}
	parents := slices.Clone(f.Parents)
	slices.SortStableFunc(parents, func(x, y []string) int { return len(y) - len(x) })
	for _, p := range parents {
		edits = append(edits, plan.KeyEdit{Path: p, Prune: true})
	}
	out, err := plan.ApplyEdits(data, edits)
	if err != nil {
		return op, nil, fmt.Errorf("%s: %w", f.Target, err)
	}
	if len(op.Keys) == 0 && string(out) == string(data) {
		return op, nil, nil
	}
	op.Action = plan.Merge
	empty := false
	if names, err := plan.TopKeys(out); err == nil && len(names) == 0 && f.Created {
		empty = true
	}
	if !inProject {
		undo, err := undoEdits(data, edits)
		if err != nil {
			return op, nil, err
		}
		w := &write{op: state.JournalOp{Target: f.Target, Root: filepath.Dir(f.Target), Name: filepath.Base(f.Target), Undo: undo}, edits: edits, home: true}
		if empty {
			op.Action = plan.Remove
			w.edits = append(w.edits, plan.KeyEdit{Prune: true})
		}
		return op, w, nil
	}
	w := &write{op: state.JournalOp{Target: f.Target, Root: project, Name: rel, Before: &state.Content{SHA256: cur.Hash, Exec: cur.Exec}}, data: out, exec: cur.Exec}
	if empty {
		op.Action, w.remove = plan.Remove, true
	} else {
		w.op.After = &state.Content{SHA256: contentHash(out), Exec: cur.Exec}
	}
	return op, w, nil
}

// excludeRestore is the write that takes dotai's block out of the exclude
// file of the record r, nil when it is not there; an exclude file dotai
// created goes when nothing else is left in it.
func (a runner) excludeRestore(r state.Project) (*write, error) {
	file := r.Exclude.File
	common := filepath.Dir(filepath.Dir(file))
	name := filepath.ToSlash(filepath.Join(filepath.Base(filepath.Dir(file)), filepath.Base(file)))
	data, cur, exists, err := readTarget(a.ctx, a.d, common, file)
	if err != nil || !exists {
		return nil, err
	}
	out := plan.WithoutExcludeBlock(data, r.Project, r.Exclude.Newline)
	if string(out) == string(data) {
		return nil, nil
	}
	w := &write{op: state.JournalOp{Target: file, Root: common, Name: name, Before: &state.Content{SHA256: cur.Hash, Exec: cur.Exec}}, data: out, exec: cur.Exec}
	if r.Exclude.Created && len(out) == 0 {
		w.remove = true
	} else {
		w.op.After = &state.Content{SHA256: contentHash(out), Exec: cur.Exec}
	}
	return w, nil
}

// dotaiRemovals are the removals of every file in the project's .dotai/
// but the record, which goes last: the tools and the pack's own files,
// copied into the backup like any removal. Anything there that is not a
// regular file is an error, since a restore never follows a link.
func (a runner) dotaiRemovals(project string) ([]write, error) {
	dir := filepath.Join(project, ".dotai")
	if err := checkDir(a.d, dir); errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	} else if err != nil {
		return nil, err
	}
	names, err := a.d.Files(dir)
	if err != nil {
		return nil, err
	}
	slices.Sort(names)
	var out []write
	for _, n := range names {
		rel := path.Join(".dotai", n)
		if rel == plan.RecordFile {
			continue
		}
		target := filepath.Join(project, filepath.FromSlash(rel))
		cur, err := a.d.Hash(a.ctx, target)
		if err != nil {
			return nil, err
		}
		out = append(out, write{op: state.JournalOp{Target: target, Root: project, Name: rel, Before: &state.Content{SHA256: cur.Hash, Exec: cur.Exec}}, remove: true})
	}
	return out, nil
}

// projectBackupCopy is the copy of target in the backup id of project.
func (a runner) projectBackupCopy(project, id, target string) ([]byte, bool, error) {
	ap := applier{runner: a, p: projectPlanned{plan: ProjectPlan{Project: project}}}
	return ap.backupCopy(id, target)
}

// writeProjectRestore carries out writes under the lock, from the record
// r: a backup of what it overwrites or deletes, the journal, each write,
// then the record and .dotai/ go. A failure rolls back from the backup.
func (a runner) writeProjectRestore(res *ProjectRestoreResult, r state.Project, writes []write) error {
	project := r.Project
	t := a.d.Now().UTC()
	root := projectBackups(a.home, project)
	if err := ensureDir(a.d, a.home, root, 0o700); err != nil {
		return err
	}
	var id, dir string
	var err error
	for range 1000 {
		id = backup.ID(t, backup.OperationRestore)
		dir = filepath.Join(root, id)
		if err = a.d.Mkdir(dir, 0o700); !errors.Is(err, fs.ErrExist) {
			break
		}
		t = t.Add(time.Millisecond)
	}
	if err != nil {
		return fmt.Errorf("the backup failed, nothing was written: %w", err)
	}
	if err := a.takeBackup(id, dir, project, t, writes, true); err != nil {
		return fmt.Errorf("the backup failed, nothing was written: %w", errors.Join(err, a.d.RemoveAll(dir)))
	}
	j := r
	j.Pending = &state.ApplyJournal{Backup: id, Prior: priorCopy, Ops: []state.JournalOp{}}
	for _, w := range writes {
		j.Pending.Ops = append(j.Pending.Ops, w.op)
	}
	if err := a.writeRecord(j); err != nil {
		return fmt.Errorf("the restore journal could not be written, nothing was written: %w", errors.Join(err, a.d.RemoveAll(dir)))
	}
	var done []write
	for _, w := range writes {
		if err = a.ctx.Err(); err != nil {
			break
		}
		var lost bool
		if lost, err = a.carry(w); err != nil {
			break
		}
		done = append(done, w)
		if lost {
			res.Warnings = append(res.Warnings, fmt.Sprintf("%s was rewritten while dotai took its MCP servers out of it, twice: close Claude Code, then check its projects entry for %s", w.op.Target, project))
		}
	}
	if err == nil {
		err = a.d.RemoveIn(project, plan.RecordFile)
	}
	if err != nil {
		rb := a.undoWrites(dir, done)
		if rb == nil {
			rb = a.putRecordBack(project, dir, priorCopy)
		}
		return &RestoreError{Cause: err, Rollback: rb, Backup: dir}
	}
	res.Backup = &BackupRef{ID: id, Path: dir}
	if err := a.d.RemoveAllIn(project, ".dotai"); err != nil {
		res.Warnings = append(res.Warnings, fmt.Sprintf("%s could not be removed: %v", filepath.Join(project, ".dotai"), err))
	}
	dirs := slices.Clone(r.Dirs)
	slices.SortFunc(dirs, func(x, y string) int { return strings.Count(y, "/") - strings.Count(x, "/") })
	for _, d := range dirs {
		_ = a.d.RemoveIn(project, d) // only an empty one goes
	}
	return nil
}
