package app

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"

	"github.com/lartweib/dotai/internal/backup"
)

// errReadOnly is a write a preview tried: a preview never writes.
var errReadOnly = errors.New("a preview writes nothing")

// readOnlyFS is d with every write refused, so that a preview, which plans
// through the same code as the operation it previews, cannot write even
// by mistake.
type readOnlyFS struct{ executeFS }

func (readOnlyFS) StagingDir() (string, error)                         { return "", errReadOnly }
func (readOnlyFS) TempDir(string) (string, error)                      { return "", errReadOnly }
func (readOnlyFS) WriteFile(string, string, []byte, fs.FileMode) error { return errReadOnly }
func (readOnlyFS) Rename(string, string) error                         { return errReadOnly }
func (readOnlyFS) RemoveAll(string) error                              { return errReadOnly }
func (readOnlyFS) WriteIn(string, string, []byte, fs.FileMode) error   { return errReadOnly }
func (readOnlyFS) RemoveIn(string, string) error                       { return errReadOnly }
func (readOnlyFS) MkdirIn(string, string) error                        { return errReadOnly }
func (readOnlyFS) MoveIn(string, string, string) error                 { return errReadOnly }
func (readOnlyFS) CopyIn(context.Context, string, string, string) (int64, error) {
	return 0, errReadOnly
}
func (readOnlyFS) RemoveAllIn(string, string) error                      { return errReadOnly }
func (readOnlyFS) LinkIn(string, string, string) error                   { return errReadOnly }
func (readOnlyFS) WriteSynced(string, string, []byte, fs.FileMode) error { return errReadOnly }
func (readOnlyFS) SyncDir(string) error                                  { return errReadOnly }
func (readOnlyFS) Remove(string) error                                   { return errReadOnly }
func (readOnlyFS) Mkdir(string, fs.FileMode) error                       { return errReadOnly }
func (readOnlyFS) MkdirAll(string, fs.FileMode) error                    { return errReadOnly }
func (readOnlyFS) Lock(string) (func() error, error)                     { return nil, errReadOnly }

// PreviewRestore is what dotai restore of the backup id, "" for the last
// install, would do in the absolute home directory home, planned as
// Restore plans it, without writing anything: the installs it undoes,
// each write, and the conflicts, which a restore overwrites only with
// consent, listed instead of refused, and whether no install is left
// after it (Uninstalled). Backup is nil, since no backup is taken. It
// refuses what Restore refuses before it asks, such as a name that is not
// a backup id or a home with no install state, and does not check the
// running harnesses, which Harnesses names. A journal a restore would
// settle first is not settled: a warning says so, and the plan is the one
// from the disk as it is.
func PreviewRestore(ctx context.Context, home, id string) (RestoreResult, error) {
	return previewRestore(ctx, readOnlyFS{osInstallFS{}}, home, id)
}

func previewRestore(ctx context.Context, d executeFS, home, id string) (RestoreResult, error) {
	if !filepath.IsAbs(home) {
		return RestoreResult{}, fmt.Errorf("home directory %q is not absolute", home)
	}
	home = filepath.Clean(home)
	if _, ok := backup.ParseID(id); id != "" && !ok {
		return RestoreResult{}, &UsageError{fmt.Sprintf("%q is not a backup id, such as 20261029T101500.000Z_install", id)}
	}
	ru := runner{ctx: ctx, d: d, home: home}
	crossings, err := ru.readCrossings()
	if err != nil {
		return RestoreResult{}, err
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
	res, chain, err := r.planned(*s, id)
	if err != nil {
		return RestoreResult{}, err
	}
	// As the restore's write finds it: no install is left before the
	// oldest one undone.
	res.Uninstalled = len(chain) > 0 && chain[len(chain)-1].prior == nil
	if len(crossings) > 0 {
		res.Warnings = append(res.Warnings, "a move across volumes was cut short: the restore settles it first, and this preview plans from the disk as it is now")
	}
	return res, nil
}

// PreviewProjectRestore is what dotai restore --project would do for the
// project of the current directory cwd and the user whose home directory
// is home, planned as RestoreProject plans it, without writing anything:
// each write, the exclude block it takes out, and the conflicts, which it
// overwrites only with consent, listed instead of refused. Backup is nil.
// A journal of the project that a restore would roll back first is not
// rolled back: a warning says so, and the plan is the one from the record
// as it is.
func PreviewProjectRestore(ctx context.Context, cwd, home string) (ProjectRestoreResult, error) {
	return previewProjectRestore(ctx, readOnlyFS{osInstallFS{}}, osGit{}, cwd, home)
}

func previewProjectRestore(ctx context.Context, d executeFS, g gitPort, cwd, home string) (ProjectRestoreResult, error) {
	if !filepath.IsAbs(home) || !filepath.IsAbs(cwd) {
		return ProjectRestoreResult{}, fmt.Errorf("home directory %q and current directory %q must be absolute", home, cwd)
	}
	home, cwd = filepath.Clean(home), filepath.Clean(cwd)
	project, mcpKey, repo, err := findProject(ctx, g, cwd, home)
	if err != nil {
		return ProjectRestoreResult{}, err
	}
	sc := scopeOf(home, project, mcpKey, repo)
	r, err := readRecord(ctx, d, sc)
	if err != nil {
		return ProjectRestoreResult{}, err
	}
	if r == nil {
		return ProjectRestoreResult{}, fmt.Errorf("nothing to restore: %s has no project record %s", project, recordPath(home, project))
	}
	ru := runner{ctx: ctx, d: d, home: home, p: planned{ignoreExec: !d.ExecBit()}}
	res, _, err := ru.planProjectRestore(*r, sc)
	if err != nil {
		return ProjectRestoreResult{}, err
	}
	if r.Pending != nil {
		res.Warnings = append(res.Warnings, "an apply or a restore of this project stopped midway: the restore rolls it back first, and this preview plans from the record as it is now")
	}
	return res, nil
}
