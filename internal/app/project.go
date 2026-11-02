package app

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"regexp"
	"slices"

	"github.com/lartweib/dotai/internal/harness"
	"github.com/lartweib/dotai/internal/pack"
	"github.com/lartweib/dotai/internal/plan"
	"github.com/lartweib/dotai/internal/state"
)

// ProjectPlan is the plan of applying profiles to a project (decision
// 0041): each target per harness, the items left uninstalled because git
// tracks their target, the entries of .git/info/exclude and what the plan
// warns about. Lists are never nil; they are empty when there are
// diagnostics.
type ProjectPlan struct {
	Pack     *PackSummary `json:"pack"`    // nil when the manifest cannot be read
	Variant  string       `json:"variant"` // the user scope's, which the cross-scope rules check the profiles against
	Profiles []string     `json:"profiles"`
	Project  string       `json:"project"` // the absolute project directory
	Record   string       `json:"record"`  // the project record, <project>/.dotai/state.json
	// Harnesses are the harnesses the plan is for, in manifest order.
	Harnesses []string         `json:"harnesses"`
	Ops       []ProjectOp      `json:"ops"`
	Skipped   []SkippedItem    `json:"skipped"`
	Exclude   ExcludePlan      `json:"exclude"`
	Warnings  []ProjectWarning `json:"warnings"`
	// Notices say why an optional harness is not in the plan, text for
	// people.
	Notices     []string     `json:"notices"`
	Diagnostics []Diagnostic `json:"diagnostics"`
}

// ProjectOp is one target of a project plan; see plan.ProjectOp. Keys are
// never nil.
type ProjectOp struct {
	Action  plan.Action  `json:"action"`
	Harness string       `json:"harness"`
	Kind    string       `json:"kind"`
	Source  string       `json:"source"`
	Target  string       `json:"target"`
	Before  string       `json:"before"`
	After   string       `json:"after"`
	Keys    []ProjectKey `json:"keys"`
}

// ProjectKey is a key a project op writes into a file merged by key, by
// its path from the top of the file, and what it does to it: create,
// merge or unchanged.
type ProjectKey struct {
	Key    []string    `json:"key"`
	Action plan.Action `json:"action"`
}

// SkippedItem is an item a project plan leaves uninstalled, and why: so
// far only tracked, git tracks Target, the file it goes to.
type SkippedItem struct {
	Harness string `json:"harness"`
	Kind    string `json:"kind"`
	Item    string `json:"item"`
	Target  string `json:"target"`
	Reason  string `json:"reason"`
}

// SkipTracked is the reason of an item whose target git tracks.
const SkipTracked = "tracked"

// ExcludePlan is the entries of .git/info/exclude a project apply adds:
// File, the exclude file, "" outside a git repository, where the entries
// are Pending until there is one.
type ExcludePlan struct {
	File    string   `json:"file"`
	Pending bool     `json:"pending"`
	Entries []string `json:"entries"`
}

// ProjectWarning is a warning of a project plan; see plan.Warning.
type ProjectWarning struct {
	Code    string `json:"code"`
	Harness string `json:"harness"`
	Item    string `json:"item"`
	Path    string `json:"path"`
	Message string `json:"message"`
}

// ApplyOptions are the choices of a project apply: the profiles, and the
// variant of the user scope, which the install state records when it is
// empty, or else the pack's default.
type ApplyOptions struct {
	Profiles []string
	Variant  string
}

// ProjectRefusedError is a project directory where the project scope would
// overlap the user scope: the home directory or the root of a drive or of
// the file system (decision 0041).
type ProjectRefusedError struct {
	Dir, Why string
}

func (e *ProjectRefusedError) Error() string {
	return fmt.Sprintf("%s is %s, where the project scope would overlap the user scope, so apply refuses it: run it in a project directory", e.Dir, e.Why)
}

// PlanProject plans applying the profiles of opts, from the pack in dir,
// to the project directory project, for the user whose home directory is
// home, and writes nothing but the rulesync cache. dir "" is the pack the
// install state records. It stages the pack as Stage does, into a
// temporary directory it removes, with ${DOTAI_PROJECT_TOOLS} at the
// project's tools directory, then places each file of the project tree as
// plan.BuildProject does, after asking git which targets it tracks. The
// harnesses are those an install would manage. rulesync is fetched into
// cacheDir as FetchRulesync does; progress may be nil.
func PlanProject(ctx context.Context, cacheDir, dir, project, home string, opts ApplyOptions, progress Progress) (ProjectPlan, error) {
	return planProject(ctx, osInstallFS{}, osHost(), osGit{}, download, generate, cacheDir, dir, project, home, opts, progress)
}

func planProject(ctx context.Context, d installFS, h host, g gitPort, fetch fetchFunc, run runFunc, cacheDir, dir, project, home string, opts ApplyOptions, progress Progress) (res ProjectPlan, err error) {
	if !filepath.IsAbs(home) || !filepath.IsAbs(project) {
		return ProjectPlan{}, fmt.Errorf("home directory %q and project %q must be absolute", home, project)
	}
	home, project = filepath.Clean(home), filepath.Clean(project)
	if err := checkProject(project, home); err != nil {
		return ProjectPlan{}, err
	}
	prev, err := readState(d, home)
	if err != nil {
		return ProjectPlan{}, err
	}
	if dir == "" {
		if prev == nil || prev.Pack.Path == "" {
			return ProjectPlan{}, &UsageError{"apply needs the pack: name its directory, or install it first so that the install state records it"}
		}
		dir = prev.Pack.Path
	}
	variant := opts.Variant
	if abs, err := filepath.Abs(dir); err == nil && variant == "" && prev != nil && samePath(filepath.Clean(prev.Pack.Path), abs) {
		variant = prev.Variant
	}
	repo, err := g.Repo(ctx, project)
	if err != nil {
		return ProjectPlan{}, err
	}
	tmp, err := d.StagingDir()
	if err != nil {
		return ProjectPlan{}, err
	}
	defer func() {
		if rmErr := d.RemoveAll(tmp); rmErr != nil {
			err = errors.Join(err, &stagingError{dir: tmp, err: rmErr})
		}
	}()
	var notices []string
	pick := func(m pack.Manifest) ([]string, error) {
		detected, err := detectAll(d, h, home, m)
		if err != nil {
			return nil, err
		}
		selected, n, _, err := selectHarnesses(m, nil, prev, detected)
		notices = n
		return selected, err
	}
	tools := ToolsIn(home, project)
	s, err := stage(ctx, d, fetch, run, cacheDir, dir, filepath.Join(tmp, "staging"), Selection{Variant: variant, Profiles: opts.Profiles}, pick, tools, false, progress)
	if err != nil {
		return ProjectPlan{}, err
	}
	res = ProjectPlan{
		Pack: s.Pack, Variant: variant, Profiles: append([]string{}, opts.Profiles...), Project: project,
		Record: filepath.Join(project, filepath.FromSlash(plan.RecordFile)), Harnesses: []string{}, Ops: []ProjectOp{}, Skipped: []SkippedItem{},
		Exclude: ExcludePlan{Pending: repo == nil, Entries: []string{}}, Warnings: []ProjectWarning{}, Notices: []string{}, Diagnostics: s.Diagnostics,
	}
	if s.Pack != nil {
		res.Variant = cmp.Or(variant, s.Pack.DefaultVariant)
	}
	if repo != nil {
		res.Exclude.File = repo.Exclude
	}
	if len(s.Diagnostics) > 0 {
		return res, nil
	}
	adapters, err := harness.Lookup(s.Harnesses)
	if err != nil {
		return ProjectPlan{}, err
	}
	res.Harnesses, res.Notices = append(res.Harnesses, s.Harnesses...), append(res.Notices, notices...)
	in, err := projectInput(ctx, d, g, filepath.Join(tmp, "staging", ScopeProject), project, home, s.Pack.Name, adapters, prev, repo)
	if u, ok := errors.AsType[*plan.UnmappedError](err); ok {
		res.Diagnostics = projectUnmapped(u)
		return res, nil
	}
	if err != nil {
		return ProjectPlan{}, err
	}
	p, err := plan.BuildProject(in)
	if err != nil {
		return ProjectPlan{}, err
	}
	for _, op := range p.Ops {
		keys := []ProjectKey{}
		for _, k := range op.Keys {
			keys = append(keys, ProjectKey{Key: k.Path, Action: k.Action})
		}
		res.Ops = append(res.Ops, ProjectOp{Action: op.Action, Harness: op.Harness, Kind: op.Kind, Source: op.Source, Target: op.Target, Before: op.Before, After: op.After, Keys: keys})
	}
	for _, sk := range p.Skipped {
		res.Skipped = append(res.Skipped, SkippedItem{Harness: sk.Harness, Kind: sk.Kind, Item: sk.Item, Target: sk.Target, Reason: SkipTracked})
	}
	res.Exclude.Entries = append(res.Exclude.Entries, p.Exclude...)
	for _, w := range p.Warnings {
		res.Warnings = append(res.Warnings, ProjectWarning(w))
	}
	return res, nil
}

// checkProject refuses a project directory that is the home directory or
// a root, where the project's .claude, .agents and .dotai would be the
// user's own.
func checkProject(project, home string) error {
	switch {
	case samePath(project, home):
		return &ProjectRefusedError{Dir: project, Why: "the home directory"}
	case filepath.Dir(project) == project && filepath.VolumeName(project) != "":
		return &ProjectRefusedError{Dir: project, Why: "the root of a drive"}
	case filepath.Dir(project) == project:
		return &ProjectRefusedError{Dir: project, Why: "the root of the file system"}
	}
	return nil
}

// projectInput reads what plan.BuildProject plans the project tree tree
// from: each staged file whole, the state of each target, which of them
// git tracks in repo (nil outside one), the paths of plan.ProjectProbes,
// and whether the project needs Claude Code's import of AGENTS.md.
func projectInput(ctx context.Context, d installFS, g gitPort, tree, project, home, packName string, adapters []harness.Adapter, prev *state.State, repo *Repo) (plan.ProjectInput, error) {
	in := plan.ProjectInput{
		Project: project, Home: home, Pack: packName, Adapters: adapters, Repo: repo != nil,
		Disk: map[string]plan.Current{}, Content: map[string][]byte{}, Tracked: map[string]bool{}, Exists: map[string]bool{}, Isolated: map[string]bool{},
	}
	if repo != nil {
		in.Prefix = repo.Prefix
	}
	if prev != nil && prev.Mode == ModeIsolated {
		for _, id := range prev.Harnesses {
			in.Isolated[id] = true
		}
	}
	names, err := d.Files(tree)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return plan.ProjectInput{}, err
	}
	slices.Sort(names)
	for _, name := range names {
		data, cur, err := d.Read(ctx, filepath.Join(tree, filepath.FromSlash(name)))
		if err != nil {
			return plan.ProjectInput{}, err
		}
		in.Files = append(in.Files, plan.File{Path: name, Hash: cur.Hash, Exec: cur.Exec, Data: data})
	}
	dirs := map[string]error{}
	if in.ImportAgents, err = needsImport(ctx, d, project, dirs); err != nil {
		return plan.ProjectInput{}, err
	}
	// The targets come from a first plan, which reads nothing.
	first, err := plan.BuildProject(in)
	if err != nil {
		return plan.ProjectInput{}, err
	}
	var rels []string
	homeDirs := map[string]error{}
	for _, op := range first.Ops {
		base, cache := project, dirs
		rel, err := filepath.Rel(project, op.Target)
		if err != nil || !filepath.IsLocal(rel) {
			base, cache = home, homeDirs
		} else {
			rels = append(rels, filepath.ToSlash(rel))
		}
		how := inspectHash
		if op.After == "" {
			how = inspectRead
		}
		cur, data, ok, err := inspectTarget(ctx, d, base, op.Target, cache, how)
		if err != nil {
			return plan.ProjectInput{}, err
		}
		if ok {
			in.Disk[op.Target], in.Content[op.Target] = cur, data
		}
	}
	if repo != nil && len(rels) > 0 {
		tracked, err := g.Tracked(ctx, project, rels)
		if err != nil {
			return plan.ProjectInput{}, err
		}
		for rel := range tracked {
			in.Tracked[filepath.Join(project, filepath.FromSlash(rel))] = true
		}
	}
	probes, err := plan.ProjectProbes(in)
	if err != nil {
		return plan.ProjectInput{}, err
	}
	for _, p := range probes {
		if _, err := d.Lstat(p); err == nil {
			in.Exists[p] = true
		} else if !errors.Is(err, fs.ErrNotExist) {
			return plan.ProjectInput{}, err
		}
	}
	return in, nil
}

// agentsImport is an import of AGENTS.md at the project root in a
// CLAUDE.md of the root.
var agentsImport = regexp.MustCompile(`(?m)(^|\s)@(\./)?AGENTS\.md(\s|$)`)

// needsImport reports whether the project has an AGENTS.md that no
// CLAUDE.md imports: Claude Code does not load a project AGENTS.md alone
// (HARNESS-NOTES), so the pack's rules file imports it. A CLAUDE.md that is
// a link, as a CLAUDE.md that points at AGENTS.md often is, counts as an
// import.
func needsImport(ctx context.Context, d installFS, project string, dirs map[string]error) (bool, error) {
	if _, err := d.Lstat(filepath.Join(project, "AGENTS.md")); errors.Is(err, fs.ErrNotExist) {
		return false, nil
	} else if err != nil {
		return false, err
	}
	for _, name := range []string{"CLAUDE.md", ".claude/CLAUDE.md", "CLAUDE.local.md"} {
		target := filepath.Join(project, filepath.FromSlash(name))
		mode, err := d.Lstat(target)
		switch {
		case errors.Is(err, fs.ErrNotExist):
			continue
		case err != nil:
			return false, err
		case !mode.IsRegular():
			return false, nil
		}
		_, data, ok, err := inspectTarget(ctx, d, project, target, dirs, inspectRead)
		if err != nil {
			return false, err
		}
		if ok && agentsImport.Match(data) {
			return false, nil
		}
	}
	return true, nil
}

// projectUnmapped reports each staged project file that no harness of the
// pack installs at the manifest.
func projectUnmapped(u *plan.UnmappedError) []Diagnostic {
	var diags []Diagnostic
	for _, p := range u.Paths {
		diags = append(diags, pack.NewDiagnostic("dotai.yaml", 0, 0, CodePathUnmapped, "staged file %s/%s is installed by no harness of the pack", ScopeProject, p).
			WithHint("add the harness that reads it to harnesses, or remove the source that renders it"))
	}
	return diags
}
