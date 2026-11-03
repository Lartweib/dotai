package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"strings"

	"github.com/lartweib/dotai/internal/backup"
	"github.com/lartweib/dotai/internal/plan"
	"github.com/lartweib/dotai/internal/state"
)

// dotai doctor compares what dotai recorded with the disk and reports what
// changed outside dotai (decisions 0020, 0028 and 0041): a file of the
// install state or of a project record that was edited or removed, a key
// dotai owns in a file merged by key that was changed or removed, Claude
// Code's MCP servers among them, a project marker that does not say what
// its record says, an entry of the list of projects or a record whose
// project is gone, and a project whose exclude block is pending or missing.
// It writes nothing, except with Fix, which completes the exclude block of
// each project as its record lists it, with a backup and a journal as an
// apply does: the next apply would write that block too (decision 0041).

// Codes of the findings of dotai doctor, besides CodeRecordInvalid,
// CodeMarkerMismatch and plan.CodeExcludePending.
const (
	// CodeFileChanged: a file dotai owns whole holds something else than
	// what dotai wrote, or a file merged by key is no longer an object.
	CodeFileChanged = "file-changed"
	// CodeFileMissing: a file dotai owns is gone.
	CodeFileMissing = "file-missing"
	// CodeKeyChanged: a key dotai owns in a file merged by key holds
	// another value than the one dotai wrote.
	CodeKeyChanged = "key-changed"
	// CodeKeyMissing: a key dotai owns in a file merged by key is gone,
	// such as an MCP server of a project in Claude Code's .claude.json.
	CodeKeyMissing = "key-missing"
	// CodeStateInvalid: the install state, or the list of projects, cannot
	// be read.
	CodeStateInvalid = "state-invalid"
	// CodeInstallInterrupted and CodeApplyInterrupted: the install state,
	// or a project record, holds the journal of an operation that stopped
	// midway, which the next one rolls back; its files are not checked.
	CodeInstallInterrupted = "install-interrupted"
	CodeApplyInterrupted   = "apply-interrupted"
	// CodeOrphanEntry: an entry of ~/.dotai/projects.json whose project
	// directory or record is gone.
	CodeOrphanEntry = "orphan-entry"
	// CodeOrphanRecord: a project record whose project directory is gone.
	CodeOrphanRecord = "orphan-record"
	// CodeProjectMoved: the directory of a project record is now inside
	// another git worktree, whose top is the project an apply there sees.
	CodeProjectMoved = "project-moved"
	// CodeExcludeMissing: the exclude block of a project is missing from
	// the exclude file, or holds other entries than its record lists.
	CodeExcludeMissing = "exclude-missing"
)

// DoctorOptions are the choices of dotai doctor: Fix completes the
// exclude blocks it finds pending or missing.
type DoctorOptions struct {
	Fix bool
}

// DoctorResult is what dotai doctor found: the install state it checked,
// "" when there is none, the projects whose records it checked, each
// finding, and the backup of each project whose exclude block Fix wrote.
// Lists are never nil.
type DoctorResult struct {
	State    string      `json:"state"`
	Projects []string    `json:"projects"`
	Findings []Finding   `json:"findings"`
	Backups  []BackupRef `json:"backups"`
}

// Problems counts the findings that are not fixed.
func (r DoctorResult) Problems() int {
	n := 0
	for _, f := range r.Findings {
		if !f.Fixed {
			n++
		}
	}
	return n
}

// Finding is one thing dotai doctor found: its code, the harness of the
// file, "" for none, the project, "" at user level, the file, the key, a
// path from the top of the file, empty for the whole file, whether --fix
// can fix it and whether it did, and text for people.
type Finding struct {
	Code    string   `json:"code"`
	Harness string   `json:"harness"`
	Project string   `json:"project"`
	Path    string   `json:"path"`
	Key     []string `json:"key"`
	Fixable bool     `json:"fixable"`
	Fixed   bool     `json:"fixed"`
	Message string   `json:"message"`
}

// Doctor checks what dotai recorded in the home directory home against the
// disk, as the comment at the top of this file describes, asking the git
// on PATH about each project.
func Doctor(ctx context.Context, home string, opts DoctorOptions) (DoctorResult, error) {
	return doctor(ctx, osInstallFS{}, osGit{}, home, opts)
}

func doctor(ctx context.Context, d executeFS, g gitPort, home string, opts DoctorOptions) (DoctorResult, error) {
	if !filepath.IsAbs(home) {
		return DoctorResult{}, fmt.Errorf("home directory %q is not absolute", home)
	}
	home = filepath.Clean(home)
	doc := doctorRun{runner: runner{ctx: ctx, d: d, home: home, p: planned{ignoreExec: !d.ExecBit()}}, g: g,
		res: DoctorResult{Projects: []string{}, Findings: []Finding{}, Backups: []BackupRef{}}}
	if err := doc.checkState(); err != nil {
		return DoctorResult{}, err
	}
	if err := doc.checkProjects(); err != nil {
		return DoctorResult{}, err
	}
	fixes, err := doc.checkRecords()
	if err != nil {
		return DoctorResult{}, err
	}
	if opts.Fix {
		for _, fx := range fixes {
			ref, err := doc.fixExclude(fx)
			if err != nil {
				return DoctorResult{}, err
			}
			if ref != nil {
				doc.res.Backups = append(doc.res.Backups, *ref)
			}
			f := &doc.res.Findings[fx.finding]
			f.Fixed = true
			f.Message = fmt.Sprintf("wrote the %s of %s to %s", plural(len(fx.record.Exclude.Entries), "exclude entry", "exclude entries"), fx.record.Project, fx.scope.exclude)
		}
	}
	return doc.res, nil
}

// doctorRun is one run of dotai doctor: its runner, with the home
// directory, git, and the result so far.
type doctorRun struct {
	runner
	g   gitPort
	res DoctorResult
}

// add adds a finding.
func (doc *doctorRun) add(f Finding) {
	if f.Key == nil {
		f.Key = []string{}
	}
	doc.res.Findings = append(doc.res.Findings, f)
}

// checkState checks each file of the install state: a file dotai owns
// whole by its hash, a settings file by the keys dotai owns there, and a
// link by its destination.
func (doc *doctorRun) checkState() error {
	name := filepath.Join(doc.home, dotaiDir, stateFile)
	s, err := readState(doc.d, doc.home)
	if err != nil {
		doc.add(Finding{Code: CodeStateInvalid, Path: name, Message: fmt.Sprintf("the install state cannot be read: %v", err)})
		return nil
	}
	if s == nil {
		return nil
	}
	doc.res.State = name
	if s.Pending != nil {
		doc.add(Finding{Code: CodeInstallInterrupted, Path: name,
			Message: fmt.Sprintf("%s holds the journal of an install that stopped midway, so its files are not checked: run dotai install or dotai restore to settle it", name)})
		return nil
	}
	dirs := map[string]error{}
	for _, f := range s.Files {
		how := inspectHash
		switch {
		case f.Kind == state.KindLink:
			how = inspectLink
		case len(f.Keys) > 0:
			how = inspectRead
		}
		cur, data, ok, err := inspectTarget(doc.ctx, doc.d, doc.home, f.Target, dirs, how)
		if movedAway(doc.home, f.Target, err) {
			continue // the harness moved it; the next install settles it
		}
		if _, notRegular := errors.AsType[*NotRegularError](err); notRegular {
			doc.add(Finding{Code: CodeFileChanged, Harness: f.Harness, Path: f.Target, Message: fmt.Sprintf("%s: %v", f.Target, err)})
			continue
		}
		if err != nil {
			return err
		}
		switch {
		case !ok:
			doc.add(Finding{Code: CodeFileMissing, Harness: f.Harness, Path: f.Target, Message: fmt.Sprintf("%s, which dotai installed, is gone: install again to put it back", f.Target)})
		case f.Kind == state.KindLink:
			if !samePath(cur.Link, f.Link) {
				doc.add(Finding{Code: CodeFileChanged, Harness: f.Harness, Path: f.Target,
					Message: fmt.Sprintf("%s links to %s, not to %s, where dotai linked it", f.Target, cur.Link, f.Link)})
			}
		case len(f.Keys) > 0:
			doc.checkSettings(f, data)
		case cur.Hash != f.SHA256 || !doc.p.ignoreExec && cur.Exec != f.Exec:
			doc.add(Finding{Code: CodeFileChanged, Harness: f.Harness, Path: f.Target,
				Message: fmt.Sprintf("%s was changed since dotai installed it: an install stops on it until you move it away, and a restore asks before overwriting it", f.Target)})
		}
	}
	return nil
}

// checkSettings checks the keys dotai owns in the settings file of f,
// which holds data.
func (doc *doctorRun) checkSettings(f state.File, data []byte) {
	have, err := plan.SettingsKeys(f.Harness, data)
	if err != nil {
		doc.add(Finding{Code: CodeFileChanged, Harness: f.Harness, Path: f.Target, Message: fmt.Sprintf("%s is no longer a JSON object dotai can read key by key: %v", f.Target, err)})
		return
	}
	for _, k := range f.Keys {
		i := slices.IndexFunc(have, func(h plan.Key) bool { return slices.Equal(h.Path, k.Path) })
		switch {
		case i < 0:
			doc.add(Finding{Code: CodeKeyMissing, Harness: f.Harness, Path: f.Target, Key: k.Path,
				Message: fmt.Sprintf("the key %s that dotai set in %s is gone: install again to put it back", k.Name(), f.Target)})
		case have[i].SHA256 != k.SHA256:
			doc.add(Finding{Code: CodeKeyChanged, Harness: f.Harness, Path: f.Target, Key: k.Path,
				Message: fmt.Sprintf("the key %s that dotai set in %s holds another value now", k.Name(), f.Target)})
		}
	}
}

// checkProjects checks each entry of the list of projects whose keys dotai
// owns in a harness's home file: its project directory and its record must
// still exist, or nothing will remove those keys.
func (doc *doctorRun) checkProjects() error {
	name := filepath.Join(doc.home, dotaiDir, projectsFile)
	list, err := readProjects(doc.d, doc.home)
	if err != nil {
		doc.add(Finding{Code: CodeStateInvalid, Path: name, Message: fmt.Sprintf("the list of projects cannot be read: %v", err)})
		return nil
	}
	for _, e := range list.Projects {
		gone, err := doc.missing(e.Project)
		if err != nil {
			return err
		}
		why := "its directory is gone"
		if !gone {
			if gone, err = doc.missing(recordPath(doc.home, e.Project)); err != nil {
				return err
			}
			why = "its record " + recordPath(doc.home, e.Project) + " is gone"
		}
		if gone {
			doc.add(Finding{Code: CodeOrphanEntry, Project: e.Project, Path: name,
				Message: fmt.Sprintf("%s lists the project %s, but %s: the keys it owns in %s stay there until you remove them and its entry by hand", name, e.Project, why, e.File)})
		}
	}
	return nil
}

// missing reports whether name is gone.
func (doc *doctorRun) missing(name string) (bool, error) {
	_, err := doc.d.Lstat(name)
	if errors.Is(err, fs.ErrNotExist) {
		return true, nil
	}
	return false, err
}

// exclusion is an exclude block --fix completes: the project's record and
// what it may name, and the index of its finding.
type exclusion struct {
	record  state.Project
	scope   recordScope
	finding int
}

// checkRecords checks the record of each project under ~/.dotai/projects:
// that it is one an apply of its project writes, that its project is still
// there, each file and key it owns, the marker and the exclude block. It
// gives the exclude blocks --fix completes.
func (doc *doctorRun) checkRecords() ([]exclusion, error) {
	root := filepath.Join(doc.home, dotaiDir, projectsDir)
	if err := checkDir(doc.d, root); errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	} else if err != nil {
		return nil, err
	}
	ids, err := doc.d.List(root)
	if err != nil {
		return nil, err
	}
	slices.Sort(ids)
	var fixes []exclusion
	for _, id := range ids {
		fx, err := doc.checkRecord(filepath.Join(root, id), id)
		if err != nil {
			return nil, err
		}
		if fx != nil {
			fixes = append(fixes, *fx)
		}
	}
	return fixes, nil
}

// checkRecord checks the record in dir, the directory of the project id;
// a directory without one, which keeps only the backups of a restored
// project, is fine.
func (doc *doctorRun) checkRecord(dir, id string) (*exclusion, error) {
	name := filepath.Join(dir, recordName)
	data, _, ok, err := readTarget(doc.ctx, doc.d, doc.home, name)
	if _, notRegular := errors.AsType[*NotRegularError](err); notRegular {
		doc.add(Finding{Code: CodeRecordInvalid, Path: name, Message: err.Error()})
		return nil, nil
	}
	if err != nil || !ok {
		return nil, err
	}
	var head struct {
		Project string `json:"project"`
	}
	if err := json.Unmarshal(data, &head); err != nil || !filepath.IsAbs(head.Project) || projectID(head.Project) != id {
		doc.add(Finding{Code: CodeRecordInvalid, Path: name, Message: (&RecordInvalidError{Path: name, Why: "is not the record of the project its directory is named for"}).Error()})
		return nil, nil
	}
	project := head.Project
	if gone, err := doc.missing(project); err != nil {
		return nil, err
	} else if gone {
		doc.add(Finding{Code: CodeOrphanRecord, Project: project, Path: name,
			Message: fmt.Sprintf("the project %s is gone, but its record %s is kept, with its backups: what it owns outside the project, such as its MCP servers in Claude Code's .claude.json, stays until you remove it by hand", project, name)})
		return nil, nil
	}
	found, mcpKey, repo, err := findProject(doc.ctx, doc.g, project, doc.home)
	if _, refused := errors.AsType[*ProjectRefusedError](err); refused {
		doc.add(Finding{Code: CodeRecordInvalid, Project: project, Path: name, Message: (&RecordInvalidError{Path: name, Why: "is of " + err.Error()}).Error()})
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if !samePath(found, project) {
		doc.add(Finding{Code: CodeProjectMoved, Project: project, Path: name,
			Message: fmt.Sprintf("%s is now inside the git worktree %s, the project an apply there sees, so its record %s is not checked: run dotai apply in %s to apply there", project, found, name, found)})
		return nil, nil
	}
	sc := scopeOf(doc.home, project, mcpKey, repo)
	r, err := readRecord(doc.ctx, doc.d, sc)
	if _, invalid := errors.AsType[*RecordInvalidError](err); invalid {
		doc.add(Finding{Code: CodeRecordInvalid, Project: project, Path: name, Message: err.Error()})
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	doc.res.Projects = append(doc.res.Projects, project)
	if r.Pending != nil {
		doc.add(Finding{Code: CodeApplyInterrupted, Project: project, Path: name,
			Message: fmt.Sprintf("%s holds the journal of an apply or a restore that stopped midway, so the project is not checked: run dotai apply or dotai restore --project in %s to roll it back", name, project)})
		return nil, nil
	}
	for _, f := range r.Files {
		if err := doc.checkOwned(*r, sc, f); err != nil {
			return nil, err
		}
	}
	if err := doc.checkMarker(*r); err != nil {
		return nil, err
	}
	return doc.checkExclude(*r, sc)
}

// checkOwned checks f, a target the record r of the project of sc owns: a
// file owned whole by its hash, and a file merged by key, in the project
// or a harness's home file, by each key dotai owns there.
func (doc *doctorRun) checkOwned(r state.Project, sc recordScope, f state.ProjectFile) error {
	root, _, _ := sc.rootOf(f.Target) // checked when the record was read
	base := r.Project
	if root != r.Project {
		base = doc.home
	}
	how := inspectRead
	if f.Hash != "" {
		how = inspectHash
	}
	cur, data, ok, err := inspectTarget(doc.ctx, doc.d, base, f.Target, map[string]error{}, how)
	found := Finding{Harness: f.Harness, Project: r.Project, Path: f.Target}
	if _, notRegular := errors.AsType[*NotRegularError](err); notRegular {
		found.Code, found.Message = CodeFileChanged, fmt.Sprintf("%s: %v", f.Target, err)
		doc.add(found)
		return nil
	}
	switch {
	case err != nil:
		return err
	case !ok && f.Hash != "":
		found.Code, found.Message = CodeFileMissing, fmt.Sprintf("%s, which dotai applied, is gone: apply again to put it back", f.Target)
	case !ok:
		found.Code, found.Message = CodeFileMissing, fmt.Sprintf("%s, where dotai set %s, is gone: apply again to put them back", f.Target, plural(len(f.Keys), "key", "keys"))
	case f.Hash != "":
		if cur.Hash == f.Hash && (doc.p.ignoreExec || cur.Exec == f.Exec) {
			return nil
		}
		found.Code, found.Message = CodeFileChanged, fmt.Sprintf("%s was changed since dotai applied it: an apply or a restore --project asks before overwriting it", f.Target)
	default:
		doc.checkKeys(found, f, data)
		return nil
	}
	doc.add(found)
	return nil
}

// checkKeys checks each key dotai owns in f, a file merged by key that
// holds data, as found describes it.
func (doc *doctorRun) checkKeys(found Finding, f state.ProjectFile, data []byte) {
	for _, k := range f.Keys {
		have, present, err := plan.GetKey(data, k.Path)
		if err != nil {
			found.Code, found.Message = CodeFileChanged, fmt.Sprintf("%s is no longer a JSON object dotai can read key by key: %v", f.Target, err)
			doc.add(found)
			return
		}
		key := found
		key.Key = k.Path
		if !present {
			key.Code, key.Message = CodeKeyMissing, fmt.Sprintf("the key %s that dotai set in %s is gone: apply again in %s to put it back", projectKeyName(k.Path), f.Target, found.Project)
			doc.add(key)
			continue
		}
		if h, err := plan.ValueHash(have); err != nil || h != k.SHA256 {
			key.Code, key.Message = CodeKeyChanged, fmt.Sprintf("the key %s that dotai set in %s holds another value now", projectKeyName(k.Path), f.Target)
			doc.add(key)
		}
	}
}

// simpleKey is a key name that reads as it is after a dot.
var simpleKey = regexp.MustCompile(`^[A-Za-z0-9_$-]+$`)

// projectKeyName is a key path as a message names it: each name after a
// dot, or quoted in brackets when it is not a simple name, such as
// projects["C:/work"].mcpServers.browser.
func projectKeyName(p []string) string {
	var b strings.Builder
	for i, k := range p {
		switch {
		case !simpleKey.MatchString(k):
			fmt.Fprintf(&b, "[%q]", k)
		case i > 0:
			b.WriteString("." + k)
		default:
			b.WriteString(k)
		}
	}
	return b.String()
}

// checkMarker checks that the project marker says what the record r says,
// as an apply writes it.
func (doc *doctorRun) checkMarker(r state.Project) error {
	want, err := state.EncodeMarker(state.Marker{Schema: state.MarkerSchema, ID: r.ID, Pack: r.Pack, Variant: r.Variant, Profiles: r.Profiles})
	if err != nil {
		return err
	}
	target := filepath.Join(r.Project, filepath.FromSlash(plan.MarkerFile))
	data, _, ok, err := readTarget(doc.ctx, doc.d, r.Project, target)
	if _, notRegular := errors.AsType[*NotRegularError](err); !notRegular && err != nil {
		return err
	}
	if !ok || !bytes.Equal(data, want) {
		doc.add(Finding{Code: CodeMarkerMismatch, Project: r.Project, Path: target, Key: []string{},
			Message: fmt.Sprintf("%s does not say what the record %s says; the record wins, and the next apply rewrites the marker", target, recordPath(doc.home, r.Project))})
	}
	return nil
}

// checkExclude checks the exclude block of the record r of the project of
// sc: entries pending outside a git repository that can be written now
// that there is one, or a block that is missing or holds other entries.
// It gives the block --fix completes, nil when it is fine.
func (doc *doctorRun) checkExclude(r state.Project, sc recordScope) (*exclusion, error) {
	entries := r.Exclude.Entries
	if len(entries) == 0 {
		return nil, nil
	}
	f := Finding{Project: r.Project, Fixable: true}
	switch {
	case r.Exclude.Pending && sc.exclude == "":
		return nil, nil // still outside a git repository
	case r.Exclude.Pending:
		f.Code, f.Path = plan.CodeExcludePending, sc.exclude
		f.Message = fmt.Sprintf("%s is in a git repository now, but its %s are not in %s yet: run dotai doctor --fix, or apply again, to add them",
			r.Project, plural(len(entries), "exclude entry", "exclude entries"), sc.exclude)
	default:
		data, _, _, err := readTarget(doc.ctx, doc.d, filepath.Dir(filepath.Dir(r.Exclude.File)), r.Exclude.File)
		if _, notRegular := errors.AsType[*NotRegularError](err); notRegular {
			f.Code, f.Path, f.Fixable, f.Message = CodeExcludeMissing, r.Exclude.File, false, err.Error()
			doc.add(f)
			return nil, nil
		}
		if err != nil {
			return nil, err
		}
		if bytes.Equal(plan.WithExcludeBlock(data, r.Project, entries), data) {
			return nil, nil
		}
		f.Code, f.Path = CodeExcludeMissing, r.Exclude.File
		f.Message = fmt.Sprintf("the exclude block of %s in %s is missing or does not list what dotai created there, so git may show those files: run dotai doctor --fix, or apply again, to write it back",
			r.Project, r.Exclude.File)
	}
	doc.add(f)
	return &exclusion{record: r, scope: sc, finding: len(doc.res.Findings) - 1}, nil
}

// FixError is a dotai doctor --fix that stopped after it started writing:
// the Cause, the outcome of the rollback, nil when everything was put back,
// and the Backup directory, which is kept either way.
type FixError struct {
	Cause    error
	Rollback error
	Backup   string
}

func (e *FixError) Error() string {
	if e.Rollback == nil {
		return fmt.Sprintf("the fix stopped: %v; what it wrote was rolled back from the backup %s, which is kept", e.Cause, e.Backup)
	}
	return fmt.Sprintf("the fix stopped: %v; the rollback failed: %v; the backup %s holds what was there before, "+
		"and the project record keeps the journal: the next apply or restore --project rolls it back", e.Cause, e.Rollback, e.Backup)
}

func (e *FixError) Unwrap() []error {
	if e.Rollback == nil {
		return []error{e.Cause}
	}
	return []error{e.Cause, e.Rollback}
}

// fixExclude writes the exclude block of fx as its record lists it, under
// ~/.dotai/lock, with the record checked again: a backup of the exclude
// file, a journal in the record, the write, then the record, which names
// the exclude file and the backup. A failure rolls back from the backup. It
// gives the backup, nil when the block was already there.
func (doc *doctorRun) fixExclude(fx exclusion) (*BackupRef, error) {
	var ref *BackupRef
	r, sc := fx.record, fx.scope
	project := r.Project
	warnings := []string{}
	err := doc.locked(&warnings, func() error {
		again, err := readRecord(doc.ctx, doc.d, sc)
		if err != nil {
			return err
		}
		if again == nil || !reflect.DeepEqual(*again, r) {
			return &ChangedError{Path: recordPath(doc.home, project)}
		}
		var prior []string
		if !r.Exclude.Pending {
			data, _, _, err := readTarget(doc.ctx, doc.d, filepath.Dir(filepath.Dir(sc.exclude)), sc.exclude)
			if err != nil {
				return err
			}
			prior, _ = plan.ExcludeBlock(data, project)
		}
		w, created, err := doc.excludeWrite(sc.exclude, project, r.Exclude.Entries, prior)
		if err != nil || w == nil {
			return err
		}
		root := projectBackups(doc.home, project)
		if err := ensureDir(doc.d, doc.home, root, 0o700); err != nil {
			return fmt.Errorf("the backup failed, nothing was written: %w", err)
		}
		id, dir, now, err := newBackupDir(doc.d, root, doc.d.Now().UTC(), backup.OperationApply)
		if err != nil {
			return fmt.Errorf("the backup failed, nothing was written: %w", err)
		}
		writes := []write{*w}
		if err := doc.takeBackup(id, dir, project, now, writes, true); err != nil {
			return fmt.Errorf("the backup failed, nothing was written: %w", errors.Join(err, doc.d.RemoveAll(dir)))
		}
		j := r
		j.Pending = &state.ApplyJournal{Backup: id, Prior: priorCopy, Ops: []state.JournalOp{writes[0].op}}
		if err := doc.writeRecord(j); err != nil {
			return fmt.Errorf("the journal could not be written, nothing was written: %w", errors.Join(err, doc.d.RemoveAll(dir)))
		}
		next := r
		next.Backups = append(slices.Clone(r.Backups), id)
		next.Exclude = state.ProjectExclude{File: sc.exclude, Entries: r.Exclude.Entries, Created: created || r.Exclude.File == sc.exclude && r.Exclude.Created}
		done, _, err := doc.executeWrites(writes)
		if err == nil {
			err = doc.writeRecord(next)
		}
		if err != nil {
			rb := doc.undoWrites(project, dir, done)
			if rb == nil {
				rb = doc.putRecordBack(project, dir, priorCopy)
			}
			return &FixError{Cause: err, Rollback: rb, Backup: dir}
		}
		ref = &BackupRef{ID: id, Path: dir}
		return nil
	})
	if err != nil {
		return nil, err
	}
	for _, w := range warnings {
		doc.add(Finding{Code: CodeCleanupFailed, Project: project, Message: w})
	}
	return ref, nil
}
