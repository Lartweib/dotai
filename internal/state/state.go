// Package state is the install state of decision 0032, kept in
// ~/.dotai/state.json: what is installed, how, and the ownership record of
// every file the install wrote (decision 0013). It does no I/O; the caller
// reads and writes the file.
package state

import (
	"encoding/json"
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

// State is the installed pack, its variant, mode and harnesses, when it
// was installed, the backup of the last install that wrote files ("" when
// none did) and the files it owns, in target order. Pending is the journal
// of an install that has started writing and not finished: it is written
// before the first write and dropped when the install finishes or is
// rolled back, so a Pending that is there means the install stopped
// midway, and its targets may hold what was there or what it wrote.
type State struct {
	Schema      string    `json:"schema"`
	Pack        Pack      `json:"pack"`
	Variant     string    `json:"variant"`
	Mode        string    `json:"mode"`
	InstalledAt time.Time `json:"installedAt"`
	Harnesses   []string  `json:"harnesses"`
	Backup      string    `json:"backup"`
	Files       []File    `json:"files"`
	Pending     *Pending  `json:"pending,omitempty"`
}

// Pending is the journal of an install that is writing: its backup and
// each operation it carries out.
type Pending struct {
	Backup string      `json:"backup"`
	Ops    []PendingOp `json:"ops"`
}

// PendingOp is one operation of an install that is writing: what its
// target had, absent for a create, and what it is given, absent for a
// remove.
type PendingOp struct {
	Action  plan.Action `json:"action"`
	Target  string      `json:"target"`
	Harness string      `json:"harness"`
	Before  *Content    `json:"before,omitempty"`
	After   *Content    `json:"after,omitempty"`
}

// Content is a file's SHA-256 in hex and its exec bit.
type Content struct {
	SHA256 string `json:"sha256"`
	Exec   bool   `json:"exec"`
}

// Pack names a pack and the absolute path it was installed from.
type Pack struct {
	Name    string `json:"name"`
	Version string `json:"version"`
	Path    string `json:"path"`
}

// File is one owned file: its absolute target, the harness it is installed
// for ("" for a tool), and the SHA-256 in hex and exec bit it was written
// with.
type File struct {
	Target  string `json:"target"`
	Harness string `json:"harness"`
	SHA256  string `json:"sha256"`
	Exec    bool   `json:"exec"`
}

// New is the state after an install of pack that carried out ops, with
// the backup backupID. Every target the plan leaves in place is owned,
// whether it was written or already had the staged content; a removed one
// is not.
func New(pack Pack, variant, mode string, t time.Time, harnesses []string, backupID string, ops []plan.Op) State {
	s := State{
		Schema: Schema, Pack: pack, Variant: variant, Mode: mode, InstalledAt: t.UTC(),
		Harnesses: append([]string{}, harnesses...), Backup: backupID, Files: []File{},
	}
	for _, op := range ops {
		if op.Action != plan.Remove {
			s.Files = append(s.Files, File{Target: op.Target, Harness: op.Harness, SHA256: op.After, Exec: op.Exec})
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
			j.Before = &Content{SHA256: op.Before, Exec: op.BeforeExec}
		}
		if op.Action != plan.Remove {
			j.After = &Content{SHA256: op.After, Exec: op.Exec}
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
// found, which is then not dotai's.
func (s State) Owned() map[string]plan.Owned {
	owned := make(map[string]plan.Owned, len(s.Files))
	for _, f := range s.Files {
		owned[f.Target] = plan.Owned{Harness: f.Harness, Hash: f.SHA256, Exec: f.Exec}
	}
	if s.Pending == nil {
		return owned
	}
	for _, op := range s.Pending.Ops {
		o, had := owned[op.Target]
		switch {
		case op.After == nil: // a remove: what is left is what was owned
			if !had && op.Before != nil {
				owned[op.Target] = plan.Owned{Harness: op.Harness, Hash: op.Before.SHA256, Exec: op.Before.Exec}
			}
		case had:
			o.Also = append(o.Also, plan.Current{Hash: op.After.SHA256, Exec: op.After.Exec})
			owned[op.Target] = o
		default:
			o = plan.Owned{Harness: op.Harness, Hash: op.After.SHA256, Exec: op.After.Exec}
			if op.Before != nil {
				o.Foreign = []plan.Current{{Hash: op.Before.SHA256, Exec: op.Before.Exec}}
			}
			owned[op.Target] = o
		}
	}
	return owned
}

// Settled is s without a journal, owning exactly the targets in onDisk
// that its record owns, with the state onDisk gives them, in target
// order: what dotai owns once an interrupted install is resolved.
func (s State) Settled(onDisk map[string]plan.Current) State {
	owned := s.Owned()
	s.Pending, s.Files = nil, []File{}
	for _, target := range slices.Sorted(maps.Keys(onDisk)) {
		if o, ok := owned[target]; ok {
			cur := onDisk[target]
			s.Files = append(s.Files, File{Target: target, Harness: o.Harness, SHA256: cur.Hash, Exec: cur.Exec})
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
	}
	if s.Harnesses == nil {
		s.Harnesses = []string{}
	}
	if s.Files == nil {
		s.Files = []File{}
	}
	return s, nil
}
