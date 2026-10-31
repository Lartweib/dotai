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
	"slices"
	"strings"

	"github.com/lartweib/dotai/internal/harness"
	"github.com/lartweib/dotai/internal/pack"
	"github.com/lartweib/dotai/internal/plan"
	"github.com/lartweib/dotai/internal/state"
)

// ModeIsolated is the install mode of decision 0031, the only one so far.
const ModeIsolated = "isolated"

// The files of dotai under the home directory besides the isolated root
// and the tools directory (decision 0032): the install state, the backups
// and the lock an install holds.
const (
	dotaiDir   = ".dotai"
	stateFile  = "state.json"
	backupsDir = "backups"
	lockFile   = "lock"
)

// CodePathUnmapped is the diagnostic of a staged file that no harness of
// the pack and no tools rule installs.
const CodePathUnmapped = "path-unmapped"

// InstallPlan is the plan of an install of a pack's user scope: the
// isolation of each harness, which decision 0018 requires to show before
// installing, the shared skills a variant that shares them takes (decision
// 0036), and the file operations. Lists are never nil; they are empty when
// there are diagnostics.
type InstallPlan struct {
	Pack         *PackSummary      `json:"pack"`    // nil when the manifest cannot be read
	Variant      string            `json:"variant"` // the variant asked for, or the pack's default
	Mode         string            `json:"mode"`
	Root         string            `json:"root"`         // the directory that holds each harness's isolated directory
	SharedSkills *SharedSkillsPlan `json:"sharedSkills"` // nil when the variant does not share skills
	Harnesses    []HarnessPlan     `json:"harnesses"`
	Ops          []InstallOp       `json:"ops"`
	Diagnostics  []Diagnostic      `json:"diagnostics"`
}

// HarnessPlan is how one harness runs isolated on this OS: its directory,
// the variables or flags that point it there, whether the user must log in
// again (yes, no, or unknown where it was not verified) and what the
// user's own setup still provides.
type HarnessPlan struct {
	ID       string        `json:"id"`
	Level    string        `json:"level"`
	Dir      string        `json:"dir"`
	Pointers []PointerPlan `json:"pointers"`
	Relogin  string        `json:"relogin"`
	Leaks    []string      `json:"leaks"`
	// Tools are the tool commands its settings run (decision 0037), as
	// resolved for this OS.
	Tools []ToolCommand `json:"tools"`
}

// PointerPlan is an environment variable (env) or a flag (flag) set to an
// absolute directory.
type PointerPlan struct {
	Mechanism string `json:"mechanism"`
	Name      string `json:"name"`
	Dir       string `json:"dir"`
}

// InstallOp is one operation of the plan; see plan.Op. Harness is empty
// for a tool. Keys are the keys it writes into or deletes from a harness's
// settings file, whose keys dotai owns (decision 0035); never nil, and
// empty for any other file. Link and BeforeLink are the destinations of
// the link the target becomes and is (decision 0036), "" for a file.
type InstallOp struct {
	Action     plan.Action      `json:"action"`
	Harness    string           `json:"harness"`
	Source     string           `json:"source"`
	Target     string           `json:"target"`
	Exec       bool             `json:"exec"`
	Before     string           `json:"before"`
	After      string           `json:"after"`
	BeforeExec bool             `json:"beforeExec"`
	Keys       []plan.KeyChange `json:"keys"`
	Link       string           `json:"link"`
	BeforeLink string           `json:"beforeLink"`
}

// NotRegularError is a path on the way to a target that the install would
// have to follow or replace: a target that is not a regular file, or a
// directory above it that is a link, a junction or another reparse point,
// or not a directory at all. Dir is set for such a directory, and Link for
// a target where the install needs a link (decision 0036) that is not one.
type NotRegularError struct {
	Path string
	Mode fs.FileMode
	Dir  bool
	Link bool
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
	switch {
	case e.Dir:
		want = "a directory"
	case e.Link:
		want = "a link"
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
	// Read reads the regular file name, which is absolute, as Hash does,
	// and gives its content with its state.
	Read(ctx context.Context, name string) ([]byte, plan.Current, error)
	// Link reads the link name, which is absolute, without following it,
	// and gives its state, plan.LinkCurrent of its destination: a symbolic
	// link, or on Windows a junction. It gives an fs.ErrNotExist error when
	// name is missing and a *NotRegularError when it is not a link.
	Link(name string) (plan.Current, error)
	ExecBit() bool // whether the file system keeps an exec bit
	// Stat is the mode of what name is or, through links, points at; it
	// only reads the user's shared skills folder, never a target.
	Stat(name string) (fs.FileMode, error)
	// CanLink refuses a destination that LinkIn cannot make a link to on
	// this OS, such as a network path for a Windows junction.
	CanLink(dest string) error
}

// PlanInstall plans the isolated install of the user scope of the pack in
// dir, with variant or the pack's default, into the absolute home
// directory home. It stages the pack as Stage does, into a temporary
// directory it removes, with ${DOTAI_TOOLS} at home's tools directory,
// then compares each staged file with its target. A file the previous
// install owned, by its state in ~/.dotai/state.json, that the pack no
// longer stages is removed; an owned file the user edited fails the plan
// with a *plan.EditedError. A harness's settings file is planned key by
// key (decision 0035): the keys dotai owns there are checked, not the
// whole file, which the harness writes too. Apart from fetching rulesync,
// it writes
// nothing else. A pack with problems, including a staged file that
// nothing installs, gives diagnostics. rulesync is fetched into cacheDir
// as FetchRulesync does; progress may be nil.
func PlanInstall(ctx context.Context, cacheDir, dir, variant, home string, progress Progress) (InstallPlan, error) {
	return planInstall(ctx, osInstallFS{}, download, generate, cacheDir, dir, variant, home, progress)
}

func planInstall(ctx context.Context, d installFS, fetch fetchFunc, run runFunc, cacheDir, dir, variant, home string, progress Progress) (InstallPlan, error) {
	var res InstallPlan
	err := withPlan(ctx, d, fetch, run, cacheDir, dir, variant, home, progress, func(p planned) error {
		res = p.plan
		return nil
	})
	if err != nil {
		return InstallPlan{}, err
	}
	return res, nil
}

// planned is a plan with what carrying it out needs: the plan's own
// operations, the user staging tree that holds the staged files, the
// state of the previous install (nil before the first one), the pack's
// harnesses, whether the variant shares skills and whether the exec bit is
// left out. Only plan is set when the plan has diagnostics.
type planned struct {
	plan       InstallPlan
	shared     bool
	ops        []plan.Op
	tree       string
	prev       *state.State
	harnesses  []string
	owned      map[string]plan.Current // the targets the previous install owns, as they are
	keys       map[string][]plan.Key   // the keys it owns in each settings file of owned
	ignoreExec bool
}

// withPlan plans the install as PlanInstall describes and calls then with
// the plan while the staging still exists; the staging is removed after
// then returns. A staging that cannot be removed is a *stagingError, on
// its own when nothing else failed.
func withPlan(ctx context.Context, d installFS, fetch fetchFunc, run runFunc, cacheDir, dir, variant, home string, progress Progress, then func(planned) error) (err error) {
	if !filepath.IsAbs(home) {
		return fmt.Errorf("home directory %q is not absolute", home)
	}
	home = filepath.Clean(home)
	tmp, err := d.StagingDir()
	if err != nil {
		return err
	}
	defer func() {
		if rmErr := d.RemoveAll(tmp); rmErr != nil {
			if err == nil {
				err = &stagingError{dir: tmp, err: rmErr}
			} else {
				err = errors.Join(err, &stagingError{dir: tmp, err: rmErr})
			}
		}
	}()
	out := filepath.Join(tmp, "staging")
	tools := ToolsIn(home, "")
	tools.Project, tools.ProjectErr = "", errors.New("an install covers the user scope only")
	s, err := stage(ctx, d, fetch, run, cacheDir, dir, out, Selection{Variant: variant}, tools, false, progress)
	if err != nil {
		return err
	}
	res := InstallPlan{
		Pack: s.Pack, Variant: variant, Mode: ModeIsolated, Root: filepath.Join(home, filepath.FromSlash(harness.IsolatedDir)),
		Harnesses: []HarnessPlan{}, Ops: []InstallOp{}, Diagnostics: s.Diagnostics,
	}
	if s.Pack != nil {
		res.Variant = cmp.Or(variant, s.Pack.DefaultVariant)
	}
	if len(s.Diagnostics) > 0 {
		return then(planned{plan: res})
	}
	adapters, err := harness.Lookup(s.Pack.Harnesses)
	if err != nil {
		return err
	}
	shared := slices.Contains(s.Pack.SharedSkills, res.Variant)
	tree := filepath.Join(out, ScopeUser)
	p, prev, sp, err := buildPlan(ctx, d, tree, home, adapters, shared)
	if u, ok := errors.AsType[*plan.UnmappedError](err); ok {
		res.Diagnostics = unmappedDiagnostics(u)
		return then(planned{plan: res})
	}
	if err != nil {
		return err
	}
	res.SharedSkills = sp
	for _, a := range adapters {
		h := harnessPlan(res.Root, a, runtime.GOOS, shared)
		for _, c := range s.ToolCommands {
			if c.Scope == ScopeUser && c.Harness == a.ID {
				h.Tools = append(h.Tools, c)
			}
		}
		res.Harnesses = append(res.Harnesses, h)
	}
	for _, op := range p.Ops {
		res.Ops = append(res.Ops, InstallOp{
			Action: op.Action, Harness: op.Harness, Source: op.Source, Target: op.Target,
			Exec: op.Exec, Before: op.Before, After: op.After, BeforeExec: op.BeforeExec, Keys: append([]plan.KeyChange{}, op.Changes...),
			Link: op.Link, BeforeLink: op.BeforeLink,
		})
	}
	return then(planned{plan: res, shared: shared, ops: p.Ops, tree: tree, prev: prev, harnesses: s.Pack.Harnesses, owned: p.Owned, keys: p.Keys, ignoreExec: !d.ExecBit()})
}

// buildPlan plans the user-scope staging tree tree into home. A first
// plan from the names alone checks every staged path and maps every file
// before anything is read; the plan is then built again with the hash of
// each staged file, the state of each target and of each file the previous
// install owned, and that install's ownership record. A harness's settings
// file, staged or as its target holds it, is read whole, since it is
// planned key by key. With shared set, the variant shares the user's
// skills, and each shared skill is planned as a link, or copied into tree
// and planned as its files (decision 0036), unless the pack stages a skill
// of its name. It also returns the previous
// install's state, nil when there is none, and the shared skills, nil
// unless shared is set.
func buildPlan(ctx context.Context, d installFS, tree, home string, adapters []harness.Adapter, shared bool) (plan.Plan, *state.State, *SharedSkillsPlan, error) {
	names, err := d.Files(tree)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return plan.Plan{}, nil, nil, err
	}
	in := plan.Input{Home: home, Adapters: adapters, IgnoreExec: !d.ExecBit(), FoldCase: foldCase}
	for _, name := range names {
		in.Files = append(in.Files, plan.File{Path: name})
	}
	first, err := plan.Build(in)
	if err != nil {
		return plan.Plan{}, nil, nil, err
	}
	var sp *SharedSkillsPlan
	if shared {
		var files []plan.File
		if files, sp, err = sharedSkills(ctx, d, tree, home, adapters, first.Ops); err != nil {
			return plan.Plan{}, nil, nil, err
		}
		in.Files = append(in.Files, files...)
		if first, err = plan.Build(in); err != nil {
			return plan.Plan{}, nil, nil, err
		}
	}
	prev, err := readState(d, home)
	if err != nil {
		return plan.Plan{}, nil, nil, err
	}
	in.Files, in.Disk, in.Content = nil, map[string]plan.Current{}, map[string][]byte{}
	var targets []string
	links, staged := map[string]bool{}, map[string]bool{} // the targets that are links, owned or staged, and staged
	for _, op := range first.Ops {
		if op.Link != "" {
			in.Files = append(in.Files, plan.File{Path: op.Source, Link: op.Link})
			targets, links[op.Target], staged[op.Target] = append(targets, op.Target), true, true
			continue
		}
		name := filepath.Join(tree, filepath.FromSlash(op.Source))
		var data []byte
		var staged plan.Current
		if _, ok := plan.SettingsOf(home, op.Target); ok {
			data, staged, err = d.Read(ctx, name)
		} else {
			staged, err = d.Hash(ctx, name)
		}
		if err != nil {
			return plan.Plan{}, nil, nil, err
		}
		in.Files = append(in.Files, plan.File{Path: op.Source, Hash: staged.Hash, Exec: staged.Exec, Data: data})
		targets = append(targets, op.Target)
	}
	dirs := map[string]error{}    // the outcome of checkDir for each directory checked
	realDirs := map[string]bool{} // the staged links whose target is a real directory
	gone := map[string]bool{}     // the owned links a killed install removed
	if prev != nil {
		in.Owned = prev.Owned()
		for _, f := range prev.Files {
			if !slices.Contains(targets, f.Target) {
				targets = append(targets, f.Target)
			}
		}
		for target := range ownedLinks(*prev) {
			links[target] = true
			if staged[target] {
				continue
			}
			// The plan removes it, so a file the pack now stages below it,
			// such as a skill of the same name, is missing while the link
			// is there: it is created once the link is gone, which comes
			// first. A link that is not what dotai made is an edit, which
			// stops the plan anyway.
			_, _, exists, err := inspectTarget(ctx, d, home, target, dirs, inspectLink)
			switch {
			case err == nil && exists:
				dirs[target] = fs.ErrNotExist
			case realDirAt(target, err) && pendingRemove(prev, target):
				// An install killed after it removed the link wrote below
				// it: the link is gone, and the directory holds the files
				// the plan reads as they are.
				gone[target] = true
			}
		}
	}
	for _, target := range targets {
		if gone[target] {
			continue
		}
		_, settings := plan.SettingsOf(home, target)
		how := inspectHash
		switch {
		case links[target]:
			how = inspectLink
		case settings:
			how = inspectRead
		}
		cur, data, ok, err := inspectTarget(ctx, d, home, target, dirs, how)
		if movedAway(home, target, err) {
			continue
		}
		if staged[target] && realDirAt(target, err) {
			realDirs[target] = true // taken as missing, if the plan empties it
			continue
		}
		if err != nil {
			return plan.Plan{}, nil, nil, err
		}
		if ok {
			in.Disk[target] = cur
		}
		if ok && settings {
			in.Content[target] = data
		}
	}
	p, err := plan.Build(in)
	if err != nil {
		return plan.Plan{}, nil, nil, err
	}
	if err := checkLinkDirs(d, p.Ops, realDirs); err != nil {
		return plan.Plan{}, nil, nil, err
	}
	if err := checkLinkable(d, p.Ops); err != nil {
		return plan.Plan{}, nil, nil, err
	}
	p.Ops = permute(p.Ops, orderLinks(p.Ops))
	return p, prev, sp, nil
}

// readState reads the state of the previous install in home, nil when
// there is none. Neither the dotai directory nor the state file is
// followed when it is a link.
func readState(d installFS, home string) (*state.State, error) {
	dir := filepath.Join(home, dotaiDir)
	if err := checkDir(d, dir); errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	} else if err != nil {
		return nil, err
	}
	name := filepath.Join(dir, stateFile)
	mode, err := d.Lstat(name)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return nil, nil
	case err != nil:
		return nil, err
	case !mode.IsRegular():
		return nil, &NotRegularError{Path: name, Mode: mode}
	}
	data, err := d.ReadFile(dir, stateFile)
	if err != nil {
		return nil, err
	}
	s, err := state.Decode(data)
	if err == nil {
		err = s.CheckLinks(home, foldCase)
	}
	if err != nil {
		return nil, fmt.Errorf("%s: %w", name, err)
	}
	return &s, nil
}

// ownedLinks is the targets s records as links, in its files and in its
// journal.
func ownedLinks(s state.State) map[string]bool {
	out := map[string]bool{}
	for _, f := range s.Files {
		if f.Kind == state.KindLink {
			out[f.Target] = true
		}
	}
	if s.Pending != nil {
		for _, op := range s.Pending.Ops {
			if op.Before != nil && op.Before.Link != "" || op.After != nil && op.After.Link != "" {
				out[op.Target] = true
			}
		}
	}
	return out
}

// inspection is how inspectTarget reads a target.
type inspection int

const (
	inspectHash inspection = iota // the hash of a regular file
	inspectRead                   // the content of a regular file too
	inspectLink                   // the destination of a link
)

// inspectTarget gives the state of target, a path under home, with its
// content for inspectRead, and whether it exists. Each directory between
// home and target must be a real directory or missing; a missing one means
// target is missing too. dirs caches the outcome of each directory across
// targets.
func inspectTarget(ctx context.Context, d installFS, home, target string, dirs map[string]error, how inspection) (plan.Current, []byte, bool, error) {
	rel, err := filepath.Rel(home, filepath.Dir(target))
	if err != nil || !filepath.IsLocal(rel) && rel != "." {
		return plan.Current{}, nil, false, fmt.Errorf("target %s is not under the home directory %s", target, home)
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
				return plan.Current{}, nil, false, nil
			}
			if err != nil {
				return plan.Current{}, nil, false, err
			}
		}
	}
	var data []byte
	var cur plan.Current
	switch how {
	case inspectRead:
		data, cur, err = d.Read(ctx, target)
	case inspectLink:
		cur, err = d.Link(target)
	default:
		cur, err = d.Hash(ctx, target)
	}
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return plan.Current{}, nil, false, nil
	case err != nil:
		return plan.Current{}, nil, false, err
	}
	return cur, data, true, nil
}

// movedAway reports an error of inspectTarget for a target on the old
// side of an adapter's move (plan.MovedFrom) that is not a real path, such
// as a link the harness left there when it moved the directory: such a
// target is gone, and nothing is read or removed through the link.
func movedAway(home, target string, err error) bool {
	_, ok := errors.AsType[*NotRegularError](err)
	return ok && plan.MovedFrom(home, target)
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

// harnessPlan is the isolation of a on goos, with its directory under
// root, for an install whose variant shares the user's skills or not
// (decision 0036).
func harnessPlan(root string, a harness.Adapter, goos string, shared bool) HarnessPlan {
	dir := filepath.Join(root, filepath.FromSlash(a.Dir))
	h := HarnessPlan{
		ID: a.ID, Level: string(a.Isolation.Level), Dir: dir, Pointers: []PointerPlan{},
		Relogin: string(a.Isolation.ReloginOn(goos)), Leaks: a.Isolation.LeaksOn(goos, shared), Tools: []ToolCommand{},
	}
	for _, ptr := range a.Isolation.PointersFor(shared) {
		h.Pointers = append(h.Pointers, PointerPlan{Mechanism: string(ptr.Mechanism), Name: ptr.Name, Dir: filepath.Join(dir, filepath.FromSlash(ptr.Path))})
	}
	return h
}

// osInstallFS is the installFS of the real file system.
type osInstallFS struct{ osStageFS }

func (osInstallFS) StagingDir() (string, error) { return os.MkdirTemp("", "dotai-install-*") }

func (osInstallFS) ExecBit() bool { return runtime.GOOS != "windows" }

func (osInstallFS) CanLink(dest string) error { return canLink(dest) }

func (osInstallFS) Stat(name string) (fs.FileMode, error) {
	info, err := os.Stat(name)
	if err != nil {
		return 0, err
	}
	return info.Mode(), nil
}

func (osInstallFS) Lstat(name string) (fs.FileMode, error) {
	info, err := os.Lstat(name)
	if err != nil {
		return 0, err
	}
	return info.Mode(), nil
}

// Hash reads name as openRegular opens it.
func (osInstallFS) Hash(ctx context.Context, name string) (plan.Current, error) {
	f, info, err := openRegular(name)
	if err != nil {
		return plan.Current{}, err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, ctxReader{ctx, f}); err != nil {
		return plan.Current{}, fmt.Errorf("%s: %w", name, err)
	}
	return plan.Current{Hash: hex.EncodeToString(h.Sum(nil)), Exec: info.Mode()&0o111 != 0}, nil
}

// openRegular opens name only after Lstat found a regular file there, and
// checks that the file it opened is that one, so that a link put there in
// between is not read. A FIFO put there in between can still block the
// open; the caller's context stops the read, not the open.
func openRegular(name string) (*os.File, fs.FileInfo, error) {
	info, err := os.Lstat(name)
	if err != nil {
		return nil, nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, nil, &NotRegularError{Path: name, Mode: info.Mode()}
	}
	f, err := os.Open(name)
	if err != nil {
		return nil, nil, err
	}
	opened, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, nil, err
	}
	if !os.SameFile(info, opened) {
		f.Close()
		return nil, nil, fmt.Errorf("%s changed while it was read", name)
	}
	return f, info, nil
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
