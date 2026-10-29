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

// Actions.
const (
	Create    Action = "create"    // the target is missing
	Replace   Action = "replace"   // the target has other content
	Unchanged Action = "unchanged" // the target already has the staged content
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

// Input is what Build plans from. Disk holds the SHA-256, in hex, of each
// target that exists, by its absolute path as Build writes it; a target
// missing from Disk does not exist.
type Input struct {
	Home     string            // the absolute home directory
	Adapters []harness.Adapter // the harnesses of the pack's manifest
	Files    []File
	Disk     map[string]string
}

// Plan is the list of operations, in target order.
type Plan struct {
	Ops []Op
}

// Op is one file operation. Harness is empty for a tool, which is not
// installed for one harness. Before is the hash of the target's current
// content, empty when it is missing, and After the hash it will have.
type Op struct {
	Action  Action
	Scope   string
	Harness string
	Source  string // slash path inside the scope's staging tree
	Target  string // absolute path
	Exec    bool
	Before  string
	After   string
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
// an UnmappedError, so that nothing staged is dropped silently.
func Build(in Input) (Plan, error) {
	if !filepath.IsAbs(in.Home) {
		return Plan{}, fmt.Errorf("home directory %q is not absolute", in.Home)
	}
	var p Plan
	var unmapped []string
	for _, f := range in.Files {
		if !fs.ValidPath(f.Path) || f.Path == "." || strings.Contains(f.Path, `\`) { // a backslash separates on Windows
			return Plan{}, fmt.Errorf("staged path %q is not a slash path inside the tree", f.Path)
		}
		if strings.HasPrefix(f.Path, sourcesDir) {
			continue
		}
		h, target, ok := locate(in, f.Path)
		if !ok {
			unmapped = append(unmapped, f.Path)
			continue
		}
		op := Op{Action: Create, Scope: ScopeUser, Harness: h, Source: f.Path, Target: target, Exec: f.Exec, After: f.Hash}
		if before, ok := in.Disk[target]; ok {
			op.Before, op.Action = before, Replace
			if before == f.Hash {
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

// locate returns the harness that installs the staged file name, "" for a
// tool, and its absolute target.
func locate(in Input, name string) (h, target string, ok bool) {
	at := func(rel string) string { return filepath.Join(in.Home, filepath.FromSlash(rel)) }
	if rest, ok := strings.CutPrefix(name, merge.ToolsDir+"/"); ok {
		return "", at(path.Join(merge.ToolsDir, rest)), true
	}
	for _, a := range in.Adapters {
		if rest, ok := strings.CutPrefix(name, a.Staging+"/"); ok {
			return a.ID, at(path.Join(harness.IsolatedDir, a.Dir, a.Install, rest)), true
		}
	}
	return "", "", false
}
