package app

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
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

// A project apply (decision 0041) carries out the plan of PlanProject as
// an install carries out its own: under ~/.dotai/lock, after a backup of
// everything it overwrites or deletes and a journal in the project record,
// with each target checked again before it is written and everything
// rolled back when anything fails. It never writes a file git tracks, one
// of the files that are the user's or the team's, or .dotai/packs/<pack>/.

// projectsFile is the list of projects whose keys dotai owns in a
// harness's home file, under ~/.dotai, which a replace install reads; and
// projectsDir the directory of each project's backups there.
const (
	projectsFile = "projects.json"
	projectsDir  = "projects"
)

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
// ~/.dotai/lock, with the project record checked again, it backs up every
// target it overwrites or deletes, writes the journal into the record and
// carries out each op: a file written whole, a file merged by key, the
// keys of Claude Code's MCP servers in the .claude.json it reads, which it
// writes as the file is then and checks after writing, the block of the
// exclude file, the list of projects a replace install reads, and the
// record last. A failure rolls everything back. Warnings that need telling
// are added to the plan's.
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
		packPath, err := filepath.Abs(p.packDir)
		if err != nil {
			return err
		}
		a := applier{runner: runner{ctx: ctx, d: d, home: home, p: planned{ignoreExec: !d.ExecBit()}}, p: p,
			pack: state.Pack{Name: p.plan.Pack.Name, Version: p.plan.Pack.Version, Path: packPath}}
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
	p    projectPlanned
	pack state.Pack
}

// write is a target the apply writes, as its journal records it, with
// how to write it: data, or with remove set nothing; or, for a harness's
// home file (home), edits made on it as it is then.
type write struct {
	op     state.JournalOp
	data   []byte
	exec   bool
	remove bool
	edits  []plan.KeyEdit
	home   bool
}

// run builds the next record and, when anything changes, writes under the
// lock.
func (a applier) run(res *ApplyResult) error {
	next := a.nextRecord()
	if a.p.prev != nil && !a.changes() && sameRecord(*a.p.prev, next) {
		return nil
	}
	warnings := []string{}
	err := a.locked(&warnings, func() error { return a.write(res, next) })
	for _, w := range warnings {
		res.Warnings = append(res.Warnings, plan.Warning{Code: CodeCleanupFailed, Message: w})
	}
	return err
}

// changes reports whether the plan writes a target.
func (a applier) changes() bool {
	return slices.ContainsFunc(a.p.plan.Ops, func(op plan.ProjectOp) bool { return op.Action != plan.Unchanged && op.Kind != plan.KindRecord })
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
		Schema: state.ProjectSchema, Pack: a.pack, Variant: p.plan.Variant, Profiles: slices.Clone(p.plan.Profiles), Harnesses: slices.Clone(p.plan.Harnesses),
		Project: p.plan.Project, MCPKey: p.mcpKey, Backups: []string{}, Files: []state.ProjectFile{}, Dirs: []string{},
		Exclude: state.ProjectExclude{File: p.plan.Exclude.File, Pending: p.plan.Exclude.Pending, Entries: slices.Clone(p.plan.Exclude.Entries)},
	}
	if p.prev != nil {
		r.FirstApply, r.Backups, r.Dirs = p.prev.FirstApply, slices.Clone(p.prev.Backups), slices.Clone(p.prev.Dirs)
		if p.prev.Exclude.File == r.Exclude.File {
			r.Exclude.Created, r.Exclude.Newline = p.prev.Exclude.Created, p.prev.Exclude.Newline
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

// write carries out the plan under the lock towards the record next.
func (a applier) write(res *ApplyResult, next state.Project) error {
	p := a.p
	record := p.plan.Record
	cur, err := readRecord(a.ctx, a.d, p.plan.Project)
	if err != nil {
		return err
	}
	if !reflect.DeepEqual(cur, p.prev) {
		return &ChangedError{Path: record}
	}
	now := a.d.Now().UTC()
	id, dir, err := a.backupDir(now)
	if err != nil {
		return fmt.Errorf("the backup failed, nothing was written: %w", err)
	}
	writes, err := a.writes(id, &next)
	if err == nil {
		err = a.takeBackup(id, dir, p.plan.Project, now, writes, p.prev != nil)
	}
	if err != nil {
		return fmt.Errorf("the backup failed, nothing was written: %w", errors.Join(err, a.d.RemoveAll(dir)))
	}
	base := state.Project{Schema: state.ProjectSchema, Pack: next.Pack, Project: next.Project, MCPKey: next.MCPKey, Profiles: []string{}, Harnesses: []string{},
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
	var done []write
	for _, w := range writes {
		if err = a.ctx.Err(); err != nil {
			break
		}
		lost := false
		if lost, err = a.carry(w); err != nil {
			if len(w.op.Dirs) > 0 {
				done = append(done, w) // its directories may be there
			}
			break
		}
		done = append(done, w)
		if lost {
			res.Warnings = append(res.Warnings, plan.Warning{Code: CodeMCPKeysLost, Harness: "claudecode", Path: w.op.Target,
				Message: fmt.Sprintf("%s was rewritten while dotai wrote its MCP servers there, twice, and lost them: close Claude Code, then apply again", w.op.Target)})
		}
	}
	if err == nil {
		next.Dirs = a.pruneDirs(next.Dirs)
		err = a.writeRecord(next)
	}
	if err != nil {
		rb := a.rollback(dir, done)
		if rb == nil {
			rb = a.putRecordBack(dir, j.Prior)
		}
		return &ApplyError{Cause: err, Rollback: rb, Backup: dir}
	}
	res.Backup = &BackupRef{ID: id, Path: dir}
	return nil
}

// priorCopy is the copy of the record before an apply in its backup.
const priorCopy = "record.json"

// backupDir makes the directory of a new backup of the project, taken at
// t, under ~/.dotai/projects/<project id>/backups/.
func (a applier) backupDir(t time.Time) (string, string, error) {
	root := projectBackups(a.home, a.p.plan.Project)
	if err := ensureDir(a.d, a.home, root, 0o700); err != nil {
		return "", "", err
	}
	var id, dir string
	var err error
	for range 1000 {
		id = backup.ID(t, backup.OperationApply)
		dir = filepath.Join(root, id)
		if err = a.d.Mkdir(dir, 0o700); !errors.Is(err, fs.ErrExist) {
			break
		}
		t = t.Add(time.Millisecond)
	}
	return id, dir, err
}

// projectBackups is the directory of the backups of project under home.
func projectBackups(home, project string) string {
	h := sha256.Sum256([]byte(foldKey(filepath.Clean(project))))
	return filepath.Join(home, dotaiDir, projectsDir, hex.EncodeToString(h[:8]), backupsDir)
}

// writes are the targets the apply writes, in order: the project's files
// and the home file of Claude Code's MCP servers in target order, then the
// exclude file and the list of projects, each read now, under the lock.
// next gets the exclude block's line break and the file it created.
func (a applier) writes(id string, next *state.Project) ([]write, error) {
	p := a.p
	var out []write
	dirs := map[string]bool{} // the directories planned to be created, by root and name
	for _, op := range p.plan.Ops {
		if op.Action == plan.Unchanged || op.Kind == plan.KindRecord {
			continue
		}
		if err := state.CheckProjectTarget(p.plan.Project, op.Target); err != nil {
			return nil, err
		}
		if p.in.Tracked[op.Target] {
			return nil, fmt.Errorf("%s is tracked by git, and an apply never writes it", op.Target)
		}
		if rel, ok := relIn(p.plan.Project, op.Target); ok {
			w := write{op: state.JournalOp{Target: op.Target, Root: p.plan.Project, Name: rel}, data: op.Data, exec: op.Owns != nil && op.Owns.Exec}
			if op.Before != "" {
				w.op.Before = &state.Content{SHA256: op.Before, Exec: p.in.Disk[op.Target].Exec}
			}
			switch {
			case op.Action == plan.Remove, op.Removal:
				w.remove = true
			case op.PutBack != "":
				data, exec, err := a.backupCopy(op.PutBack, op.Target)
				if err != nil {
					return nil, err
				}
				w.data, w.exec = data, exec
			}
			if !w.remove {
				w.op.After = &state.Content{SHA256: contentHash(w.data), Exec: w.exec}
				missing, err := a.missingDirs(p.plan.Project, rel)
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
			continue
		}
		// The home file of Claude Code's MCP servers, under the home
		// directory, which Claude Code rewrites while it runs.
		if _, ok := relIn(a.home, op.Target); !ok {
			return nil, fmt.Errorf("%s is neither in the project nor under the home directory", op.Target)
		}
		root, name := filepath.Dir(op.Target), filepath.Base(op.Target)
		if err := ensureParents(a.d, a.home, root); err != nil {
			return nil, err
		}
		undo, err := undoEdits(p.in.Content[op.Target], op.Edits)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", op.Target, err)
		}
		w := write{op: state.JournalOp{Target: op.Target, Root: root, Name: name, Undo: undo}, edits: op.Edits, home: true}
		if op.Before == "" {
			// A last prune without a path: the file, missing before, goes when
			// it is left without a key.
			w.op.Undo = append(w.op.Undo, plan.KeyEdit{Prune: true})
		}
		out = append(out, w)
	}
	slices.SortStableFunc(out, func(x, y write) int { return strings.Compare(x.op.Target, y.op.Target) })
	ex, err := a.excludeWrite(next)
	if err != nil {
		return nil, err
	}
	if ex != nil {
		out = append(out, *ex)
	}
	pj, err := a.projectsWrite(*next)
	if err != nil {
		return nil, err
	}
	if pj != nil {
		out = append(out, *pj)
	}
	return out, nil
}

// excludeWrite is the write of the exclude file with dotai's block for
// next, nil when it is already there or the project is in no repository.
func (a applier) excludeWrite(next *state.Project) (*write, error) {
	file := next.Exclude.File
	if file == "" {
		return nil, nil
	}
	common := filepath.Dir(filepath.Dir(file))
	name := filepath.ToSlash(filepath.Join(filepath.Base(filepath.Dir(file)), filepath.Base(file)))
	data, cur, exists, err := readTarget(a.ctx, a.d, common, file)
	if err != nil {
		return nil, err
	}
	out, newline := plan.WithExcludeBlock(data, next.Project, next.Exclude.Entries)
	if exists && bytes.Equal(out, data) {
		return nil, nil
	}
	if newline {
		next.Exclude.Newline = true
	}
	if !exists {
		next.Exclude.Created = true
	}
	w := &write{op: state.JournalOp{Target: file, Root: common, Name: name, After: &state.Content{SHA256: contentHash(out)}}, data: out}
	if exists {
		w.op.Before = &state.Content{SHA256: cur.Hash, Exec: cur.Exec}
		w.exec = cur.Exec
	}
	missing, err := a.missingDirs(common, name)
	if err != nil {
		return nil, err
	}
	w.op.Dirs = missing
	return w, nil
}

// projectsWrite is the write of ~/.dotai/projects.json with the keys next
// owns in the home file of Claude Code's MCP servers, nil when it holds
// them already.
func (a applier) projectsWrite(next state.Project) (*write, error) {
	file := a.p.homeMCP
	var keys [][]string
	for _, f := range next.Files {
		if f.Target == file {
			for _, k := range f.Keys {
				keys = append(keys, k.Path)
			}
		}
	}
	return a.projectsWriteWith(next.Project, file, keys)
}

// projectsWriteWith is the write of ~/.dotai/projects.json with project
// owning keys in file, nil when it holds that already.
func (a runner) projectsWriteWith(project, file string, keys [][]string) (*write, error) {
	dir := filepath.Join(a.home, dotaiDir)
	name := filepath.Join(dir, projectsFile)
	data, cur, exists, err := readTarget(a.ctx, a.d, a.home, name)
	if err != nil {
		return nil, err
	}
	list := state.Projects{Schema: state.ProjectsSchema}
	if exists {
		if list, err = state.DecodeProjects(data); err != nil {
			return nil, fmt.Errorf("%s: %w", name, err)
		}
	}
	nextList := list.With(project, file, keys)
	if len(nextList.Projects) == 0 && !exists {
		return nil, nil
	}
	if reflect.DeepEqual(nextList.Projects, list.Projects) && exists {
		return nil, nil
	}
	out, err := state.EncodeProjects(nextList)
	if err != nil {
		return nil, err
	}
	w := &write{op: state.JournalOp{Target: name, Root: dir, Name: projectsFile, After: &state.Content{SHA256: contentHash(out)}}, data: out}
	if exists {
		w.op.Before = &state.Content{SHA256: cur.Hash, Exec: cur.Exec}
	}
	if len(nextList.Projects) == 0 {
		w.remove, w.op.After = true, nil
	}
	return w, nil
}

// takeBackup copies into dir each target of writes that exists, checked
// against the plan, and with prior the record of project as it is, then
// writes the manifest of the backup id of operation, taken at t, each
// synced to the disk.
func (a runner) takeBackup(id, dir, project string, t time.Time, writes []write, prior bool) error {
	op, _ := backup.Operation(id)
	m := backup.ProjectManifest{Schema: backup.ProjectSchema, ID: id, Operation: op, CreatedAt: t, Project: project, Copies: []backup.Copy{}}
	if prior {
		data, _, err := a.d.Read(a.ctx, filepath.Join(project, filepath.FromSlash(plan.RecordFile)))
		if err != nil {
			return err
		}
		if err := a.d.WriteSynced(dir, priorCopy, data, 0o600); err != nil {
			return err
		}
	}
	for i := range writes {
		w := &writes[i]
		if w.op.Before == nil || w.home {
			continue
		}
		data, cur, err := a.d.Read(a.ctx, w.op.Target)
		if errors.Is(err, fs.ErrNotExist) || err == nil && cur.Hash != w.op.Before.SHA256 {
			return &ChangedError{Path: w.op.Target}
		}
		if err != nil {
			return err
		}
		w.op.Copy = backup.CopyFile(len(m.Copies))
		if err := a.d.WriteSynced(dir, w.op.Copy, data, 0o600); err != nil {
			return err
		}
		m.Copies = append(m.Copies, backup.Copy{Target: w.op.Target, File: w.op.Copy, SHA256: cur.Hash, Exec: cur.Exec})
	}
	return a.writeManifest(dir, m)
}

// writeManifest writes the manifest m of the project backup dir and syncs
// the directory.
func (a runner) writeManifest(dir string, m backup.ProjectManifest) error {
	data, err := backup.EncodeProject(m)
	if err != nil {
		return err
	}
	if err := a.d.WriteSynced(dir, backup.ManifestName, data, 0o600); err != nil {
		return err
	}
	if err := a.d.SyncDir(filepath.Join(dir, backup.FilesDir)); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return a.d.SyncDir(dir)
}

// carry writes w, once the directories above it are real directories and
// the target is still as the plan found it. lost reports MCP servers that
// a harness's rewrite of its home file lost twice.
func (a runner) carry(w write) (lost bool, err error) {
	if w.home {
		return a.writeHomeKeys(w)
	}
	if _, err := a.missingDirs(w.op.Root, w.op.Name); err != nil {
		return false, err
	}
	cur, err := a.d.Hash(a.ctx, w.op.Target)
	switch {
	case w.op.Before == nil && errors.Is(err, fs.ErrNotExist):
	case w.op.Before == nil && err == nil, w.op.Before != nil && errors.Is(err, fs.ErrNotExist):
		return false, &ChangedError{Path: w.op.Target}
	case err != nil:
		return false, err
	case cur.Hash != w.op.Before.SHA256:
		return false, &ChangedError{Path: w.op.Target}
	}
	if w.remove {
		return false, a.d.RemoveIn(w.op.Root, w.op.Name)
	}
	if d := path.Dir(w.op.Name); d != "." {
		if err := a.d.MkdirIn(w.op.Root, d); err != nil {
			return false, err
		}
	}
	return false, a.d.WriteIn(w.op.Root, w.op.Name, w.data, perm(w.exec))
}

// writeHomeKeys makes the edits of w on the home file as it is now, and
// reads it again to check that it holds the keys dotai owns there: a
// harness that rewrote it meanwhile may have lost them, so it is written
// once more, and lost reports keys lost again. A rename refused for a
// moment, as by a harness that holds the file open on Windows, is retried
// with a growing wait (WriteIn).
func (a runner) writeHomeKeys(w write) (lost bool, err error) {
	edits, removeEmpty := cutRemoveEmpty(w.edits)
	for attempt := range 2 {
		data, _, exists, err := readTarget(a.ctx, a.d, a.home, w.op.Target)
		if err != nil {
			return false, err
		}
		out, err := plan.ApplyEdits(data, edits)
		if err != nil {
			return false, fmt.Errorf("%s: %w; fix it, then apply again", w.op.Target, err)
		}
		if names, _ := plan.TopKeys(out); removeEmpty && exists && len(names) == 0 {
			return false, a.d.RemoveIn(w.op.Root, w.op.Name)
		}
		if !exists || !bytes.Equal(out, data) {
			if err := a.d.WriteIn(w.op.Root, w.op.Name, out, 0o600); err != nil {
				return false, err
			}
		}
		again, _, _, err := readTarget(a.ctx, a.d, a.home, w.op.Target)
		if err != nil {
			return false, err
		}
		if holdsKeys(again, edits) {
			return false, nil
		}
		if attempt == 1 {
			return true, nil
		}
	}
	return true, nil
}

// cutRemoveEmpty is edits without the prune without a path that may end
// them, which asks for the file to go once it holds no key, and whether
// they end so.
func cutRemoveEmpty(edits []plan.KeyEdit) ([]plan.KeyEdit, bool) {
	if n := len(edits); n > 0 && edits[n-1].Prune && edits[n-1].Path == nil {
		return edits[:n-1], true
	}
	return edits, false
}

// holdsKeys reports whether data holds what edits set and nothing of what
// they delete.
func holdsKeys(data []byte, edits []plan.KeyEdit) bool {
	want, err := plan.ApplyEdits(data, edits)
	return err == nil && bytes.Equal(want, data)
}

// undoEdits are the edits that give back, to a file that held data before
// edits (nil when it was missing), the value each key edits sets or
// deletes had, then remove the objects edits create that are left empty.
func undoEdits(data []byte, edits []plan.KeyEdit) ([]plan.KeyEdit, error) {
	doc := data
	var undo, prunes []plan.KeyEdit
	for _, e := range edits {
		if e.Prune {
			continue
		}
		before, _, err := keyValue(doc, e.Path)
		if err != nil {
			return nil, err
		}
		undo = append(undo, plan.KeyEdit{Path: e.Path, Value: before})
		for j := len(e.Path) - 1; j >= 1; j-- {
			if _, ok, _ := keyValue(doc, e.Path[:j]); !ok && !slices.ContainsFunc(prunes, func(p plan.KeyEdit) bool { return slices.Equal(p.Path, e.Path[:j]) }) {
				prunes = append(prunes, plan.KeyEdit{Path: e.Path[:j], Prune: true})
			}
		}
	}
	slices.SortStableFunc(prunes, func(x, y plan.KeyEdit) int { return len(y.Path) - len(x.Path) })
	return append(undo, prunes...), nil
}

// keyValue is the value at p in data, a JSON or JSONC object, and whether
// it is there.
func keyValue(data []byte, p []string) ([]byte, bool, error) { return plan.GetKey(data, p) }

// rollback undoes done, last first, with a context of its own: a file the
// apply wrote gets back its copy from the backup in dir, or goes when it
// was missing, unless something else changed it since, which is left and
// reported; a harness's home file gets back its keys; and each directory
// the apply created goes when it is empty.
func (a applier) rollback(dir string, done []write) error {
	return a.runner.undoWrites(dir, done)
}

// undoWrites undoes done as rollback describes.
func (a runner) undoWrites(dir string, done []write) error {
	a, cancel := a.detached()
	defer cancel()
	var errs []error
	for _, w := range slices.Backward(done) {
		if err := a.undoOp(dir, w.op); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// undoOp undoes one op of a journal, from the backup in dir. It does
// nothing to a target that holds what it held before, so that it undoes
// an op that was never carried out too.
func (a runner) undoOp(dir string, op state.JournalOp) error {
	defer a.removeDirs(op.Root, op.Dirs)
	if op.Undo != nil {
		data, _, exists, err := readTarget(a.ctx, a.d, a.home, op.Target)
		if err != nil || !exists {
			return err
		}
		edits, removeEmpty := cutRemoveEmpty(op.Undo)
		out, err := plan.ApplyEdits(data, edits)
		if err != nil {
			return fmt.Errorf("%s: %w", op.Target, err)
		}
		if removeEmpty {
			if names, _ := plan.TopKeys(out); len(names) == 0 {
				return a.d.RemoveIn(op.Root, op.Name)
			}
		}
		if bytes.Equal(out, data) {
			return nil
		}
		return a.d.WriteIn(op.Root, op.Name, out, 0o600)
	}
	cur, err := a.d.Hash(a.ctx, op.Target)
	exists := err == nil
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	changed := fmt.Errorf("%s was left as it is: it changed after the apply wrote it", op.Target)
	switch {
	case op.Before == nil && !exists:
		return nil
	case op.Before != nil && exists && cur.Hash == op.Before.SHA256:
		return nil
	case exists && (op.After == nil || cur.Hash != op.After.SHA256):
		return changed
	case op.Before == nil:
		return a.d.RemoveIn(op.Root, op.Name)
	}
	data, err := a.d.ReadFile(dir, op.Copy)
	if err != nil {
		return err
	}
	if contentHash(data) != op.Before.SHA256 {
		return fmt.Errorf("the backup copy of %s does not match what was there", op.Target)
	}
	if d := path.Dir(op.Name); d != "." {
		if err := a.d.MkdirIn(op.Root, d); err != nil {
			return err
		}
	}
	return a.d.WriteIn(op.Root, op.Name, data, perm(op.Before.Exec))
}

// removeDirs removes each of dirs, slash paths inside root, deepest first,
// when it is empty.
func (a runner) removeDirs(root string, dirs []string) {
	for _, d := range slices.Backward(dirs) {
		_ = a.d.RemoveIn(root, d)
	}
}

// putRecordBack writes back the record before the apply from its copy in
// the backup dir, or removes the record when there was none.
func (a applier) putRecordBack(dir, prior string) error {
	return a.runner.putRecordBack(a.p.plan.Project, dir, prior)
}

// putRecordBack writes back the record of project from the copy prior in
// the backup dir, or removes it, and .dotai/ when it is left empty, when
// prior is "".
func (a runner) putRecordBack(project, dir, prior string) error {
	if prior == "" {
		err := a.d.RemoveIn(project, plan.RecordFile)
		if errors.Is(err, fs.ErrNotExist) {
			err = nil
		}
		_ = a.d.RemoveIn(project, path.Dir(plan.RecordFile))
		return err
	}
	data, err := a.d.ReadFile(dir, prior)
	if err != nil {
		return err
	}
	return a.d.WriteIn(project, plan.RecordFile, data, 0o644)
}

// writeRecord writes r to the project record.
func (a applier) writeRecord(r state.Project) error {
	return a.runner.writeRecord(r)
}

// writeRecord writes r to the record of its project, creating .dotai/.
func (a runner) writeRecord(r state.Project) error {
	data, err := state.EncodeProject(r)
	if err != nil {
		return err
	}
	if err := a.d.MkdirIn(r.Project, path.Dir(plan.RecordFile)); err != nil {
		return err
	}
	return a.d.WriteIn(r.Project, plan.RecordFile, data, 0o644)
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
	return left
}

// missingDirs checks each directory between root and name, a slash path
// inside it, and gives the missing ones, outermost first, as slash paths
// inside root; a link, a junction or a file on the way is an error.
func (a runner) missingDirs(root, name string) ([]string, error) {
	var missing []string
	dir := ""
	for _, seg := range strings.Split(path.Dir(name), "/") {
		if seg == "." {
			break
		}
		dir = path.Join(dir, seg)
		if len(missing) > 0 {
			missing = append(missing, dir)
			continue
		}
		err := checkDir(a.d, filepath.Join(root, filepath.FromSlash(dir)))
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

// ensureParents checks that each directory from home down to dir is a
// real directory; a missing one is created.
func ensureParents(d executeFS, home, dir string) error {
	if samePath(home, dir) {
		return nil
	}
	return ensureDir(d, home, dir, 0o755)
}

// backupCopy is the copy of target in the project backup id, with the
// exec bit it had.
func (a applier) backupCopy(id, target string) ([]byte, bool, error) {
	dir := filepath.Join(projectBackups(a.home, a.p.plan.Project), id)
	data, err := a.d.ReadFile(dir, backup.ManifestName)
	if err != nil {
		return nil, false, fmt.Errorf("the backup %s that holds what %s held before dotai wrote it: %w", id, target, err)
	}
	m, err := backup.DecodeProject(data, id, a.p.plan.Project)
	if err != nil {
		return nil, false, err
	}
	c, ok := m.Find(target)
	if !ok {
		return nil, false, fmt.Errorf("the backup %s holds no copy of %s", id, target)
	}
	copied, err := a.d.ReadFile(dir, c.File)
	if err != nil {
		return nil, false, err
	}
	if contentHash(copied) != c.SHA256 {
		return nil, false, &CorruptBackupError{ID: id, File: c.File}
	}
	return copied, c.Exec, nil
}

// relIn is target as a slash path inside root, and whether it is inside.
func relIn(root, target string) (string, bool) {
	rel, err := filepath.Rel(root, target)
	if err != nil || !filepath.IsLocal(rel) {
		return "", false
	}
	return filepath.ToSlash(rel), true
}

// readTarget reads target, a path under base, without following a link on
// the way: its content and state, and whether it exists.
func readTarget(ctx context.Context, d installFS, base, target string) ([]byte, plan.Current, bool, error) {
	cur, data, ok, err := inspectTarget(ctx, d, base, target, map[string]error{}, inspectRead)
	return data, cur, ok, err
}

// readRecord reads the project record of project, nil when there is none.
func readRecord(ctx context.Context, d installFS, project string) (*state.Project, error) {
	name := filepath.Join(project, filepath.FromSlash(plan.RecordFile))
	data, _, ok, err := readTarget(ctx, d, project, name)
	if err != nil || !ok {
		return nil, err
	}
	r, err := state.DecodeProject(data, project)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", name, err)
	}
	return &r, nil
}

// readProjects reads ~/.dotai/projects.json, empty when it is missing.
func readProjects(d installFS, home string) (state.Projects, error) {
	name := filepath.Join(home, dotaiDir, projectsFile)
	data, _, ok, err := readTarget(context.Background(), d, home, name)
	if err != nil || !ok {
		return state.Projects{Schema: state.ProjectsSchema, Projects: []state.ProjectEntry{}}, err
	}
	p, err := state.DecodeProjects(data)
	if err != nil {
		return state.Projects{}, fmt.Errorf("%s: %w", name, err)
	}
	return p, nil
}

// settleProject rolls back, under the lock, the journal an apply or a
// restore of the project of cwd left in its record when it stopped
// midway, from its backup: each target it wrote gets back what it held,
// and the record its copy, or none.
func settleProject(ctx context.Context, d executeFS, g gitPort, cwd, home string) error {
	project, _, _, err := findProject(ctx, g, cwd, home)
	if err != nil {
		return err
	}
	r, err := readRecord(ctx, d, project)
	if err != nil || r == nil || r.Pending == nil {
		return err
	}
	ru := runner{ctx: ctx, d: d, home: home}
	var warnings []string
	return ru.locked(&warnings, func() error {
		r, err := readRecord(ctx, d, project)
		if err != nil || r == nil || r.Pending == nil {
			return err
		}
		dir := filepath.Join(projectBackups(home, project), r.Pending.Backup)
		var done []write
		for _, op := range r.Pending.Ops {
			done = append(done, write{op: op})
		}
		if err := ru.undoWrites(dir, done); err != nil {
			return fmt.Errorf("rolling back the apply or restore that stopped midway in %s: %w; the backup %s holds what was there before", project, err, dir)
		}
		return ru.putRecordBack(project, dir, r.Pending.Prior)
	})
}
