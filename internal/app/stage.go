package app

import (
	"fmt"
	"os"
	"path"

	"github.com/lartweib/dotai/internal/merge"
	"github.com/lartweib/dotai/internal/pack"
)

// Staging scopes, also the directories under --out.
const (
	ScopeUser    = "user"
	ScopeProject = "project"
)

// StageResult is the outcome of staging a pack. Files is empty when there
// are diagnostics.
type StageResult struct {
	Files       []StagedFile      `json:"files"`
	Diagnostics []pack.Diagnostic `json:"diagnostics"`
}

// StagedFile is one written file: its scope, its path inside the scope's
// staging tree and the layer that provides it.
type StagedFile struct {
	Scope string `json:"scope"`
	Path  string `json:"path"`
	Layer string `json:"layer"`
}

// Stage merges the layers of the pack in dir that sel names and writes one
// staging tree per scope to out/user and out/project. A pack with problems
// gives diagnostics and writes nothing. out must be missing or empty unless
// force is set; then files with the same path are replaced and others are
// kept.
func Stage(dir, out string, sel Selection, force bool) (StageResult, error) {
	fsys, err := packFS(dir)
	if err != nil {
		return StageResult{}, err
	}
	p, diags, err := pack.Load(fsys)
	if err != nil {
		return StageResult{}, err
	}
	res := StageResult{Files: []StagedFile{}, Diagnostics: append([]pack.Diagnostic{}, diags...)}
	if p == nil || len(diags) > 0 {
		return res, nil
	}
	s, diags := merge.Stage(p, sel)
	if len(diags) > 0 {
		res.Diagnostics = diags
		return res, nil
	}
	trees := []struct {
		scope string
		files []merge.File
	}{{ScopeUser, s.User}, {ScopeProject, s.Project}}
	if err := prepareOut(out, force); err != nil {
		return StageResult{}, err
	}
	root, err := os.OpenRoot(out)
	if err != nil {
		return StageResult{}, err
	}
	defer root.Close()
	for _, t := range trees {
		for _, f := range t.files {
			if err := write(root, path.Join(t.scope, f.Path), f.Data); err != nil {
				return StageResult{}, err
			}
			res.Files = append(res.Files, StagedFile{Scope: t.scope, Path: f.Path, Layer: f.Layer})
		}
	}
	return res, nil
}

// prepareOut creates out, and refuses it when it has entries and force is
// not set.
func prepareOut(out string, force bool) error {
	if err := os.MkdirAll(out, 0o755); err != nil {
		return err
	}
	entries, err := os.ReadDir(out)
	if err != nil {
		return err
	}
	if len(entries) > 0 && !force {
		return fmt.Errorf("%s is not empty; use --force to write into it", out)
	}
	return nil
}

// write writes data to name inside root. root refuses any path or link
// that leads outside it, so nothing is written outside --out.
func write(root *os.Root, name string, data []byte) error {
	if err := root.MkdirAll(path.Dir(name), 0o755); err != nil {
		return err
	}
	return root.WriteFile(name, data, 0o644)
}
