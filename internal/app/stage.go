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

	"github.com/lartweib/dotai/internal/harness"
	"github.com/lartweib/dotai/internal/merge"
	"github.com/lartweib/dotai/internal/pack"
	"github.com/lartweib/dotai/internal/plan"
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
	// ToolCommands are the tool commands the settings run (decision 0037),
	// which an install plan shows.
	ToolCommands []ToolCommand `json:"-"`
	// Harnesses are the harnesses it rendered, which an install plans.
	Harnesses []string `json:"-"`
	// Warnings are what an install plan warns about the staging, such as
	// a harness that does not get the session-start hook (decision 0045).
	Warnings []plan.Warning `json:"-"`
	// TemplateFiles are the files of the pack that are the template's,
	// unchanged (decision 0046), which an install or apply plan warns
	// about in the layers it installs.
	TemplateFiles []string `json:"-"`
}

// StagedFile is one written file: its scope, its path inside the scope's
// staging tree and the layer that provides it. A merged file has no layer
// and gives the layer of each entry in Origins. A file rendered from the
// staged sources has no layer and names its Generator, such as rulesync,
// and so does a file dotai makes, such as a mod's plugin manifest;
// a settings file also gives the layer of each key in Origins.
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
	ReadFile(dir, name string) ([]byte, error)                       // name is a slash path inside dir; an fs.ErrNotExist error when it is missing
	Mode(dir, name string) (fs.FileMode, error)                      // the permission bits of the file name, a slash path inside dir
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
// home directory, the project scope at those of a project; where rulesync
// names that home directory, the real one of tools takes its place. Each
// scope's tools are staged at their install path, .dotai/tools/, and the path
// variables of every staged text file become the paths in tools before
// rulesync runs. After rulesync, the merged settings of each scope are
// written into each harness's settings file, after the keys rulesync wrote
// there; a key written by both is a settings-conflict diagnostic, and
// nothing is written. rulesync is fetched into cacheDir as FetchRulesync
// does; progress may be nil.
func Stage(ctx context.Context, cacheDir, dir, out string, sel Selection, tools ToolPaths, force bool, progress Progress) (StageResult, error) {
	return stage(ctx, osStageFS{}, download, generate, cacheDir, dir, out, sel, nil, tools, force, progress)
}

// pickHarnesses chooses, once the pack is valid, the harnesses a stage
// renders, in the manifest's order; nil renders every harness the pack
// supports, the optional ones included (decision 0038).
type pickHarnesses func(m pack.Manifest) ([]string, error)

// GeneratorRulesync is the Generator of the files rulesync renders.
const GeneratorRulesync = "rulesync"

func stage(ctx context.Context, w stageFS, fetch fetchFunc, run runFunc, cacheDir, dir, out string, sel Selection, pick pickHarnesses, tools ToolPaths, force bool, progress Progress) (StageResult, error) {
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
	res := StageResult{Files: []StagedFile{}, Diagnostics: v.Diagnostics, Pack: v.Pack, TemplateFiles: templateFiles(v.Warnings)}
	if !v.Valid {
		return res, nil
	}
	s, diags := merge.Stage(p, sel)
	if len(diags) > 0 {
		res.Diagnostics = diags
		return res, nil
	}
	harnesses := p.Manifest.Harnesses
	if pick != nil {
		if harnesses, err = pick(p.Manifest); err != nil {
			return StageResult{}, err
		}
	}
	res.Harnesses = harnesses
	if tools.Onboarding != nil {
		e, warnings := onboardingEngine(p, *v.Pack, harnesses, *tools.Onboarding, tools.goos())
		res.Warnings = append(res.Warnings, warnings...)
		if e != nil {
			s.AddEngine(*e)
		}
	}
	for _, t := range []struct {
		scope    string
		settings map[string][]merge.Setting
	}{{ScopeUser, s.UserSettings}, {ScopeProject, s.ProjectSettings}} {
		commands, err := toolCommands(t.scope, harnesses, t.settings, tools)
		if err != nil {
			return StageResult{}, err
		}
		res.ToolCommands = append(res.ToolCommands, commands...)
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
			data := f.Data
			if f.Path == merge.HooksFile {
				if data, err = expandHooks(data, f.Origins, tools, t.scope, harnesses); err != nil {
					if d, ok := hookPathDiagnostic(err); ok {
						res.Files, res.Diagnostics = []StagedFile{}, []Diagnostic{d}
						return res, nil
					}
					return StageResult{}, fmt.Errorf("%s/%s: %w", t.scope, f.Path, err)
				}
			}
			// A mod is the pack author's own plugin tree, copied byte for byte.
			if t.scope != ScopeProject || !strings.HasPrefix(f.Path, merge.ModsDir+"/") {
				if data, err = expandPaths(data, tools); err != nil {
					return StageResult{}, fmt.Errorf("%s/%s: %w", t.scope, f.Path, err)
				}
			}
			c := content{data: data, perm: 0o644}
			if f.Exec {
				c.perm = 0o755
			}
			files[path.Join(t.scope, f.Path)] = c
			res.Files = append(res.Files, StagedFile{Scope: t.scope, Path: f.Path, Layer: f.Layer, Origins: f.Origins, Generator: f.Generator})
		}
	}
	bin, err := fetch(ctx, cacheDir, p.Manifest.Rulesync, progress)
	if err != nil {
		return StageResult{}, err
	}
	// render runs rulesync over the scopes that have sources, then writes
	// the settings of each scope, and returns the files they added.
	render := func(tmp string) ([]StagedFile, error) {
		var jobs []rulesync.Job
		for _, scope := range []string{ScopeUser, ScopeProject} {
			if hasSources(files, scope) {
				jobs = append(jobs, rulesync.Job{Dir: filepath.Join(tmp, scope), Global: scope == ScopeUser})
			}
		}
		if len(jobs) > 0 {
			if err := run(ctx, bin, harnesses, jobs...); err != nil {
				return nil, err
			}
		}
		adapters, err := harness.Lookup(harnesses)
		if err != nil {
			return nil, err
		}
		if err := renameProjectFiles(w, filepath.Join(tmp, ScopeProject), adapters, p.Manifest.Rulesync); err != nil {
			return nil, err
		}
		for _, j := range jobs {
			if err := writeRealHome(w, tmp, filepath.Base(j.Dir), files, tools); err != nil {
				return nil, err
			}
		}
		var rendered []StagedFile
		for _, t := range []struct {
			scope    string
			settings map[string][]merge.Setting
		}{{ScopeUser, s.UserSettings}, {ScopeProject, s.ProjectSettings}} {
			dir := filepath.Join(tmp, t.scope)
			written, err := renderSettings(w, dir, t.scope, harnesses, t.settings, tools)
			if err != nil {
				return nil, err
			}
			if !hasSources(files, t.scope) && len(written) == 0 {
				continue
			}
			names, err := w.Files(dir)
			if err != nil {
				return nil, err
			}
			for _, name := range names {
				i := slices.IndexFunc(written, func(f StagedFile) bool { return f.Path == name })
				switch _, staged := files[path.Join(t.scope, name)]; {
				case i >= 0:
					rendered = append(rendered, written[i])
				case !staged:
					rendered = append(rendered, StagedFile{Scope: t.scope, Path: name, Generator: GeneratorRulesync})
				}
			}
		}
		return rendered, nil
	}
	rendered, err := swap(w, out, replace, files, render)
	if c, ok := errors.AsType[*SettingsConflict](err); ok {
		res.Files, res.Diagnostics = []StagedFile{}, []Diagnostic{c.diagnostic()}
		return res, nil
	}
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
// leads outside dir. dir is created when it is missing.
func (osStageFS) WriteFile(dir, name string, data []byte, perm fs.FileMode) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
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

func (osStageFS) ReadFile(dir, name string) ([]byte, error) {
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	return root.ReadFile(name)
}

func (osStageFS) Mode(dir, name string) (fs.FileMode, error) {
	root, err := os.OpenRoot(dir)
	if err != nil {
		return 0, err
	}
	defer root.Close()
	fi, err := root.Stat(name)
	if err != nil {
		return 0, err
	}
	return fi.Mode().Perm(), nil
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
