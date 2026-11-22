package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"path/filepath"
	"slices"
	"strings"

	"github.com/lartweib/dotai/internal/backup"
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

// The kinds of a wiped entry, as its backup records them.
const (
	KindFile = backup.KindFile
	KindDir  = backup.KindDir
	KindLink = backup.KindLink
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
// lists: a relocation variable that is set (Variable), or (Path) an entry
// no keep-list classifies whose name, or that of something inside it,
// looks like a login, a secret or a history, a directory on the way to a
// harness directory or a leak entry that is a link, a junction or a file,
// or a split file that dotai cannot read or split by key.
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
	RefuseLinkedDir    = "linked-directory"
)

// replacePlan is what a replace install does to the harnesses' own
// configuration besides installing the pack. blocked are the directories
// a refusal keeps it from looking into, such as a harness directory that
// is a link.
type replacePlan struct {
	wipes    []WipePlan
	keeps    []KeepPlan
	keys     []WipeKey
	refusals []Refusal
	blocked  []string
}

// merge is rp with what other plans too.
func (rp replacePlan) merge(other replacePlan) replacePlan {
	return replacePlan{
		wipes:    slices.Concat(rp.wipes, other.wipes),
		keeps:    slices.Concat(rp.keeps, other.keeps),
		keys:     slices.Concat(rp.keys, other.keys),
		refusals: slices.Concat(rp.refusals, other.refusals),
		blocked:  slices.Concat(rp.blocked, other.blocked),
	}
}

// relocations are the refusals of rp for a relocation variable, the only
// ones that apply to a harness that is updated and not reset.
func (rp replacePlan) relocations() []Refusal {
	var out []Refusal
	for _, r := range rp.refusals {
		if r.Code == RefuseRelocation {
			out = append(out, r)
		}
	}
	return out
}

// reinstalled are the harnesses of selected that a replace install is a
// later one for (decision 0051): those the install state prev records as
// installed in the replace mode, whose install is settled. An install that
// stopped midway while it reset, as its journal says, did not finish the
// first one, so it is not a later one: each wipe and each split file of the
// journal is mapped to the harnesses whose reset it belongs to, by their
// areas, leak entries and split files, and only those are left out; one
// that no harness claims leaves out every harness. A harness that is not in
// the state's harnesses, such as one added to the selection since, has none
// either. Never nil.
func reinstalled(home string, prev *state.State, selected []string) []string {
	out := []string{}
	if prev == nil || prev.Mode != ModeReplace {
		return out
	}
	unsettled := map[string]bool{}
	all := false
	if prev.Pending != nil {
		for _, name := range slices.Concat(prev.Pending.Wipes, prev.Pending.Splits) {
			claimed := false
			for _, a := range harness.All() {
				if resets(home, a.Real, name) {
					unsettled[a.ID], claimed = true, true
				}
			}
			all = all || !claimed
		}
	}
	for _, id := range selected {
		if slices.Contains(prev.Harnesses, id) && !all && !unsettled[id] {
			out = append(out, id)
		}
	}
	return out
}

// resets reports whether the reset of the harness with the own configuration
// r wipes or rewrites name, an absolute path under home: it is, or is below,
// one of its areas, a leak entry or a split file.
func resets(home string, r harness.Real, name string) bool {
	in := func(rel string) bool {
		dir := filepath.Join(home, filepath.FromSlash(rel))
		return samePath(dir, name) || below(dir, name)
	}
	for _, ar := range r.Areas {
		if in(ar.Dir) {
			return true
		}
	}
	for _, l := range r.Leaks {
		if in(l.Path) {
			return true
		}
	}
	for _, sf := range r.Split {
		if in(sf.Path) {
			return true
		}
	}
	return false
}

// wipedKeys are the keys rp wipes in the split file target.
func (rp replacePlan) wipedKeys(target string) [][]string {
	var out [][]string
	for _, k := range rp.keys {
		if k.File == target {
			out = append(out, k.Key)
		}
	}
	return out
}

// unread reports whether target is a wiped entry or below one, which is
// missing once the wipe is done, or below a blocked directory, which the
// plan does not read through: either way the plan takes it as missing
// without reading it.
func (rp replacePlan) unread(target string) bool {
	return slices.ContainsFunc(rp.wipes, func(w WipePlan) bool { return samePath(w.Path, target) || below(w.Path, target) }) ||
		slices.ContainsFunc(rp.blocked, func(dir string) bool { return below(dir, target) })
}

// planWipes plans the reset of each of adapters to a fresh install in home
// (decision 0038). Each entry of an area that its keep-list does not keep
// is wiped, unless dotai owns it, in owned, or owns a target below it, in
// which case the directory is looked into instead. Each leak entry that
// exists is wiped whole, and each split file has its configuration keys
// wiped. Refused instead: an entry it would wipe whose name, or the name
// of anything inside it, looks like a login, a secret or a history; an
// area or a leak entry below a link, a junction or a file, which is never
// looked through; a split file that is not a readable JSON object; and a
// relocation variable of h's environment. A key of a split file that a
// project apply owns, by projects, is never wiped (decision 0041), nor one
// that the previous install owns there, by keys (decision 0049).
func planWipes(ctx context.Context, d installFS, h host, home string, adapters []harness.Adapter, shared bool, owned map[string]bool, projects state.Projects, keys map[string][][]string) (replacePlan, error) {
	rp := replacePlan{}
	dirs := map[string]error{}
	for _, a := range adapters {
		// File systems ignore case on Windows and, by default, on macOS.
		fold := h.goos == "windows" || h.goos == "darwin"
		vendored := func(p string) bool {
			rel, err := filepath.Rel(home, p)
			return err == nil && a.Real.Vendored(filepath.ToSlash(rel), fold)
		}
		for _, name := range a.Real.Relocated(h.environ, h.goos == "windows") {
			rp.refusals = append(rp.refusals, Refusal{Code: RefuseRelocation, Harness: a.ID, Variable: name,
				Message: fmt.Sprintf("%s is set, so %s may read its configuration from somewhere a replace install does not reset: unset it, then install again", name, a.ID)})
		}
		for _, ar := range a.Real.Areas {
			dir := filepath.Join(home, filepath.FromSlash(ar.Dir))
			ok, err := rp.realDir(d, a.ID, home, dir, dirs)
			if err != nil || !ok {
				if err != nil {
					return replacePlan{}, err
				}
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
				if err := rp.wipe(d, a.ID, entry, ar.Collateral, owned, vendored); err != nil {
					return replacePlan{}, err
				}
			}
		}
		for _, l := range a.Real.Leaks {
			entry := filepath.Join(home, filepath.FromSlash(l.Path))
			ok, err := rp.realDir(d, a.ID, home, filepath.Dir(entry), dirs)
			if err != nil || !ok {
				if err != nil {
					return replacePlan{}, err
				}
				continue
			}
			if _, err := d.Lstat(entry); errors.Is(err, fs.ErrNotExist) {
				continue
			} else if err != nil {
				return replacePlan{}, err
			}
			if err := rp.wipe(d, a.ID, entry, l.Collateral, owned, vendored); err != nil {
				return replacePlan{}, err
			}
		}
		for _, sf := range a.Real.Split {
			name := filepath.Join(home, filepath.FromSlash(sf.Path))
			if err := rp.split(ctx, d, a.ID, name, sf, slices.Concat(projects.OwnedKeys(name), keys[name])); err != nil {
				return replacePlan{}, err
			}
		}
	}
	return rp, nil
}

// wipe plans the wipe of entry, an entry that no keep-list keeps, for
// harness h: whole, or, when dotai owns a target below it, entry by entry
// inside it. An entry dotai owns stays, since the plan takes care of it.
// An entry whose name looks like a login, a secret or a history, or that
// holds, found without following a link or looking into a vendor tree, by
// vendored, anything whose name looks like a credential, is not wiped:
// each such path is a refusal. An entry that is gone since its directory was
// listed is not planned.
func (rp *replacePlan) wipe(d installFS, h, entry, collateral string, owned map[string]bool, vendored func(string) bool) error {
	if ownedAt(owned, entry) {
		return nil
	}
	mode, err := d.Lstat(entry)
	if errors.Is(err, fs.ErrNotExist) { // gone since its directory was listed
		return nil
	}
	if err != nil {
		return err
	}
	real := mode.IsDir() && mode&(fs.ModeSymlink|fs.ModeIrregular) == 0
	if real && ownedBelow(owned, entry) {
		names, err := d.List(entry)
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		if err != nil {
			return err
		}
		slices.Sort(names)
		for _, name := range names {
			if err := rp.wipe(d, h, filepath.Join(entry, name), collateral, owned, vendored); err != nil {
				return err
			}
		}
		return nil
	}
	sensitive, err := sensitivePaths(d, entry, real, harness.LooksSensitive, vendored)
	if err != nil {
		return err
	}
	for _, p := range sensitive {
		rp.refusals = append(rp.refusals, Refusal{Code: RefuseUnclassified, Harness: h, Path: p,
			Message: fmt.Sprintf("%s looks like a login, a secret or a history, and no keep-list of %s classifies it, so a replace install does not wipe it: move it away if it is neither", p, h)})
	}
	if len(sensitive) == 0 {
		rp.wipes = append(rp.wipes, WipePlan{Harness: h, Path: entry, Kind: entryKind(mode), Collateral: collateral})
	}
	return nil
}

// sensitivePaths are entry, when looks reports its name, and, for a real
// directory that is not a vendor tree, by vendored, every path inside it
// whose name looks like a credential, in walk order; nothing is followed
// through a link, and no vendor tree is looked into. An entry that is gone
// since its directory was listed, as a temporary file a running tool
// renames, is not there to look at.
func sensitivePaths(d installFS, entry string, real bool, looks, vendored func(string) bool) ([]string, error) {
	var out []string
	if looks(filepath.Base(entry)) {
		out = append(out, entry)
	}
	if !real || vendored(entry) {
		return out, nil
	}
	names, err := d.List(entry)
	if errors.Is(err, fs.ErrNotExist) {
		return out, nil
	}
	if err != nil {
		return nil, err
	}
	slices.Sort(names)
	for _, name := range names {
		child := filepath.Join(entry, name)
		mode, err := d.Lstat(child)
		if errors.Is(err, fs.ErrNotExist) { // gone since the listing
			continue
		}
		if err != nil {
			return nil, err
		}
		below, err := sensitivePaths(d, child, mode.IsDir() && mode&(fs.ModeSymlink|fs.ModeIrregular) == 0, harness.LooksSensitiveInside, vendored)
		if err != nil {
			return nil, err
		}
		out = append(out, below...)
	}
	return out, nil
}

// ownedAt reports whether dotai owns path, by owned, compared without case
// where the OS folds it.
func ownedAt(owned map[string]bool, path string) bool {
	if owned[path] {
		return true
	}
	for target := range owned {
		if samePath(target, path) {
			return true
		}
	}
	return false
}

// ownedBelow reports whether dotai owns a target below dir, by owned.
func ownedBelow(owned map[string]bool, dir string) bool {
	for target := range owned {
		if below(dir, target) {
			return true
		}
	}
	return false
}

// realDir reports whether dir, under home, exists as a real directory, as
// each directory between home and it does; a missing one gives false. A
// link, a junction or a file on the way is a refusal of harness h, once
// per path, and gives false: nothing below it is read. dirs caches the
// outcome of each directory.
func (rp *replacePlan) realDir(d installFS, h, home, dir string, dirs map[string]error) (bool, error) {
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
		if nr, ok := errors.AsType[*NotRegularError](err); ok {
			if !slices.ContainsFunc(rp.refusals, func(r Refusal) bool { return r.Code == RefuseLinkedDir && r.Harness == h && r.Path == nr.Path }) {
				rp.refusals = append(rp.refusals, Refusal{Code: RefuseLinkedDir, Harness: h, Path: nr.Path,
					Message: fmt.Sprintf("%s: a replace install of %s never reads or wipes through it", nr.Error(), h)})
				rp.blocked = append(rp.blocked, nr.Path)
			}
			return false, nil
		}
		if err != nil {
			return false, err
		}
	}
	return true, nil
}

// split plans the wipe of the configuration keys of the split file name
// of harness h, as sf names them: each top-level key of Keys, and each key
// of ProjectKeys in every entry of the Projects object, in that order,
// projects by name, that holds a value that is not empty. A key that holds
// keys dotai owns, in kept, such as a project apply's or the previous
// install's MCP servers, is wiped member by member instead, each but
// those. A missing file has none; one
// that is not a regular file holding a JSON object is a refusal.
func (rp *replacePlan) split(ctx context.Context, d installFS, h, name string, sf harness.SplitFile, kept [][]string) error {
	data, _, err := d.Read(ctx, name)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if _, ok := errors.AsType[*NotRegularError](err); ok {
		rp.refusals = append(rp.refusals, Refusal{Code: RefuseSplitFile, Harness: h, Path: name,
			Message: fmt.Sprintf("%s is not a regular file, so a replace install cannot wipe its configuration keys and keep the rest: dotai never follows links", name)})
		return nil
	}
	if err != nil && ctx.Err() == nil {
		rp.refusals = append(rp.refusals, Refusal{Code: RefuseSplitFile, Harness: h, Path: name,
			Message: fmt.Sprintf("%s cannot be read (%v), so a replace install cannot wipe its configuration keys and keep the rest", name, err)})
		return nil
	}
	if err != nil {
		return err
	}
	top, ok := jsonObject(data)
	if !ok {
		rp.refusals = append(rp.refusals, Refusal{Code: RefuseSplitFile, Harness: h, Path: name,
			Message: fmt.Sprintf("%s is not a JSON object, so a replace install cannot wipe its configuration keys and keep the rest", name)})
		return nil
	}
	for _, k := range sf.Keys {
		rp.keys = append(rp.keys, configKeys(h, name, []string{k}, top[k], kept)...)
	}
	projects, _ := jsonObject(top[sf.Projects])
	for _, p := range slices.Sorted(maps.Keys(projects)) {
		fields, _ := jsonObject(projects[p])
		for _, k := range sf.ProjectKeys {
			rp.keys = append(rp.keys, configKeys(h, name, []string{sf.Projects, p, k}, fields[k], kept)...)
		}
	}
	return nil
}

// configKeys are the keys to wipe at the key at of the split file name of
// harness h, which holds raw: none when it holds no configuration, the key
// itself, or, when it is an object that holds keys of kept, each of its
// members that holds configuration but those.
func configKeys(h, name string, at []string, raw json.RawMessage, kept [][]string) []WipeKey {
	if !holdsConfig(raw) {
		return nil
	}
	owned := slices.ContainsFunc(kept, func(o []string) bool { return len(o) > len(at) && slices.Equal(o[:len(at)], at) })
	members, isObject := jsonObject(raw)
	if !owned || !isObject {
		return []WipeKey{{Harness: h, File: name, Key: at}}
	}
	var out []WipeKey
	for _, m := range slices.Sorted(maps.Keys(members)) {
		key := append(slices.Clone(at), m)
		if holdsConfig(members[m]) && !slices.ContainsFunc(kept, func(o []string) bool { return slices.Equal(o, key) }) {
			out = append(out, WipeKey{Harness: h, File: name, Key: key})
		}
	}
	return out
}

// jsonObject decodes data as a JSON object, with the raw value of each key.
func jsonObject(data []byte) (map[string]json.RawMessage, bool) {
	var m map[string]json.RawMessage
	if len(bytes.TrimSpace(data)) == 0 || json.Unmarshal(data, &m) != nil || m == nil {
		return nil, false
	}
	return m, true
}

// holdsConfig reports whether raw is a JSON value that holds
// configuration: not missing, null, false, an empty string, array or
// object.
func holdsConfig(raw json.RawMessage) bool {
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

// ownedKeys are the keys prev owns in each settings file, in its files and
// its journal, by target.
func ownedKeys(prev *state.State) map[string][][]string {
	out := map[string][][]string{}
	if prev == nil {
		return out
	}
	for _, f := range prev.Files {
		for _, k := range f.Keys {
			out[f.Target] = append(out[f.Target], k.Path)
		}
	}
	if prev.Pending != nil {
		for _, op := range prev.Pending.Ops {
			for _, k := range op.Keys {
				out[op.Target] = append(out[op.Target], k.Path)
			}
		}
	}
	return out
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
