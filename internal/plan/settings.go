package plan

import (
	"cmp"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"maps"
	"slices"
	"strings"

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
