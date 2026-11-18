package app

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// Adding a git pack (decision 0044). A fetch goes into a temporary
// sibling of ~/.dotai/packs/<identity>, is checked and validated there,
// and only then takes the clone's place, under the lock, with the
// registry.

// The warning codes of an addition or an update.
const (
	WarnRefBranch      = "ref-branch"
	WarnPackSameCommit = "pack-same-commit"
	WarnDeleteFailed   = "pack-delete-failed"
	WarnCleanupFailed  = "pack-cleanup-failed"
)

// PackGitOptions are the choices of an addition or an update of a git
// pack. Ref is a tag, a branch or a full commit SHA, "" for the default
// branch, or, for a pack already added, the ref it records. Interactive
// leaves git's prompts on, as on the command line. An update first shows
// Confirm, when it is set, the plan it is about to carry out (the web UI's
// digest), then, when the plan moves the pack to another commit, asks
// Consent, the prompt or --yes; without consent it is a *PackConsentError.
type PackGitOptions struct {
	Ref         string
	Interactive bool
	Confirm     func(PackUpdatePlan) error
	Consent     func(PackUpdatePlan) (bool, error)
}

// The steps of a placement a test can fail through packFault.
const (
	faultPlace = "place" // before the checked clone takes its place
	faultWrite = "write" // before the registry is written
)

// packFault lets a test fail a step of a placement.
var packFault = func(step string) error { return nil }

// AddGitPack fetches the pack at rawURL, a GitHub repository, checks its
// tree, validates it and adds it to the registry of home, cloned at
// ~/.dotai/packs/<identity>. On any failure nothing is registered and the
// fetch is deleted; a fetch that cannot be deleted is a warning. A pack of
// that identity already added is updated, as UpdatePack does, and keeps
// the URL last used.
func AddGitPack(ctx context.Context, home, rawURL string, opts PackGitOptions) (res PackAddResult, err error) {
	home, err = requireAbs(home)
	if err != nil {
		return PackAddResult{}, err
	}
	src, err := ParsePackURL(rawURL)
	if err != nil {
		return PackAddResult{}, err
	}
	if err := CheckPackRef(opts.Ref); err != nil {
		return PackAddResult{}, err
	}
	g, done, err := newPackGit(ctx, opts.Interactive)
	if err != nil {
		return PackAddResult{}, err
	}
	defer done()
	doc, err := readPacks(home)
	if err != nil {
		return PackAddResult{}, err
	}
	// The same identity is the same pack, from any form or case of its
	// URL: adding it again is an update, to another ref when one is given.
	if i := slices.IndexFunc(doc.Packs, func(p packsItem) bool { return p.ID == src.ID && p.Source != nil }); i >= 0 {
		entry := doc.Packs[i]
		ref := opts.Ref
		if ref == "" {
			ref = entry.Source.Ref
		}
		return updatePack(ctx, g, home, entry, src, ref, opts)
	}
	f, err := fetchChecked(ctx, g, home, src, opts.Ref)
	if err != nil {
		return PackAddResult{}, err
	}
	defer f.cleanUp(&res, &err)
	source := PackSource{URL: src.URL, Ref: opts.Ref, FullRef: f.fullRef, Commit: f.commit}
	warnings := append(refWarnings(source), f.warnings...)
	list, deleteWarn, err := placePack(home, src.ID, f.dir, source, func(doc packsDoc) error {
		if slices.ContainsFunc(doc.Packs, func(p packsItem) bool { return p.ID == src.ID }) {
			return fmt.Errorf("the pack %s was added while it was fetched: run the command again", src.ID)
		}
		for _, p := range doc.Packs {
			if p.Source != nil && p.Source.Commit == source.Commit {
				warnings = append(warnings, PackWarning{Code: WarnPackSameCommit,
					Message: fmt.Sprintf("%s is at the commit %s of %s, which is already added: a renamed or transferred repository keeps its first identity, so remove one of them if they are the same pack", src.ID, ShortCommit(source.Commit), p.ID)})
			}
		}
		return nil
	})
	if err != nil {
		return PackAddResult{}, err
	}
	return addResult(list, byID(src.ID), PackAdded, append(warnings, deleteWarn...)), nil
}

// fetched is a checked and validated fetch of a pack in dir, a temporary
// sibling of its clone path, at commit, of the ref named fullRef, with the
// modes of its tree by path, until cleanUp removes what is left of it.
type fetched struct {
	dir     string
	commit  string
	fullRef string
	modes   map[string]string
	// warnings are what the validation found to tell, such as files of the
	// template left unchanged.
	warnings []PackWarning
}

// cleanUp removes what is left of the fetch once the operation that holds
// it ends: nothing, once its clone took its place. A removal that fails is
// joined to *err, or, when the operation worked, a warning of *res.
func (f fetched) cleanUp(res *PackAddResult, err *error) {
	cerr := removeFetch(f.dir)
	switch {
	case cerr == nil:
	case *err != nil:
		*err = errors.Join(*err, cerr)
	default:
		res.Warnings = append(res.Warnings, PackWarning{Code: WarnCleanupFailed, Message: cerr.Error()})
	}
}

// removeFetch removes the temporary directory of a fetch.
func removeFetch(dir string) error {
	if err := os.RemoveAll(dir); err != nil {
		return fmt.Errorf("the temporary fetch %s could not be deleted: %v; delete it by hand", dir, err)
	}
	return nil
}

// fetchChecked fetches ref of src with g into a temporary sibling of its
// clone path in home, checks its tree and validates it. On an error
// nothing of the fetch is left, or the error says what is. The directories
// of the owner and of the host stay, since a concurrent addition may use
// them.
func fetchChecked(ctx context.Context, g packGit, home string, src GitSource, ref string) (fetched, error) {
	target, err := clonePath(home, src.ID)
	if err != nil {
		return fetched{}, err
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return fetched{}, err
	}
	dir, err := os.MkdirTemp(filepath.Dir(target), "."+filepath.Base(target)+".fetch-")
	if err != nil {
		return fetched{}, err
	}
	got, err := g.fetchPack(ctx, dir, src.URL, ref)
	var res ValidateResult
	if err == nil {
		res, err = Validate(dir, Selection{})
		if err == nil && (!res.Valid || res.Pack == nil) {
			err = &PackInvalidError{Path: fmt.Sprintf("%s at %s", src.URL, ShortCommit(got.Commit)), Result: res}
		}
	}
	if err != nil {
		if cerr := removeFetch(dir); cerr != nil {
			err = errors.Join(err, cerr)
		}
		return fetched{}, err
	}
	return fetched{dir: dir, commit: got.Commit, fullRef: got.FullRef, modes: got.Modes, warnings: templatePackWarnings(res)}, nil
}

// refWarnings warns of a branch, which can move.
func refWarnings(src PackSource) []PackWarning {
	if branch, ok := strings.CutPrefix(src.FullRef, "refs/heads/"); ok {
		return []PackWarning{{Code: WarnRefBranch, Message: fmt.Sprintf("the branch %s can move: the pack stays at the commit %s until dotai pack update", branch, ShortCommit(src.Commit))}}
	}
	return []PackWarning{}
}

// ShortCommit is the first 12 digits of a commit, for people.
func ShortCommit(commit string) string { return commit[:min(12, len(commit))] }

// clonePath is ~/.dotai/packs/<identity> in home, for a GitHub identity
// only.
func clonePath(home, id string) (string, error) {
	owner, repo, ok := gitHubID(id)
	if !ok {
		return "", fmt.Errorf("%q is not the identity of a GitHub pack", id)
	}
	return filepath.Join(home, dotaiDir, packsDir, gitHubHost, owner, repo), nil
}

// placePack moves the checked clone dir to the clone path of id and
// registers it with src, under the lock, once check accepts the registry
// as it reads then. What is at the clone path is moved aside first and
// deleted at the end, or moved back on any failure; a deletion that fails
// is a warning. dir is gone on success.
func placePack(home, id, dir string, src PackSource, check func(packsDoc) error) (PackList, []PackWarning, error) {
	target, err := clonePath(home, id)
	if err != nil {
		return PackList{}, nil, err
	}
	var list PackList
	var warnings []PackWarning
	recovered, err := withPacksLock(home, func() error {
		doc, err := readPacks(home)
		if err != nil {
			return err
		}
		if err := check(doc); err != nil {
			return err
		}
		old, err := moveAside(target, "old")
		if err != nil {
			return err
		}
		if err := packFault(faultPlace); err != nil {
			return errors.Join(err, old.restore())
		}
		if err := os.Rename(dir, target); err != nil {
			return errors.Join(err, old.restore())
		}
		entry := packsItem{ID: id, Path: target, Source: &src}
		if i := slices.IndexFunc(doc.Packs, func(p packsItem) bool { return p.ID == id }); i >= 0 {
			doc.Packs[i] = entry
		} else {
			doc.Packs = append(doc.Packs, entry)
		}
		list = describePacks(home, doc)
		err = packFault(faultWrite)
		if err == nil {
			err = writePacks(home, doc, list)
		}
		if err != nil {
			placed := aside{from: dir, to: target}
			return errors.Join(err, placed.restore(), old.restore())
		}
		if err := old.discard(); err != nil {
			warnings = append(warnings, PackWarning{Code: WarnDeleteFailed, Message: err.Error()})
		}
		return nil
	})
	if err != nil {
		return PackList{}, nil, err
	}
	return list.with(recovered), warnings, nil
}

// aside is a directory moved from its place to a temporary sibling, to,
// "" when nothing was at from.
type aside struct {
	from, to string
}

// moveAside renames what is at path, if anything, to a new temporary
// sibling named for why.
func moveAside(path, why string) (aside, error) {
	if _, err := os.Lstat(path); errors.Is(err, os.ErrNotExist) {
		return aside{from: path}, nil
	} else if err != nil {
		return aside{}, err
	}
	to, err := os.MkdirTemp(filepath.Dir(path), "."+filepath.Base(path)+"."+why+"-")
	if err != nil {
		return aside{}, err
	}
	// A directory cannot be renamed onto another on every OS.
	if err := os.Remove(to); err != nil {
		return aside{}, err
	}
	if err := os.Rename(path, to); err != nil {
		return aside{}, err
	}
	return aside{from: path, to: to}, nil
}

// restore moves the directory back to its place.
func (a aside) restore() error {
	if a.to == "" {
		return nil
	}
	if err := os.Rename(a.to, a.from); err != nil {
		return fmt.Errorf("%s could not be moved back from %s: %w", a.from, a.to, err)
	}
	return nil
}

// discard deletes the directory moved aside.
func (a aside) discard() error {
	if a.to == "" {
		return nil
	}
	if err := os.RemoveAll(a.to); err != nil {
		return fmt.Errorf("the previous %s, moved to %s, could not be deleted: %v; delete it by hand", a.from, a.to, err)
	}
	return nil
}

// byID matches the entry of the identity id.
func byID(id string) func(PackEntry) bool { return func(e PackEntry) bool { return e.ID == id } }

// addResult is the result of an addition with list, the registry after
// it, whose entry match picks.
func addResult(list PackList, match func(PackEntry) bool, outcome string, warnings []PackWarning) PackAddResult {
	out := PackAddResult{PackList: list, Outcome: outcome, Warnings: warnings}
	if out.Warnings == nil {
		out.Warnings = []PackWarning{}
	}
	for _, e := range list.Packs {
		if match(e) {
			out.Pack = e
		}
	}
	return out
}
