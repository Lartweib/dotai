package app

import (
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"slices"
	"time"

	"github.com/lartweib/dotai/internal/backup"
	"github.com/lartweib/dotai/internal/plan"
	"github.com/lartweib/dotai/internal/state"
)

// The fix of dotai doctor (Doctor): the exclude block of a project written
// as its record lists it, journaled as an apply is, in a backup of its own
// operation, backup.OperationFix.

// exclusion is an exclude block --fix completes: the project's record and
// what it may name, and the index of its finding.
type exclusion struct {
	record  state.Project
	scope   recordScope
	finding int
}

// fix completes the exclude block of fx and marks its finding fixed, with
// the backup it took. Its error is an *ApplyError of the operation "fix"
// once it started writing, after which everything it wrote is rolled back.
func (doc *doctorRun) fix(fx exclusion) error {
	warnings := []string{}
	var ref *BackupRef
	err := doc.locked(&warnings, func() error {
		w, next, err := doc.prepareExclude(fx)
		if err != nil || w == nil {
			return err
		}
		ref, err = doc.writeExclude(fx.record, *w, next)
		return err
	})
	for _, w := range warnings {
		doc.add(Finding{Code: CodeCleanupFailed, Project: fx.record.Project, Message: w})
	}
	if err != nil {
		return err
	}
	if ref != nil {
		doc.res.Backups = append(doc.res.Backups, *ref)
	}
	f := &doc.res.Findings[fx.finding]
	f.Fixed = true
	f.Message = fmt.Sprintf("wrote the %s of %s to %s", plural(len(fx.record.Exclude.Entries), "exclude entry", "exclude entries"), fx.record.Project, fx.scope.exclude)
	return nil
}

// prepareExclude checks, under the lock, that the record of fx is still
// the one doctor read, and gives the write of its exclude block, with the
// project's block as it is for its journal, and the record that follows
// it, without the backup; a nil write when the block is there already.
func (doc *doctorRun) prepareExclude(fx exclusion) (*write, state.Project, error) {
	r, sc := fx.record, fx.scope
	again, err := readRecord(doc.ctx, doc.d, sc)
	if err != nil {
		return nil, state.Project{}, err
	}
	if again == nil || !reflect.DeepEqual(*again, r) {
		return nil, state.Project{}, &ChangedError{Path: recordPath(doc.home, r.Project)}
	}
	var prior []string
	if !r.Exclude.Pending {
		data, _, _, err := readTarget(doc.ctx, doc.d, filepath.Dir(filepath.Dir(sc.exclude)), sc.exclude)
		if err != nil {
			return nil, state.Project{}, err
		}
		prior, _ = plan.ExcludeBlock(data, r.Project)
	}
	w, created, err := doc.excludeWrite(sc.exclude, r.Project, r.Exclude.Entries, prior)
	if err != nil || w == nil {
		return nil, state.Project{}, err
	}
	next := r
	next.Exclude = state.ProjectExclude{File: sc.exclude, Entries: r.Exclude.Entries, Created: created || r.Exclude.File == sc.exclude && r.Exclude.Created}
	return w, next, nil
}

// writeExclude carries out w, the write of the exclude block of the
// project of the record r, journaled in r, and then writes next, which
// names the backup too.
func (doc *doctorRun) writeExclude(r state.Project, w write, next state.Project) (*BackupRef, error) {
	ref, _, err := doc.journaled(journaledWrite{project: r.Project, operation: backup.OperationFix, what: "fix", base: r, prior: true, writes: []write{w}},
		func(id string, _ time.Time) error {
			next.Backups = append(slices.Clone(r.Backups), id)
			return doc.writeRecord(next)
		})
	if js, ok := errors.AsType[*journalStop](err); ok {
		return nil, &ApplyError{Operation: "fix", Cause: js.cause, Rollback: js.rollback, Backup: js.backup}
	}
	if err != nil {
		return nil, err
	}
	return &ref, nil
}
