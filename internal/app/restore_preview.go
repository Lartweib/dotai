package app

import (
	"context"
	"errors"
	"io/fs"
)

// errReadOnly refuses a write that a preview tried: a preview never writes.
var errReadOnly = errors.New("a preview writes nothing")

// readOnlyFS wraps an executeFS and refuses every write, so that a
// preview, which plans through the same code as the operation it
// previews, cannot write even by mistake.
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

// PreviewRestore plans what dotai restore of the backup id ("" for the
// last install) would do in the absolute home directory home, through the
// same code as Restore, and writes nothing. The result lists the installs
// the restore undoes, each write, the conflicts, which a restore
// overwrites only with consent, and whether no install is left after it
// (Uninstalled); Backup is nil, since no backup is taken. PreviewRestore
// refuses what Restore refuses before it asks, such as a name that is not
// a backup id or a home with no install state. It does not check the
// running harnesses, which Harnesses names. It does not settle a journal
// that a restore would settle first: a warning says so, and the plan
// follows the disk as it is.
func PreviewRestore(ctx context.Context, home, id string) (RestoreResult, error) {
	return previewRestore(ctx, readOnlyFS{osInstallFS{}}, home, id)
}

func previewRestore(ctx context.Context, d executeFS, home, id string) (RestoreResult, error) {
	home, err := restoreArgs(home, id)
	if err != nil {
		return RestoreResult{}, err
	}
	crossings, err := runner{ctx: ctx, d: d, home: home}.readCrossings()
	if err != nil {
		return RestoreResult{}, err
	}
	p, err := planRestore(ctx, d, home, id)
	if err != nil {
		return RestoreResult{}, err
	}
	res := p.res
	if len(crossings) > 0 {
		res.Warnings = append(res.Warnings, "a move across volumes was cut short: the restore settles it first, and this preview plans from the disk as it is now")
	}
	return res, nil
}

// PreviewProjectRestore plans what dotai restore --project would do for
// the project of the current directory cwd and the user whose home
// directory is home, through the same code as RestoreProject, and writes
// nothing. The result lists each write, the exclude block the restore
// takes out, and the conflicts, which it overwrites only with consent;
// Backup is nil. PreviewProjectRestore does not roll back a journal that
// a restore would roll back first: a warning says so, and the plan
// follows the record as it is.
func PreviewProjectRestore(ctx context.Context, cwd, home string) (ProjectRestoreResult, error) {
	return previewProjectRestore(ctx, readOnlyFS{osInstallFS{}}, osGit{}, cwd, home)
}

func previewProjectRestore(ctx context.Context, d executeFS, g gitPort, cwd, home string) (ProjectRestoreResult, error) {
	cwd, home, err := projectRestoreArgs(cwd, home)
	if err != nil {
		return ProjectRestoreResult{}, err
	}
	p, err := planRestoreOfProject(ctx, d, g, cwd, home)
	if err != nil {
		return ProjectRestoreResult{}, err
	}
	res := p.res
	if p.record.Pending != nil {
		res.Warnings = append(res.Warnings, "an apply or a restore of this project stopped midway: the restore rolls it back first, and this preview plans from the record as it is now")
	}
	return res, nil
}
