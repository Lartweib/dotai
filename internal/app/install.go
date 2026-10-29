package app

import (
	"cmp"
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

	"context"

	"github.com/lartweib/dotai/internal/harness"
	"github.com/lartweib/dotai/internal/plan"
)

// ModeIsolated is the install mode of decision 0031, the only one so far.
const ModeIsolated = "isolated"

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

// NotRegularError is a target that exists and is not a regular file, such
// as a directory or a link. The plan neither follows nor replaces it.
type NotRegularError struct {
	Path string
	Mode fs.FileMode
}

func (e *NotRegularError) Error() string {
	kind := "not a regular file"
	switch {
	case e.Mode&fs.ModeSymlink != 0:
		kind = "a link"
	case e.Mode.IsDir():
		kind = "a directory"
	}
	return fmt.Sprintf("%s is %s; dotai installs only over regular files and does not follow links", e.Path, kind)
}

// installFS is the port PlanInstall reads the staged files and the disk
// through. It stages through the stageFS it embeds.
type installFS interface {
	stageFS
	StagingDir() (string, error)               // a new empty temporary directory
	Inspect(name string) (plan.Current, error) // name is absolute; an fs.ErrNotExist error when it is missing, a *NotRegularError when it is not a regular file
	ExecBit() bool                             // whether the file system keeps an exec bit
}

// PlanInstall plans the isolated install of the user scope of the pack in
// dir, with variant or the pack's default, into the absolute home
// directory home. It stages the pack as Stage does, into a temporary
// directory it removes, with ${DOTAI_TOOLS} at home's tools directory,
// then compares each staged file with its target. Apart from fetching rulesync, it writes nothing else.
// A pack with problems gives diagnostics. rulesync is fetched into
// cacheDir as FetchRulesync does; progress may be nil.
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
	in, err := inspectStaging(d, filepath.Join(out, ScopeUser), home, adapters)
	if err != nil {
		return InstallPlan{}, err
	}
	p, err := plan.Build(in)
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

// inspectStaging hashes the files of the user-scope staging tree tree and
// the targets they have under home, into the input of plan.Build.
func inspectStaging(d installFS, tree, home string, adapters []harness.Adapter) (plan.Input, error) {
	in := plan.Input{Home: home, Adapters: adapters, Disk: map[string]plan.Current{}, IgnoreExec: !d.ExecBit()}
	names, err := d.Files(tree)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return plan.Input{}, err
	}
	for _, name := range names {
		staged, err := d.Inspect(filepath.Join(tree, filepath.FromSlash(name)))
		if err != nil {
			return plan.Input{}, err
		}
		in.Files = append(in.Files, plan.File{Path: name, Hash: staged.Hash, Exec: staged.Exec})
		if strings.HasPrefix(name, ".rulesync/") {
			continue // never installed, so its target is never read
		}
		_, target, ok := plan.Locate(home, adapters, name)
		if !ok {
			continue // plan.Build reports it
		}
		cur, err := d.Inspect(target)
		switch {
		case errors.Is(err, fs.ErrNotExist):
		case err != nil:
			return plan.Input{}, err
		default:
			in.Disk[target] = cur
		}
	}
	return in, nil
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

// Inspect does not follow a link at name. The file it opens must be the
// one it checked, so that a link put there in between is not read either.
func (osInstallFS) Inspect(name string) (plan.Current, error) {
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
	if _, err := io.Copy(h, f); err != nil {
		return plan.Current{}, fmt.Errorf("%s: %w", name, err)
	}
	return plan.Current{Hash: hex.EncodeToString(h.Sum(nil)), Exec: info.Mode()&0o111 != 0}, nil
}
