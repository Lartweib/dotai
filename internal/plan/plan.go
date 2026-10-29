// Package plan builds the plan of decision 0028: the exact file operations
// a command will carry out, decided before anything is written. It does no
// I/O; the caller hashes the staged files and the current targets.
//
// It covers the user scope of an isolated install (decision 0031): each
// staged file is created, replaced or left unchanged at its install path.
// Key-level merges into files other tools also write come with the project
// scope, and removing files an earlier install owned comes with the
// ownership record.
package plan

import (
	"cmp"
	"fmt"
	"io/fs"
	"path"
	"path/filepath"
	"slices"
	"strings"

	"github.com/lartweib/dotai/internal/harness"
	"github.com/lartweib/dotai/internal/merge"
)

// ScopeUser is the scope of the user-level configuration.
const ScopeUser = "user"

// sourcesDir holds rulesync's sources in a staging tree. They are the
// input of the render, not part of the install, so they are never planned.
const sourcesDir = ".rulesync/"

// Action is what an operation does to its target.
type Action string

// Actions. A target with the staged content but another exec bit is
// replaced, so that it ends with the staged mode.
const (
	Create    Action = "create"    // the target is missing
	Replace   Action = "replace"   // the target has other content or another exec bit
	Unchanged Action = "unchanged" // the target already has the staged content and exec bit
)

// File is one file of the user-scope staging tree: its slash path inside
// the tree, the SHA-256 of its content in hex and whether it is
// executable. The tree holds the files of a scope only; the stage marker
// sits beside it and is never part of it.
type File struct {
	Path string
	Hash string
	Exec bool
}

// Current is the state of a target that exists: the SHA-256 of its
// content in hex and whether it is executable.
type Current struct {
	Hash string
	Exec bool
}

// Input is what Build plans from. Disk holds the current state of each
// target that exists; a target missing from Disk does not exist. Its keys
// must be the targets Locate returns, which are also the Targets of the
// plan. IgnoreExec leaves the exec bit out of the comparison, for a file
// system that has none, such as on Windows; the caller decides it, so that
// Build behaves the same on every OS.
type Input struct {
	Home       string            // the absolute home directory
	Adapters   []harness.Adapter // the harnesses of the pack's manifest
	Files      []File
	Disk       map[string]Current
	IgnoreExec bool
}

// Plan is the list of operations, in target order.
type Plan struct {
	Ops []Op
}

// Op is one file operation. Harness is empty for a tool, which is not
// installed for one harness. Before and BeforeExec are the target's
// current hash and exec bit, empty and false when it is missing, so that
// execution and backup can check that it did not change after planning.
// After and Exec are what it will have.
type Op struct {
	Action     Action
	Scope      string
	Harness    string
	Source     string // slash path inside the scope's staging tree
	Target     string // absolute path
	Exec       bool
	Before     string
	BeforeExec bool
	After      string
}

// UnmappedError is a staged file that no harness of the pack and no tools
// rule installs. Paths lists every such file, sorted.
type UnmappedError struct {
	Paths []string
}

func (e *UnmappedError) Error() string {
	return "no harness of the pack installs the staged files " + strings.Join(e.Paths, ", ")
}

// Build plans the install of the user-scope staging tree. A file under
// one of the adapters' staging paths installs into that harness's
// isolated directory, a file under the tools directory into the user's
// tools directory, and rulesync's sources are skipped. Any other file is
// an UnmappedError, so that nothing staged is dropped silently. A path
// Windows cannot store is an error on every OS, and so are two files with
// one target, a harness given twice and a harness without isolation.
func Build(in Input) (Plan, error) {
	if !filepath.IsAbs(in.Home) {
		return Plan{}, fmt.Errorf("home directory %q is not absolute", in.Home)
	}
	seen := map[string]bool{}
	for _, a := range in.Adapters {
		if seen[a.ID] {
			return Plan{}, fmt.Errorf("harness %q is given twice", a.ID)
		}
		if a.Isolation.Level == harness.None {
			return Plan{}, fmt.Errorf("harness %q does not offer the isolated mode (decision 0018)", a.ID)
		}
		seen[a.ID] = true
	}
	var p Plan
	var unmapped []string
	sources := map[string]string{} // the source of each target
	for _, f := range in.Files {
		if err := checkPath(f.Path); err != nil {
			return Plan{}, err
		}
		if strings.HasPrefix(f.Path, sourcesDir) {
			continue
		}
		h, target, ok := Locate(in.Home, in.Adapters, f.Path)
		if !ok {
			unmapped = append(unmapped, f.Path)
			continue
		}
		if prev, ok := sources[target]; ok {
			return Plan{}, fmt.Errorf("staged files %s and %s install to the same target %s", prev, f.Path, target)
		}
		sources[target] = f.Path
		op := Op{Action: Create, Scope: ScopeUser, Harness: h, Source: f.Path, Target: target, Exec: f.Exec, After: f.Hash}
		if cur, ok := in.Disk[target]; ok {
			op.Before, op.BeforeExec, op.Action = cur.Hash, cur.Exec, Replace
			if cur.Hash == f.Hash && (in.IgnoreExec || cur.Exec == f.Exec) {
				op.Action = Unchanged
			}
		}
		p.Ops = append(p.Ops, op)
	}
	if len(unmapped) > 0 {
		slices.Sort(unmapped)
		return Plan{}, &UnmappedError{Paths: unmapped}
	}
	slices.SortFunc(p.Ops, func(a, b Op) int { return cmp.Compare(a.Target, b.Target) })
	return p, nil
}

// Locate returns the harness that installs the staged file source, "" for
// a tool, and its absolute target under home with the OS separator. Build
// uses it, so a caller that builds Input.Disk with it gets the same keys.
// ok is false when nothing installs source. rulesync's sources are not
// special here; Build skips them before it calls Locate.
func Locate(home string, adapters []harness.Adapter, source string) (h, target string, ok bool) {
	at := func(rel string) string { return filepath.Join(home, filepath.FromSlash(rel)) }
	if rest, ok := strings.CutPrefix(source, merge.ToolsDir+"/"); ok {
		return "", at(path.Join(merge.ToolsDir, rest)), true
	}
	for _, a := range adapters {
		if rest, ok := strings.CutPrefix(source, a.Staging+"/"); ok {
			return a.ID, at(path.Join(harness.IsolatedDir, a.Dir, a.Install, rest)), true
		}
	}
	return "", "", false
}

// reserved are the device names Windows refuses as a file name, with or
// without an extension.
var reserved = []string{"CON", "PRN", "AUX", "NUL",
	"COM1", "COM2", "COM3", "COM4", "COM5", "COM6", "COM7", "COM8", "COM9",
	"LPT1", "LPT2", "LPT3", "LPT4", "LPT5", "LPT6", "LPT7", "LPT8", "LPT9"}

// forbidden are the characters Windows refuses in a file name, besides
// the control characters and the separators.
const forbidden = `:<>"|?*`

// checkPath rejects a staged path that is not a slash path inside the
// tree, or that Windows cannot store: a backslash, which separates there,
// or a segment with a control character such as NUL, a character in
// forbidden or a device name.
func checkPath(name string) error {
	if !fs.ValidPath(name) || name == "." || strings.Contains(name, `\`) {
		return fmt.Errorf("staged path %q is not a slash path inside the tree", name)
	}
	for seg := range strings.SplitSeq(name, "/") {
		base, _, _ := strings.Cut(seg, ".")
		control := strings.ContainsFunc(seg, func(r rune) bool { return r < 0x20 })
		if control || strings.ContainsAny(seg, forbidden) || slices.Contains(reserved, strings.ToUpper(base)) {
			return fmt.Errorf("staged path %q: %q is not a valid file name on Windows", name, seg)
		}
	}
	return nil
}
