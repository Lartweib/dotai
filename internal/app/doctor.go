package app

import (
	"context"
	"fmt"
)

// Codes of the findings of dotai doctor, besides CodeRecordInvalid,
// CodeMarkerMismatch, CodeCleanupFailed and plan.CodeExcludePending.
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
	// CodeCheckFailed: something doctor had to read or ask could not be,
	// such as a file it may not read or git, which failed or timed out;
	// what depends on it is not checked, and the rest is.
	CodeCheckFailed = "check-failed"
	// CodeOnboardingRecordInvalid: the onboarding record of a project in a
	// git repository, .dotai/onboarding.json, is missing or is not one
	// dotai writes, so its session-start hook says the profiles may no
	// longer fit (decision 0045).
	CodeOnboardingRecordInvalid = "onboarding-record-invalid"
)

// DoctorOptions are the choices of dotai doctor: Fix completes the
// exclude blocks it finds pending or missing. Confirm, when it is not nil
// and Fix is set, is asked with what doctor found, as it finds it without
// Fix, before the first fix: an error stops doctor, which fixes nothing.
type DoctorOptions struct {
	Fix     bool
	Confirm func(DoctorResult) error
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

// Doctor compares what dotai recorded in the home directory home with the
// disk and reports what changed outside dotai (decisions 0020, 0028 and
// 0041), asking the git on PATH about each project: a file of the install
// state or of a project record that was edited or removed, a key dotai
// owns in a file merged by key that was changed or removed, Claude Code's
// MCP servers among them, a project marker that does not say what its
// record says, an entry of the list of projects or a record whose project
// is gone, and a project whose exclude block is pending or missing (the
// checks are in doctor_check.go). What cannot be read or asked is a
// finding too, and the rest is still checked; only a cancelled ctx stops
// it. It writes nothing, except with opts.Fix, which completes the exclude
// block of each project as its record lists it, with a backup and a
// journal as an apply has (doctor_fix.go): the next apply would write that
// block too. A fix that fails stops the fixes and gives its error with
// what was found and fixed so far.
func Doctor(ctx context.Context, home string, opts DoctorOptions) (DoctorResult, error) {
	return doctor(ctx, osInstallFS{}, osGit{}, home, opts)
}

func doctor(ctx context.Context, d executeFS, g gitPort, home string, opts DoctorOptions) (DoctorResult, error) {
	home, err := requireAbs(home)
	if err != nil {
		return DoctorResult{}, err
	}
	doc := doctorRun{runner: runner{ctx: ctx, d: d, home: home, p: planned{ignoreExec: !d.ExecBit()}}, g: g, written: map[string][]string{},
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
	if !opts.Fix {
		return doc.res, nil
	}
	if opts.Confirm != nil {
		if err := opts.Confirm(doc.res); err != nil {
			return DoctorResult{}, err
		}
	}
	for _, fx := range fixes {
		if err := doc.fix(fx); err != nil {
			return doc.res, err
		}
	}
	return doc.res, nil
}

// doctorRun is one run of dotai doctor: its runner, with the home
// directory, git, the hashes the valid records say dotai wrote at each key
// of a harness's home file, by writtenKey, and the result so far.
type doctorRun struct {
	runner
	g       gitPort
	written map[string][]string
	res     DoctorResult
}

// add adds a finding.
func (doc *doctorRun) add(f Finding) {
	if f.Key == nil {
		f.Key = []string{}
	}
	doc.res.Findings = append(doc.res.Findings, f)
}

// failed reports err, which kept doctor from checking path of project ("" at
// user level), as a finding, and gives nil so that doctor goes on; a
// cancelled run gives its error instead, which stops it.
func (doc *doctorRun) failed(project, path string, err error) error {
	if cerr := doc.ctx.Err(); cerr != nil {
		return cerr
	}
	doc.add(Finding{Code: CodeCheckFailed, Project: project, Path: path, Message: fmt.Sprintf("%s could not be checked: %v", path, err)})
	return nil
}
