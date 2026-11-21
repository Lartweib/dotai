package plan

import (
	"bytes"
	"cmp"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
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
// one by one, as the settings merge identifies them (decision 0030), and
// mcpKey the key of its .claude.json whose servers are owned one by one,
// as the MCP merge identifies them (decision 0049). In Claude Code's files
// each is an object owned member by member, and an object of them that a
// write empties goes.
const (
	envKey = "env"
	mcpKey = "mcpServers"
)

// memberKeys are the keys of Claude Code's files owned member by member.
var memberKeys = []string{envKey, mcpKey}

// Key is a key dotai owns in a settings file: its path, the top-level key
// or, in Claude Code's files, ["env", VAR] for each variable,
// ["mcpServers", NAME] for each MCP server and ["hooks", EVENT, MATCHER, ID]
// for each entry of a hook event (decision 0050), and the SHA-256 in hex of
// its value as canonical JSON (see jsonc.Canonical).
type Key struct {
	Path   []string `json:"path"`
	SHA256 string   `json:"sha256"`
}

// Name is the key's path joined with dots, such as env.DOTAI_TERMINAL; a
// hook entry reads as hooks.SessionStart[startup]: <command>.
func (k Key) Name() string {
	if name, ok := hookName(k.Path); ok {
		return name
	}
	return strings.Join(k.Path, ".")
}

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
		hash, err := valueHash(kv.value)
		if err != nil {
			return nil, err
		}
		keys = append(keys, Key{Path: kv.path, SHA256: hash})
	}
	return keys, nil
}

// WriteKeys returns data, the settings file of harness h, with each key of
// paths set to its value in from, or deleted when from does not have it;
// an env or mcpServers of Claude Code's that it leaves empty goes too. Every byte of data that holds no
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
	emptied := map[string]bool{} // the member keys a member was deleted from
	for _, p := range paths {
		if h == claudeCode && hookPath(p) {
			if err := writeHook(doc, src, p); err != nil {
				return nil, fmt.Errorf("key %s: %w", Key{Path: p}.Name(), err)
			}
			continue
		}
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
		if h == claudeCode && len(p) == 2 && slices.Contains(memberKeys, p[0]) {
			emptied[p[0]] = true
		}
	}
	for _, k := range memberKeys {
		if names, ok := doc.Keys([]string{k}); emptied[k] && ok && len(names) == 0 {
			if err := doc.Delete([]string{k}); err != nil {
				return nil, err
			}
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

// CodeHooksFileRebased is the warning of a hooks file that an install before
// decision 0050 owned whole and that changed since: dotai cannot tell which
// of its keys are its own, and rewrites the keys the pack stages.
const CodeHooksFileRebased = "hooks-file-rebased"

// hooksKey is the key of Claude Code's settings that holds its hooks:
// an object of events, each an array of matcher groups. The groups are
// owned one by one, since other programs append groups of their own to the
// same events (decision 0050). A group is identified by its event, its
// matcher and its first command, and its value is the group.
const hooksKey = "hooks"

// hookPath reports whether path is that of a hook entry, a group of an
// event.
func hookPath(path []string) bool { return len(path) == 4 && path[0] == hooksKey }

// hookName is how a hook entry's path reads: hooks.<event>[<matcher>]: <id>.
func hookName(path []string) (string, bool) {
	if !hookPath(path) {
		return "", false
	}
	name := hooksKey + "." + path[1]
	if path[2] != "" {
		name += "[" + path[2] + "]"
	}
	return name + ": " + path[3], true
}

// LegacyKey reports whether k, a key a record holds for harness h, is the
// hooks key of an install before decision 0050, which owned all hooks of
// Claude Code's settings as one key and cannot tell dotai's entries from
// those another program added.
func LegacyKey(h string, k Key) bool {
	return h == claudeCode && slices.Equal(k.Path, []string{hooksKey})
}

// withoutLegacy returns keys without the legacy hooks key, nil staying nil.
func withoutLegacy(h string, keys []Key) []Key {
	if keys == nil {
		return nil
	}
	return slices.DeleteFunc(slices.Clone(keys), func(k Key) bool { return LegacyKey(h, k) })
}

// HeldKeys returns the keys of data, the settings file of harness h, as
// SettingsKeys does and, when one of the recorded keys is the legacy hooks
// key, that key too, as the whole hooks value holds it now, so that a
// record from before decision 0050 is compared as it always was.
func HeldKeys(h string, data []byte, recorded ...[]Key) ([]Key, error) {
	keys, err := SettingsKeys(h, data)
	if err != nil {
		return nil, err
	}
	if !slices.ContainsFunc(slices.Concat(recorded...), func(k Key) bool { return LegacyKey(h, k) }) {
		return keys, nil
	}
	doc, err := parseSettings(data)
	if err != nil {
		return nil, err
	}
	if value, ok := doc.Get([]string{hooksKey}); ok {
		hash, err := valueHash(value)
		if err != nil {
			return nil, err
		}
		keys = append(keys, Key{Path: []string{hooksKey}, SHA256: hash})
	}
	return keys, nil
}

// hookEntry is a group of a hook event as a settings file holds it.
type hookEntry struct {
	matcher, id string
	index       int
	value       []byte
}

// hookEntries are the groups of the event array of doc, in order, each with
// its identity: the matcher it holds, and the command of its first command
// hook, or a hash of the group when it has none. A group that repeats an
// identity gets a number after it. ok is false when the event is not an
// array.
func hookEntries(doc *jsonc.Document, event string) ([]hookEntry, bool) {
	groups, ok := doc.Elements([]string{hooksKey, event})
	if !ok {
		return nil, false
	}
	var out []hookEntry
	seen := map[[2]string]int{}
	for i, g := range groups {
		matcher, id := groupID(g)
		n := seen[[2]string{matcher, id}]
		seen[[2]string{matcher, id}]++
		if n > 0 {
			id = fmt.Sprintf("%s#%d", id, n+1)
		}
		out = append(out, hookEntry{matcher: matcher, id: id, index: i, value: g})
	}
	return out, true
}

// groupID is the matcher and the id of a matcher group, as compact JSON.
func groupID(group []byte) (matcher, id string) {
	var g struct {
		Matcher string `json:"matcher"`
		Hooks   []struct {
			Command string `json:"command"`
		} `json:"hooks"`
	}
	if json.Unmarshal(group, &g) == nil {
		for _, h := range g.Hooks {
			if h.Command != "" {
				return g.Matcher, h.Command
			}
		}
	} else {
		g.Matcher = ""
	}
	hash, _ := valueHash(group)
	return g.Matcher, "#" + hash[:16]
}

// hookAt is the entry of the event array of doc that path names.
func hookAt(doc *jsonc.Document, path []string) (hookEntry, bool) {
	entries, _ := hookEntries(doc, path[1])
	i := slices.IndexFunc(entries, func(e hookEntry) bool { return e.matcher == path[2] && e.id == path[3] })
	if i < 0 {
		return hookEntry{}, false
	}
	return entries[i], true
}

// writeHook sets the hook entry at path in doc to its value in src, or
// deletes it when src does not have it; the event and the hooks object that
// a deletion empties go too.
func writeHook(doc, src *jsonc.Document, path []string) error {
	at := []string{hooksKey, path[1]}
	have, held := hookAt(doc, path)
	if want, ok := hookAt(src, path); ok {
		if held {
			return doc.ReplaceElement(at, have.index, want.value)
		}
		return doc.Union(at, want.value)
	}
	if !held {
		return nil
	}
	if err := doc.RemoveElement(at, have.index); err != nil {
		return err
	}
	if left, _ := doc.Elements(at); len(left) > 0 {
		return nil
	}
	if err := doc.Delete(at); err != nil {
		return err
	}
	if names, ok := doc.Keys([]string{hooksKey}); ok && len(names) == 0 {
		return doc.Delete([]string{hooksKey})
	}
	return nil
}

// settingsFile is a settings target that Build plans key by key: its
// harness, the staged file, nil when the pack no longer stages it, and its
// ownership record, nil when dotai does not own it.
type settingsFile struct {
	harness string
	staged  *File
	owned   *Owned
}

// SettingsOf returns the harness whose settings file, hooks file or
// HomeConfig is target, an absolute path under home, among every harness
// dotai knows and in the layout of either mode, so that the file of a
// harness the pack dropped, or of an install in the other mode, is still
// one. All are owned key by key (decisions 0035, 0049 and 0050).
func SettingsOf(home, target string) (string, bool) {
	for _, a := range harness.All() {
		for _, m := range modes {
			if a.Settings != "" && filepath.Join(home, filepath.FromSlash(a.SettingsTarget(m))) == target ||
				a.Hooks != "" && filepath.Join(home, filepath.FromSlash(a.HooksTarget(m))) == target ||
				a.HomeConfig != "" && filepath.Join(home, filepath.FromSlash(a.HomeConfigTarget(m))) == target {
				return a.ID, true
			}
		}
	}
	return "", false
}

// hooksFileOf reports whether target, an absolute path under home, is the
// Hooks file of a harness in the layout of either mode (decision 0050).
func hooksFileOf(home, target string) bool {
	for _, a := range harness.All() {
		for _, m := range modes {
			if a.Hooks != "" && filepath.Join(home, filepath.FromSlash(a.HooksTarget(m))) == target {
				return true
			}
		}
	}
	return false
}

// planSettings plans the settings target from what it holds, the keys the
// pack stages in it and the keys dotai owns there (decision 0035). It gives
// the op, nil when there is nothing to do, the keys dotai owned there before
// the install, as the state it started from records them, and whether it
// owned the file whole. A key dotai owns whose value changed, or a file an older install
// owned whole that changed, is added to edited and planned no further.
//
// dotai sets each key the pack stages, a key the harness wrote included,
// and deletes each key it owns that the pack no longer stages; every other
// key stays as it is. A missing target is created as staged. A target left
// with no key is removed when it held keys of dotai's, and one in which
// dotai keeps no key is no longer dotai's.
func (in Input) planSettings(target string, st *settingsFile, edited *EditedError) (*Op, []Key, bool, error) {
	cur, exists := in.Disk[target]
	var data []byte
	if exists {
		var ok bool
		if data, ok = in.Content[target]; !ok {
			return nil, nil, false, fmt.Errorf("the settings file %s was not read", target)
		}
	}
	owned, legacy, whole, ok, err := in.ownedKeys(target, st, cur, exists, data, edited)
	if err != nil || !ok {
		return nil, nil, false, err
	}
	// The record the install starts from keeps the legacy hooks key it
	// dropped, so that a restore of the chain puts the old hooks back.
	rec := slices.Concat(owned, legacy)
	op := &Op{Scope: ScopeUser, Harness: st.harness, Target: target, Settings: true}
	var staged []byte
	if st.staged != nil {
		staged, op.Source, op.Exec = st.staged.Data, st.staged.Path, st.staged.Exec
		if op.Keys, err = SettingsKeys(st.harness, staged); err != nil {
			return nil, nil, false, fmt.Errorf("staged settings file %s: %w", st.staged.Path, err)
		}
	}
	if !exists {
		if len(op.Keys) == 0 {
			return nil, rec, whole, nil
		}
		op.Action, op.After = Create, st.staged.Hash
		op.Changes, err = DiffKeys(st.harness, nil, staged)
		return op, rec, whole, err
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
		return nil, nil, false, fmt.Errorf("%s: %w; fix it or move it away, then install again", target, err)
	}
	left, err := SettingsKeys(st.harness, content)
	if err != nil {
		return nil, nil, false, err
	}
	if op.Changes, err = DiffKeys(st.harness, data, content); err != nil {
		return nil, nil, false, err
	}
	switch {
	case len(left) == 0 && len(owned) == 0:
		return nil, rec, whole, nil
	case len(left) == 0:
		op.Action, op.Exec = Remove, false
	case bytes.Equal(content, data) && (in.IgnoreExec || op.Exec == cur.Exec):
		if st.staged == nil {
			return nil, rec, whole, nil
		}
		op.Action, op.After = Unchanged, cur.Hash
	default:
		h := sha256.Sum256(content)
		op.Action, op.Data, op.After = Replace, content, hex.EncodeToString(h[:])
	}
	return op, rec, whole, nil
}

// ownedKeys gives the keys dotai owns in the settings target as it is,
// those its record names that it still holds, and false when the target is
// edited, which it adds to edited. A key that holds the value the pack now
// stages is not edited, whatever its record says. A record from before decision 0035 owns
// every key of the file as it wrote it.
func (in Input) ownedKeys(target string, st *settingsFile, cur Current, exists bool, data []byte, edited *EditedError) (owned, legacy []Key, whole, ok bool, err error) {
	o := st.owned
	if o == nil || !exists {
		return nil, nil, false, true, nil
	}
	held := func(cs []Current) bool {
		return slices.ContainsFunc(cs, func(c Current) bool { return in.same(cur, c) })
	}
	asWritten := in.same(cur, Current{Hash: o.Hash, Exec: o.Exec})
	// A record from before decision 0050 owns all hooks of Claude Code's
	// settings as one key, which no longer tells dotai's entries from those
	// another program added: the entries the pack stages are set, and the
	// key is dropped.
	keys := withoutLegacy(st.harness, o.Keys)
	alsoKeys := withoutLegacy(st.harness, o.AlsoKeys)
	// The record the install starts from keeps it, though.
	legacy = slices.DeleteFunc(slices.Clone(o.Keys), func(k Key) bool { return !LegacyKey(st.harness, k) })
	switch {
	case !asWritten && !held(o.Also) && held(o.Foreign):
		return nil, nil, false, true, nil // still the file dotai found there
	case held(o.Also) && o.AlsoKeys != nil:
		keys = alsoKeys
		legacy = slices.DeleteFunc(slices.Clone(o.AlsoKeys), func(k Key) bool { return !LegacyKey(st.harness, k) })
	case o.Keys == nil && (asWritten || held(o.Also)):
		k, err := SettingsKeys(st.harness, data)
		if err != nil {
			return nil, nil, false, false, fmt.Errorf("%s: %w", target, err)
		}
		return k, nil, false, true, nil
	case o.Keys == nil && hooksFileOf(in.Home, target):
		// A hooks file that an install before decision 0050 owned whole,
		// changed since, as other programs add keys to it: dotai owns the
		// keys the pack stages from now on, and the rest is not its own.
		// The record the install starts from still owns it whole.
		return nil, nil, true, true, nil
	case o.Keys == nil:
		edited.Targets = append(edited.Targets, target)
		return nil, nil, false, false, nil
	case held(o.Also):
		keys = alsoKeys
		legacy = slices.DeleteFunc(slices.Clone(o.AlsoKeys), func(k Key) bool { return !LegacyKey(st.harness, k) })
	}
	have, err := SettingsKeys(st.harness, data)
	if err != nil {
		return nil, nil, false, false, fmt.Errorf("%s: %w; fix it or move it away, then install again", target, err)
	}
	var staged []Key
	if st.staged != nil {
		if staged, err = SettingsKeys(st.harness, st.staged.Data); err != nil {
			return nil, nil, false, false, fmt.Errorf("staged settings file %s: %w", st.staged.Path, err)
		}
	}
	present, changed := heldKeys(have, keys, staged)
	if len(changed) > 0 && o.AlsoKeys != nil && !held(o.Also) {
		if p, c := heldKeys(have, alsoKeys, staged); len(c) == 0 {
			present, changed = p, nil
		}
	}
	if len(changed) > 0 {
		if edited.Keys == nil {
			edited.Keys = map[string][]string{}
		}
		slices.Sort(changed)
		edited.Keys[target] = changed
		return nil, nil, false, false, nil
	}
	return present, legacy, false, true, nil
}

// heldKeys splits keys, as a record holds them, by what have holds: the
// keys it holds with the same value, and the names of those it holds with
// another. A key it does not hold is in neither. A key changed to the
// value staged gives it, the value the install would write, is no change:
// it is present with that value.
func heldKeys(have, keys, staged []Key) (present []Key, changed []string) {
	for _, k := range keys {
		cur, ok := keyAt(have, k.Path)
		switch {
		case !ok:
		case cur.SHA256 == k.SHA256:
			present = append(present, k)
		case slices.ContainsFunc(staged, func(s Key) bool { return slices.Equal(s.Path, cur.Path) && s.SHA256 == cur.SHA256 }):
			present = append(present, cur)
		default:
			changed = append(changed, k.Name())
		}
	}
	return present, changed
}

// keyAt is the key of keys at path.
func keyAt(keys []Key, path []string) (Key, bool) {
	i := slices.IndexFunc(keys, func(k Key) bool { return slices.Equal(k.Path, path) })
	if i < 0 {
		return Key{}, false
	}
	return keys[i], true
}

// claudeCode is the harness whose env is owned per variable, and its MCP
// servers per server.
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
		if events, ok := doc.Keys([]string{name}); h == claudeCode && name == hooksKey && ok {
			for _, event := range events {
				entries, isList := hookEntries(doc, event)
				if !isList {
					value, _ := doc.Get([]string{name, event})
					out = append(out, keyValue{path: []string{name, event}, value: value})
				}
				for _, e := range entries {
					out = append(out, keyValue{path: []string{name, event, e.matcher, e.id}, value: e.value})
				}
			}
			continue
		}
		if members, ok := doc.Keys([]string{name}); h == claudeCode && slices.Contains(memberKeys, name) && ok {
			for _, m := range members {
				value, _ := doc.Get([]string{name, m})
				out = append(out, keyValue{path: []string{name, m}, value: value})
			}
			continue
		}
		value, _ := doc.Get([]string{name})
		out = append(out, keyValue{path: []string{name}, value: value})
	}
	return out
}

// valueHash is the SHA-256 in hex of a value as canonical JSON.
func valueHash(value []byte) (string, error) {
	c, err := jsonc.Canonical(value)
	if err != nil {
		return "", err
	}
	h := sha256.Sum256(c)
	return hex.EncodeToString(h[:]), nil
}
