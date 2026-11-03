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
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/lartweib/dotai/internal/backup"
	"github.com/lartweib/dotai/internal/harness"
	"github.com/lartweib/dotai/internal/plan"
	"github.com/lartweib/dotai/internal/state"
)

// A project apply and the restore of a project share how they write: the
// record of the project under the home directory, a backup of what they
// overwrite or delete beside it, a journal in the record, each write
// checked again before it is made, and a rollback from the backup. The
// record and its journal are never read from the project, which a
// repository could ship with a crafted one: the project only holds a
// marker, which dotai never trusts. A record is checked, before anything is
// written, to name only what an apply of that project writes.

// The files of dotai under ~/.dotai for projects: the list of projects
// whose keys dotai owns in a harness's home file, which a replace install
// reads, and the directory of each project's record and backups, with the
// record's name there and that of the copy of the record in a backup.
const (
	projectsFile = "projects.json"
	projectsDir  = "projects"
	recordName   = "record.json"
	priorCopy    = "record.json"
)

// CodeRecordInvalid is the code of a project record that names what an
// apply of its project never writes.
const CodeRecordInvalid = "record-invalid"

// RecordInvalidError is a project record, Path, that is not one an apply
// of the project writes, such as one that names a target outside the
// project or a backup outside its backups: nothing was written.
type RecordInvalidError struct {
	Path, Why string
}

func (e *RecordInvalidError) Error() string {
	return fmt.Sprintf("the project record %s %s, which an apply of this project never writes: nothing was written; move the record away to apply again from scratch", e.Path, e.Why)
}

// projectID is the id of the project at the absolute path project: the
// first 16 hex digits of the SHA-256 of its path, compared as this OS
// compares paths.
func projectID(project string) string {
	h := sha256.Sum256([]byte(foldKey(filepath.Clean(project))))
	return hex.EncodeToString(h[:8])
}

// projectHome is the directory of the record and the backups of project
// under home.
func projectHome(home, project string) string {
	return filepath.Join(home, dotaiDir, projectsDir, projectID(project))
}

// projectBackups is the directory of the backups of project under home.
func projectBackups(home, project string) string {
	return filepath.Join(projectHome(home, project), backupsDir)
}

// recordPath is the record of project under home.
func recordPath(home, project string) string {
	return filepath.Join(projectHome(home, project), recordName)
}

// recordScope is what a record of a project may name: the project, its id
// and the key of Claude Code's projects entry, the exclude file git names,
// "" outside a repository, the list of projects, and the home files of
// every harness's MCP servers, real or isolated.
type recordScope struct {
	home, project, id, mcpKey string
	exclude, projects         string
	homeFiles                 []string
}

// scopeOf is the recordScope of project, in repo (nil outside one), with
// mcpKey, under home.
func scopeOf(home, project, mcpKey string, repo *Repo) recordScope {
	sc := recordScope{home: home, project: project, id: projectID(project), mcpKey: mcpKey, projects: filepath.Join(home, dotaiDir, projectsFile)}
	if repo != nil {
		sc.exclude = repo.Exclude
	}
	for _, a := range harness.All() {
		if m := a.Project.MCP; m != nil {
			sc.homeFiles = append(sc.homeFiles, filepath.Join(home, filepath.FromSlash(m.Real)), filepath.Join(home, filepath.FromSlash(m.Isolated)))
		}
	}
	return sc
}

// rootOf is the directory target is written through and its slash path
// inside it, and false for a target an apply of the project never writes.
func (sc recordScope) rootOf(target string) (root, name string, ok bool) {
	switch {
	case slices.Contains(sc.homeFiles, target):
		return filepath.Dir(target), filepath.Base(target), true
	case sc.exclude != "" && target == sc.exclude:
		return filepath.Dir(filepath.Dir(target)), filepath.Base(filepath.Dir(target)) + "/" + filepath.Base(target), true
	case target == sc.projects:
		return filepath.Dir(target), projectsFile, true
	}
	if rel, ok := relIn(sc.project, target); ok && state.CheckProjectTarget(sc.project, target) == nil {
		return sc.project, rel, true
	}
	return "", "", false
}

// homeKey reports whether p is a key a project apply may own in a home
// file: an MCP server under the project's entry, or, with object set, one
// of the objects on the way to it.
func (sc recordScope) homeKey(p []string, object bool) bool {
	server := len(p) == 4 && p[0] == "projects" && p[1] == sc.mcpKey && p[2] == "mcpServers" && p[3] != ""
	parent := object && (slices.Equal(p, []string{"projects"}) || slices.Equal(p, []string{"projects", sc.mcpKey}) || slices.Equal(p, []string{"projects", sc.mcpKey, "mcpServers"}))
	return server || parent
}

// copyName is the name of a copy in a project backup.
var copyName = regexp.MustCompile(`^files/[0-9]+$`)

// validBackup reports whether id is the id of a backup of a project apply
// or restore, which names a directory of the project's backups.
func validBackup(id string) bool {
	op, ok := backup.Operation(id)
	return ok && (op == backup.OperationApply || op == backup.OperationRestore)
}

// cleanRel reports whether p is a clean slash path that stays inside the
// directory it is relative to.
func cleanRel(p string) bool {
	return p != "" && path.Clean(p) == p && filepath.IsLocal(filepath.FromSlash(p))
}

// check reports a record r that names anything but what an apply of the
// project of sc writes (rootOf), keys of a home file that are not the
// project's MCP servers, backups that are not the project's, or a journal
// whose roots, names, copies or directories are not where its targets are.
func (sc recordScope) check(r state.Project) error {
	bad := func(format string, args ...any) error { return fmt.Errorf(format, args...) }
	if r.ID != sc.id || r.MCPKey != sc.mcpKey {
		return bad("is of the project id %q and the Claude Code key %q, not %q and %q", r.ID, r.MCPKey, sc.id, sc.mcpKey)
	}
	for _, id := range r.Backups {
		if !validBackup(id) {
			return bad("names the backup %q", id)
		}
	}
	for _, f := range r.Files {
		if _, _, ok := sc.rootOf(f.Target); !ok || f.Target == sc.exclude || f.Target == sc.projects {
			return bad("owns %s", f.Target)
		}
		if f.Backup != "" && !validBackup(f.Backup) {
			return bad("names the backup %q for %s", f.Backup, f.Target)
		}
		if !slices.Contains(sc.homeFiles, f.Target) {
			continue
		}
		if f.Hash != "" {
			return bad("owns the whole of %s", f.Target)
		}
		for _, k := range f.Keys {
			if !sc.homeKey(k.Path, false) {
				return bad("owns the key %s of %s", k.Name(), f.Target)
			}
		}
		for _, q := range f.Parents {
			if !sc.homeKey(q, true) {
				return bad("owns the object %s of %s", strings.Join(q, "."), f.Target)
			}
		}
	}
	if r.Exclude.File != "" && r.Exclude.File != sc.exclude {
		return bad("names the exclude file %s", r.Exclude.File)
	}
	j := r.Pending
	if j == nil {
		return nil
	}
	if !validBackup(j.Backup) || j.Prior != "" && j.Prior != priorCopy {
		return bad("has a journal of the backup %q with the prior record %q", j.Backup, j.Prior)
	}
	for _, op := range j.Ops {
		root, name, ok := sc.rootOf(op.Target)
		switch {
		case !ok:
			return bad("has a journal that writes %s", op.Target)
		case op.Root != root || op.Name != name:
			return bad("has a journal that writes %s through %s as %q", op.Target, op.Root, op.Name)
		case op.Copy != "" && !copyName.MatchString(op.Copy):
			return bad("has a journal whose copy of %s is %q", op.Target, op.Copy)
		case op.Shared == state.SharedExclude && op.Target != sc.exclude, op.Shared == state.SharedProjects && op.Target != sc.projects,
			op.Shared != "" && op.Shared != state.SharedExclude && op.Shared != state.SharedProjects:
			return bad("has a journal that shares %s as %q", op.Target, op.Shared)
		case (op.Undo != nil || op.RemoveIfEmpty) && !slices.Contains(sc.homeFiles, op.Target):
			return bad("has a journal that edits the keys of %s", op.Target)
		case op.PriorEntry != nil && op.PriorEntry.Project != sc.project:
			return bad("has a journal that gives %s back another project's entry", op.Target)
		}
		for _, e := range op.Undo {
			if !sc.homeKey(e.Path, true) {
				return bad("has a journal that edits the key %s of %s", strings.Join(e.Path, "."), op.Target)
			}
		}
		for _, d := range op.Dirs {
			if !cleanRel(d) || !strings.HasPrefix(op.Name, d+"/") {
				return bad("has a journal that creates the directory %q for %s", d, op.Target)
			}
		}
	}
	return nil
}

// readRecord reads the record of the project of sc, nil when there is
// none, and refuses, with a *RecordInvalidError, one that is not what an
// apply of the project writes.
func readRecord(ctx context.Context, d installFS, sc recordScope) (*state.Project, error) {
	name := recordPath(sc.home, sc.project)
	data, _, ok, err := readTarget(ctx, d, sc.home, name)
	if err != nil || !ok {
		return nil, err
	}
	r, err := state.DecodeProject(data, sc.project)
	if err == nil {
		err = sc.check(r)
	}
	if err != nil {
		return nil, &RecordInvalidError{Path: name, Why: err.Error()}
	}
	return &r, nil
}

// writeRecord writes r to the record of its project under the home
// directory, readable by the user only.
func (a runner) writeRecord(r state.Project) error {
	data, err := state.EncodeProject(r)
	if err != nil {
		return err
	}
	dir := projectHome(a.home, r.Project)
	if err := ensureDir(a.d, a.home, dir, 0o700); err != nil {
		return err
	}
	return a.d.WriteIn(dir, recordName, data, 0o600)
}

// putRecordBack writes back the record of project from the copy prior in
// the backup dir, or removes it when prior is "".
func (a runner) putRecordBack(project, dir, prior string) error {
	root := projectHome(a.home, project)
	if prior == "" {
		if err := a.d.RemoveIn(root, recordName); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return err
		}
		return nil
	}
	data, err := a.d.ReadFile(dir, prior)
	if err != nil {
		return err
	}
	return a.d.WriteIn(root, recordName, data, 0o600)
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

// journaledWrite is a write of a project carried out as an apply's is:
// the project, the operation of its backup, what it is called in a
// message, the record the journal goes into, whether the record exists,
// so that the backup keeps a copy of it, and the writes, in order.
type journaledWrite struct {
	project, operation, what string
	base                     state.Project
	prior                    bool
	writes                   []write
}

// journalStop is a journaled write that stopped after its journal was
// written: the cause, the outcome of the rollback, nil when everything was
// put back, and the backup directory. Callers report it as their own
// error.
type journalStop struct {
	cause, rollback error
	backup          string
}

func (e *journalStop) Error() string { return e.cause.Error() }

// journaled carries out j under the lock the caller holds: a new backup
// of what the writes overwrite or delete, with a copy of the record when
// j.prior; the journal in j.base, written into the record; each write;
// then finish, given the backup's id and when it was taken, which writes
// the record that follows or removes it. A failure before the journal is
// written leaves nothing behind; one after it rolls the writes back from
// the backup, puts the record back as it was and is a *journalStop. It
// gives the backup and the home files whose keys a harness lost twice.
func (a runner) journaled(j journaledWrite, finish func(id string, at time.Time) error) (BackupRef, []string, error) {
	root := projectBackups(a.home, j.project)
	if err := ensureDir(a.d, a.home, root, 0o700); err != nil {
		return BackupRef{}, nil, fmt.Errorf("the backup failed, nothing was written: %w", err)
	}
	id, dir, now, err := newBackupDir(a.d, root, a.d.Now().UTC(), j.operation)
	if err != nil {
		return BackupRef{}, nil, fmt.Errorf("the backup failed, nothing was written: %w", err)
	}
	if err := a.takeBackup(id, dir, j.project, now, j.writes, j.prior); err != nil {
		return BackupRef{}, nil, fmt.Errorf("the backup failed, nothing was written: %w", errors.Join(err, a.d.RemoveAll(dir)))
	}
	journal := &state.ApplyJournal{Backup: id, Ops: []state.JournalOp{}}
	if j.prior {
		journal.Prior = priorCopy
	}
	for _, w := range j.writes {
		journal.Ops = append(journal.Ops, w.op)
	}
	base := j.base
	base.Pending = journal
	if err := a.writeRecord(base); err != nil {
		return BackupRef{}, nil, fmt.Errorf("the %s journal could not be written, nothing was written: %w", j.what, errors.Join(err, a.d.RemoveAll(dir)))
	}
	done, lost, err := a.executeWrites(j.writes)
	if err == nil {
		err = finish(id, now)
	}
	if err != nil {
		rb := a.undoWrites(j.project, dir, done)
		if rb == nil {
			rb = a.putRecordBack(j.project, dir, journal.Prior)
		}
		return BackupRef{}, lost, &journalStop{cause: err, rollback: rb, backup: dir}
	}
	return BackupRef{ID: id, Path: dir}, lost, nil
}

// write is a target a project apply or restore writes, as its journal
// records it, with how to write it: data, or with remove set nothing; or,
// for a harness's home file (home), edits made on it as it is then.
type write struct {
	op     state.JournalOp
	data   []byte
	exec   bool
	remove bool
	edits  []plan.KeyEdit
	home   bool
}

// takeBackup makes the backup id of project in dir, taken at t: a copy of
// each target of writes that exists, checked against what the journal
// says it holds, unless it is a harness's home file, which a running
// harness rewrites and whose copy is kept for the user only, never put
// back whole; with prior, a copy of the record as it is; then the
// manifest, each synced to the disk.
func (a runner) takeBackup(id, dir, project string, t time.Time, writes []write, prior bool) error {
	op, _ := backup.Operation(id)
	m := backup.ProjectManifest{Schema: backup.ProjectSchema, ID: id, Operation: op, CreatedAt: t, Project: project, Copies: []backup.Copy{}}
	if prior {
		data, _, err := a.d.Read(a.ctx, recordPath(a.home, project))
		if err != nil {
			return err
		}
		if err := a.d.WriteSynced(dir, priorCopy, data, 0o600); err != nil {
			return err
		}
	}
	for i := range writes {
		w := &writes[i]
		if w.op.Before == nil && !w.home {
			continue
		}
		data, cur, err := a.d.Read(a.ctx, w.op.Target)
		switch {
		case w.home && errors.Is(err, fs.ErrNotExist):
			continue
		case errors.Is(err, fs.ErrNotExist), err == nil && !w.home && cur.Hash != w.op.Before.SHA256:
			return &ChangedError{Path: w.op.Target}
		case err != nil:
			return err
		}
		w.op.Copy = backup.CopyFile(len(m.Copies))
		if err := a.d.WriteSynced(dir, w.op.Copy, data, 0o600); err != nil {
			return err
		}
		m.Copies = append(m.Copies, backup.Copy{Target: w.op.Target, File: w.op.Copy, SHA256: cur.Hash, Exec: cur.Exec})
	}
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

// backupCopy is the copy of target in the backup id of project, with the
// exec bit it had.
func (a runner) backupCopy(project, id, target string) ([]byte, bool, error) {
	if !validBackup(id) {
		return nil, false, fmt.Errorf("%q is not the id of a backup of the project", id)
	}
	dir := filepath.Join(projectBackups(a.home, project), id)
	data, err := a.d.ReadFile(dir, backup.ManifestName)
	if err != nil {
		return nil, false, fmt.Errorf("the backup %s that holds what %s held before dotai wrote it: %w", id, target, err)
	}
	m, err := backup.DecodeProject(data, id, project)
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

// carry writes w, once the directories above it are real directories and
// the target is still as the journal says. lost reports MCP servers that
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
// reads it again to check that it holds them. A file that changed between
// the read and the write, which a harness wrote meanwhile, is read again
// and edited again rather than written over with what it held before; a
// file whose keys a harness lost after the write is written once more, and
// lost reports keys lost again. With RemoveIfEmpty, a file left without a
// key goes. A rename refused for a moment, as by a harness that holds the
// file open on Windows, is retried with a growing wait (WriteIn).
func (a runner) writeHomeKeys(w write) (lost bool, err error) {
	redone, rewritten := false, false
	for {
		data, cur, exists, err := readTarget(a.ctx, a.d, a.home, w.op.Target)
		if err != nil {
			return false, err
		}
		out, err := plan.ApplyEdits(data, w.edits)
		if err != nil {
			return false, fmt.Errorf("%s: %w; fix it, then try again", w.op.Target, err)
		}
		if names, _ := plan.TopKeys(out); w.op.RemoveIfEmpty && exists && len(names) == 0 {
			return false, a.d.RemoveIn(w.op.Root, w.op.Name)
		}
		if !exists || !bytes.Equal(out, data) {
			now, err := a.d.Hash(a.ctx, w.op.Target)
			switch {
			case !redone && (exists && err == nil && now.Hash != cur.Hash || !exists && err == nil):
				redone = true
				continue // a harness wrote it meanwhile: edit it once more, as it is now
			case err != nil && !errors.Is(err, fs.ErrNotExist):
				return false, err
			}
			if err := a.d.WriteIn(w.op.Root, w.op.Name, out, 0o600); err != nil {
				return false, err
			}
		}
		again, _, _, err := readTarget(a.ctx, a.d, a.home, w.op.Target)
		if err != nil {
			return false, err
		}
		if holdsKeys(again, w.edits) {
			return false, nil
		}
		if rewritten {
			return true, nil
		}
		rewritten = true
	}
}

// holdsKeys reports whether data holds what edits set and nothing of what
// they delete.
func holdsKeys(data []byte, edits []plan.KeyEdit) bool {
	want, err := plan.ApplyEdits(data, edits)
	return err == nil && bytes.Equal(want, data)
}

// undoEdits are the edits that give back, to a file that held data before
// edits (nil when it was missing), the value each key edits sets or
// deletes had, then remove the objects edits create, once empty.
func undoEdits(data []byte, edits []plan.KeyEdit) ([]plan.KeyEdit, error) {
	var undo []plan.KeyEdit
	var parents [][]string
	for _, e := range edits {
		if e.Prune {
			continue
		}
		before, _, err := plan.GetKey(data, e.Path)
		if err != nil {
			return nil, err
		}
		undo = append(undo, plan.KeyEdit{Path: e.Path, Value: before})
		parents = append(parents, plan.CreatedParents(func(q []string) bool { _, ok, _ := plan.GetKey(data, q); return ok }, e.Path, parents)...)
	}
	prunes, _ := plan.PruneEdits(parents, nil)
	return append(undo, prunes...), nil
}

// undoWrites undoes done, last first, from the backup dir of project,
// with a context of its own, so that a cancelled operation is still rolled
// back: a file gets back its copy, or goes when it was missing, unless
// something else changed it since, which is left and reported; a harness's
// home file gets back its keys; a shared file gets back this project's own
// part; and each directory the write created goes when it is empty. Each
// undo does nothing to a target that holds what it held before, so that it
// undoes a write that was never made too.
func (a runner) undoWrites(project, dir string, done []write) error {
	a, cancel := a.detached()
	defer cancel()
	var errs []error
	for _, w := range slices.Backward(done) {
		if err := a.undoOp(project, dir, w.op); err != nil {
			errs = append(errs, err)
		}
		for _, d := range slices.Backward(w.op.Dirs) {
			_ = a.d.RemoveIn(w.op.Root, d) // only an empty one goes
		}
	}
	return errors.Join(errs...)
}

// undoOp undoes one op of a journal, as undoWrites describes.
func (a runner) undoOp(project, dir string, op state.JournalOp) error {
	switch {
	case op.Undo != nil || op.RemoveIfEmpty:
		// A key that another project's apply owns now, as another worktree's
		// that found it there meanwhile, is left to it.
		list, err := readProjects(a.d, a.home)
		if err != nil {
			return err
		}
		edits := slices.DeleteFunc(slices.Clone(op.Undo), func(e plan.KeyEdit) bool {
			return !e.Prune && slices.ContainsFunc(list.Projects, func(o state.ProjectEntry) bool {
				return o.Project != project && o.File == op.Target && slices.ContainsFunc(o.Keys, func(k []string) bool { return slices.Equal(k, e.Path) })
			})
		})
		return a.edit(op, edits, op.RemoveIfEmpty)
	case op.Shared == state.SharedExclude:
		return a.rewrite(op, func(data []byte) ([]byte, error) { return plan.WithExcludeBlock(data, project, op.PriorBlock), nil })
	case op.Shared == state.SharedProjects:
		return a.rewrite(op, func(data []byte) ([]byte, error) {
			list := state.Projects{Schema: state.ProjectsSchema}
			if len(data) > 0 {
				var err error
				if list, err = state.DecodeProjects(data); err != nil {
					return nil, err
				}
			}
			e := state.ProjectEntry{Project: project}
			if op.PriorEntry != nil {
				e = *op.PriorEntry
			}
			list = list.With(e)
			if len(list.Projects) == 0 {
				return nil, nil
			}
			return state.EncodeProjects(list)
		})
	}
	cur, err := a.d.Hash(a.ctx, op.Target)
	exists := err == nil
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	switch {
	case op.Before == nil && !exists, op.Before != nil && exists && cur.Hash == op.Before.SHA256:
		return nil
	case exists && (op.After == nil || cur.Hash != op.After.SHA256):
		return fmt.Errorf("%s was left as it is: it changed after it was written", op.Target)
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

// edit makes edits on the home file of op as it is now; with removeEmpty,
// the file goes when it is left without a key.
func (a runner) edit(op state.JournalOp, edits []plan.KeyEdit, removeEmpty bool) error {
	data, _, exists, err := readTarget(a.ctx, a.d, a.home, op.Target)
	if err != nil || !exists {
		return err
	}
	out, err := plan.ApplyEdits(data, edits)
	if err != nil {
		return fmt.Errorf("%s: %w", op.Target, err)
	}
	if names, _ := plan.TopKeys(out); removeEmpty && len(names) == 0 {
		return a.d.RemoveIn(op.Root, op.Name)
	}
	if bytes.Equal(out, data) {
		return nil
	}
	return a.d.WriteIn(op.Root, op.Name, out, 0o600)
}

// rewrite gives the shared file of op back this project's own part, as
// part computes the file from what it holds now (nil when it is missing):
// what other projects wrote there stays. A file the operation created,
// which part leaves empty, goes.
func (a runner) rewrite(op state.JournalOp, part func([]byte) ([]byte, error)) error {
	data, cur, exists, err := readTarget(a.ctx, a.d, filepath.Dir(op.Root), op.Target)
	if err != nil || !exists {
		return err
	}
	out, err := part(data)
	if err != nil {
		return fmt.Errorf("%s: %w", op.Target, err)
	}
	switch {
	case len(out) == 0 && op.Before == nil:
		return a.d.RemoveIn(op.Root, op.Name)
	case bytes.Equal(out, data):
		return nil
	}
	return a.d.WriteIn(op.Root, op.Name, out, perm(cur.Exec))
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

// settleProject rolls back, under the lock, the journal an apply or a
// restore of the project of cwd left in its record when it stopped midway,
// from its backup: each target it wrote gets back what it held, and the
// record its copy, or none. A record that is not one an apply of the
// project writes is refused before anything is written.
func settleProject(ctx context.Context, d executeFS, g gitPort, cwd, home string) error {
	project, mcpKey, repo, err := findProject(ctx, g, cwd, home)
	if err != nil {
		return err
	}
	sc := scopeOf(home, project, mcpKey, repo)
	r, err := readRecord(ctx, d, sc)
	if err != nil || r == nil || r.Pending == nil {
		return err
	}
	ru := runner{ctx: ctx, d: d, home: home}
	var warnings []string
	return ru.locked(&warnings, func() error {
		r, err := readRecord(ctx, d, sc)
		if err != nil || r == nil || r.Pending == nil {
			return err
		}
		dir := filepath.Join(projectBackups(home, project), r.Pending.Backup)
		var done []write
		for _, op := range r.Pending.Ops {
			done = append(done, write{op: op})
		}
		if err := ru.undoWrites(project, dir, done); err != nil {
			return fmt.Errorf("rolling back the apply or restore that stopped midway in %s: %w; the backup %s holds what was there before", project, err, dir)
		}
		return ru.putRecordBack(project, dir, r.Pending.Prior)
	})
}

// projectsWrite is the write of ~/.dotai/projects.json with e as the
// entry of its project, nil when it holds that already. Its journal gives
// back the project's entry as it was.
func (a runner) projectsWrite(e state.ProjectEntry) (*write, error) {
	dir := filepath.Join(a.home, dotaiDir)
	name := filepath.Join(dir, projectsFile)
	data, cur, exists, err := readTarget(a.ctx, a.d, a.home, name)
	if err != nil {
		return nil, err
	}
	list := state.Projects{Schema: state.ProjectsSchema, Projects: []state.ProjectEntry{}}
	if exists {
		if list, err = state.DecodeProjects(data); err != nil {
			return nil, fmt.Errorf("%s: %w", name, err)
		}
	}
	next := list.With(e)
	if len(next.Projects) == 0 && !exists {
		return nil, nil
	}
	out, err := state.EncodeProjects(next)
	if err != nil {
		return nil, err
	}
	if exists && bytes.Equal(out, data) {
		return nil, nil
	}
	w := &write{op: state.JournalOp{Target: name, Root: dir, Name: projectsFile, Shared: state.SharedProjects, PriorEntry: list.Entry(e.Project),
		After: &state.Content{SHA256: contentHash(out)}}, data: out}
	if exists {
		w.op.Before = &state.Content{SHA256: cur.Hash, Exec: cur.Exec}
	}
	if len(next.Projects) == 0 {
		w.remove, w.op.After = true, nil
	}
	return w, nil
}

// excludeWrite is the write of the exclude file of project with its block
// holding entries (none removes it), nil when the file holds that already
// or is missing with no entries to write. created reports an exclude file
// the write creates. Its journal gives back the project's block as it was,
// prior.
func (a runner) excludeWrite(file, project string, entries, prior []string) (w *write, created bool, err error) {
	common := filepath.Dir(filepath.Dir(file))
	name := filepath.Base(filepath.Dir(file)) + "/" + filepath.Base(file)
	data, cur, exists, err := readTarget(a.ctx, a.d, common, file)
	if err != nil {
		return nil, false, err
	}
	out := plan.WithExcludeBlock(data, project, entries)
	if exists && bytes.Equal(out, data) || !exists && len(entries) == 0 {
		return nil, false, nil
	}
	w = &write{op: state.JournalOp{Target: file, Root: common, Name: name, Shared: state.SharedExclude, PriorBlock: prior}, data: out, exec: cur.Exec}
	if exists {
		w.op.Before = &state.Content{SHA256: cur.Hash, Exec: cur.Exec}
	}
	missing, err := a.missingDirs(common, name)
	if err != nil {
		return nil, false, err
	}
	w.op.Dirs = missing
	w.op.After = &state.Content{SHA256: contentHash(out), Exec: cur.Exec}
	return w, !exists, nil
}

// execute carries out writes in order and stops at the first failure. It
// gives the writes carried out, with the one that failed when it may have
// written: a home file, whose undo is made on it as it is, or a write that
// created directories; and the warnings about home files that lost their
// keys twice.
func (a runner) executeWrites(writes []write) (done []write, lost []string, err error) {
	for _, w := range writes {
		if err = a.ctx.Err(); err != nil {
			return done, lost, err
		}
		var l bool
		if l, err = a.carry(w); err != nil {
			if w.home || len(w.op.Dirs) > 0 {
				done = append(done, w)
			}
			return done, lost, err
		}
		done = append(done, w)
		if l {
			lost = append(lost, w.op.Target)
		}
	}
	return done, lost, nil
}
