// Package state is the install state of decision 0032, kept in
// ~/.dotai/state.json: what is installed, how, and the ownership record of
// every file the install wrote (decision 0013). It does no I/O; the caller
// reads and writes the file.
package state

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"reflect"
	"time"

	"github.com/lartweib/dotai/internal/plan"
)

// Schema is the version of the state format.
const Schema = "dotai.state/v1"

// State is the installed pack, its variant, mode and harnesses, when it
// was installed, the backup of the last install that wrote files ("" when
// none did) and the files it owns, in target order.
type State struct {
	Schema      string    `json:"schema"`
	Pack        Pack      `json:"pack"`
	Variant     string    `json:"variant"`
	Mode        string    `json:"mode"`
	InstalledAt time.Time `json:"installedAt"`
	Harnesses   []string  `json:"harnesses"`
	Backup      string    `json:"backup"`
	Files       []File    `json:"files"`
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

// Owned is the ownership record as the plan reads it, by target.
func (s State) Owned() map[string]plan.Owned {
	owned := make(map[string]plan.Owned, len(s.Files))
	for _, f := range s.Files {
		owned[f.Target] = plan.Owned{Harness: f.Harness, Hash: f.SHA256, Exec: f.Exec}
	}
	return owned
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
