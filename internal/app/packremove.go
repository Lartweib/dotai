package app

import (
	"errors"
	"fmt"
	"path/filepath"
)

// Removing a pack from the registry (decision 0044): only a git pack's
// clone at ~/.dotai/packs/<identity> is ever deleted.

// PackRemoveResult is a removal from the registry: the registry after it,
// the entries removed and the clone deleted, "" when none was.
type PackRemoveResult struct {
	PackList
	Removed  []PackEntry   `json:"removed"`
	Deleted  string        `json:"deleted"`
	Warnings []PackWarning `json:"warnings"`
}

// RemovePackID removes the pack id from the registry of home. It deletes
// the clone of a git pack at its clone path, ~/.dotai/packs/<identity>,
// and nothing else: a local pack's directory, or a git pack listed at
// another path, stays. An id the registry does not list is a usage error.
// Only a v1 registry can list two local packs of the same identity: both
// are removed.
func RemovePackID(home, id string) (PackRemoveResult, error) {
	home, err := requireAbs(home)
	if err != nil {
		return PackRemoveResult{}, err
	}
	if id == "" {
		return PackRemoveResult{}, &UsageError{msg: "the pack identity is empty"}
	}
	return removePacks(home, byID(id), func() error {
		return &UsageError{msg: fmt.Sprintf("the registry lists no pack %s: dotai pack list shows the identities", id)}
	})
}

// RemovePack forgets the pack listed at dir in the registry of home, as
// RemovePackID does; a path that is not listed is no error. A path that is
// not absolute can only be an entry of a registry written by hand: it is
// removed as it is written.
func RemovePack(home, dir string) (PackRemoveResult, error) {
	home, err := requireAbs(home)
	if err != nil {
		return PackRemoveResult{}, err
	}
	if dir == "" {
		return PackRemoveResult{}, &UsageError{msg: "the pack path is empty"}
	}
	if filepath.IsAbs(dir) {
		dir = filepath.Clean(dir)
	}
	return removePacks(home, func(e PackEntry) bool { return e.Path == dir || samePath(e.Path, dir) }, nil)
}

// removePacks removes the entries match picks, under the lock, and deletes
// the clone of a git pack at its clone path: moved aside first, and back
// when the registry cannot be written. none, when not nil, is the error
// when match picks nothing.
func removePacks(home string, match func(PackEntry) bool, none func() error) (PackRemoveResult, error) {
	res := PackRemoveResult{Removed: []PackEntry{}, Warnings: []PackWarning{}}
	recovered, err := withPacksLock(home, func() error {
		doc, err := readPacks(home)
		if err != nil {
			return err
		}
		described := describePacks(home, doc)
		kept := packsDoc{Schema: doc.Schema, Packs: []packsItem{}}
		res.PackList = PackList{Path: described.Path, Packs: []PackEntry{}}
		var clone string
		for i, e := range described.Packs {
			if !match(e) {
				kept.Packs = append(kept.Packs, doc.Packs[i])
				res.Packs = append(res.Packs, e)
				continue
			}
			res.Removed = append(res.Removed, e)
			if target, err := clonePath(home, e.ID); err == nil && e.Source != nil && samePath(e.Path, target) {
				clone = target
			}
		}
		if len(res.Removed) == 0 {
			if none != nil {
				return none()
			}
			return nil
		}
		gone := aside{from: clone}
		if clone != "" {
			if gone, err = moveAside(clone, "remove"); err != nil {
				return err
			}
		}
		if err := writePacks(home, kept, res.PackList); err != nil {
			return errors.Join(err, gone.restore())
		}
		if gone.to != "" {
			res.Deleted = clone
			if err := gone.discard(); err != nil {
				res.Warnings = append(res.Warnings, PackWarning{Code: WarnDeleteFailed, Message: err.Error()})
			}
		}
		return nil
	})
	if err != nil {
		return PackRemoveResult{}, err
	}
	res.PackList = res.with(recovered)
	return res, nil
}
