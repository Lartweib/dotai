// Package state is the install state of decision 0032, kept in
// ~/.dotai/state.json: what is installed, how, and the ownership record of
// every file the install wrote (decision 0013), with the keys dotai owns in
// each harness settings file (decision 0035) and the links to shared
// skills (decision 0036). It does no I/O; the caller reads and writes the
// file.
package state

import (
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"path/filepath"
	"reflect"
	"slices"
	"time"

	"github.com/lartweib/dotai/internal/plan"
)

// Schema is the version of the state format.
const Schema = "dotai.state/v1"

// State is the installed pack, its variant, mode and harnesses, whether
// the variant shares the user's skills (decision 0036), when it was
// installed, the backup of the last install that wrote files ("" when
// none did) and the files it owns, in target order. Pending is the journal
// of an install that has started writing and not finished: it is written
// before the first write and dropped when the install finishes or is
// rolled back, so a Pending that is there means the install stopped
// midway, and its targets may hold what was there or what it wrote.
type State struct {
	Schema       string    `json:"schema"`
	Pack         Pack      `json:"pack"`
	Variant      string    `json:"variant"`
	SharedSkills bool      `json:"sharedSkills"`
	Mode         string    `json:"mode"`
	InstalledAt  time.Time `json:"installedAt"`
	Harnesses    []string  `json:"harnesses"`
	Backup       string    `json:"backup"`
	Files        []File    `json:"files"`
	Pending      *Pending  `json:"pending,omitempty"`
}

// Pending is the journal of an install that is writing: its backup and
// each operation it carries out.
type Pending struct {
	Backup string      `json:"backup"`
	Ops    []PendingOp `json:"ops"`
}

// PendingOp is one operation of an install that is writing: what its
// target had, absent for a create, and what it is given, absent for a
// remove. Keys are the keys dotai owns in a settings file once the
// operation is carried out, absent for any other file and when it owns
// none.
type PendingOp struct {
	Action  plan.Action `json:"action"`
	Target  string      `json:"target"`
	Harness string      `json:"harness"`
	Before  *Content    `json:"before,omitempty"`
	After   *Content    `json:"after,omitempty"`
	Keys    []plan.Key  `json:"keys,omitempty"`
}

// Content is a file's SHA-256 in hex and its exec bit, or, with Link and
// no SHA-256, the destination of a link (decision 0036).
type Content struct {
	SHA256 string `json:"sha256,omitempty"`
	Exec   bool   `json:"exec"`
	Link   string `json:"link,omitempty"`
}

// content is what a target holds with hash and exec, or is the link to
// link when it is not "".
func content(hash string, exec bool, link string) *Content {
	if link != "" {
		return &Content{Link: link}
	}
	return &Content{SHA256: hash, Exec: exec}
}

// Current is c as the plan reads it.
func (c Content) Current() plan.Current {
	if c.Link != "" {
		return plan.LinkCurrent(c.Link)
	}
	return plan.Current{Hash: c.SHA256, Exec: c.Exec}
}

// KindLink is the kind of an owned link.
const KindLink = "link"

// Pack names a pack and the absolute path it was installed from.
type Pack struct {
	Name    string `json:"name"`
	Version string `json:"version"`
	Path    string `json:"path"`
}

// File is one owned file: its absolute target, the harness it is installed
// for ("" for a tool), and the SHA-256 in hex and exec bit it was written
// with. For a harness settings file, Keys are the keys dotai owns in it,
// never empty, and the file is otherwise the harness's; a settings file
// without Keys was recorded whole, by an install before decision 0035.
// A link to a shared skill (decision 0036) has the Kind KindLink, its
// destination Link and no SHA-256.
type File struct {
	Target  string     `json:"target"`
	Harness string     `json:"harness"`
	Kind    string     `json:"kind,omitempty"`
	Link    string     `json:"link,omitempty"`
	SHA256  string     `json:"sha256,omitempty"`
	Exec    bool       `json:"exec"`
	Keys    []plan.Key `json:"keys,omitempty"`
}

// file is the record of an owned target that holds cur.
func file(target, harness string, cur plan.Current, keys []plan.Key) File {
	if cur.Link != "" {
		return File{Target: target, Harness: harness, Kind: KindLink, Link: cur.Link}
	}
	return File{Target: target, Harness: harness, SHA256: cur.Hash, Exec: cur.Exec, Keys: keys}
}

// Current is what f records its target holds, as the plan reads it.
func (f File) Current() plan.Current {
	if f.Kind == KindLink {
		return plan.LinkCurrent(f.Link)
	}
	return plan.Current{Hash: f.SHA256, Exec: f.Exec}
}

// New is the state after an install of pack that carried out ops, with
// the backup backupID. Every target the plan leaves in place is owned,
// whether it was written or already had the staged content; a removed one
// is not, nor a settings file in which dotai keeps no key.
func New(pack Pack, variant, mode string, t time.Time, harnesses []string, backupID string, ops []plan.Op) State {
	s := State{
		Schema: Schema, Pack: pack, Variant: variant, Mode: mode, InstalledAt: t.UTC(),
		Harnesses: append([]string{}, harnesses...), Backup: backupID, Files: []File{},
	}
	for _, op := range ops {
		if op.Action != plan.Remove && (!op.Settings || len(op.Keys) > 0) {
			s.Files = append(s.Files, file(op.Target, op.Harness, plan.Current{Hash: op.After, Exec: op.Exec, Link: op.Link}, op.Keys))
		}
	}
	return s
}

// Journal is base with the journal of an install that is about to carry
// out ops with the backup backupID; unchanged ops are left out.
func Journal(base State, backupID string, ops []plan.Op) State {
	p := &Pending{Backup: backupID, Ops: []PendingOp{}}
	for _, op := range ops {
		if op.Action == plan.Unchanged {
			continue
		}
		j := PendingOp{Action: op.Action, Target: op.Target, Harness: op.Harness}
		if op.Action != plan.Create {
			j.Before = content(op.Before, op.BeforeExec, op.BeforeLink)
		}
		if op.Action != plan.Remove {
			j.After = content(op.After, op.Exec, op.Link)
		}
		if op.Settings && len(op.Keys) > 0 {
			j.Keys = op.Keys
		}
		p.Ops = append(p.Ops, j)
	}
	base.Files = append([]File{}, base.Files...)
	base.Pending = p
	return base
}

// Owned is the ownership record as the plan reads it, by target. A target
// of a Pending operation may also hold what that operation writes; one
// that dotai did not own and was replacing may still hold the file it
// found, which is then not dotai's. A settings file being written also
// gives the keys dotai owns once that operation is carried out.
func (s State) Owned() map[string]plan.Owned {
	owned := make(map[string]plan.Owned, len(s.Files))
	for _, f := range s.Files {
		cur := f.Current()
		owned[f.Target] = plan.Owned{Harness: f.Harness, Hash: cur.Hash, Exec: cur.Exec, Keys: f.Keys}
	}
	if s.Pending == nil {
		return owned
	}
	for _, op := range s.Pending.Ops {
		o, had := owned[op.Target]
		switch {
		case op.After == nil: // a remove: what is left is what was owned
			if !had && op.Before != nil {
				before := op.Before.Current()
				owned[op.Target] = plan.Owned{Harness: op.Harness, Hash: before.Hash, Exec: before.Exec}
			}
		case had:
			after := op.After.Current()
			o.Also = append(o.Also, plan.Current{Hash: after.Hash, Exec: after.Exec})
			o.AlsoKeys = op.Keys
			owned[op.Target] = o
		default:
			after := op.After.Current()
			o = plan.Owned{Harness: op.Harness, Hash: after.Hash, Exec: after.Exec, Keys: op.Keys}
			if op.Before != nil {
				before := op.Before.Current()
				o.Foreign = []plan.Current{{Hash: before.Hash, Exec: before.Exec}}
			}
			owned[op.Target] = o
		}
	}
	return owned
}

// Settled is s without a journal, owning exactly the targets in onDisk
// that its record owns, with the state onDisk gives them and, for a
// settings file, the keys that keys gives it, in target order: what dotai
// owns once an interrupted install is resolved.
func (s State) Settled(onDisk map[string]plan.Current, keys map[string][]plan.Key) State {
	owned := s.Owned()
	s.Pending, s.Files = nil, []File{}
	for _, target := range slices.Sorted(maps.Keys(onDisk)) {
		if o, ok := owned[target]; ok {
			cur := onDisk[target]
			s.Files = append(s.Files, file(target, o.Harness, cur, keys[target]))
		}
	}
	return s
}

// SameInstall reports whether s and o record the same install, apart from
// when it ran and its backup.
func (s State) SameInstall(o State) bool {
	o.InstalledAt, o.Backup = s.InstalledAt, s.Backup
	return reflect.DeepEqual(s, o)
}

// Encode is the state as indented JSON with a final newline.
func Encode(s State) ([]byte, error) {
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(data, '\n'), nil
}

// Decode reads a state of this Schema whose files each have one absolute
// target.
func Decode(data []byte) (State, error) {
	var s State
	if err := json.Unmarshal(data, &s); err != nil {
		return State{}, fmt.Errorf("install state: %w", err)
	}
	if s.Schema != Schema {
		return State{}, fmt.Errorf("install state has schema %q, want %q", s.Schema, Schema)
	}
	if s.Pending != nil {
		for _, op := range s.Pending.Ops {
			if !filepath.IsAbs(op.Target) {
				return State{}, fmt.Errorf("install state: pending target %q is not absolute", op.Target)
			}
			if err := checkKeys(op.Keys); err != nil {
				return State{}, fmt.Errorf("install state: pending %s: %w", op.Target, err)
			}
			for _, c := range []*Content{op.Before, op.After} {
				if c != nil && c.Link != "" && (!filepath.IsAbs(c.Link) || c.SHA256 != "") {
					return State{}, fmt.Errorf("install state: pending %s: a link needs an absolute destination and no hash", op.Target)
				}
			}
		}
	}
	seen := map[string]bool{}
	for _, f := range s.Files {
		if !filepath.IsAbs(f.Target) {
			return State{}, fmt.Errorf("install state: target %q is not absolute", f.Target)
		}
		if seen[f.Target] {
			return State{}, fmt.Errorf("install state: target %s is owned twice", f.Target)
		}
		seen[f.Target] = true
		if err := checkKeys(f.Keys); err != nil {
			return State{}, fmt.Errorf("install state: %s: %w", f.Target, err)
		}
		if err := checkKind(f); err != nil {
			return State{}, fmt.Errorf("install state: %s: %w", f.Target, err)
		}
	}
	if s.Harnesses == nil {
		s.Harnesses = []string{}
	}
	if s.Files == nil {
		s.Files = []File{}
	}
	return s, nil
}

// checkKind rejects a file of an unknown kind, a link without an absolute
// destination or with a hash, exec bit or keys, and a file with a
// destination.
func checkKind(f File) error {
	switch f.Kind {
	case "":
		if f.Link != "" {
			return errors.New("a file that is not a link has a link destination")
		}
	case KindLink:
		if !filepath.IsAbs(f.Link) || f.SHA256 != "" || f.Exec || len(f.Keys) > 0 {
			return errors.New("a link needs an absolute destination, and no hash, exec bit or keys")
		}
	default:
		return fmt.Errorf("unknown kind %q, want none or %q", f.Kind, KindLink)
	}
	return nil
}

// checkKeys rejects a key of a settings file that has no path.
func checkKeys(keys []plan.Key) error {
	for _, k := range keys {
		if len(k.Path) == 0 {
			return errors.New("a key has no path")
		}
	}
	return nil
}
