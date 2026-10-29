// Package backup is the backup format of decisions 0018 and 0032: one
// directory per operation under ~/.dotai/backups/, named by its id, with a
// manifest.json and, under files/, a copy of each target the operation
// replaced or removed at its path relative to the home directory. It does
// no I/O; the caller copies the files and writes the manifest.
package backup

import (
	"encoding/json"
	"fmt"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/lartweib/dotai/internal/plan"
)

// Schema is the version of the manifest format.
const Schema = "dotai.backup/v1"

// The operations that take a backup: an install, and a restore, which
// backs up what it overwrites or deletes.
const (
	OperationInstall = "install"
	OperationRestore = "restore"
)

// ManifestName is the manifest's name in a backup directory, FilesDir the
// directory that holds the copies and StateName the copy of the install
// state from before the operation.
const (
	ManifestName = "manifest.json"
	FilesDir     = "files"
	StateName    = "state.json"
)

// idTime is the layout of the timestamp of an id: UTC with milliseconds
// and no colon, so that ids sort in time order and are valid file names on
// Windows.
const idTime = "20060102T150405.000Z"

var idPattern = regexp.MustCompile(`^(\d{8}T\d{6}\.\d{3}Z)_([a-z]+)$`)

// ID is the id of a backup of operation taken at t, also its directory
// name, such as 20261029T101500.000Z_install.
func ID(t time.Time, operation string) string {
	return t.UTC().Format(idTime) + "_" + operation
}

// ParseID gives the time of the backup id; ok is false for a name that is
// not a backup id.
func ParseID(id string) (time.Time, bool) {
	m := idPattern.FindStringSubmatch(id)
	if m == nil {
		return time.Time{}, false
	}
	t, err := time.Parse(idTime, m[1])
	return t, err == nil
}

// Operation gives the operation of the backup id, such as install; ok is
// false for a name that is not a backup id.
func Operation(id string) (string, bool) {
	if _, ok := ParseID(id); !ok {
		return "", false
	}
	return idPattern.FindStringSubmatch(id)[2], true
}

// Manifest describes one backup. PriorState is the install state before
// the operation, nil in a backup taken by a dotai that did not record it;
// Restored is set once a restore has undone the operation.
type Manifest struct {
	Schema    string    `json:"schema"`
	ID        string    `json:"id"`
	Operation string    `json:"operation"`
	CreatedAt time.Time `json:"createdAt"`
	Home      string    `json:"home"` // the home directory each File is relative to
	Info
	PriorState *PriorState `json:"priorState,omitempty"`
	Restored   *Restored   `json:"restored,omitempty"`
	Ops        []Op        `json:"ops"`
}

// PriorState is the install state from before the operation: None when
// there was none, or else its copy File, StateName in the backup
// directory, and that copy's SHA-256 in hex.
type PriorState struct {
	None   bool   `json:"none,omitempty"`
	File   string `json:"file,omitempty"`
	SHA256 string `json:"sha256,omitempty"`
}

// Restored is when a restore undid the operation, and the id of the backup
// that restore took, "" when it wrote no file.
type Restored struct {
	At     time.Time `json:"at"`
	Backup string    `json:"backup"`
}

// Info is what the operation installed: its mode, the pack, the variant
// and the harnesses.
type Info struct {
	Mode      string   `json:"mode"`
	Pack      Pack     `json:"pack"`
	Variant   string   `json:"variant"`
	Harnesses []string `json:"harnesses"`
}

// Pack names a pack and the absolute path it was installed from.
type Pack struct {
	Name    string `json:"name"`
	Version string `json:"version"`
	Path    string `json:"path"`
}

// Op is one target the operation wrote. Before is what the target had,
// absent for a create; After what it was given, absent for a remove. File
// is the slash path of its copy inside the backup directory, set for a
// replace and a remove; a created target has no copy, so that a restore
// deletes it.
type Op struct {
	Action plan.Action `json:"action"`
	Target string      `json:"target"`
	File   string      `json:"file,omitempty"`
	Before *Content    `json:"before,omitempty"`
	After  *Content    `json:"after,omitempty"`
}

// Content is a file's SHA-256 in hex and its exec bit.
type Content struct {
	SHA256 string `json:"sha256"`
	Exec   bool   `json:"exec"`
}

// New is the manifest of the backup id of operation, taken at t, of the
// plan ops into home. Unchanged ops are left out, since nothing is written
// for them. Every target must be under home.
func New(id, operation string, t time.Time, home string, info Info, ops []plan.Op) (Manifest, error) {
	m := Manifest{Schema: Schema, ID: id, Operation: operation, CreatedAt: t.UTC(), Home: home, Info: info, Ops: []Op{}}
	if m.Harnesses == nil {
		m.Harnesses = []string{}
	}
	for _, op := range ops {
		if op.Action == plan.Unchanged {
			continue
		}
		rel, err := Rel(home, op.Target)
		if err != nil {
			return Manifest{}, err
		}
		b := Op{Action: op.Action, Target: op.Target}
		if op.Action != plan.Create {
			b.File = path.Join(FilesDir, rel)
			b.Before = &Content{SHA256: op.Before, Exec: op.BeforeExec}
		}
		if op.Action != plan.Remove {
			b.After = &Content{SHA256: op.After, Exec: op.Exec}
		}
		m.Ops = append(m.Ops, b)
	}
	return m, nil
}

// Rel is the slash path of target relative to home, where its copy sits
// under FilesDir. target must be inside home.
func Rel(home, target string) (string, error) {
	rel, err := filepath.Rel(home, target)
	if err != nil || !filepath.IsLocal(rel) {
		return "", fmt.Errorf("target %s is not under the home directory %s", target, home)
	}
	return filepath.ToSlash(rel), nil
}

// Encode is the manifest as indented JSON with a final newline.
func Encode(m Manifest) ([]byte, error) {
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(data, '\n'), nil
}

// Decode reads a manifest of this Schema.
func Decode(data []byte) (Manifest, error) {
	var m Manifest
	if err := json.Unmarshal(data, &m); err != nil {
		return Manifest{}, fmt.Errorf("backup manifest: %w", err)
	}
	if m.Schema != Schema {
		return Manifest{}, fmt.Errorf("backup manifest has schema %q, want %q", m.Schema, Schema)
	}
	return m, nil
}

// Check reports a manifest m that is not one this format writes for the
// backup id taken into home: another id, operation or home directory, an
// op whose before, after and copy do not fit its action, a copy that is
// not at its target's path under FilesDir, a target that is not under home
// or that is named twice, or a prior state that is neither none nor
// StateName. It does not check where under home a target is.
func Check(m Manifest, id, home string) error {
	op, ok := Operation(id)
	switch {
	case !ok || m.ID != id:
		return fmt.Errorf("backup manifest has the id %q, want %q", m.ID, id)
	case m.Operation != op:
		return fmt.Errorf("backup manifest %s has the operation %q, want %q", id, m.Operation, op)
	case m.Home != home:
		return fmt.Errorf("backup %s was taken for the home directory %s, not %s", id, m.Home, home)
	}
	if p := m.PriorState; p != nil && (p.None == (p.File != "") || p.File != "" && (p.File != StateName || p.SHA256 == "") || p.None && p.SHA256 != "") {
		return fmt.Errorf("backup manifest %s: the prior state must be none or %s with its SHA-256", id, StateName)
	}
	seen := map[string]bool{}
	for _, o := range m.Ops {
		rel, err := Rel(home, o.Target)
		if err != nil || !filepath.IsAbs(o.Target) {
			return fmt.Errorf("backup manifest %s: target %s is not under the home directory %s", id, o.Target, home)
		}
		if seen[o.Target] {
			return fmt.Errorf("backup manifest %s: target %s is named twice", id, o.Target)
		}
		seen[o.Target] = true
		var fits bool
		switch o.Action {
		case plan.Create:
			fits = o.File == "" && o.Before == nil && o.After != nil
		case plan.Replace:
			fits = o.Before != nil && o.After != nil
		case plan.Remove:
			fits = o.Before != nil && o.After == nil
		default:
			return fmt.Errorf("backup manifest %s: target %s has the action %q, want create, replace or remove", id, o.Target, o.Action)
		}
		if !fits {
			return fmt.Errorf("backup manifest %s: the %s of %s does not have the before and after a %s has", id, o.Action, o.Target, o.Action)
		}
		if o.Action != plan.Create && o.File != path.Join(FilesDir, rel) {
			return fmt.Errorf("backup manifest %s: the copy of %s is %q, want %q", id, o.Target, o.File, path.Join(FilesDir, rel))
		}
	}
	return nil
}

// Entry is a backup directory found on disk: its id, the time its
// manifest records and whether it has a manifest at all. A backup without
// one is incomplete: it was being taken when its install stopped.
type Entry struct {
	ID        string
	CreatedAt time.Time
	Complete  bool
}

// Prune gives the ids of entries to delete, sorted, so that only the last
// keep complete backups remain, besides the oldest complete one, which
// holds what was there before the first write and is never deleted. They
// are ordered by the time their manifests record, then by id, so that a
// clock that went back does not reorder them by name. Incomplete backups
// are deleted and do not count. A backup in protect, such as the one just
// taken or the one the install state names, is never deleted.
func Prune(entries []Entry, keep int, protect ...string) []string {
	var complete []Entry
	var out []string
	for _, e := range entries {
		switch {
		case slices.Contains(protect, e.ID):
		case !e.Complete:
			out = append(out, e.ID)
		}
		if e.Complete {
			complete = append(complete, e)
		}
	}
	slices.SortFunc(complete, func(a, b Entry) int {
		if c := a.CreatedAt.Compare(b.CreatedAt); c != 0 {
			return c
		}
		return strings.Compare(a.ID, b.ID)
	})
	if len(complete) > keep+1 {
		for _, e := range complete[1 : len(complete)-keep] {
			if !slices.Contains(protect, e.ID) {
				out = append(out, e.ID)
			}
		}
	}
	slices.Sort(out)
	return out
}
