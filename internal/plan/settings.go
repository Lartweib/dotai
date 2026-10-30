package plan

import (
	"bytes"
	"cmp"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"maps"
	"path/filepath"
	"slices"
	"strings"

	"github.com/lartweib/dotai/internal/harness"
	"github.com/lartweib/dotai/internal/plan/jsonc"
)

// A harness writes into its own settings file while it runs, so dotai owns
// only the keys it puts there, not the whole file (decision 0035). The
// functions below read and write those keys through the jsonc edit layer,
// so that every other byte of the file stays as the harness left it.

// envKey is the key of Claude Code's settings whose variables are owned
// one by one, as the settings merge identifies them (decision 0030).
const envKey = "env"

// Key is a key dotai owns in a settings file: its path, the top-level key
// or, in Claude Code's settings, ["env", VAR] for each variable, and the
// SHA-256 in hex of its value as canonical JSON (see jsonc.Canonical).
type Key struct {
	Path   []string `json:"path"`
	SHA256 string   `json:"sha256"`
}

// Name is the key's path joined with dots, such as env.DOTAI_TERMINAL.
func (k Key) Name() string { return strings.Join(k.Path, ".") }

// KeyChange is a key an operation writes into a settings file or deletes
// from it: its name, as Key.Name gives it, and whether it is created,
// replaced or removed.
type KeyChange struct {
	Key    string `json:"key"`
	Action Action `json:"action"`
}

// keyValue is a key of a settings file with its value as compact JSON.
type keyValue struct {
	path  []string
	value []byte
}

// SettingsKeys returns the keys of data, the settings file of harness h, in
// the order of the file. An empty file, an empty object and an empty env
// have none.
func SettingsKeys(h string, data []byte) ([]Key, error) {
	doc, err := parseSettings(data)
	if err != nil {
		return nil, err
	}
	var keys []Key
	for _, kv := range settingsValues(h, doc) {
		keys = append(keys, Key{Path: kv.path, SHA256: valueHash(kv.value)})
	}
	return keys, nil
}

// WriteKeys returns data, the settings file of harness h, with each key of
// paths set to its value in from, or deleted when from does not have it;
// an env that it leaves empty goes too. Every byte of data that holds no
// such key stays as it was, comments and formatting included.
func WriteKeys(h string, data, from []byte, paths [][]string) ([]byte, error) {
	doc, err := parseSettings(data)
	if err != nil {
		return nil, err
	}
	src, err := parseSettings(from)
	if err != nil {
		return nil, err
	}
	env := false // a variable of env was deleted
	for _, p := range paths {
		if v, ok := src.Get(p); ok {
			if err := doc.Set(p, v); err != nil {
				return nil, fmt.Errorf("key %s: %w", strings.Join(p, "."), err)
			}
			continue
		}
		if _, ok := doc.Get(p); !ok {
			continue
		}
		if err := doc.Delete(p); err != nil {
			return nil, fmt.Errorf("key %s: %w", strings.Join(p, "."), err)
		}
		env = env || h == claudeCode && len(p) == 2 && p[0] == envKey
	}
	if names, ok := doc.Keys([]string{envKey}); env && ok && len(names) == 0 {
		if err := doc.Delete([]string{envKey}); err != nil {
			return nil, err
		}
	}
	return doc.Bytes(), nil
}

// DiffKeys returns the keys that before and after, two contents of the
// settings file of harness h, do not hold alike, by name. A nil content
// is a missing file.
func DiffKeys(h string, before, after []byte) ([]KeyChange, error) {
	b, err := SettingsKeys(h, before)
	if err != nil {
		return nil, err
	}
	a, err := SettingsKeys(h, after)
	if err != nil {
		return nil, err
	}
	was := map[string]string{}
	for _, k := range b {
		was[k.Name()] = k.SHA256
	}
	var out []KeyChange
	for _, k := range a {
		switch old, ok := was[k.Name()]; {
		case !ok:
			out = append(out, KeyChange{Key: k.Name(), Action: Create})
		case old != k.SHA256:
			out = append(out, KeyChange{Key: k.Name(), Action: Replace})
		}
		delete(was, k.Name())
	}
	for _, name := range slices.Sorted(maps.Keys(was)) {
		out = append(out, KeyChange{Key: name, Action: Remove})
	}
	slices.SortFunc(out, func(x, y KeyChange) int { return cmp.Compare(x.Key, y.Key) })
	return out, nil
}

// settingsFile is a settings target that Build plans key by key: its
// harness, the staged file, nil when the pack no longer stages it, and its
// ownership record, nil when dotai does not own it.
type settingsFile struct {
	harness string
	staged  *File
	owned   *Owned
}

// SettingsOf returns the harness whose settings file is target, an
// absolute path under home, among every harness dotai knows, so that the
// file of a harness the pack dropped is still one.
func SettingsOf(home, target string) (string, bool) {
	for _, a := range harness.All() {
		if a.Settings != "" && filepath.Join(home, filepath.FromSlash(a.SettingsTarget())) == target {
			return a.ID, true
		}
	}
	return "", false
}

// planSettings plans the settings target from what it holds, the keys the
// pack stages in it and the keys dotai owns there (decision 0035). It gives
// the op, nil when there is nothing to do, and the keys dotai owns there
// now. A key dotai owns whose value changed, or a file an older install
// owned whole that changed, is added to edited and planned no further.
//
// dotai sets each key the pack stages, a key the harness wrote included,
// and deletes each key it owns that the pack no longer stages; every other
// key stays as it is. A missing target is created as staged. A target left
// with no key is removed when it held keys of dotai's, and one in which
// dotai keeps no key is no longer dotai's.
func (in Input) planSettings(target string, st *settingsFile, edited *EditedError) (*Op, []Key, error) {
	cur, exists := in.Disk[target]
	var data []byte
	if exists {
		var ok bool
		if data, ok = in.Content[target]; !ok {
			return nil, nil, fmt.Errorf("the settings file %s was not read", target)
		}
	}
	owned, ok, err := in.ownedKeys(target, st, cur, exists, data, edited)
	if err != nil || !ok {
		return nil, nil, err
	}
	op := &Op{Scope: ScopeUser, Harness: st.harness, Target: target, Settings: true}
	var staged []byte
	if st.staged != nil {
		staged, op.Source, op.Exec = st.staged.Data, st.staged.Path, st.staged.Exec
		if op.Keys, err = SettingsKeys(st.harness, staged); err != nil {
			return nil, nil, fmt.Errorf("staged settings file %s: %w", st.staged.Path, err)
		}
	}
	if !exists {
		if len(op.Keys) == 0 {
			return nil, owned, nil
		}
		op.Action, op.After = Create, st.staged.Hash
		op.Changes, err = DiffKeys(st.harness, nil, staged)
		return op, owned, err
	}
	op.Before, op.BeforeExec = cur.Hash, cur.Exec
	if st.staged == nil {
		op.Exec = cur.Exec
	}
	var paths [][]string
	for _, k := range slices.Concat(owned, op.Keys) {
		if !slices.ContainsFunc(paths, func(p []string) bool { return slices.Equal(p, k.Path) }) {
			paths = append(paths, k.Path)
		}
	}
	content, err := WriteKeys(st.harness, data, staged, paths)
	if err != nil {
		return nil, nil, fmt.Errorf("%s: %w; fix it or move it away, then install again", target, err)
	}
	left, err := SettingsKeys(st.harness, content)
	if err != nil {
		return nil, nil, err
	}
	if op.Changes, err = DiffKeys(st.harness, data, content); err != nil {
		return nil, nil, err
	}
	switch {
	case len(left) == 0 && len(owned) == 0:
		return nil, owned, nil
	case len(left) == 0:
		op.Action, op.Exec = Remove, false
	case bytes.Equal(content, data) && (in.IgnoreExec || op.Exec == cur.Exec):
		if st.staged == nil {
			return nil, owned, nil
		}
		op.Action, op.After = Unchanged, cur.Hash
	default:
		h := sha256.Sum256(content)
		op.Action, op.Data, op.After = Replace, content, hex.EncodeToString(h[:])
	}
	return op, owned, nil
}

// ownedKeys gives the keys dotai owns in the settings target as it is,
// those its record names that it still holds, and false when the target is
// edited, which it adds to edited. A record from before decision 0035 owns
// every key of the file as it wrote it.
func (in Input) ownedKeys(target string, st *settingsFile, cur Current, exists bool, data []byte, edited *EditedError) ([]Key, bool, error) {
	o := st.owned
	if o == nil || !exists {
		return nil, true, nil
	}
	held := func(cs []Current) bool {
		return slices.ContainsFunc(cs, func(c Current) bool { return in.same(cur, c) })
	}
	asWritten := in.same(cur, Current{Hash: o.Hash, Exec: o.Exec})
	keys := o.Keys
	switch {
	case !asWritten && !held(o.Also) && held(o.Foreign):
		return nil, true, nil // still the file dotai found there
	case held(o.Also) && o.AlsoKeys != nil:
		keys = o.AlsoKeys
	case o.Keys == nil && (asWritten || held(o.Also)):
		k, err := SettingsKeys(st.harness, data)
		if err != nil {
			return nil, false, fmt.Errorf("%s: %w", target, err)
		}
		return k, true, nil
	case o.Keys == nil:
		edited.Targets = append(edited.Targets, target)
		return nil, false, nil
	case held(o.Also):
		keys = o.AlsoKeys
	}
	have, err := SettingsKeys(st.harness, data)
	if err != nil {
		return nil, false, fmt.Errorf("%s: %w; fix it or move it away, then install again", target, err)
	}
	present, changed := heldKeys(have, keys)
	if len(changed) > 0 && o.AlsoKeys != nil && !held(o.Also) {
		if p, c := heldKeys(have, o.AlsoKeys); len(c) == 0 {
			present, changed = p, nil
		}
	}
	if len(changed) > 0 {
		if edited.Keys == nil {
			edited.Keys = map[string][]string{}
		}
		slices.Sort(changed)
		edited.Keys[target] = changed
		return nil, false, nil
	}
	return present, true, nil
}

// heldKeys splits keys, as a record holds them, by what have holds: the
// keys it holds with the same value, and the names of those it holds with
// another. A key it does not hold is in neither.
func heldKeys(have, keys []Key) (present []Key, changed []string) {
	for _, k := range keys {
		i := slices.IndexFunc(have, func(h Key) bool { return slices.Equal(h.Path, k.Path) })
		switch {
		case i < 0:
		case have[i].SHA256 == k.SHA256:
			present = append(present, k)
		default:
			changed = append(changed, k.Name())
		}
	}
	return present, changed
}

// claudeCode is the harness whose env is owned per variable.
const claudeCode = "claudecode"

// parseSettings parses a settings file, which must be empty or a JSON or
// JSONC object.
func parseSettings(data []byte) (*jsonc.Document, error) {
	doc, err := jsonc.Parse(data)
	if err != nil {
		return nil, fmt.Errorf("not a JSON object dotai can edit key by key: %w", err)
	}
	return doc, nil
}

// settingsValues returns the keys of doc with their values, in order.
func settingsValues(h string, doc *jsonc.Document) []keyValue {
	names, _ := doc.Keys(nil)
	var out []keyValue
	for _, name := range names {
		if vars, ok := doc.Keys([]string{envKey}); h == claudeCode && name == envKey && ok {
			for _, v := range vars {
				value, _ := doc.Get([]string{envKey, v})
				out = append(out, keyValue{path: []string{envKey, v}, value: value})
			}
			continue
		}
		value, _ := doc.Get([]string{name})
		out = append(out, keyValue{path: []string{name}, value: value})
	}
	return out
}

// valueHash is the SHA-256 in hex of a value as canonical JSON.
func valueHash(value []byte) string {
	h := sha256.Sum256(jsonc.Canonical(value))
	return hex.EncodeToString(h[:])
}
