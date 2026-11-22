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

// The install modes: isolated (decision 0031), and replace, which resets
// each selected harness that has no replace install yet to a fresh install
// (decision 0038), and only updates the pack for one that has (decision
// 0051).
const (
	ModeIsolated = string(harness.Isolated)
	ModeReplace  = string(harness.Replace)
)

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
	Root         string            `json:"root"`         // the directory that holds each harness's directory: the isolated root, or the home directory in replace mode
	SharedSkills *SharedSkillsPlan `json:"sharedSkills"` // nil when the variant does not share skills
	// Harnesses are every harness dotai knows, the ones the pack is
	// compatible with first, managed by the install or not, each with its
	// state (decision 0047).
	Harnesses []HarnessPlan `json:"harnesses"`
	Ops       []InstallOp   `json:"ops"`
	// Wipes, Keeps and WipeKeys are what a replace install does to each
	// harness's own configuration before it installs the pack, and
	// Refusals why it would refuse (decision 0038); empty in the isolated
	// mode.
	Wipes    []WipePlan `json:"wipes"`
	Keeps    []KeepPlan `json:"keeps"`
	WipeKeys []WipeKey  `json:"wipeKeys"`
	Refusals []Refusal  `json:"refusals"`
	// Notices say why a compatible harness is not managed, or why a preview
	// one is, text for people.
	Notices []string `json:"notices"`
	// Warnings are what the install carries out but the user should read
	// before confirming it, by code, such as a pack that replaces dotai's
	// onboarding or a harness that does not get its session-start hook
	// (decision 0045). A warning neither blocks the plan nor needs
	// consent; a code that does says so where it is defined.
	Warnings    []plan.Warning `json:"warnings"`
	Diagnostics []Diagnostic   `json:"diagnostics"`
	// Reinstall are the managed harnesses that the install state records
	// as installed in the replace mode: for them this install is a later
	// one (decision 0051). Reset is whether it resets them again, as a
	// first replace install does; false, it only updates the pack there,
	// wiping nothing, and WouldReset is what a reset would do instead. A
	// harness that is not in Reinstall is reset either way.
	Reinstall  []string      `json:"reinstall"`
	Reset      bool          `json:"reset"`
	WouldReset *ResetPreview `json:"wouldReset"` // nil unless Reinstall is not empty and Reset is false
	// Blocked says that the install refuses the plan whatever the consent,
	// for its diagnostics or refusals; Consent that it needs consent
	// (--yes) for what it asks to confirm: what a replace install wipes or
	// overwrites that dotai does not own (ConsentForeign), and the files
	// of the pack's template that it installs unchanged (TemplateFiles,
	// its template-unchanged warnings).
	Blocked        bool `json:"blocked"`
	Consent        bool `json:"consent"`
	ConsentForeign bool `json:"consentForeign"`
}

// ResetPreview is what a replace install would do to the harnesses it
// updates only, if it reset them again (decision 0051): what it would
// wipe, and why it would refuse. Lists are never nil.
type ResetPreview struct {
	Wipes    []WipePlan `json:"wipes"`
	WipeKeys []WipeKey  `json:"wipeKeys"`
	Refusals []Refusal  `json:"refusals"`
}

// empty reports whether a reset would wipe nothing.
func (r ResetPreview) empty() bool { return len(r.Wipes) == 0 && len(r.WipeKeys) == 0 }

// ResetChoice is what InstallOptions.ChooseReset is asked: the harnesses
// that a later replace install updates, and what resetting them would do.
type ResetChoice struct {
	Harnesses []string
	Preview   ResetPreview
}

// InstallOptions are the choices of an install besides the pack: the
// variant, the pack's default when empty, and the harnesses to manage,
// --harness, nil to reuse the saved selection or take the pack's
// harnesses that are detected (decision 0038).
//
// Mode is the install mode, isolated or replace; empty, the mode the
// install state records, or isolated when there is none. IgnoreRunning
// skips the check that refuses a replace install while a harness it
// resets is running (decision 0038).
//
// A replace install of a harness that the install state records in the
// replace mode only updates the pack there (decision 0051), unless Reset is
// set, which resets it again as a first replace install does, or
// ChooseReset, when it is not nil and a reset would wipe something, answers
// true. It is asked while planning, before anything is written, and an
// error from it stops the install. Neither matters for a harness that is
// not recorded in the replace mode, nor outside the replace mode.
//
// Confirm, when it is not nil, is asked with the plan, so that a caller
// carries out only the plan its user saw. The install first completes the
// recovery of a move across volumes that an earlier install or restore
// cut short, as the command line does, so that the plan reflects the disk
// as it is then; Confirm is asked after that and before any refusal,
// consent, lock or write of the plan: an error stops the install, which
// writes nothing of the plan.
type InstallOptions struct {
	Variant       string
	Harnesses     []string
	Mode          string
	IgnoreRunning bool
	Reset         bool
	ChooseReset   func(ResetChoice) (bool, error)
	Confirm       func(InstallPlan) error
}

// HarnessPlan is one harness of the pack: whether the pack requires it,
// whether it is detected and the install manages it (decision 0038), and,
// when it does, how it runs isolated on this OS: its directory, the
// variables or flags that point it there, whether the user must log in
// again (yes, no, or unknown where it was not verified) and what the
// user's own setup still provides. A harness the install does not manage
// has only its detection; its other lists are empty.
type HarnessPlan struct {
	ID string `json:"id"`
	// Compatible is whether the pack lists the harness in its harnesses,
	// Required in its requires; Status and StatusReason are the harness's
	// status, supported or preview, and why it is a preview (decision
	// 0047).
	Compatible   bool   `json:"compatible"`
	Required     bool   `json:"required"`
	Status       string `json:"status"`
	StatusReason string `json:"statusReason"`
	Managed      bool   `json:"managed"`
	Detection
	Level    string        `json:"level"`
	Dir      string        `json:"dir"`
	Pointers []PointerPlan `json:"pointers"`
	Relogin  string        `json:"relogin"`
	Leaks    []string      `json:"leaks"`
	// Tools are the tool commands its settings run (decision 0037), as
	// resolved for this OS.
	Tools []ToolCommand `json:"tools"`
}

// IsPreview reports whether h is a preview harness: any status but
// supported counts as one, as the selection counts it (decision 0047).
func (h HarnessPlan) IsPreview() bool { return h.State().IsPreview() }

// State is the state of h as a selection sees it, without its isolation.
func (h HarnessPlan) State() HarnessState {
	return HarnessState{ID: h.ID, Compatible: h.Compatible, Required: h.Required, Status: h.Status, StatusReason: h.StatusReason, Managed: h.Managed, Detection: h.Detection}
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
	// Kind is the kind of item the target holds, as the harness lays out
	// its user scope: rules, skills, agents, commands or settings, tools
	// for a file of the tools directory, and "" for another file.
	Kind string `json:"kind"`
}

// KindTools is the kind of a file of the tools directory.
const KindTools = "tools"

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

// PlanInstall plans the install of the user scope of the pack in dir into
// the absolute home directory home, with the options opts: the variant of
// opts or the pack's default; the mode of opts, the one the install state
// records, or isolated (resolveMode); and the harnesses selectHarnesses
// chooses from opts, the saved --harness choice and what is detected. It
// stages the pack as Stage does, into a temporary directory it removes,
// with ${DOTAI_TOOLS} at home's tools directory, then compares each staged
// file with its target in the layout of the mode. In the replace mode it
// also plans what is wiped and kept of each harness's own configuration,
// and why the install would refuse (planWipes). A file the previous
// install owned, by its state in ~/.dotai/state.json, that the pack no
// longer stages is removed; an owned file the user edited fails the plan
// with a *plan.EditedError. A harness's settings file is planned key by
// key (decision 0035): the keys dotai owns there are checked, not the
// whole file, which the harness writes too. Apart from fetching rulesync,
// it writes nothing. A pack with problems, including a staged file that
// nothing installs, gives diagnostics, with the mode and its root.
// rulesync is fetched into cacheDir as FetchRulesync does; progress may be
// nil.
func PlanInstall(ctx context.Context, cacheDir, dir, home string, opts InstallOptions, progress Progress) (InstallPlan, error) {
	return planInstall(ctx, osInstallFS{}, osHost(), download, generate, cacheDir, dir, home, opts, progress)
}

func planInstall(ctx context.Context, d installFS, h host, fetch fetchFunc, run runFunc, cacheDir, dir, home string, opts InstallOptions, progress Progress) (InstallPlan, error) {
	var res InstallPlan
	err := withPlan(ctx, d, h, fetch, run, cacheDir, dir, home, opts, progress, func(p planned) error {
		res = p.plan
		return nil
	})
	if err != nil {
		return InstallPlan{}, err
	}
	return res, nil
}

// settle sets what the plan of p says of carrying it out: whether it is
// blocked, by diagnostics or refusals, which no consent overrides, and
// whether it needs consent, for what it wipes or overwrites that dotai does
// not own (foreign) or for the template files it installs unchanged.
func (p *planned) settle(d installFS) error {
	p.plan.Blocked = len(p.plan.Diagnostics) > 0 || len(p.plan.Refusals) > 0
	if len(p.plan.Diagnostics) > 0 {
		return nil
	}
	entries, keys, files, err := foreign(d, *p)
	p.plan.ConsentForeign = entries+keys+files > 0
	p.plan.Consent = p.plan.ConsentForeign || len(p.plan.TemplateFiles()) > 0
	return err
}

// planned is a plan with what carrying it out needs: the plan's own
// operations, the user staging tree that holds the staged files, the
// state of the previous install (nil before the first one), the harnesses
// it manages, whether the variant shares skills and whether the exec bit
// is left out. Only plan is set when the plan has diagnostics.
type planned struct {
	plan       InstallPlan
	shared     bool
	ops        []plan.Op
	tree       string
	prev       *state.State
	harnesses  []string
	choice     []string                // the --harness choice to save, nil for none
	owned      map[string]plan.Current // the targets the previous install owns, as they are
	keys       map[string][]plan.Key   // the keys it owns in each settings file of owned
	ignoreExec bool
	running    runCheck // what the processes show of the harnesses, when it is a replace install that checks (decision 0053)
}

// withPlan plans the install as PlanInstall describes and calls then with
// the plan while the staging still exists; the staging is removed after
// then returns. A staging that cannot be removed is a *stagingError, on
// its own when nothing else failed.
func withPlan(ctx context.Context, d installFS, h host, fetch fetchFunc, run runFunc, cacheDir, dir, home string, opts InstallOptions, progress Progress, then func(planned) error) (err error) {
	next := then
	then = func(p planned) error {
		if err := p.settle(d); err != nil {
			return err
		}
		return next(p)
	}
	home, err = requireAbs(home)
	if err != nil {
		return err
	}
	// The mode comes first, so that the plan of a pack with problems has
	// it too, and a mode the install cannot use stops it before anything
	// is staged.
	prev, err := readState(d, home)
	if err != nil {
		return err
	}
	mode, err := resolveMode(opts.Mode, prev)
	if err != nil {
		return err
	}
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
	if h.exe != nil {
		tools.Onboarding = &OnboardingBinary{}
		tools.Onboarding.Path, tools.Onboarding.Err = h.exe()
	}
	// The harnesses are detected and selected only for a pack that is
	// valid, before rulesync is fetched.
	var sel harnessSelection
	pick := func(m pack.Manifest) ([]string, error) {
		detected, err := detectAll(d, h, home, m)
		if err != nil {
			return nil, err
		}
		sel, err = selectHarnesses(m, opts.Harnesses, prev, detected, byInstall)
		return sel.Selected, err
	}
	s, err := stage(ctx, d, fetch, run, cacheDir, dir, out, Selection{Variant: opts.Variant}, pick, tools, false, progress)
	if err != nil {
		return err
	}
	root := filepath.Join(home, filepath.FromSlash(harness.IsolatedDir))
	if mode == ModeReplace {
		root = home
	}
	res := InstallPlan{
		Pack: s.Pack, Variant: opts.Variant, Mode: mode, Root: root,
		Harnesses: []HarnessPlan{}, Ops: []InstallOp{}, Notices: []string{}, Warnings: append([]plan.Warning{}, s.Warnings...), Diagnostics: s.Diagnostics,
		Wipes: []WipePlan{}, Keeps: []KeepPlan{}, WipeKeys: []WipeKey{}, Refusals: []Refusal{}, Reinstall: []string{},
	}
	if s.Pack != nil {
		res.Variant = cmp.Or(opts.Variant, s.Pack.DefaultVariant)
	}
	if len(s.Diagnostics) > 0 {
		return then(planned{plan: res})
	}
	adapters, err := harness.Lookup(s.Harnesses)
	if err != nil {
		return err
	}
	res.Notices = append(res.Notices, sel.Notices...)
	res.Warnings = append(res.Warnings, sel.Warnings...)
	res.Warnings = append(res.Warnings, templatePlanWarnings(s.TemplateFiles, userLayers(s.Pack, res.Variant), "install")...)
	shared := slices.Contains(s.Pack.SharedSkills, res.Variant)
	tree := filepath.Join(out, ScopeUser)
	// Only the isolated mode: in the replace mode the real home directory
	// is the harness's own (decision 0038).
	if mode == ModeIsolated {
		for _, a := range adapters {
			notice, err := excludeHomeInstructions(d, tree, home, a)
			if err != nil {
				return err
			}
			if notice != "" {
				res.Notices = append(res.Notices, notice)
			}
		}
	}
	var rp replacePlan
	if mode == ModeReplace {
		res.Reinstall = reinstalled(home, prev, s.Harnesses)
		projects, err := readProjects(d, home)
		if err != nil {
			return err
		}
		var gone []string
		projects, gone = livingProjects(d, home, projects)
		for _, p := range gone {
			res.Notices = append(res.Notices, fmt.Sprintf("the entry of %s in %s is ignored: the project or its record is gone, so the replace install wipes its MCP servers like any other",
				p, filepath.Join(home, dotaiDir, projectsFile)))
		}
		// A harness the previous install recorded in the replace mode is
		// updated only, unless it is reset again (decision 0051); the
		// others are reset, as a first replace install does.
		var first, later []harness.Adapter
		for _, a := range adapters {
			if slices.Contains(res.Reinstall, a.ID) {
				later = append(later, a)
			} else {
				first = append(first, a)
			}
		}
		wipesOf := func(as []harness.Adapter) (replacePlan, error) {
			return planWipes(ctx, d, h, home, as, shared, ownedTargets(prev), projects, ownedKeys(prev))
		}
		if rp, err = wipesOf(first); err != nil {
			return err
		}
		if len(later) > 0 {
			lp, err := wipesOf(later)
			if err != nil {
				return err
			}
			reset := opts.Reset
			preview := ResetPreview{Wipes: append([]WipePlan{}, lp.wipes...), WipeKeys: append([]WipeKey{}, lp.keys...), Refusals: append([]Refusal{}, lp.refusals...)}
			if !reset && opts.ChooseReset != nil && !preview.empty() {
				if reset, err = opts.ChooseReset(ResetChoice{Harnesses: res.Reinstall, Preview: preview}); err != nil {
					return err
				}
			}
			if reset {
				res.Reset = true
				rp = rp.merge(lp)
			} else {
				// A harness that is only updated is still refused when a
				// variable relocates its configuration: the pack would be
				// written where the harness does not read it.
				rp.refusals = append(rp.refusals, lp.relocations()...)
				res.WouldReset = &preview
			}
		}
		res.Wipes, res.Keeps = append(res.Wipes, rp.wipes...), append(res.Keeps, rp.keeps...)
		res.WipeKeys, res.Refusals = append(res.WipeKeys, rp.keys...), append(res.Refusals, rp.refusals...)
	}
	p, sp, err := buildPlan(ctx, d, tree, home, harness.Mode(mode), adapters, prev, shared, rp.unread, rp.wipedKeys)
	if u, ok := errors.AsType[*plan.UnmappedError](err); ok {
		res.Diagnostics = unmappedDiagnostics(u)
		return then(planned{plan: res})
	}
	if err != nil {
		return err
	}
	res.SharedSkills = sp
	for _, st := range sel.States {
		hp := HarnessPlan{ID: st.ID, Pointers: []PointerPlan{}, Leaks: []string{}, Tools: []ToolCommand{}}
		if found, err := harness.Lookup([]string{st.ID}); err == nil && st.Managed {
			hp = harnessPlan(harness.Mode(mode), home, found[0], h.goos, shared)
		}
		hp.Compatible, hp.Required, hp.Status, hp.StatusReason, hp.Managed, hp.Detection = st.Compatible, st.Required, st.Status, st.StatusReason, st.Managed, st.Detection
		for _, c := range s.ToolCommands {
			if hp.Managed && c.Scope == ScopeUser && c.Harness == st.ID {
				hp.Tools = append(hp.Tools, c)
			}
		}
		res.Harnesses = append(res.Harnesses, hp)
	}
	byID := map[string]harness.Adapter{}
	for _, a := range adapters {
		byID[a.ID] = a
	}
	for _, op := range p.Ops {
		res.Ops = append(res.Ops, InstallOp{
			Action: op.Action, Harness: op.Harness, Source: op.Source, Target: op.Target,
			Exec: op.Exec, Before: op.Before, After: op.After, BeforeExec: op.BeforeExec, Keys: append([]plan.KeyChange{}, op.Changes...),
			Link: op.Link, BeforeLink: op.BeforeLink, Kind: opKind(byID, harness.Mode(mode), home, op),
		})
	}
	res.Warnings = append(res.Warnings, p.Warnings...)
	pl := planned{plan: res, shared: shared, ops: p.Ops, tree: tree, prev: prev, harnesses: s.Harnesses, choice: sel.Choice, owned: p.Owned, keys: p.Keys, ignoreExec: !d.ExecBit()}
	if mode == ModeReplace && !opts.IgnoreRunning {
		// A harness that runs while the install only updates files it does
		// not rewrite is warned about in the plan (decision 0053); the
		// install refuses for the rest.
		pl.running = checkRunning(h, s.Harnesses)
		if _, allowed, _ := pl.guardInstall(h, home, pl.running); len(allowed) > 0 {
			pl.plan.Warnings = append(pl.plan.Warnings, runningWarnings(allowed)...)
		}
	}
	return then(pl)
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
// of its name. prev is the previous install's state, nil when there is
// none. The staged files install in the layout of mode. A target for
// which wiped is true is taken as missing without reading it: a replace
// install wipes it first. A settings target of which keys gives keys to
// wipe, a split file that is also a harness's HomeConfig, such as
// ~/.claude.json (decision 0049), is planned from what it holds once they
// are wiped, and its op, which the install carries out with that wipe as
// one write, starts from what it holds now. It also returns the shared
// skills, nil unless shared is set.
func buildPlan(ctx context.Context, d installFS, tree, home string, mode harness.Mode, adapters []harness.Adapter, prev *state.State, shared bool, wiped func(string) bool, keys func(string) [][]string) (plan.Plan, *SharedSkillsPlan, error) {
	names, err := d.Files(tree)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return plan.Plan{}, nil, err
	}
	in := plan.Input{Home: home, Mode: mode, Adapters: adapters, IgnoreExec: !d.ExecBit(), FoldCase: foldCase}
	if prev != nil {
		in.PrevMode = harness.Mode(prev.Mode)
	}
	for _, name := range names {
		in.Files = append(in.Files, plan.File{Path: name})
	}
	first, err := plan.Build(in)
	if err != nil {
		return plan.Plan{}, nil, err
	}
	var sp *SharedSkillsPlan
	if shared {
		var files []plan.File
		if files, sp, err = sharedSkills(ctx, d, tree, home, mode, adapters, first.Ops); err != nil {
			return plan.Plan{}, nil, err
		}
		in.Files = append(in.Files, files...)
		if first, err = plan.Build(in); err != nil {
			return plan.Plan{}, nil, err
		}
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
			return plan.Plan{}, nil, err
		}
		in.Files = append(in.Files, plan.File{Path: op.Source, Hash: staged.Hash, Exec: staged.Exec, Data: data})
		targets = append(targets, op.Target)
	}
	dirs := map[string]error{}          // the outcome of checkDir for each directory checked
	realDirs := map[string]bool{}       // the staged links whose target is a real directory
	folded := map[string]plan.Current{} // the settings targets planned after their wipe, as they are now
	gone := map[string]bool{}           // the owned links a killed install removed
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
		if gone[target] || wiped != nil && wiped(target) {
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
			return plan.Plan{}, nil, err
		}
		if wipe := keysOf(keys, target); ok && settings && len(wipe) > 0 {
			if data, err = deleteKeys(data, wipe); err != nil {
				return plan.Plan{}, nil, fmt.Errorf("%s: %w", target, err)
			}
			folded[target] = cur
			cur.Hash = contentHash(data)
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
		return plan.Plan{}, nil, err
	}
	for target, cur := range folded {
		if _, owned := p.Owned[target]; owned {
			p.Owned[target] = cur
		}
		for i, op := range p.Ops {
			if op.Target == target && op.Action != plan.Unchanged {
				p.Ops[i].Before, p.Ops[i].BeforeExec = cur.Hash, cur.Exec
			}
		}
	}
	if err := checkLinkDirs(d, p.Ops, realDirs); err != nil {
		return plan.Plan{}, nil, err
	}
	if err := checkLinkable(d, p.Ops); err != nil {
		return plan.Plan{}, nil, err
	}
	p.Ops = permute(p.Ops, orderLinks(p.Ops))
	return p, sp, nil
}

// keysOf is what keys gives for target, nil when keys is nil.
func keysOf(keys func(string) [][]string, target string) [][]string {
	if keys == nil {
		return nil
	}
	return keys(target)
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
			WithHint("%s", unmappedHint(p)))
	}
	return diags
}

// unmappedHint is the hint for the staged user-scope file p that nothing
// installs: a file of a harness the install does not include is fixed by
// adding the harness, and any other is one that dotai has no place for.
func unmappedHint(p string) string {
	for _, a := range harness.All() {
		if p == a.HomeConfig || strings.HasPrefix(p, a.Staging+"/") {
			return fmt.Sprintf("it is a file of %s, which this install does not include: add %s to harnesses, or remove the source that renders it", a.Name, a.ID)
		}
	}
	return "dotai knows no place to install it for any harness: remove the source that renders it, and report the path if a harness should read it"
}

// opKind is the kind of item the target of op holds: by its staged
// source, or, for a remove, which has none, by the staged path of its
// target.
func opKind(adapters map[string]harness.Adapter, m harness.Mode, home string, op plan.Op) string {
	if op.Harness == "" {
		return KindTools
	}
	a, ok := adapters[op.Harness]
	if !ok {
		return ""
	}
	staged := op.Source
	if staged == "" {
		if rel, err := filepath.Rel(home, op.Target); err == nil {
			staged = a.StagedOf(m, filepath.ToSlash(rel))
		}
	}
	return string(a.KindOf(staged))
}

// harnessPlan is how a, a harness the install manages, runs after an
// install in mode into home. In the replace mode, that is its own
// directory, where it keeps its login. In the isolated mode, it is its
// isolation on goos, with its directory under the isolated root, for an
// install whose variant shares the user's skills or not (decision 0036).
func harnessPlan(mode harness.Mode, home string, a harness.Adapter, goos string, shared bool) HarnessPlan {
	if mode == harness.Replace {
		return HarnessPlan{ID: a.ID, Dir: filepath.Join(home, filepath.FromSlash(a.Root(mode))), Relogin: string(harness.ReplaceRelogin),
			Pointers: []PointerPlan{}, Leaks: []string{}, Tools: []ToolCommand{}}
	}
	dir := filepath.Join(home, filepath.FromSlash(a.Root(mode)))
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

// layouts are the layouts the targets of p may be in: that of its mode,
// and that of the install before it, whose files it may remove.
func (p planned) layouts() []harness.Mode {
	out := []harness.Mode{harness.Mode(cmp.Or(p.plan.Mode, ModeIsolated))}
	if p.prev != nil && p.prev.Mode != "" && harness.Mode(p.prev.Mode) != out[0] {
		out = append(out, harness.Mode(p.prev.Mode))
	}
	return out
}

// livingProjects is list without the entries of the projects whose
// directory or record under home is gone, which a replace install no
// longer keeps the keys of, and gives those projects.
func livingProjects(d installFS, home string, list state.Projects) (state.Projects, []string) {
	out := state.Projects{Schema: list.Schema, Projects: []state.ProjectEntry{}}
	var gone []string
	for _, e := range list.Projects {
		_, errDir := d.Lstat(e.Project)
		_, errRecord := d.Lstat(recordPath(home, e.Project))
		if errDir != nil || errRecord != nil {
			gone = append(gone, e.Project)
			continue
		}
		out.Projects = append(out.Projects, e)
	}
	return out, gone
}
