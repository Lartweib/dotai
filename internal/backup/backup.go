// Package backup is the backup format of decisions 0018 and 0032: one
// directory per operation under ~/.dotai/backups/, named by its id, with a
// manifest.json and, under files/, a copy of each target the operation
// replaced or removed at its path relative to the home directory. A link
// (decision 0036) is recorded with its destination and never copied. It
// does no I/O; the caller copies the files and writes the manifest.
package backup

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"path"
	"path/filepath"
	"regexp"
	"runtime"
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
// directory that holds the copies and the entries a replace install moved
// there, MovedDir the one that holds what a restore moved out of the way of
// the entries it moved back, and StateName the copy of the install state
// from before the operation.
const (
	ManifestName = "manifest.json"
	FilesDir     = "files"
	MovedDir     = "moved"
	StateName    = "state.json"
)

// The kinds of a wiped entry and of each item in it.
const (
	KindFile = "file"
	KindDir  = "dir"
	KindLink = "link"
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
	// Pinned marks the first backup of a replace install in the home
	// directory, which holds the configuration from before dotai: retention
	// never prunes it, nor the history from the install state back to it,
	// until a restore undoes it (decision 0038).
	Pinned bool `json:"pinned,omitempty"`
	Ops    []Op `json:"ops"`
	// Wipes are the entries the operation moved into the backup: those a
	// replace install wiped, or those a restore moved out of the way of the
	// entries it moved back. Splits are the files it rewrote by key.
	Wipes  []Wipe  `json:"wipes,omitempty"`
	Splits []Split `json:"splits,omitempty"`
}

// Wipe is an entry moved into the backup, a file, a directory with
// everything in it or a link, never followed (decision 0038): the harness
// it belongs to, its absolute Path, its Kind, File, the slash path it was
// moved to inside the backup directory, and Items, what it held.
type Wipe struct {
	Harness string `json:"harness"`
	Path    string `json:"path"`
	Kind    string `json:"kind"`
	File    string `json:"file"`
	Items   []Item `json:"items"`
}

// Item is one path of a wiped entry, in walk order: Path, its slash path
// inside the entry, "" for the entry itself, its Kind, and for a file its
// SHA-256 and exec bit, for a link its destination, "" when it cannot be
// read, such as a reparse point that is not a junction.
type Item struct {
	Path   string `json:"path"`
	Kind   string `json:"kind"`
	SHA256 string `json:"sha256,omitempty"`
	Exec   bool   `json:"exec"`
	Link   string `json:"link,omitempty"`
}

// Split is a file that mixes configuration and state, such as
// ~/.claude.json, whose keys the operation deleted or put back, keeping
// the rest of it (decision 0038): its harness, its absolute Target, File,
// the copy of what it held before, Before and After, and the Keys, each a
// path from the top of the file.
type Split struct {
	Harness string     `json:"harness"`
	Target  string     `json:"target"`
	File    string     `json:"file"`
	Before  Content    `json:"before"`
	After   Content    `json:"after"`
	Keys    [][]string `json:"keys"`
}

// MovedFile is the slash path inside a restore's backup that the entry at
// target, under home, is moved to when it stands where the restore moves
// back an entry of the install backup id.
func MovedFile(home, id, target string) (string, error) {
	rel, err := Rel(home, target)
	if err != nil {
		return "", err
	}
	return path.Join(MovedDir, id, rel), nil
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
// replace and a remove of a file; a created target has no copy, so that a
// restore deletes it, and neither has a link, which its Content describes.
type Op struct {
	Action plan.Action `json:"action"`
	Target string      `json:"target"`
	File   string      `json:"file,omitempty"`
	Before *Content    `json:"before,omitempty"`
	After  *Content    `json:"after,omitempty"`
}

// Content is a file's SHA-256 in hex and its exec bit, or, with Link and
// no SHA-256, the destination of a link (decision 0036).
type Content struct {
	SHA256 string `json:"sha256,omitempty"`
	Exec   bool   `json:"exec"`
	Link   string `json:"link,omitempty"`
}

// Current is c as the plan reads it.
func (c Content) Current() plan.Current {
	if c.Link != "" {
		return plan.LinkCurrent(c.Link)
	}
	return plan.Current{Hash: c.SHA256, Exec: c.Exec}
}

// content is what a target holds with hash and exec, or is the link to
// link when it is not "".
func content(hash string, exec bool, link string) *Content {
	if link != "" {
		return &Content{Link: link}
	}
	return &Content{SHA256: hash, Exec: exec}
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
			b.Before = content(op.Before, op.BeforeExec, op.BeforeLink)
			if op.BeforeLink == "" {
				b.File = path.Join(FilesDir, rel)
			}
		}
		if op.Action != plan.Remove {
			b.After = content(op.After, op.Exec, op.Link)
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
// not at its target's path under FilesDir, a link with a copy, a hash or a
// destination other than the shared skill of its name (plan.LinkDest), a
// target that is not under home
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
		for _, c := range []*Content{o.Before, o.After} {
			if c != nil && (c.Link != "" && (c.SHA256 != "" || c.Exec || !filepath.IsAbs(c.Link)) || c.Link == "" && c.SHA256 == "") {
				return fmt.Errorf("backup manifest %s: %s needs a SHA-256, or the absolute destination of a link and nothing else", id, o.Target)
			}
			if c != nil && c.Link != "" {
				if err := plan.CheckLinkDest(home, o.Target, c.Link, plan.FoldsCase(runtime.GOOS)); err != nil {
					return fmt.Errorf("backup manifest %s: %w", id, err)
				}
			}
		}
		want := path.Join(FilesDir, rel)
		if o.Action == plan.Create || o.Before.Link != "" {
			want = ""
		}
		if o.File != want {
			return fmt.Errorf("backup manifest %s: the copy of %s is %q, want %q", id, o.Target, o.File, want)
		}
	}
	if err := checkWipes(m, id, home); err != nil {
		return err
	}
	return checkSplits(m, id, home, seen)
}

// checkWipes reports a wipe of m that is not one this format writes: a
// path outside home, named twice or at or below another wiped entry, an
// unknown kind, an install's entry that is not at its path under FilesDir
// or a restore's that is not at its path under MovedDir and the id of an
// install backup, or items that do not describe an entry of its kind. A
// pinned backup must be an install's.
func checkWipes(m Manifest, id, home string) error {
	seen := map[string]bool{}
	if m.Pinned && m.Operation != OperationInstall {
		return fmt.Errorf("backup manifest %s: only an install's backup is pinned", id)
	}
	for _, w := range m.Wipes {
		rel, err := Rel(home, w.Path)
		if err != nil || !filepath.IsAbs(w.Path) {
			return fmt.Errorf("backup manifest %s: wiped entry %s is not under the home directory %s", id, w.Path, home)
		}
		if !Clean(w.Path) {
			return fmt.Errorf("backup manifest %s: wiped entry %s is not a clean absolute path", id, w.Path)
		}
		for target := range seen {
			if target == w.Path || strings.HasPrefix(target, w.Path+string(filepath.Separator)) || strings.HasPrefix(w.Path, target+string(filepath.Separator)) {
				return fmt.Errorf("backup manifest %s: wiped entry %s overlaps %s", id, w.Path, target)
			}
		}
		seen[w.Path] = true
		want := path.Join(FilesDir, rel)
		if m.Operation == OperationRestore {
			from, ok := strings.CutPrefix(w.File, MovedDir+"/")
			from, inner, _ := strings.Cut(from, "/")
			if op, _ := Operation(from); !ok || op != OperationInstall || inner != rel {
				return fmt.Errorf("backup manifest %s: the entry %s was moved to %q, want %s/<install backup id>/%s", id, w.Path, w.File, MovedDir, rel)
			}
			want = w.File
		}
		if w.File != want {
			return fmt.Errorf("backup manifest %s: the entry %s was moved to %q, want %q", id, w.Path, w.File, want)
		}
		if err := checkItems(w); err != nil {
			return fmt.Errorf("backup manifest %s: the entry %s: %w", id, w.Path, err)
		}
	}
	return nil
}

// Clean reports whether p is an absolute path in the form filepath.Clean
// gives, so that no ".." or "." in it can lead a move elsewhere than where
// a check of its text found it.
func Clean(p string) bool { return filepath.IsAbs(p) && p == filepath.Clean(p) }

// checkItems reports items of w that do not describe an entry of its kind:
// the entry itself first, then, for a directory only, each path inside it
// once, below a directory of the list, as a file with a SHA-256, a
// directory or a link with nothing else.
func checkItems(w Wipe) error {
	if len(w.Items) == 0 || w.Items[0].Path != "" || w.Items[0].Kind != w.Kind {
		return fmt.Errorf("its items must start with the entry itself, of the kind %q", w.Kind)
	}
	dirs, seen := map[string]bool{"": true}, map[string]bool{}
	for i, it := range w.Items {
		switch it.Kind {
		case KindFile:
			if it.SHA256 == "" || it.Link != "" {
				return fmt.Errorf("the file %q needs a SHA-256 and no link", it.Path)
			}
		case KindDir, KindLink:
			if it.SHA256 != "" || it.Exec || it.Kind == KindDir && it.Link != "" {
				return fmt.Errorf("the %s %q has a SHA-256 or an exec bit", it.Kind, it.Path)
			}
		default:
			return fmt.Errorf("%q has the kind %q, want file, dir or link", it.Path, it.Kind)
		}
		if i == 0 {
			continue
		}
		if w.Kind != KindDir || seen[it.Path] || !fs.ValidPath(it.Path) || !dirs[parentOf(it.Path)] {
			return fmt.Errorf("the item %q is not a new path inside a directory of the entry", it.Path)
		}
		seen[it.Path] = true
		if it.Kind == KindDir {
			dirs[it.Path] = true
		}
	}
	return nil
}

// parentOf is the slash path of the directory that holds p inside a wiped
// entry, "" for the entry itself.
func parentOf(p string) string {
	if d := path.Dir(p); d != "." {
		return d
	}
	return ""
}

// checkSplits reports a split file of m that is not one this format
// writes: a target outside home or named twice, a copy that is not at its
// path under FilesDir, a before or after without a SHA-256, or no key, or
// a key without a path.
func checkSplits(m Manifest, id, home string, seen map[string]bool) error {
	for _, s := range m.Splits {
		rel, err := Rel(home, s.Target)
		if err != nil || !filepath.IsAbs(s.Target) {
			return fmt.Errorf("backup manifest %s: split file %s is not under the home directory %s", id, s.Target, home)
		}
		if !Clean(s.Target) {
			return fmt.Errorf("backup manifest %s: split file %s is not a clean absolute path", id, s.Target)
		}
		if seen[s.Target] {
			return fmt.Errorf("backup manifest %s: target %s is named twice", id, s.Target)
		}
		seen[s.Target] = true
		// A restore that puts back a split file that is missing has no copy
		// of what was there.
		if want := path.Join(FilesDir, rel); s.File != want && !(m.Operation == OperationRestore && s.File == "" && s.Before == Content{}) {
			return fmt.Errorf("backup manifest %s: the copy of %s is %q, want %q", id, s.Target, s.File, want)
		}
		if s.File != "" && s.Before.SHA256 == "" || s.After.SHA256 == "" || s.Before.Link != "" || s.After.Link != "" || len(s.Keys) == 0 ||
			slices.ContainsFunc(s.Keys, func(k []string) bool { return len(k) == 0 }) {
			return fmt.Errorf("backup manifest %s: the split file %s needs a SHA-256 before and after, and keys with paths", id, s.Target)
		}
	}
	return nil
}

// Entry is a backup directory found on disk: its id, the time its
// manifest records, whether it has a manifest at all and whether the
// install state leads to it, as the last install's backup or one before
// it. A backup without a manifest is incomplete: it was being taken when
// its operation stopped.
type Entry struct {
	ID        string
	CreatedAt time.Time
	Complete  bool
	Chain     bool
}

// Prune gives the ids of entries to delete, sorted, so that no more than
// keep complete backups remain besides the oldest complete one, which
// holds what was there before the first write and is never deleted. They
// are ordered by the time their manifests record, then by id, so that a
// clock that went back does not reorder them by name. Backups the state
// does not lead to (restored installs and restores) go first, oldest
// first; then the history beyond its newest keep backups, oldest first,
// never those keep. Incomplete backups are deleted and do not count. A
// backup in protect, such as the one just taken or the one the install
// state names, is never deleted.
func Prune(entries []Entry, keep int, protect ...string) []string {
	var complete []Entry
	var out []string
	for _, e := range entries {
		switch {
		case e.Complete:
			complete = append(complete, e)
		case !slices.Contains(protect, e.ID):
			out = append(out, e.ID)
		}
	}
	slices.SortFunc(complete, func(a, b Entry) int {
		if c := a.CreatedAt.Compare(b.CreatedAt); c != 0 {
			return c
		}
		return strings.Compare(a.ID, b.ID)
	})
	if len(complete) <= keep+1 {
		slices.Sort(out)
		return out
	}
	var spent, history []Entry
	for _, e := range complete[1:] {
		if e.Chain {
			history = append(history, e)
		} else {
			spent = append(spent, e)
		}
	}
	if len(history) > keep {
		history = history[:len(history)-keep]
	} else {
		history = nil
	}
	excess := len(complete) - (keep + 1)
	for _, e := range slices.Concat(spent, history) {
		if excess == 0 {
			break
		}
		if !slices.Contains(protect, e.ID) {
			out = append(out, e.ID)
			excess--
		}
	}
	slices.Sort(out)
	return out
}
