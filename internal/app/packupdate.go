package app

import (
	"context"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"
)

// WarnCloneMissing is an update of a pack whose clone was gone, which it
// fetched again.
const WarnCloneMissing = "pack-clone-missing"

// PackUpdatePlan is what an update of a git pack changes, as fetched and
// checked: the pack, its source before and after, the changes of its
// executable content, sorted by path, and the warnings of the new source.
// Missing says the clone was gone: every file shows as added. The web UI
// confirms an update by the digest of its plan.
type PackUpdatePlan struct {
	ID       string        `json:"id"`
	Path     string        `json:"path"`
	From     PackSource    `json:"from"`
	To       PackSource    `json:"to"`
	Missing  bool          `json:"missing"`
	Changes  []ExecChange  `json:"changes"`
	Warnings []PackWarning `json:"warnings"`
}

// Moves reports whether the update installs another commit.
func (p PackUpdatePlan) Moves() bool { return p.From.Commit != p.To.Commit }

// PackConsentError is an update that would install another commit
// without the user's consent: no --yes, and no terminal to ask on.
// Nothing changed; Plan is what it would do.
type PackConsentError struct {
	Plan PackUpdatePlan
}

func (e *PackConsentError) Error() string {
	return fmt.Sprintf("the update of %s from %s to %s changes %d files of executable content and needs consent: run it with --yes once you have read the plan", e.Plan.ID, ShortCommit(e.Plan.From.Commit), ShortCommit(e.Plan.To.Commit), len(e.Plan.Changes))
}

// UpdatePack fetches the recorded full ref of the git pack id again and,
// when it is at another commit, shows the plan to opts.Confirm, asks
// opts.Consent and replaces the clone; a clone that is gone is fetched
// again. An unreachable remote or a failed check leaves the pack as it
// was.
func UpdatePack(ctx context.Context, home, id string, opts PackGitOptions) (PackAddResult, error) {
	home, err := requireAbs(home)
	if err != nil {
		return PackAddResult{}, err
	}
	entry, err := gitEntry(home, id)
	if err != nil {
		return PackAddResult{}, err
	}
	g, done, err := newPackGit(ctx, opts.Interactive)
	if err != nil {
		return PackAddResult{}, err
	}
	defer done()
	return updatePack(ctx, g, home, entry, GitSource{URL: entry.Source.URL, ID: entry.ID}, entry.Source.Ref, false, opts)
}

// PlanPackUpdate is the plan of UpdatePack, which it fetches and checks
// in a temporary directory that it deletes: nothing else is written. A
// deletion that fails is a warning, outside the plan.
func PlanPackUpdate(ctx context.Context, home, id string, opts PackGitOptions) (PackUpdatePlan, []PackWarning, error) {
	home, err := requireAbs(home)
	if err != nil {
		return PackUpdatePlan{}, nil, err
	}
	entry, err := gitEntry(home, id)
	if err != nil {
		return PackUpdatePlan{}, nil, err
	}
	g, done, err := newPackGit(ctx, opts.Interactive)
	if err != nil {
		return PackUpdatePlan{}, nil, err
	}
	defer done()
	p, f, err := planUpdate(ctx, g, home, entry, GitSource{URL: entry.Source.URL, ID: entry.ID}, entry.Source.Ref, false)
	if err != nil {
		return PackUpdatePlan{}, nil, err
	}
	warnings := []PackWarning{}
	if err := removeFetch(f.dir); err != nil {
		warnings = append(warnings, PackWarning{Code: WarnCleanupFailed, Message: err.Error()})
	}
	return p, warnings, nil
}

// gitEntry is the registry's entry of the git pack id.
func gitEntry(home, id string) (packsItem, error) {
	if id == "" {
		return packsItem{}, &UsageError{msg: "the pack identity is empty"}
	}
	doc, err := readPacks(home)
	if err != nil {
		return packsItem{}, err
	}
	for _, p := range doc.Packs {
		if p.ID == id && p.Source != nil {
			return p, nil
		}
		if p.ID == id {
			return packsItem{}, &UsageError{msg: fmt.Sprintf("%s is a local pack, which dotai does not fetch: edit its directory, or add it again with --dir", id)}
		}
	}
	return packsItem{}, &UsageError{msg: fmt.Sprintf("the registry lists no git pack %s: dotai pack list shows the identities", id)}
}

// planUpdate fetches ref of src ("" for the default branch) for the
// registered entry with g, and plans the update against the clone, or
// against nothing when the clone is gone. The fetch is left for the caller
// to place or clean up. explicit says the user gave ref, as dotai pack add
// --ref does, rather than the update reusing the recorded one.
func planUpdate(ctx context.Context, g packGit, home string, entry packsItem, src GitSource, ref string, explicit bool) (PackUpdatePlan, fetched, error) {
	// The recorded ref is fetched by its full name, as it was resolved
	// when the pack was added: a tag that wins over a branch of the same
	// name, or the default branch of then. latest is resolved again, to the
	// release that is the latest now, when the user gives it or the pack
	// follows it; a latest recorded as a branch or a tag before it meant a
	// release stays that one until the user gives latest again.
	fetchRef := ref
	switch {
	case ref == LatestRef && (explicit || entry.Source.TracksLatest()):
	case ref == entry.Source.Ref:
		fetchRef = entry.Source.FullRef
	}
	f, err := fetchChecked(ctx, g, home, src, fetchRef)
	if err != nil {
		return PackUpdatePlan{}, fetched{}, err
	}
	to := PackSource{URL: src.URL, Ref: ref, FullRef: f.fullRef, Commit: f.commit}
	// A pack that follows latest never goes down by itself: the release it
	// follows was deleted or its tag was lowered, which the user decides.
	if entry.Source.TracksLatest() && to.TracksLatest() && lowerRelease(to.FullRef, entry.Source.FullRef) {
		return PackUpdatePlan{}, fetched{}, errors.Join(&LatestDowngradeError{ID: entry.ID,
			From: strings.TrimPrefix(entry.Source.FullRef, "refs/tags/"), To: strings.TrimPrefix(to.FullRef, "refs/tags/")}, removeFetch(f.dir))
	}
	old, oldModes := entry.Path, map[string]string{}
	_, statErr := os.Stat(entry.Path)
	missing := errors.Is(statErr, os.ErrNotExist)
	if missing {
		old = ""
	} else {
		// The modes of the clone, as its own commit records them; unknown
		// when git cannot read them there, such as a clone without .git.
		oldModes = g.treeModes(ctx, entry.Path)
	}
	changes, err := execChanges(old, f.dir, oldModes, f.modes)
	if err != nil {
		return PackUpdatePlan{}, fetched{}, errors.Join(err, removeFetch(f.dir))
	}
	p := PackUpdatePlan{ID: entry.ID, Path: entry.Path, From: *entry.Source, To: to, Missing: missing, Changes: changes, Warnings: refWarnings(to)}
	return p, f, nil
}

// updatePack plans the update of entry to ref of src and carries it out:
// Confirm sees the plan first; a plan that moves the pack to another
// commit needs Consent; one at the same commit only records the source,
// unless the clone is gone, when it is placed again.
func updatePack(ctx context.Context, g packGit, home string, entry packsItem, src GitSource, ref string, explicit bool, opts PackGitOptions) (res PackAddResult, err error) {
	p, f, err := planUpdate(ctx, g, home, entry, src, ref, explicit)
	if err != nil {
		return PackAddResult{}, err
	}
	defer f.cleanUp(&res, &err)
	if opts.Confirm != nil {
		if err := opts.Confirm(p); err != nil {
			return PackAddResult{}, err
		}
	}
	unchanged := func(doc packsDoc) error {
		i := slices.IndexFunc(doc.Packs, func(q packsItem) bool { return q.ID == entry.ID })
		if i < 0 || doc.Packs[i].Source == nil || *doc.Packs[i].Source != *entry.Source {
			return fmt.Errorf("the pack %s changed in the registry while it was fetched: run the command again", entry.ID)
		}
		return nil
	}
	if !p.Moves() && !p.Missing {
		list, err := recordSource(home, entry.ID, p.To, unchanged)
		if err != nil {
			return PackAddResult{}, err
		}
		res := addResult(list, byID(entry.ID), PackUnchanged, slices.Clone(p.Warnings))
		res.Update = &p
		return res, nil
	}
	if p.Moves() {
		ok := false
		if opts.Consent != nil {
			if ok, err = opts.Consent(p); err != nil {
				return PackAddResult{}, err
			}
		}
		if !ok {
			return PackAddResult{}, &PackConsentError{Plan: p}
		}
	}
	list, deleteWarn, err := placePack(home, entry.ID, f.dir, p.To, unchanged)
	if err != nil {
		return PackAddResult{}, err
	}
	warnings := append(slices.Clone(p.Warnings), deleteWarn...)
	if p.Missing {
		warnings = append(warnings, PackWarning{Code: WarnCloneMissing, Message: fmt.Sprintf("the clone of %s at %s was gone: it was fetched again", entry.ID, entry.Path)})
	}
	res = addResult(list, byID(entry.ID), PackUpdated, warnings)
	res.Update = &p
	return res, nil
}

// recordSource records src as the source of the pack id, under the lock,
// once check accepts the registry, without touching its clone: an update
// or an addition again at the same commit keeps the URL and the ref last
// used.
func recordSource(home, id string, src PackSource, check func(packsDoc) error) (PackList, error) {
	return editPacks(home, func(doc *packsDoc) (bool, error) {
		if err := check(*doc); err != nil {
			return false, err
		}
		i := slices.IndexFunc(doc.Packs, func(q packsItem) bool { return q.ID == id })
		if *doc.Packs[i].Source == src {
			return false, nil
		}
		doc.Packs[i].Source = &src
		return true, nil
	})
}
