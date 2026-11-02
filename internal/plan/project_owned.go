package plan

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"github.com/lartweib/dotai/internal/plan/jsonc"
)

// A project apply owns what it wrote, as its record in .dotai/ says: the
// files it wrote whole, and the keys it set in a file merged by key, each
// with the value the key held before, so that a later apply that no longer
// writes it, or the restore of the project, puts that value back. A list
// merges as a union with what was there (decision 0040), so the value
// before is never lost. The functions below plan an apply against that
// record.

// CodeMCPServerForeign is the refusal of a project apply whose Claude Code
// MCP server has the name of one dotai does not own in the projects entry
// of the .claude.json Claude Code reads (decision 0041).
const CodeMCPServerForeign = "mcp-server-foreign"

// CodeRecordTracked is the refusal of a project apply whose record, which
// must stay local, git tracks.
const CodeRecordTracked = "record-tracked"

// ProjectOwned is what the project record says dotai owns in a target.
// For a file it wrote whole, Hash and Exec are what it wrote; Backup is
// the backup that holds the file it overwrote there, "" when there was
// none. For a file merged by key, Keys are the keys it set and Parents the
// objects it created on the way to them, each a path from the top of the
// file. Created marks a target that was missing before dotai wrote it, and
// Exclude is the entry of the exclude file that covers it, "" for none.
type ProjectOwned struct {
	Harness string     `json:"harness"`
	Kind    string     `json:"kind"`
	Hash    string     `json:"sha256,omitempty"`
	Exec    bool       `json:"exec"`
	Created bool       `json:"created"`
	Backup  string     `json:"backup,omitempty"`
	Exclude string     `json:"exclude,omitempty"`
	Keys    []OwnedKey `json:"keys,omitempty"`
	Parents [][]string `json:"parents,omitempty"`
}

// OwnedKey is a key dotai set in a file merged by key: its path from the
// top of the file, the SHA-256 in hex of the value it wrote as canonical
// JSON, and Before, the value the key held before dotai first set it, nil
// when it was missing.
type OwnedKey struct {
	Path   []string        `json:"path"`
	SHA256 string          `json:"sha256"`
	Before json.RawMessage `json:"before,omitempty"`
}

// Name is the key's path joined with dots.
func (k OwnedKey) Name() string { return strings.Join(k.Path, ".") }

// KeyEdit is one change to a file merged by key: the key at Path gets
// Value, or is deleted when Value is nil; with Prune, the object at Path is
// deleted only when it is empty.
type KeyEdit struct {
	Path  []string        `json:"path"`
	Value json.RawMessage `json:"value,omitempty"`
	Prune bool            `json:"prune,omitempty"`
}

// ApplyEdits applies edits to data, a JSON or JSONC object, in order, and
// gives the result; every byte that holds no key it edits stays as it was.
func ApplyEdits(data []byte, edits []KeyEdit) ([]byte, error) {
	doc, err := jsonc.Parse(data)
	if err != nil {
		return nil, err
	}
	for _, e := range edits {
		switch {
		case e.Prune:
			if names, ok := doc.Keys(e.Path); ok && len(names) == 0 {
				err = doc.Delete(e.Path)
			}
		case e.Value == nil:
			err = doc.Delete(e.Path)
		default:
			err = doc.Set(e.Path, e.Value)
		}
		if err != nil {
			return nil, fmt.Errorf("key %s: %w", strings.Join(e.Path, "."), err)
		}
	}
	return doc.Bytes(), nil
}

// ValueHash is the SHA-256 in hex of a JSON value as canonical JSON, the
// hash an OwnedKey records.
func ValueHash(value []byte) (string, error) { return valueHash(value) }

// mergedValue is the value dotai writes at a key that held before (nil
// when it was missing) for the staged value: the union of the two when
// both are arrays, so that the entries already there stay first, and the
// staged value otherwise.
func mergedValue(before, staged []byte) ([]byte, error) {
	var a, b []json.RawMessage
	if before == nil || json.Unmarshal(before, &a) != nil || json.Unmarshal(staged, &b) != nil {
		return staged, nil
	}
	doc, err := jsonc.Parse([]byte(`{"v":` + string(before) + `}`))
	if err != nil {
		return nil, err
	}
	elems := make([][]byte, len(b))
	for i, e := range b {
		elems[i] = e
	}
	if err := doc.Union([]string{"v"}, elems...); err != nil {
		return nil, err
	}
	v, _ := doc.Get([]string{"v"})
	return v, nil
}

// mergeKeys plans the file merged by key t from what it holds, doc (nil
// when it is missing), and what dotai owns there, owned (nil for nothing).
// Each staged key is set to its merged value; a key the record owns that
// the profiles no longer give gets back the value it held before, and an
// object dotai created that this leaves empty goes too. It gives the keys
// as the plan reports them, the edits, what dotai owns there afterwards,
// and the refusals: in the home file of a harness's MCP servers, a server
// of the same name that dotai does not own is never taken over.
func (in ProjectInput) mergeKeys(t located, doc *jsonc.Document, owned *ProjectOwned) ([]ProjectKey, []KeyEdit, *ProjectOwned, []Warning, error) {
	paths, values, err := t.stagedKeys(in.mcpKey())
	if err != nil {
		return nil, nil, nil, nil, fmt.Errorf("staged %s: %w", t.file.Path, err)
	}
	get := func(p []string) ([]byte, bool) {
		if doc == nil {
			return nil, false
		}
		return doc.Get(p)
	}
	next := &ProjectOwned{Harness: t.harness, Kind: t.kind, Created: doc == nil}
	if owned != nil {
		next.Created, next.Exclude = owned.Created || doc == nil, owned.Exclude
		next.Parents = slices.Clone(owned.Parents)
	}
	ownedKey := func(p []string) (OwnedKey, bool) {
		if owned == nil {
			return OwnedKey{}, false
		}
		i := slices.IndexFunc(owned.Keys, func(k OwnedKey) bool { return slices.Equal(k.Path, p) })
		if i < 0 {
			return OwnedKey{}, false
		}
		return owned.Keys[i], true
	}
	var keys []ProjectKey
	var edits []KeyEdit
	var refusals []Warning
	for i, p := range paths {
		have, present := get(p)
		prev, isOwned := ownedKey(p)
		if h, err := valueHash(have); t.home != nil && present && !isOwned && in.shared(p) && err == nil {
			prev, isOwned = OwnedKey{Path: p, SHA256: h}, true
		}
		if t.home != nil && present && !isOwned {
			name := p[len(p)-1]
			refusals = append(refusals, Warning{Code: CodeMCPServerForeign, Harness: t.harness, Item: "MCP server " + name, Path: t.target,
				Message: fmt.Sprintf("%s already has an MCP server %s for this project that dotai did not add, so apply leaves it and refuses: rename or remove it, then apply again", t.target, name)})
			keys = append(keys, ProjectKey{Path: p, Action: Unchanged})
			continue
		}
		before := json.RawMessage(nil)
		switch {
		case isOwned:
			before = prev.Before
		case present:
			before = have
		}
		value, err := mergedValue(before, values[i])
		if err != nil {
			return nil, nil, nil, nil, fmt.Errorf("staged %s: key %s: %w", t.file.Path, strings.Join(p, "."), err)
		}
		hash, err := valueHash(value)
		if err != nil {
			return nil, nil, nil, nil, err
		}
		k := ProjectKey{Path: p, Action: Create}
		if present {
			k.Action = Merge
			if same, err := sameJSON(have, value); err == nil && same {
				k.Action = Unchanged
			} else if h, err := valueHash(have); isOwned && (err != nil || h != prev.SHA256) {
				k.Action = Conflict // changed since dotai wrote it
			}
		}
		for j := 1; j < len(p); j++ {
			if _, ok := get(p[:j]); !ok && !slices.ContainsFunc(next.Parents, func(q []string) bool { return slices.Equal(q, p[:j]) }) {
				next.Parents = append(next.Parents, slices.Clone(p[:j]))
			}
		}
		keys = append(keys, k)
		if k.Action != Unchanged {
			edits = append(edits, KeyEdit{Path: p, Value: value})
		}
		next.Keys = append(next.Keys, OwnedKey{Path: p, SHA256: hash, Before: before})
	}
	if owned != nil {
		for _, k := range owned.Keys {
			if slices.ContainsFunc(paths, func(p []string) bool { return slices.Equal(p, k.Path) }) {
				continue
			}
			have, present := get(k.Path)
			if !present && k.Before == nil || t.home != nil && in.shared(k.Path) {
				continue // gone already, or another project's too
			}
			pk := ProjectKey{Path: k.Path, Action: Remove}
			if h, err := valueHash(have); present && (err != nil || h != k.SHA256) {
				pk.Action = Conflict
			}
			keys = append(keys, pk)
			edits = append(edits, KeyEdit{Path: k.Path, Value: k.Before})
		}
	}
	// An object dotai created goes once no key it owns is left in it, and
	// only when it is empty: whatever someone else put there keeps it.
	var kept [][]string
	for _, q := range next.Parents {
		if slices.ContainsFunc(next.Keys, func(k OwnedKey) bool { return len(k.Path) > len(q) && slices.Equal(k.Path[:len(q)], q) }) {
			kept = append(kept, q)
			continue
		}
		edits = append(edits, KeyEdit{Path: q, Prune: true})
	}
	slices.SortStableFunc(edits, func(a, b KeyEdit) int {
		if a.Prune != b.Prune { // prunes last, deepest first
			if a.Prune {
				return 1
			}
			return -1
		}
		if a.Prune {
			return len(b.Path) - len(a.Path)
		}
		return 0
	})
	next.Parents = kept
	return keys, edits, next, refusals, nil
}

// shared reports whether another project's apply owns the key at p.
func (in ProjectInput) shared(p []string) bool {
	return slices.ContainsFunc(in.Shared, func(q []string) bool { return slices.Equal(q, p) })
}

// GetKey is the value at path in data, a JSON or JSONC object, as compact
// JSON, and whether it is there.
func GetKey(data []byte, path []string) ([]byte, bool, error) {
	doc, err := jsonc.Parse(data)
	if err != nil {
		return nil, false, err
	}
	v, ok := doc.Get(path)
	return v, ok, nil
}

// TopKeys are the top-level keys of data, a JSON or JSONC object.
func TopKeys(data []byte) ([]string, error) {
	doc, err := jsonc.Parse(data)
	if err != nil {
		return nil, err
	}
	names, _ := doc.Keys(nil)
	return names, nil
}
