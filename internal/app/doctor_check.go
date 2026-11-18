package app

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"slices"
	"strings"

	"github.com/lartweib/dotai/internal/plan"
	"github.com/lartweib/dotai/internal/state"
)

// The checks of dotai doctor (Doctor). Each one that cannot read what it
// checks reports it through failed and goes on with the rest.

// checkState checks each file of the install state: a file dotai owns
// whole by its hash, a settings file by the keys dotai owns there, and a
// link by its destination; and each hooks file of a harness, by the
// binary its session-start hook runs (checkHook).
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
		case len(f.Keys) > 0, f.Harness != "" && hookFile(f.Target):
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
			if err := doc.failed("", f.Target, err); err != nil {
				return err
			}
			continue
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
		if ok && f.Harness != "" && data != nil {
			doc.checkHook(f.Target, f.Harness, data)
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
				Message: fmt.Sprintf("the key %s that dotai set in %s is gone: install again to put it back", plan.KeyPath(k.Path), f.Target)})
		case have[i].SHA256 != k.SHA256:
			doc.add(Finding{Code: CodeKeyChanged, Harness: f.Harness, Path: f.Target, Key: k.Path,
				Message: fmt.Sprintf("the key %s that dotai set in %s holds another value now", plan.KeyPath(k.Path), f.Target)})
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
		why := "its directory is gone"
		gone, err := doc.missing(e.Project)
		if err == nil && !gone {
			gone, err = doc.missing(recordPath(doc.home, e.Project))
			why = "its record " + recordPath(doc.home, e.Project) + " is gone"
		}
		if err != nil {
			if err := doc.failed(e.Project, name, err); err != nil {
				return err
			}
			continue
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

// loaded is a valid project record and what it may name.
type loaded struct {
	record state.Project
	scope  recordScope
}

// writtenKey is the key of doctorRun.written for the key path of file.
func writtenKey(file string, path []string) string {
	return file + "\x00" + strings.Join(path, "\x00")
}

// checkRecords checks the record of each project under ~/.dotai/projects:
// first that it is one an apply of its project writes and that its project
// is still there, then each file and key it owns, the marker and the
// exclude block. It gives the exclude blocks --fix completes.
func (doc *doctorRun) checkRecords() ([]exclusion, error) {
	root := filepath.Join(doc.home, dotaiDir, projectsDir)
	err := checkDir(doc.d, root)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	var ids []string
	if err == nil {
		ids, err = doc.d.List(root)
	}
	if err != nil {
		return nil, doc.failed("", root, err)
	}
	slices.Sort(ids)
	var records []loaded
	for _, id := range ids {
		l, err := doc.loadRecord(filepath.Join(root, id), id)
		if err != nil {
			return nil, err
		}
		if l == nil {
			continue
		}
		records = append(records, *l)
		// The worktrees of a repository own a key of a home file jointly:
		// a value any of them wrote there is dotai's.
		for _, f := range l.record.Files {
			if slices.Contains(l.scope.homeFiles, f.Target) {
				for _, k := range f.Keys {
					doc.written[writtenKey(f.Target, k.Path)] = append(doc.written[writtenKey(f.Target, k.Path)], k.SHA256)
				}
			}
		}
	}
	var fixes []exclusion
	for _, l := range records {
		for _, f := range l.record.Files {
			if err := doc.checkOwned(l.record, l.scope, f); err != nil {
				return nil, err
			}
		}
		if err := doc.checkMarker(l.record); err != nil {
			return nil, err
		}
		if err := doc.checkOnboarding(l.record); err != nil {
			return nil, err
		}
		fx, err := doc.checkExclude(l.record, l.scope)
		if err != nil {
			return nil, err
		}
		if fx != nil {
			fixes = append(fixes, *fx)
		}
	}
	return fixes, nil
}

// loadRecord reads the record in dir, the directory of the project id, and
// gives it when it is valid, its project is there and it holds no journal;
// otherwise it reports why and gives nil. A directory without a record,
// which keeps only the backups of a restored project, is fine.
func (doc *doctorRun) loadRecord(dir, id string) (*loaded, error) {
	name := filepath.Join(dir, recordName)
	data, _, ok, err := readTarget(doc.ctx, doc.d, doc.home, name)
	if _, notRegular := errors.AsType[*NotRegularError](err); notRegular {
		doc.add(Finding{Code: CodeRecordInvalid, Path: name, Message: err.Error()})
		return nil, nil
	}
	if err != nil {
		return nil, doc.failed("", name, err)
	}
	if !ok {
		return nil, nil
	}
	project, err := projectNamedFor(data, id)
	if err != nil {
		doc.add(Finding{Code: CodeRecordInvalid, Path: name, Message: (&RecordInvalidError{Path: name, Why: "is not the record of the project its directory is named for"}).Error()})
		return nil, nil
	}
	gone, err := doc.missing(project)
	if err != nil {
		return nil, doc.failed(project, project, err)
	}
	if gone {
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
		return nil, doc.failed(project, project, err)
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
	if err != nil || r == nil {
		return nil, doc.failed(project, name, cmpErr(err, "the record went away while doctor read it"))
	}
	doc.res.Projects = append(doc.res.Projects, project)
	if r.Pending != nil {
		doc.add(Finding{Code: CodeApplyInterrupted, Project: project, Path: name,
			Message: fmt.Sprintf("%s holds the journal of an apply or a restore that stopped midway, so the project is not checked: run dotai apply or dotai restore --project in %s to roll it back", name, project)})
		return nil, nil
	}
	return &loaded{record: *r, scope: sc}, nil
}

// cmpErr is err, or an error of msg when it is nil.
func cmpErr(err error, msg string) error {
	if err == nil {
		return errors.New(msg)
	}
	return err
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
		return doc.failed(r.Project, f.Target, err)
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
// holds data, as found describes it. A key whose value is the one another
// worktree's record says dotai wrote there, as the worktrees of a
// repository own the keys of Claude Code's projects entry jointly, did not
// change outside dotai.
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
			key.Code, key.Message = CodeKeyMissing, fmt.Sprintf("the key %s that dotai set in %s is gone: apply again in %s to put it back", plan.KeyPath(k.Path), f.Target, found.Project)
			doc.add(key)
			continue
		}
		h, err := plan.ValueHash(have)
		if err == nil && (h == k.SHA256 || slices.Contains(doc.written[writtenKey(f.Target, k.Path)], h)) {
			continue
		}
		key.Code, key.Message = CodeKeyChanged, fmt.Sprintf("the key %s that dotai set in %s holds another value now", plan.KeyPath(k.Path), f.Target)
		doc.add(key)
	}
}

// checkMarker checks that the project marker says what the record r says,
// as an apply writes it.
func (doc *doctorRun) checkMarker(r state.Project) error {
	if r.DismissOnly {
		return nil // no apply ran, so there is no marker (decision 0045)
	}
	want, err := state.EncodeMarker(state.Marker{Schema: state.MarkerSchema, ID: r.ID, Pack: r.Pack, Variant: r.Variant, Profiles: r.Profiles})
	if err != nil {
		return err
	}
	target := filepath.Join(r.Project, filepath.FromSlash(plan.MarkerFile))
	data, _, ok, err := readTarget(doc.ctx, doc.d, r.Project, target)
	if _, notRegular := errors.AsType[*NotRegularError](err); !notRegular && err != nil {
		return doc.failed(r.Project, target, err)
	}
	if !ok || !bytes.Equal(data, want) {
		doc.add(Finding{Code: CodeMarkerMismatch, Project: r.Project, Path: target,
			Message: fmt.Sprintf("%s does not say what the record %s says; the record wins, and the next apply rewrites the marker", target, recordPath(doc.home, r.Project))})
	}
	return nil
}

// checkOnboarding checks the onboarding record of the project of r, in a
// git repository, as an apply or a dismissal writes it: one dotai can
// read. What it holds is checked by the session-start hook, which compares
// its fingerprint, never by doctor.
func (doc *doctorRun) checkOnboarding(r state.Project) error {
	if r.Exclude.File == "" {
		return nil // outside a git repository, where neither writes it
	}
	target := filepath.Join(r.Project, filepath.FromSlash(plan.OnboardingFile))
	data, _, ok, err := readTarget(doc.ctx, doc.d, r.Project, target)
	if _, notRegular := errors.AsType[*NotRegularError](err); !notRegular && err != nil {
		return doc.failed(r.Project, target, err)
	}
	if ok {
		if _, err = state.DecodeOnboarding(data); err == nil {
			return nil
		}
	}
	doc.add(Finding{Code: CodeOnboardingRecordInvalid, Project: r.Project, Path: target,
		Message: fmt.Sprintf("%s is missing or is not an onboarding record dotai writes, so the session-start hook says the profiles may no longer fit; dotai doctor --fix cannot take its fingerprint: run dotai apply, or dotai onboarding dismiss, in %s", target, r.Project)})
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
			return nil, doc.failed(r.Project, r.Exclude.File, err)
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
