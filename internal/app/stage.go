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
	List(dir string) ([]string, error)             // entry names; an fs.ErrNotExist error when dir is missing
	Files(dir string) ([]string, error)            // slash paths of every file under dir
	TempDir(beside string) (string, error)         // a new empty directory next to beside, on its volume
	WriteFile(dir, name string, data []byte) error // name is a slash path inside dir; nothing outside dir is written
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
// home directory, the project scope at those of a project.
func Stage(ctx context.Context, rs Rulesync, dir, out string, sel Selection, force bool) (StageResult, error) {
	return stage(ctx, osStageFS{}, rs, dir, out, sel, force)
}

func stage(ctx context.Context, w stageFS, rs Rulesync, dir, out string, sel Selection, force bool) (StageResult, error) {
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
	files := map[string][]byte{StageMarker: []byte("Written by dotai stage. dotai stage --force replaces this directory.\n")}
	for _, t := range []struct {
		scope string
		files []merge.File
	}{{ScopeUser, s.User}, {ScopeProject, s.Project}} {
		for _, f := range t.files {
			files[path.Join(t.scope, f.Path)] = f.Data
			res.Files = append(res.Files, StagedFile{Scope: t.scope, Path: f.Path, Layer: f.Layer, Origins: f.Origins})
		}
	}
	bin, err := rs.Fetch(ctx, p.Manifest.Rulesync)
	if err != nil {
		return StageResult{}, err
	}
	render := func(tmp string) error {
		for _, scope := range []string{ScopeUser, ScopeProject} {
			if !hasSources(files, scope) {
				continue
			}
			d := filepath.Join(tmp, scope)
			if err := rs.Run(ctx, bin, d, scope == ScopeUser, p.Manifest.Harnesses); err != nil {
				return fmt.Errorf("rendering the %s scope: %w", scope, err)
			}
			written, err := w.Files(d)
			if err != nil {
				return err
			}
			for _, name := range written {
				if _, staged := files[path.Join(scope, name)]; !staged {
					res.Files = append(res.Files, StagedFile{Scope: scope, Path: name, Generator: "rulesync"})
				}
			}
		}
		return nil
	}
	return res, swap(w, out, replace, files, render)
}

// hasSources reports whether files give scope a .rulesync/ to render.
func hasSources(files map[string][]byte, scope string) bool {
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
// add to it, then moves it into place. An existing out is first moved aside and is put back if the
// move fails. On failure out is unchanged and the temporary directory is
// removed.
func swap(w stageFS, out string, replace bool, files map[string][]byte, render func(tmp string) error) (err error) {
	failed := func(err error) error {
		return fmt.Errorf("staging into %s failed, it was left as it was: %w", out, err)
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
		if err := w.WriteFile(tmp, name, files[name]); err != nil {
			return failed(err)
		}
	}
	if err := render(tmp); err != nil {
		return failed(err)
	}
	if !replace {
		if err := w.Rename(tmp, out); err != nil {
			return failed(err)
		}
		return nil
	}
	old := tmp + ".old"
	if err := w.Rename(out, old); err != nil {
		return failed(err)
	}
	if err := w.Rename(tmp, out); err != nil {
		return failed(errors.Join(err, w.Rename(old, out)))
	}
	if err := w.RemoveAll(old); err != nil {
		return fmt.Errorf("staged into %s, but the previous staging at %s could not be removed: %w", out, old, err)
	}
	return nil
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

func (osStageFS) TempDir(beside string) (string, error) {
	parent := filepath.Dir(beside)
	if err := os.MkdirAll(parent, 0o755); err != nil {
		return "", err
	}
	return os.MkdirTemp(parent, StageMarker+"-*")
}

// WriteFile writes through os.Root, which refuses any path or link that
// leads outside dir.
func (osStageFS) WriteFile(dir, name string, data []byte) error {
	root, err := os.OpenRoot(dir)
	if err != nil {
		return err
	}
	defer root.Close()
	if err := root.MkdirAll(path.Dir(name), 0o755); err != nil {
		return err
	}
	return root.WriteFile(name, data, 0o644)
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
