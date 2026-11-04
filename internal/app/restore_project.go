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
// under the home directory and their backups: every file dotai created
// goes, every file it wrote over another gets that one back, every key it
// set gets back the value it had before the first apply, its block leaves
// the exclude file, its entry leaves the list of projects, and .dotai/
// goes, the marker and the pack's own directory included. What someone
// else put there since is kept; what dotai wrote that someone changed
// since is a conflict, overwritten only with consent.

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
// planned before the first write, from the record under the home
// directory, which is refused when it names anything an apply of the
// project never writes; a target or a key that changed since dotai wrote
// it is a conflict: overwrite is asked once with the plan, and the restore
// writes nothing unless it answers true, a *ConflictError (a nil overwrite
// refuses). Under ~/.dotai/lock, with the record checked again, every file
// it overwrites or deletes, everything in .dotai/ included, is copied into
// a backup of its own, and a journal is written into the record; a failure
// rolls back from that backup. The record goes last, then .dotai/.
func RestoreProject(ctx context.Context, cwd, home string, opts ProjectRestoreOptions, overwrite func(ProjectRestoreResult) (bool, error)) (ProjectRestoreResult, error) {
	return restoreProject(ctx, osInstallFS{}, osGit{}, cwd, home, opts, overwrite)
}

// ProjectRestoreOptions are the choices of a restore of a project:
// Confirm, when it is not nil, is asked first with the plan, as
// PreviewProjectRestore gives it, before overwrite, the lock or any write:
// an error stops the restore, which writes nothing more.
type ProjectRestoreOptions struct {
	Confirm func(ProjectRestoreResult) error
}

func restoreProject(ctx context.Context, d executeFS, g gitPort, cwd, home string, opts ProjectRestoreOptions, overwrite func(ProjectRestoreResult) (bool, error)) (ProjectRestoreResult, error) {
	cwd, home, err := projectRestoreArgs(cwd, home)
	if err != nil {
		return ProjectRestoreResult{}, err
	}
	if err := settleProject(ctx, d, g, cwd, home); err != nil {
		return ProjectRestoreResult{}, err
	}
	p, err := planRestoreOfProject(ctx, d, g, cwd, home)
	if err != nil {
		return ProjectRestoreResult{}, err
	}
	ru, sc, r, res := p.ru, p.sc, p.record, p.res
	if opts.Confirm != nil {
		if err := opts.Confirm(res); err != nil {
			return ProjectRestoreResult{}, err
		}
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
		again, err := readRecord(ctx, d, sc)
		if err != nil {
			return err
		}
		if !reflect.DeepEqual(again, r) {
			return &StaleError{What: "the project record " + res.Record}
		}
		check, writes, err := ru.planProjectRestore(*r, sc)
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

// projectRestoreArgs checks the current and home directories of a restore
// of a project, as RestoreProject and PreviewProjectRestore both take
// them, and returns them cleaned.
func projectRestoreArgs(cwd, home string) (string, string, error) {
	if !filepath.IsAbs(home) || !filepath.IsAbs(cwd) {
		return "", "", fmt.Errorf("home directory %q and current directory %q must be absolute", home, cwd)
	}
	return filepath.Clean(cwd), filepath.Clean(home), nil
}

// projectRestorePlan holds a restore of a project planned from its
// record: the runner and the scope that planned it, the record, and the
// plan.
type projectRestorePlan struct {
	ru     runner
	sc     recordScope
	record *state.Project
	res    ProjectRestoreResult
}

// planRestoreOfProject plans the restore of the project of cwd from its
// record, as both RestoreProject and PreviewProjectRestore plan it, so
// that a preview shows what the restore does. It writes nothing.
func planRestoreOfProject(ctx context.Context, d executeFS, g gitPort, cwd, home string) (projectRestorePlan, error) {
	project, mcpKey, repo, err := findProject(ctx, g, cwd, home)
	if err != nil {
		return projectRestorePlan{}, err
	}
	sc := scopeOf(home, project, mcpKey, repo)
	r, err := readRecord(ctx, d, sc)
	if err != nil {
		return projectRestorePlan{}, err
	}
	if r == nil {
		return projectRestorePlan{}, fmt.Errorf("nothing to restore: %s has no project record %s", project, recordPath(home, project))
	}
	ru := runner{ctx: ctx, d: d, home: home, p: planned{ignoreExec: !d.ExecBit()}}
	res, _, err := ru.planProjectRestore(*r, sc)
	if err != nil {
		return projectRestorePlan{}, err
	}
	return projectRestorePlan{ru: ru, sc: sc, record: r, res: res}, nil
}

// planProjectRestore plans the restore of the project of the record r, as
// RestoreProject describes, and gives the writes that carry it out.
func (a runner) planProjectRestore(r state.Project, sc recordScope) (ProjectRestoreResult, []write, error) {
	project := r.Project
	res := ProjectRestoreResult{Project: project, Record: recordPath(a.home, project),
		Ops: []ProjectRestoreOp{}, Conflicts: []RestoreConflict{}, Warnings: []string{}, Exclude: ExcludeRestore{File: r.Exclude.File}}
	shared, err := readProjects(a.d, a.home)
	if err != nil {
		return ProjectRestoreResult{}, nil, err
	}
	var writes []write
	for _, f := range r.Files {
		op, w, err := a.restoreFile(r, f, sc, shared)
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
	if file := r.Exclude.File; file != "" && !r.Exclude.Pending {
		w, _, err := a.excludeWrite(file, project, nil, r.Exclude.Entries)
		if err != nil {
			return ProjectRestoreResult{}, nil, err
		}
		if w != nil {
			if r.Exclude.Created && len(w.data) == 0 {
				w.remove, w.op.After = true, nil
			}
			res.Exclude.Removed = true
			writes = append(writes, *w)
		}
	}
	if shared.Entry(project) != nil {
		w, err := a.projectsWrite(state.ProjectEntry{Project: project})
		if err != nil {
			return ProjectRestoreResult{}, nil, err
		}
		if w != nil {
			writes = append(writes, *w)
		}
	}
	planned := map[string]bool{}
	for _, w := range writes {
		planned[w.op.Target] = true
	}
	more, err := a.dotaiRemovals(project, planned)
	if err != nil {
		return ProjectRestoreResult{}, nil, err
	}
	return res, append(writes, more...), nil
}

// restoreFile plans the restore of f, an owned target of the record r of
// the project of sc, with shared the list of projects, and gives the op
// and its write, nil when there is nothing to do.
func (a runner) restoreFile(r state.Project, f state.ProjectFile, sc recordScope, shared state.Projects) (ProjectRestoreOp, *write, error) {
	project := r.Project
	op := ProjectRestoreOp{Harness: f.Harness, Kind: f.Kind, Target: f.Target, Keys: []plan.ProjectKey{}}
	root, name, _ := sc.rootOf(f.Target) // checked when the record was read
	inProject := root == project
	base := project
	if !inProject {
		base = a.home
	}
	data, cur, exists, err := readTarget(a.ctx, a.d, base, f.Target)
	if err != nil {
		return op, nil, err
	}
	if f.Hash != "" {
		w := &write{op: state.JournalOp{Target: f.Target, Root: root, Name: name}}
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
			copied, exec, err := a.backupCopy(project, f.Backup, f.Target)
			if err != nil {
				return op, nil, err
			}
			if exists && contentHash(copied) == cur.Hash {
				return op, nil, nil
			}
			op.Action, op.From, w.data, w.exec = plan.Replace, f.Backup, copied, exec
			w.op.After = &state.Content{SHA256: contentHash(copied), Exec: exec}
			if w.op.Dirs, err = a.missingDirs(root, name); err != nil {
				return op, nil, err
			}
		}
		return op, w, nil
	}
	if !exists {
		return op, nil, nil
	}
	parents, created := f.Parents, f.Created
	if e := shared.Entry(project); !inProject && e != nil && e.File == f.Target {
		parents, created = append(slices.Clone(parents), e.Parents...), created || e.Created // owned jointly, handed over
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
	prunes, _ := plan.PruneEdits(parents, nil)
	edits = append(edits, prunes...)
	out, err := plan.ApplyEdits(data, edits)
	if err != nil {
		return op, nil, fmt.Errorf("%s: %w", f.Target, err)
	}
	names, _ := plan.TopKeys(out)
	empty := created && len(names) == 0
	if len(op.Keys) == 0 && string(out) == string(data) && !empty {
		return op, nil, nil
	}
	op.Action = plan.Merge
	if empty {
		op.Action = plan.Remove
	}
	if !inProject {
		undo, err := undoEdits(data, edits)
		if err != nil {
			return op, nil, err
		}
		return op, &write{op: state.JournalOp{Target: f.Target, Root: root, Name: name, Undo: undo, RemoveIfEmpty: created}, edits: edits, home: true}, nil
	}
	w := &write{op: state.JournalOp{Target: f.Target, Root: root, Name: name, Before: &state.Content{SHA256: cur.Hash, Exec: cur.Exec}}, data: out, exec: cur.Exec}
	if empty {
		w.remove = true
	} else {
		w.op.After = &state.Content{SHA256: contentHash(out), Exec: cur.Exec}
	}
	return op, w, nil
}

// dotaiRemovals are the removals of every file in the project's .dotai/
// that is not planned already: the marker, the tools and the pack's own
// files, copied into the backup like any removal. Anything there that is
// not a regular file is an error, since a restore never follows a link.
func (a runner) dotaiRemovals(project string, planned map[string]bool) ([]write, error) {
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
		target := filepath.Join(project, filepath.FromSlash(rel))
		if planned[target] {
			continue
		}
		cur, err := a.d.Hash(a.ctx, target)
		if err != nil {
			return nil, err
		}
		out = append(out, write{op: state.JournalOp{Target: target, Root: project, Name: rel, Before: &state.Content{SHA256: cur.Hash, Exec: cur.Exec}}, remove: true})
	}
	return out, nil
}

// writeProjectRestore carries out writes under the lock, from the record
// r: a backup of what it overwrites or deletes and of the record, the
// journal, each write, then the record and .dotai/ go. A failure rolls
// back from the backup.
func (a runner) writeProjectRestore(res *ProjectRestoreResult, r state.Project, writes []write) error {
	project := r.Project
	ref, lost, err := a.journaled(journaledWrite{project: project, operation: backup.OperationRestore, what: "restore", base: r, prior: true, writes: writes},
		func(string, time.Time) error { return a.d.RemoveIn(projectHome(a.home, project), recordName) })
	for _, target := range lost {
		res.Warnings = append(res.Warnings, fmt.Sprintf("%s was rewritten while dotai took its MCP servers out of it, twice: close Claude Code, then check its projects entry for %s", target, project))
	}
	if js, ok := errors.AsType[*journalStop](err); ok {
		return &RestoreError{Cause: js.cause, Rollback: js.rollback, Backup: js.backup}
	}
	if err != nil {
		return err
	}
	res.Backup = &ref
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
