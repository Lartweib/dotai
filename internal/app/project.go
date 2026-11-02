package app

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"slices"

	"github.com/lartweib/dotai/internal/harness"
	"github.com/lartweib/dotai/internal/pack"
	"github.com/lartweib/dotai/internal/plan"
	"github.com/lartweib/dotai/internal/state"
)

// ProjectPlan is the plan of applying profiles to a project (decision
// 0041), as dotai apply --dry-run prints it: the plan.Placement of each
// target per harness, with the items left uninstalled because git tracks
// their target and the warnings, and the exclude entries. Lists are never
// nil; they are empty when there are diagnostics.
type ProjectPlan struct {
	Pack     *PackSummary `json:"pack"`    // nil when the manifest cannot be read
	Variant  string       `json:"variant"` // the user scope's, which the cross-scope rules check the profiles against
	Profiles []string     `json:"profiles"`
	// Project is the absolute project directory: the top of the worktree
	// the current directory is in, or the current directory outside git.
	Project string `json:"project"`
	Record  string `json:"record"` // the project record, <project>/.dotai/state.json
	// Harnesses are the harnesses the plan is for, in manifest order.
	Harnesses []string         `json:"harnesses"`
	Ops       []plan.ProjectOp `json:"ops"`
	Skipped   []plan.Skipped   `json:"skipped"`
	Exclude   ExcludePlan      `json:"exclude"`
	Warnings  []plan.Warning   `json:"warnings"`
	// Notices say why an optional harness is not in the plan, text for
	// people.
	Notices     []string     `json:"notices"`
	Diagnostics []Diagnostic `json:"diagnostics"`
}

// ExcludePlan is the entries of .git/info/exclude a project apply adds:
// File, the exclude file, "" outside a git repository, where the entries
// are Pending until there is one.
type ExcludePlan struct {
	File    string   `json:"file"`
	Pending bool     `json:"pending"`
	Entries []string `json:"entries"`
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
// the file system, however a link or a short name spells it (decision
// 0041).
type ProjectRefusedError struct {
	Dir, Why string
}

func (e *ProjectRefusedError) Error() string {
	return fmt.Sprintf("%s is %s, where the project scope would overlap the user scope, so apply refuses it: run it in a project directory", e.Dir, e.Why)
}

// PlanProject plans applying the profiles of opts, from the pack in dir,
// to the project of the current directory cwd, for the user whose home
// directory is home, and writes nothing but the rulesync cache. The
// project is the top of the worktree cwd is in, or cwd outside git. dir ""
// is the pack the install state records. It stages the pack as Stage
// does, into a temporary directory it removes, with ${DOTAI_PROJECT_TOOLS}
// at the project's tools directory, then places each file of the project
// tree as plan.BuildProject does, after asking git which targets it
// tracks. The harnesses are those an install would manage. rulesync is
// fetched into cacheDir as FetchRulesync does; progress may be nil.
func PlanProject(ctx context.Context, cacheDir, dir, cwd, home string, opts ApplyOptions, progress Progress) (ProjectPlan, error) {
	return planProject(ctx, osInstallFS{}, osHost(), osGit{}, download, generate, cacheDir, dir, cwd, home, opts, progress)
}

func planProject(ctx context.Context, d installFS, h host, g gitPort, fetch fetchFunc, run runFunc, cacheDir, dir, cwd, home string, opts ApplyOptions, progress Progress) (res ProjectPlan, err error) {
	if !filepath.IsAbs(home) || !filepath.IsAbs(cwd) {
		return ProjectPlan{}, fmt.Errorf("home directory %q and current directory %q must be absolute", home, cwd)
	}
	home, cwd = filepath.Clean(home), filepath.Clean(cwd)
	if err := checkProject(cwd, home); err != nil {
		return ProjectPlan{}, err
	}
	repo, err := g.Repo(ctx, cwd)
	if err != nil {
		return ProjectPlan{}, err
	}
	project, mcpKey := cwd, filepath.ToSlash(cwd)
	if repo != nil {
		project, mcpKey = repo.Top, filepath.ToSlash(repo.Main)
		if err := checkProject(project, home); err != nil {
			return ProjectPlan{}, err
		}
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
	s, err := stage(ctx, d, fetch, run, cacheDir, dir, filepath.Join(tmp, "staging"), Selection{Variant: variant, Profiles: opts.Profiles}, pick, ToolsIn(home, project), false, progress)
	if err != nil {
		return ProjectPlan{}, err
	}
	res = ProjectPlan{
		Pack: s.Pack, Variant: variant, Profiles: append([]string{}, opts.Profiles...), Project: project,
		Record: filepath.Join(project, filepath.FromSlash(plan.RecordFile)), Harnesses: []string{}, Ops: []plan.ProjectOp{}, Skipped: []plan.Skipped{},
		Exclude: ExcludePlan{Pending: repo == nil, Entries: []string{}}, Warnings: []plan.Warning{}, Notices: []string{}, Diagnostics: s.Diagnostics,
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
	in := plan.ProjectInput{Project: project, Home: home, Pack: s.Pack.Name, Adapters: adapters, Repo: repo != nil, MCPKey: mcpKey}
	err = readProject(ctx, d, g, filepath.Join(tmp, "staging", ScopeProject), prev, &in)
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
	res.Ops, res.Skipped = append(res.Ops, p.Ops...), append(res.Skipped, p.Skipped...)
	res.Exclude.Entries, res.Warnings = append(res.Exclude.Entries, p.Exclude...), append(res.Warnings, p.Warnings...)
	return res, nil
}

// checkProject refuses a project directory that is the home directory or
// a root, where the project's .claude, .agents and .dotai would be the
// user's own, as either is spelled and as each resolves through links and
// short names.
func checkProject(project, home string) error {
	for _, p := range []string{project, realPath(project)} {
		switch {
		case samePath(p, home) || samePath(p, realPath(home)):
			return &ProjectRefusedError{Dir: project, Why: "the home directory"}
		case filepath.Dir(p) == p && filepath.VolumeName(p) != "":
			return &ProjectRefusedError{Dir: project, Why: "the root of a drive"}
		case filepath.Dir(p) == p:
			return &ProjectRefusedError{Dir: project, Why: "the root of the file system"}
		}
	}
	return nil
}

// realPath is p with its links resolved and, on Windows, its junctions and
// short names too (resolvePath).
func realPath(p string) string { return resolvePath(p) }

// readProject fills in with what plan.BuildProject plans the project tree
// tree from: each staged file whole, the harnesses that need the import of
// a rules file, the state of each target and which of them git tracks,
// the harnesses installed isolated by prev, and the paths of
// plan.ProjectProbes that exist.
func readProject(ctx context.Context, d installFS, g gitPort, tree string, prev *state.State, in *plan.ProjectInput) error {
	in.Disk, in.Content, in.Tracked, in.Exists = map[string]plan.Current{}, map[string][]byte{}, map[string]bool{}, map[string]bool{}
	in.Isolated, in.Import = map[string]bool{}, map[string]bool{}
	if prev != nil && prev.Mode == ModeIsolated {
		for _, id := range prev.Harnesses {
			in.Isolated[id] = true
		}
	}
	names, err := d.Files(tree)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	slices.Sort(names)
	for _, name := range names {
		data, cur, err := d.Read(ctx, filepath.Join(tree, filepath.FromSlash(name)))
		if err != nil {
			return err
		}
		in.Files = append(in.Files, plan.File{Path: name, Hash: cur.Hash, Exec: cur.Exec, Data: data})
	}
	dirs, homeDirs := map[string]error{}, map[string]error{}
	for _, a := range in.Adapters {
		if in.Import[a.ID], err = needsImport(ctx, d, in.Project, a.Project, dirs); err != nil {
			return err
		}
	}
	targets, err := plan.ProjectTargets(*in)
	if err != nil {
		return err
	}
	var rels []string
	for _, t := range targets {
		base, cache := in.Project, dirs
		if rel, err := filepath.Rel(in.Project, t.Path); err == nil && filepath.IsLocal(rel) {
			rels = append(rels, filepath.ToSlash(rel))
		} else {
			base, cache = in.Home, homeDirs
		}
		how := inspectHash
		if t.Read {
			how = inspectRead
		}
		cur, data, ok, err := inspectTarget(ctx, d, base, t.Path, cache, how)
		if err != nil {
			return err
		}
		if ok {
			in.Disk[t.Path], in.Content[t.Path] = cur, data
		}
	}
	if in.Repo && len(rels) > 0 {
		tracked, err := g.Tracked(ctx, in.Project, rels)
		if err != nil {
			return err
		}
		for rel := range tracked {
			in.Tracked[filepath.Join(in.Project, filepath.FromSlash(rel))] = true
		}
	}
	probes, err := plan.ProjectProbes(*in)
	if err != nil {
		return err
	}
	for _, p := range probes {
		if _, err := d.Lstat(p); err == nil {
			in.Exists[p] = true
		} else if !errors.Is(err, fs.ErrNotExist) {
			return err
		}
	}
	return nil
}

// needsImport reports whether the project has the rules file l.Imports
// that the harness does not load alone (HARNESS-NOTES) and that none of
// l.ImportFiles imports, so that the pack's rules file imports it. A file
// of l.ImportFiles that is a link, as a CLAUDE.md that points at AGENTS.md
// often is, counts as an import.
func needsImport(ctx context.Context, d installFS, project string, l harness.ProjectLayout, dirs map[string]error) (bool, error) {
	if l.Imports == "" {
		return false, nil
	}
	if _, err := d.Lstat(filepath.Join(project, filepath.FromSlash(l.Imports))); errors.Is(err, fs.ErrNotExist) {
		return false, nil
	} else if err != nil {
		return false, err
	}
	for _, f := range l.ImportFiles {
		target := filepath.Join(project, filepath.FromSlash(f.Path))
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
		if ok && l.ImportedBy(f.Path, data) {
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
