package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"slices"
	"strings"

	"github.com/lartweib/dotai/internal/harness"
	"github.com/lartweib/dotai/internal/state"
)

// WipePlan is an entry a replace install moves into its backup (decision
// 0038): a file, a directory with everything in it, or a link, never
// followed. Collateral is what that does to other tools that read it, text
// for people, "" when no other tool is known to read it.
type WipePlan struct {
	Harness    string `json:"harness"`
	Path       string `json:"path"`
	Kind       string `json:"kind"`
	Collateral string `json:"collateral"`
}

// The kinds of a wiped entry.
const (
	KindFile = "file"
	KindDir  = "dir"
	KindLink = "link"
)

// KeepPlan is an entry of a harness directory that a replace install
// keeps, and why: login, history, cache, state, or shared for the user's
// shared skills folder of a variant that shares it.
type KeepPlan struct {
	Harness string `json:"harness"`
	Path    string `json:"path"`
	Kind    string `json:"kind"`
}

// WipeKey is a key of a file that mixes configuration and state, such as
// ~/.claude.json, that a replace install deletes, keeping the rest of the
// file: Key is its path from the top of the file.
type WipeKey struct {
	Harness string   `json:"harness"`
	File    string   `json:"file"`
	Key     []string `json:"key"`
}

// Refusal is a reason the replace install refuses, which its dry run
// lists: a relocation variable that is set (Variable), an entry no
// keep-list classifies whose name looks like a login or history, or a
// split file that dotai cannot split by key (Path).
type Refusal struct {
	Code     string `json:"code"`
	Harness  string `json:"harness"`
	Path     string `json:"path"`
	Variable string `json:"variable"`
	Message  string `json:"message"`
}

// The codes of a Refusal.
const (
	RefuseRelocation   = "relocation-variable"
	RefuseUnclassified = "unclassified-entry"
	RefuseSplitFile    = "split-file-invalid"
)

// replacePlan is what a replace install does to the harnesses' own
// configuration besides installing the pack.
type replacePlan struct {
	wipes    []WipePlan
	keeps    []KeepPlan
	keys     []WipeKey
	refusals []Refusal
}

// wiped reports whether target is a wiped entry or below one: it is
// missing once the wipe is done.
func (rp replacePlan) wiped(target string) bool {
	return slices.ContainsFunc(rp.wipes, func(w WipePlan) bool { return samePath(w.Path, target) || below(w.Path, target) })
}

// planWipes plans the reset of each of adapters to a fresh install in home
// (decision 0038). Each entry of an area that its keep-list does not keep
// is wiped, unless dotai owns it, in owned, or owns a target below it, in
// which case the directory is looked into instead. An entry it would wipe
// whose name looks like a login or history is refused instead. Each leak
// entry that exists is wiped whole, and each split file has its
// configuration keys wiped. A relocation variable of h's environment is a
// refusal. Nothing is followed through a link: an area, or a directory
// above it, that is a link or not a directory is a *NotRegularError.
func planWipes(ctx context.Context, d installFS, h host, home string, adapters []harness.Adapter, shared bool, owned map[string]bool) (replacePlan, error) {
	var rp replacePlan
	dirs := map[string]error{}
	for _, a := range adapters {
		for _, name := range a.Real.Relocated(h.environ, h.goos == "windows") {
			rp.refusals = append(rp.refusals, Refusal{Code: RefuseRelocation, Harness: a.ID, Variable: name,
				Message: fmt.Sprintf("%s is set, so %s may read its configuration from somewhere a replace install does not reset: unset it, then install again", name, a.ID)})
		}
		for _, ar := range a.Real.Areas {
			dir := filepath.Join(home, filepath.FromSlash(ar.Dir))
			ok, err := realDir(d, home, dir, dirs)
			if err != nil {
				return replacePlan{}, err
			}
			if !ok {
				continue
			}
			names, err := d.List(dir)
			if err != nil {
				return replacePlan{}, err
			}
			slices.Sort(names)
			for _, name := range names {
				entry := filepath.Join(dir, name)
				if kind, kept := ar.Kept(name, shared); kept {
					rp.keeps = append(rp.keeps, KeepPlan{Harness: a.ID, Path: entry, Kind: string(kind)})
					continue
				}
				if err := rp.wipe(d, a.ID, entry, ar.Collateral, owned); err != nil {
					return replacePlan{}, err
				}
			}
		}
		for _, l := range a.Real.Leaks {
			entry := filepath.Join(home, filepath.FromSlash(l.Path))
			if ok, err := realDir(d, home, filepath.Dir(entry), dirs); err != nil || !ok {
				if err != nil {
					return replacePlan{}, err
				}
				continue
			}
			mode, err := d.Lstat(entry)
			switch {
			case errors.Is(err, fs.ErrNotExist) || err == nil && owned[entry]:
				continue
			case err != nil:
				return replacePlan{}, err
			}
			rp.wipes = append(rp.wipes, WipePlan{Harness: a.ID, Path: entry, Kind: entryKind(mode), Collateral: l.Collateral})
		}
		for _, sf := range a.Real.Split {
			if err := rp.split(ctx, d, a.ID, filepath.Join(home, filepath.FromSlash(sf.Path)), sf); err != nil {
				return replacePlan{}, err
			}
		}
	}
	return rp, nil
}

// wipe plans the wipe of entry, an entry of a harness directory that no
// keep-list keeps, for harness h: whole, or, when dotai owns a target
// below it, entry by entry inside it. An entry dotai owns stays, since the
// plan takes care of it.
func (rp *replacePlan) wipe(d installFS, h, entry, collateral string, owned map[string]bool) error {
	if owned[entry] {
		return nil
	}
	mode, err := d.Lstat(entry)
	if err != nil {
		return err
	}
	ownsBelow := false
	for target := range owned {
		if below(entry, target) {
			ownsBelow = true
			break
		}
	}
	if ownsBelow && mode.IsDir() && mode&(fs.ModeSymlink|fs.ModeIrregular) == 0 {
		names, err := d.List(entry)
		if err != nil {
			return err
		}
		slices.Sort(names)
		for _, name := range names {
			if err := rp.wipe(d, h, filepath.Join(entry, name), collateral, owned); err != nil {
				return err
			}
		}
		return nil
	}
	if harness.LooksSensitive(filepath.Base(entry)) {
		rp.refusals = append(rp.refusals, Refusal{Code: RefuseUnclassified, Harness: h, Path: entry,
			Message: fmt.Sprintf("%s looks like a login or a history, and no keep-list of %s classifies it, so a replace install does not wipe it: move it away if it is neither", entry, h)})
		return nil
	}
	rp.wipes = append(rp.wipes, WipePlan{Harness: h, Path: entry, Kind: entryKind(mode), Collateral: collateral})
	return nil
}

// split plans the wipe of the configuration keys of the split file name
// of harness h, as sf names them: each top-level key of Keys, and each key
// of ProjectKeys in every entry of the Projects object, in that order,
// projects by name, that holds a value that is not empty. A missing file
// has none; one that is not a regular file holding a JSON object is a
// refusal.
func (rp *replacePlan) split(ctx context.Context, d installFS, h, name string, sf harness.SplitFile) error {
	data, _, err := d.Read(ctx, name)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if _, ok := errors.AsType[*NotRegularError](err); ok {
		rp.refusals = append(rp.refusals, Refusal{Code: RefuseSplitFile, Harness: h, Path: name,
			Message: fmt.Sprintf("%s is not a regular file, so a replace install cannot wipe its configuration keys and keep the rest: dotai never follows links", name)})
		return nil
	}
	if err != nil {
		return err
	}
	top, ok := object(data)
	if !ok {
		rp.refusals = append(rp.refusals, Refusal{Code: RefuseSplitFile, Harness: h, Path: name,
			Message: fmt.Sprintf("%s is not a JSON object, so a replace install cannot wipe its configuration keys and keep the rest", name)})
		return nil
	}
	for _, k := range sf.Keys {
		if holds(top[k]) {
			rp.keys = append(rp.keys, WipeKey{Harness: h, File: name, Key: []string{k}})
		}
	}
	projects, _ := object(top[sf.Projects])
	for _, p := range slicesSorted(projects) {
		fields, _ := object(projects[p])
		for _, k := range sf.ProjectKeys {
			if holds(fields[k]) {
				rp.keys = append(rp.keys, WipeKey{Harness: h, File: name, Key: []string{sf.Projects, p, k}})
			}
		}
	}
	return nil
}

// object decodes data as a JSON object, with the raw value of each key.
func object(data []byte) (map[string]json.RawMessage, bool) {
	var m map[string]json.RawMessage
	if len(bytes.TrimSpace(data)) == 0 || json.Unmarshal(data, &m) != nil || m == nil {
		return nil, false
	}
	return m, true
}

// holds reports whether raw is a JSON value that holds configuration: not
// missing, null, false, an empty string, array or object.
func holds(raw json.RawMessage) bool {
	var v any
	if len(raw) == 0 || json.Unmarshal(raw, &v) != nil {
		return false
	}
	switch v := v.(type) {
	case nil:
		return false
	case bool:
		return v
	case string:
		return v != ""
	case []any:
		return len(v) > 0
	case map[string]any:
		return len(v) > 0
	}
	return true
}

func slicesSorted(m map[string]json.RawMessage) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	slices.Sort(out)
	return out
}

// entryKind is the kind of a wiped entry of mode.
func entryKind(mode fs.FileMode) string {
	switch {
	case mode&(fs.ModeSymlink|fs.ModeIrregular) != 0:
		return KindLink
	case mode.IsDir():
		return KindDir
	}
	return KindFile
}

// realDir reports whether dir, under home, exists as a real directory, as
// each directory between home and it does; a missing one gives false. A
// link, a junction or a file on the way is a *NotRegularError. dirs caches
// the outcome of each directory.
func realDir(d installFS, home, dir string, dirs map[string]error) (bool, error) {
	rel, err := filepath.Rel(home, dir)
	if err != nil || !filepath.IsLocal(rel) {
		return false, fmt.Errorf("%s is not under the home directory %s", dir, home)
	}
	cur := home
	for seg := range strings.SplitSeq(rel, string(filepath.Separator)) {
		cur = filepath.Join(cur, seg)
		err, seen := dirs[cur]
		if !seen {
			err = checkDir(d, cur)
			dirs[cur] = err
		}
		if errors.Is(err, fs.ErrNotExist) {
			return false, nil
		}
		if err != nil {
			return false, err
		}
	}
	return true, nil
}

// ownedTargets are the targets prev owns, in its files and its journal.
func ownedTargets(prev *state.State) map[string]bool {
	out := map[string]bool{}
	if prev == nil {
		return out
	}
	for _, f := range prev.Files {
		out[f.Target] = true
	}
	if prev.Pending != nil {
		for _, op := range prev.Pending.Ops {
			out[op.Target] = true
		}
	}
	return out
}

// resolveMode is the mode of an install asked for mode, after the install
// prev, nil when there is none: the mode asked for, or the one prev
// records, or isolated. An unknown mode, and a switch from a replace
// install to isolated, which only dotai restore undoes, are a
// *UsageError (decision 0038).
func resolveMode(mode string, prev *state.State) (string, error) {
	if mode != "" && mode != ModeIsolated && mode != ModeReplace {
		return "", &UsageError{fmt.Sprintf("unknown mode %q: the modes are %s and %s", mode, ModeIsolated, ModeReplace)}
	}
	prevMode := ""
	if prev != nil {
		prevMode = prev.Mode
	}
	if mode == ModeIsolated && prevMode == ModeReplace {
		return "", &UsageError{"the install state records a replace install, and install does not switch it to isolated: " +
			"run dotai restore to put your own configuration back first, then install --mode isolated"}
	}
	for _, m := range []string{mode, prevMode} {
		if m != "" {
			return m, nil
		}
	}
	return ModeIsolated, nil
}

// errReplaceUnavailable refuses an install that would write in the replace
// mode, which comes with the backup by move (roadmap PRs 9b-3 and 9b-4).
func errReplaceUnavailable(fromState bool) error {
	why := "dotai install --mode replace is not available yet"
	if fromState {
		why = "the install state records a replace install, and installing in the replace mode is not available yet"
	}
	return &UsageError{why + ": dotai install --mode replace --dry-run shows what it would wipe, keep and install"}
}
