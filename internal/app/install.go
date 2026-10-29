package app

import (
	"cmp"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/lartweib/dotai/internal/harness"
	"github.com/lartweib/dotai/internal/pack"
	"github.com/lartweib/dotai/internal/plan"
)

// ModeIsolated is the install mode of decision 0031, the only one so far.
const ModeIsolated = "isolated"

// CodePathUnmapped is the diagnostic of a staged file that no harness of
// the pack and no tools rule installs.
const CodePathUnmapped = "path-unmapped"

// InstallPlan is the plan of an install of a pack's user scope: the
// isolation of each harness, which decision 0018 requires to show before
// installing, and the file operations. Lists are never nil; they are
// empty when there are diagnostics.
type InstallPlan struct {
	Pack        *PackSummary  `json:"pack"`    // nil when the manifest cannot be read
	Variant     string        `json:"variant"` // the variant asked for, or the pack's default
	Mode        string        `json:"mode"`
	Root        string        `json:"root"` // the directory that holds each harness's isolated directory
	Harnesses   []HarnessPlan `json:"harnesses"`
	Ops         []InstallOp   `json:"ops"`
	Diagnostics []Diagnostic  `json:"diagnostics"`
}

// HarnessPlan is how one harness runs isolated: its directory, the
// variables or flags that point it there, whether the user must log in
// again and what the user's own setup still provides.
type HarnessPlan struct {
	ID       string        `json:"id"`
	Level    string        `json:"level"`
	Dir      string        `json:"dir"`
	Pointers []PointerPlan `json:"pointers"`
	Relogin  bool          `json:"relogin"`
	Leaks    []string      `json:"leaks"`
}

// PointerPlan is an environment variable (env) or a flag (flag) set to an
// absolute directory.
type PointerPlan struct {
	Mechanism string `json:"mechanism"`
	Name      string `json:"name"`
	Dir       string `json:"dir"`
}

// InstallOp is one operation of the plan; see plan.Op. Harness is empty
// for a tool.
type InstallOp struct {
	Action     plan.Action `json:"action"`
	Harness    string      `json:"harness"`
	Source     string      `json:"source"`
	Target     string      `json:"target"`
	Exec       bool        `json:"exec"`
	Before     string      `json:"before"`
	After      string      `json:"after"`
	BeforeExec bool        `json:"beforeExec"`
}

// NotRegularError is a path on the way to a target that the install would
// have to follow or replace: a target that is not a regular file, or a
// directory above it that is a link, a junction or another reparse point,
// or not a directory at all. Dir is set for such a directory.
type NotRegularError struct {
	Path string
	Mode fs.FileMode
	Dir  bool
}

func (e *NotRegularError) Error() string {
	var kind string
	switch {
	case e.Mode&fs.ModeSymlink != 0:
		kind = "a link"
	case e.Mode&fs.ModeIrregular != 0:
		kind = "a junction or another special file"
	case e.Mode.IsDir():
		kind = "a directory"
	case e.Mode.IsRegular():
		kind = "a file"
	default:
		kind = "not a regular file"
	}
	want := "a regular file"
	if e.Dir {
		want = "a directory"
	}
	return fmt.Sprintf("%s is %s where the install needs %s; dotai never follows links or replaces what it did not write: move or remove it, then plan again", e.Path, kind, want)
}

// installFS is the port PlanInstall reads the staged files and the disk
// through. It stages through the stageFS it embeds.
type installFS interface {
	stageFS
	StagingDir() (string, error)            // a new empty temporary directory
	Lstat(name string) (fs.FileMode, error) // the mode of name itself, not of what a link at it points to
	// Hash reads the regular file name, which is absolute, without
	// following a link at it, and stops when ctx is done. It gives an
	// fs.ErrNotExist error when name is missing and a *NotRegularError
	// when it is not a regular file.
	Hash(ctx context.Context, name string) (plan.Current, error)
	ExecBit() bool // whether the file system keeps an exec bit
}

// PlanInstall plans the isolated install of the user scope of the pack in
// dir, with variant or the pack's default, into the absolute home
// directory home. It stages the pack as Stage does, into a temporary
// directory it removes, with ${DOTAI_TOOLS} at home's tools directory,
// then compares each staged file with its target. Apart from fetching
// rulesync, it writes nothing else. A pack with problems, including a
// staged file that nothing installs, gives diagnostics. rulesync is
// fetched into cacheDir as FetchRulesync does; progress may be nil.
func PlanInstall(ctx context.Context, cacheDir, dir, variant, home string, progress Progress) (InstallPlan, error) {
	return planInstall(ctx, osInstallFS{}, download, generate, cacheDir, dir, variant, home, progress)
}

func planInstall(ctx context.Context, d installFS, fetch fetchFunc, run runFunc, cacheDir, dir, variant, home string, progress Progress) (res InstallPlan, err error) {
	if !filepath.IsAbs(home) {
		return InstallPlan{}, fmt.Errorf("home directory %q is not absolute", home)
	}
	tmp, err := d.StagingDir()
	if err != nil {
		return InstallPlan{}, err
	}
	defer func() {
		if rmErr := d.RemoveAll(tmp); rmErr != nil {
			res, err = InstallPlan{}, errors.Join(err, fmt.Errorf("the temporary staging %s could not be removed: %w", tmp, rmErr))
		}
	}()
	out := filepath.Join(tmp, "staging")
	tools := ToolsIn(home, "")
	tools.Project, tools.ProjectErr = "", errors.New("an install covers the user scope only")
	s, err := stage(ctx, d, fetch, run, cacheDir, dir, out, Selection{Variant: variant}, tools, false, progress)
	if err != nil {
		return InstallPlan{}, err
	}
	res = InstallPlan{
		Pack: s.Pack, Variant: variant, Mode: ModeIsolated, Root: filepath.Join(home, filepath.FromSlash(harness.IsolatedDir)),
		Harnesses: []HarnessPlan{}, Ops: []InstallOp{}, Diagnostics: s.Diagnostics,
	}
	if s.Pack != nil {
		res.Variant = cmp.Or(variant, s.Pack.DefaultVariant)
	}
	if len(s.Diagnostics) > 0 {
		return res, nil
	}
	adapters, err := harness.Lookup(s.Pack.Harnesses)
	if err != nil {
		return InstallPlan{}, err
	}
	p, err := buildPlan(ctx, d, filepath.Join(out, ScopeUser), home, adapters)
	if u, ok := errors.AsType[*plan.UnmappedError](err); ok {
		res.Diagnostics = unmappedDiagnostics(u)
		return res, nil
	}
	if err != nil {
		return InstallPlan{}, err
	}
	for _, a := range adapters {
		res.Harnesses = append(res.Harnesses, harnessPlan(res.Root, a))
	}
	for _, op := range p.Ops {
		res.Ops = append(res.Ops, InstallOp{
			Action: op.Action, Harness: op.Harness, Source: op.Source, Target: op.Target,
			Exec: op.Exec, Before: op.Before, After: op.After, BeforeExec: op.BeforeExec,
		})
	}
	return res, nil
}

// buildPlan plans the user-scope staging tree tree into home. A first
// plan from the names alone checks every staged path and maps every file
// before anything is read; the plan is then built again with the hash of
// each staged file and the state of each target.
func buildPlan(ctx context.Context, d installFS, tree, home string, adapters []harness.Adapter) (plan.Plan, error) {
	names, err := d.Files(tree)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return plan.Plan{}, err
	}
	in := plan.Input{Home: home, Adapters: adapters, IgnoreExec: !d.ExecBit()}
	for _, name := range names {
		in.Files = append(in.Files, plan.File{Path: name})
	}
	first, err := plan.Build(in)
	if err != nil {
		return plan.Plan{}, err
	}
	in.Files, in.Disk = nil, map[string]plan.Current{}
	dirs := map[string]error{} // the outcome of checkDir for each directory checked
	for _, op := range first.Ops {
		staged, err := d.Hash(ctx, filepath.Join(tree, filepath.FromSlash(op.Source)))
		if err != nil {
			return plan.Plan{}, err
		}
		in.Files = append(in.Files, plan.File{Path: op.Source, Hash: staged.Hash, Exec: staged.Exec})
		cur, ok, err := inspectTarget(ctx, d, home, op.Target, dirs)
		if err != nil {
			return plan.Plan{}, err
		}
		if ok {
			in.Disk[op.Target] = cur
		}
	}
	return plan.Build(in)
}

// inspectTarget gives the state of target, a path under home, and whether
// it exists. Each directory between home and target must be a real
// directory or missing; a missing one means target is missing too. dirs
// caches the outcome of each directory across targets.
func inspectTarget(ctx context.Context, d installFS, home, target string, dirs map[string]error) (plan.Current, bool, error) {
	rel, err := filepath.Rel(home, filepath.Dir(target))
	if err != nil || !filepath.IsLocal(rel) && rel != "." {
		return plan.Current{}, false, fmt.Errorf("target %s is not under the home directory %s", target, home)
	}
	dir := home
	if rel != "." {
		for seg := range strings.SplitSeq(rel, string(filepath.Separator)) {
			dir = filepath.Join(dir, seg)
			err, seen := dirs[dir]
			if !seen {
				err = checkDir(d, dir)
				dirs[dir] = err
			}
			if errors.Is(err, fs.ErrNotExist) {
				return plan.Current{}, false, nil
			}
			if err != nil {
				return plan.Current{}, false, err
			}
		}
	}
	cur, err := d.Hash(ctx, target)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return plan.Current{}, false, nil
	case err != nil:
		return plan.Current{}, false, err
	}
	return cur, true, nil
}

// checkDir reports a dir that is missing, with an fs.ErrNotExist error,
// or that is not a real directory, with a *NotRegularError.
func checkDir(d installFS, dir string) error {
	mode, err := d.Lstat(dir)
	if err != nil {
		return err
	}
	if mode&(fs.ModeSymlink|fs.ModeIrregular) != 0 || !mode.IsDir() {
		return &NotRegularError{Path: dir, Mode: mode, Dir: true}
	}
	return nil
}

// unmappedDiagnostics reports each staged file that nothing installs at
// the manifest, whose harnesses decide what is installed.
func unmappedDiagnostics(u *plan.UnmappedError) []Diagnostic {
	var diags []Diagnostic
	for _, p := range u.Paths {
		diags = append(diags, pack.NewDiagnostic("dotai.yaml", 0, 0, CodePathUnmapped, "staged file %s/%s is installed by no harness of the pack", ScopeUser, p).
			WithHint("add the harness that reads it to harnesses, or remove the source that renders it"))
	}
	return diags
}

// harnessPlan is the isolation of a, with its directory under root.
func harnessPlan(root string, a harness.Adapter) HarnessPlan {
	dir := filepath.Join(root, filepath.FromSlash(a.Dir))
	h := HarnessPlan{
		ID: a.ID, Level: string(a.Isolation.Level), Dir: dir, Pointers: []PointerPlan{},
		Relogin: a.Isolation.Relogin, Leaks: append([]string{}, a.Isolation.Leaks...),
	}
	for _, ptr := range a.Isolation.Pointers {
		h.Pointers = append(h.Pointers, PointerPlan{Mechanism: string(ptr.Mechanism), Name: ptr.Name, Dir: filepath.Join(dir, filepath.FromSlash(ptr.Path))})
	}
	return h
}

// osInstallFS is the installFS of the real file system.
type osInstallFS struct{ osStageFS }

func (osInstallFS) StagingDir() (string, error) { return os.MkdirTemp("", "dotai-install-*") }

func (osInstallFS) ExecBit() bool { return runtime.GOOS != "windows" }

func (osInstallFS) Lstat(name string) (fs.FileMode, error) {
	info, err := os.Lstat(name)
	if err != nil {
		return 0, err
	}
	return info.Mode(), nil
}

// Hash opens name only after Lstat found a regular file there, and checks
// that the file it opened is that one, so that a link put there in
// between is not read. A FIFO put there in between can still block the
// open; ctx stops the read, not the open.
func (osInstallFS) Hash(ctx context.Context, name string) (plan.Current, error) {
	info, err := os.Lstat(name)
	if err != nil {
		return plan.Current{}, err
	}
	if !info.Mode().IsRegular() {
		return plan.Current{}, &NotRegularError{Path: name, Mode: info.Mode()}
	}
	f, err := os.Open(name)
	if err != nil {
		return plan.Current{}, err
	}
	defer f.Close()
	opened, err := f.Stat()
	if err != nil {
		return plan.Current{}, err
	}
	if !os.SameFile(info, opened) {
		return plan.Current{}, fmt.Errorf("%s changed while it was read", name)
	}
	h := sha256.New()
	if _, err := io.Copy(h, ctxReader{ctx, f}); err != nil {
		return plan.Current{}, fmt.Errorf("%s: %w", name, err)
	}
	return plan.Current{Hash: hex.EncodeToString(h.Sum(nil)), Exec: info.Mode()&0o111 != 0}, nil
}

// ctxReader reads from r until ctx is done.
type ctxReader struct {
	ctx context.Context
	r   io.Reader
}

func (c ctxReader) Read(p []byte) (int, error) {
	if err := c.ctx.Err(); err != nil {
		return 0, err
	}
	return c.r.Read(p)
}
