package app

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"

	"github.com/lartweib/dotai/internal/merge"
	"github.com/lartweib/dotai/internal/rulesync"
)

// Staging scopes, also the directories under --out.
const (
	ScopeUser    = "user"
	ScopeProject = "project"
)

// StageMarker is the file that marks a directory written by Stage. Only a
// directory that has it is ever replaced.
const StageMarker = ".dotai-stage"

// StageResult is the outcome of staging a pack. Files is empty when there
// are diagnostics.
type StageResult struct {
	Files       []StagedFile `json:"files"`
	Diagnostics []Diagnostic `json:"diagnostics"`
	Pack        *PackSummary `json:"-"` // names the pack in the text report; nil when the manifest cannot be read
}

// StagedFile is one written file: its scope, its path inside the scope's
// staging tree and the layer that provides it. A merged file has no layer
// and gives the layer of each entry in Origins. A file rendered from the
// staged sources has no layer and names its Generator, such as rulesync.
type StagedFile struct {
	Scope     string            `json:"scope"`
	Path      string            `json:"path"`
	Layer     string            `json:"layer"`
	Origins   map[string]string `json:"origins,omitempty"`
	Generator string            `json:"generator,omitempty"`
}

// UsageError is a command called with arguments that cannot work, such as
// an --out inside the pack.
type UsageError struct{ msg string }

func (e *UsageError) Error() string { return e.msg }

// stageFS is the port Stage writes through.
type stageFS interface {
	List(dir string) ([]string, error)                               // entry names; an fs.ErrNotExist error when dir is missing
	Files(dir string) ([]string, error)                              // slash paths of every file under dir
	TempDir(beside string) (string, error)                           // a new empty directory next to beside, on its volume
	WriteFile(dir, name string, data []byte, perm fs.FileMode) error // name is a slash path inside dir; nothing outside dir is written
	Rename(from, to string) error
	RemoveAll(path string) error
}

// Stage merges the layers of the pack in dir that sel names and writes one
// staging tree per scope to out/user and out/project. Like an install, it
// first validates every layer of the pack; a pack with problems gives
// diagnostics and writes nothing. out must not be inside the pack. It must
// be missing or empty, or hold an earlier staging, which is replaced as a
// whole when force is set. The new staging is written next to out and
// moved into place, so a failure leaves out as it was. Before the move,
// the rulesync the pack pins runs over the .rulesync/ of each scope tree,
// with that tree as its home directory, and writes the output of the
// pack's harnesses into it: the user scope at the paths it has under the
// home directory, the project scope at those of a project. Each scope's
// tools are staged at their install path, .dotai/tools/, and the path
// variables of every staged text file become the paths in tools before
// rulesync runs. rulesync is fetched into cacheDir as FetchRulesync does;
// progress may be nil.
func Stage(ctx context.Context, cacheDir, dir, out string, sel Selection, tools ToolPaths, force bool, progress Progress) (StageResult, error) {
	return stage(ctx, osStageFS{}, download, generate, cacheDir, dir, out, sel, tools, force, progress)
}

// GeneratorRulesync is the Generator of the files rulesync renders.
const GeneratorRulesync = "rulesync"

func stage(ctx context.Context, w stageFS, fetch fetchFunc, run runFunc, cacheDir, dir, out string, sel Selection, tools ToolPaths, force bool, progress Progress) (StageResult, error) {
	if err := checkOut(dir, out); err != nil {
		return StageResult{}, err
	}
	fsys, err := packFS(dir)
	if err != nil {
		return StageResult{}, err
	}
	v, p, err := validate(fsys, Selection{})
	if err != nil {
		return StageResult{}, err
	}
	res := StageResult{Files: []StagedFile{}, Diagnostics: v.Diagnostics, Pack: v.Pack}
	if !v.Valid {
		return res, nil
	}
	s, diags := merge.Stage(p, sel)
	if len(diags) > 0 {
		res.Diagnostics = diags
		return res, nil
	}
	replace, err := checkTarget(w, out, force)
	if err != nil {
		return StageResult{}, err
	}
	files := map[string]content{StageMarker: {data: []byte("Written by dotai stage. dotai stage --force replaces this directory.\n"), perm: 0o644}}
	for _, t := range []struct {
		scope string
		files []merge.File
	}{{ScopeUser, s.User}, {ScopeProject, s.Project}} {
		for _, f := range t.files {
			c := content{data: expandPaths(f.Data, tools), perm: 0o644}
			if f.Exec {
				c.perm = 0o755
			}
			files[path.Join(t.scope, f.Path)] = c
			res.Files = append(res.Files, StagedFile{Scope: t.scope, Path: f.Path, Layer: f.Layer, Origins: f.Origins})
		}
	}
	bin, err := fetch(ctx, cacheDir, p.Manifest.Rulesync, progress)
	if err != nil {
		return StageResult{}, err
	}
	// render runs rulesync over the scopes that have sources and returns
	// the files it added.
	render := func(tmp string) ([]StagedFile, error) {
		var scopes []string
		var jobs []rulesync.Job
		for _, scope := range []string{ScopeUser, ScopeProject} {
			if hasSources(files, scope) {
				scopes = append(scopes, scope)
				jobs = append(jobs, rulesync.Job{Dir: filepath.Join(tmp, scope), Global: scope == ScopeUser})
			}
		}
		if len(jobs) == 0 {
			return nil, nil
		}
		if err := run(ctx, bin, p.Manifest.Harnesses, jobs...); err != nil {
			return nil, err
		}
		var rendered []StagedFile
		for i, scope := range scopes {
			names, err := w.Files(jobs[i].Dir)
			if err != nil {
				return nil, err
			}
			for _, name := range names {
				if _, staged := files[path.Join(scope, name)]; !staged {
					rendered = append(rendered, StagedFile{Scope: scope, Path: name, Generator: GeneratorRulesync})
				}
			}
		}
		return rendered, nil
	}
	rendered, err := swap(w, out, replace, files, render)
	if err != nil {
		return StageResult{}, err
	}
	res.Files = append(res.Files, rendered...)
	return res, nil
}

// content is a file to write and its permissions.
type content struct {
	data []byte
	perm fs.FileMode
}

// hasSources reports whether files give scope a .rulesync/ to render.
func hasSources(files map[string]content, scope string) bool {
	for name := range files {
		if strings.HasPrefix(name, scope+"/.rulesync/") {
			return true
		}
	}
	return false
}

// checkOut rejects an out that is the pack directory or inside it.
func checkOut(dir, out string) error {
	d, err := filepath.Abs(dir)
	if err != nil {
		return err
	}
	o, err := filepath.Abs(out)
	if err != nil {
		return err
	}
	if rel, err := filepath.Rel(d, o); err == nil && (rel == "." || filepath.IsLocal(rel)) {
		return &UsageError{fmt.Sprintf("--out %s is inside the pack %s", out, dir)}
	}
	return nil
}

// checkTarget reports whether out exists and must be replaced. A non-empty
// out is replaced only when it has the marker and force is set.
func checkTarget(w stageFS, out string, force bool) (replace bool, err error) {
	names, err := w.List(out)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return false, nil
	case err != nil:
		return false, err
	case len(names) == 0:
		return true, nil
	case !slices.Contains(names, StageMarker):
		return false, fmt.Errorf("%s is not empty and was not written by dotai stage; refusing to replace it", out)
	case !force:
		return false, fmt.Errorf("%s holds an earlier staging; use --force to replace it", out)
	}
	return true, nil
}

// swap writes files into a temporary directory next to out, lets render
// add to it and returns what render added, then moves the directory into
// place. An existing out is first moved aside and is put back if the move
// fails. On failure out is unchanged and the temporary directory is
// removed.
func swap(w stageFS, out string, replace bool, files map[string]content, render func(tmp string) ([]StagedFile, error)) (rendered []StagedFile, err error) {
	failed := func(err error) ([]StagedFile, error) {
		return nil, fmt.Errorf("staging into %s failed, it was left as it was: %w", out, err)
	}
	tmp, err := w.TempDir(out)
	if err != nil {
		return failed(err)
	}
	defer func() {
		if err != nil {
			err = errors.Join(err, w.RemoveAll(tmp))
		}
	}()
	for _, name := range slices.Sorted(maps.Keys(files)) {
		if err := w.WriteFile(tmp, name, files[name].data, files[name].perm); err != nil {
			return failed(err)
		}
	}
	if rendered, err = render(tmp); err != nil {
		return failed(err)
	}
	if !replace {
		if err := w.Rename(tmp, out); err != nil {
			return failed(err)
		}
		return rendered, nil
	}
	old := tmp + ".old"
	if err := w.Rename(out, old); err != nil {
		return failed(err)
	}
	if err := w.Rename(tmp, out); err != nil {
		return failed(errors.Join(err, w.Rename(old, out)))
	}
	if err := w.RemoveAll(old); err != nil {
		return nil, fmt.Errorf("staged into %s, but the previous staging at %s could not be removed: %w", out, old, err)
	}
	return rendered, nil
}

// osStageFS is the stageFS of the real file system.
type osStageFS struct{}

func (osStageFS) List(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	return names, err
}

// TempDir returns an absolute path, so that a relative --out still gives
// rulesync an absolute home directory.
func (osStageFS) TempDir(beside string) (string, error) {
	abs, err := filepath.Abs(beside)
	if err != nil {
		return "", err
	}
	parent := filepath.Dir(abs)
	if err := os.MkdirAll(parent, 0o755); err != nil {
		return "", err
	}
	return os.MkdirTemp(parent, StageMarker+"-*")
}

// WriteFile writes through os.Root, which refuses any path or link that
// leads outside dir.
func (osStageFS) WriteFile(dir, name string, data []byte, perm fs.FileMode) error {
	root, err := os.OpenRoot(dir)
	if err != nil {
		return err
	}
	defer root.Close()
	if err := root.MkdirAll(path.Dir(name), 0o755); err != nil {
		return err
	}
	return root.WriteFile(name, data, perm)
}

func (osStageFS) Files(dir string) ([]string, error) {
	var names []string
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, err := filepath.Rel(dir, p)
		names = append(names, filepath.ToSlash(rel))
		return err
	})
	return names, err
}

func (osStageFS) Rename(from, to string) error { return os.Rename(from, to) }
func (osStageFS) RemoveAll(path string) error  { return os.RemoveAll(path) }
