package app

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"slices"
	"strings"

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
	// Record is the project's record under the home directory,
	// ~/.dotai/projects/<project id>/record.json, and Marker the marker in
	// the project, <project>/.dotai/state.json, which dotai never trusts.
	Record string `json:"record"`
	Marker string `json:"marker"`
	// Harnesses are the harnesses the plan is for, in manifest order.
	Harnesses []string         `json:"harnesses"`
	Ops       []plan.ProjectOp `json:"ops"`
	Skipped   []plan.Skipped   `json:"skipped"`
	Exclude   ExcludePlan      `json:"exclude"`
	Warnings  []plan.Warning   `json:"warnings"`
	// Refusals are why the apply would refuse, which no consent overrides.
	Refusals []plan.Warning `json:"refusals"`
	// FirstApply is set when the project has no record of an earlier apply.
	FirstApply bool `json:"firstApply"`
	// Notices say why an optional harness is not in the plan, and when
	// Claude Code loads each mod the plan installs, text for people.
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

func planProject(ctx context.Context, d installFS, h host, g gitPort, fetch fetchFunc, run runFunc, cacheDir, dir, cwd, home string, opts ApplyOptions, progress Progress) (ProjectPlan, error) {
	var res ProjectPlan
	err := withProjectPlan(ctx, d, h, g, fetch, run, cacheDir, dir, cwd, home, opts, progress, func(p projectPlanned) error {
		if p.prev != nil && p.prev.Pending != nil {
			return &InterruptedError{Record: p.plan.Record}
		}
		res = p.plan
		return nil
	})
	return res, err
}

// InterruptedError is a project whose record holds the journal of an
// apply or a restore that stopped midway, which a dry run cannot plan
// from: the next apply or restore rolls it back first.
type InterruptedError struct {
	Record string
}

func (e *InterruptedError) Error() string {
	return fmt.Sprintf("the project record %s holds the journal of an apply or a restore that stopped midway: run dotai apply or dotai restore --project to roll it back first", e.Record)
}

// projectPlanned is a project plan with what carrying it out needs: what
// it was planned with, the record of the earlier apply (nil before the
// first one), what a record of the project may name, the pack, what dotai
// owns that the plan leaves since git tracks it, the list of projects
// whose keys dotai owns in a home file, and the home file of Claude Code's
// MCP servers.
type projectPlanned struct {
	plan    ProjectPlan
	in      plan.ProjectInput
	prev    *state.Project
	scope   recordScope
	pack    state.Pack
	kept    map[string]plan.ProjectOwned
	shared  state.Projects
	homeMCP string
}

// findProject is the project of the current directory cwd, as PlanProject
// finds it, with the key of Claude Code's projects entry for it and the
// repository it is in, nil outside one; the home directory and a root are
// refused.
func findProject(ctx context.Context, g gitPort, cwd, home string) (project, mcpKey string, repo *Repo, err error) {
	if err := checkProject(cwd, home); err != nil {
		return "", "", nil, err
	}
	if repo, err = g.Repo(ctx, cwd); err != nil {
		return "", "", nil, err
	}
	project, mcpKey = cwd, filepath.ToSlash(cwd)
	if repo != nil {
		project, mcpKey = repo.Top, filepath.ToSlash(repo.Main)
		if err := checkProject(project, home); err != nil {
			return "", "", nil, err
		}
	}
	return project, mcpKey, repo, nil
}

// withProjectPlan plans the apply as PlanProject describes, against the
// project record of the earlier apply, and calls then with the plan while
// the staging still exists; the staging is removed after then returns.
func withProjectPlan(ctx context.Context, d installFS, h host, g gitPort, fetch fetchFunc, run runFunc, cacheDir, dir, cwd, home string, opts ApplyOptions, progress Progress, then func(projectPlanned) error) (err error) {
	if !filepath.IsAbs(home) || !filepath.IsAbs(cwd) {
		return fmt.Errorf("home directory %q and current directory %q must be absolute", home, cwd)
	}
	home, cwd = filepath.Clean(home), filepath.Clean(cwd)
	project, mcpKey, repo, err := findProject(ctx, g, cwd, home)
	if err != nil {
		return err
	}
	prev, err := readState(d, home)
	if err != nil {
		return err
	}
	scope := scopeOf(home, project, mcpKey, repo)
	record, err := readRecord(ctx, d, scope)
	if err != nil {
		return err
	}
	shared, err := readProjects(d, home)
	if err != nil {
		return err
	}
	if dir == "" {
		if prev == nil || prev.Pack.Path == "" {
			return &UsageError{"apply needs the pack: name its directory, or install it first so that the install state records it"}
		}
		dir = prev.Pack.Path
	}
	variant := opts.Variant
	if abs, err := filepath.Abs(dir); err == nil && variant == "" && prev != nil && samePath(filepath.Clean(prev.Pack.Path), abs) {
		variant = prev.Variant
	}
	tmp, err := d.StagingDir()
	if err != nil {
		return err
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
		return err
	}
	res := ProjectPlan{
		Pack: s.Pack, Variant: variant, Profiles: append([]string{}, opts.Profiles...), Project: project,
		Record: recordPath(home, project), Marker: filepath.Join(project, filepath.FromSlash(plan.MarkerFile)), FirstApply: record == nil, Harnesses: []string{}, Ops: []plan.ProjectOp{}, Skipped: []plan.Skipped{},
		Exclude: ExcludePlan{Pending: repo == nil, Entries: []string{}}, Warnings: []plan.Warning{}, Refusals: []plan.Warning{}, Notices: []string{}, Diagnostics: s.Diagnostics,
	}
	if s.Pack != nil {
		res.Variant = cmp.Or(variant, s.Pack.DefaultVariant)
	}
	if repo != nil {
		res.Exclude.File = repo.Exclude
	}
	packPath, err := filepath.Abs(dir)
	if err != nil {
		return err
	}
	tree := filepath.Join(tmp, "staging", ScopeProject)
	planned := projectPlanned{plan: res, prev: record, scope: scope, shared: shared}
	if s.Pack != nil {
		planned.pack = state.Pack{Name: s.Pack.Name, Version: s.Pack.Version, Path: packPath}
	}
	if len(s.Diagnostics) > 0 {
		return then(planned)
	}
	adapters, err := harness.Lookup(s.Harnesses)
	if err != nil {
		return err
	}
	res.Harnesses, res.Notices = append(res.Harnesses, s.Harnesses...), append(res.Notices, notices...)
	in := plan.ProjectInput{Project: project, Home: home, Pack: s.Pack.Name, Adapters: adapters, Repo: repo != nil, MCPKey: mcpKey}
	if record != nil {
		in.Owned = record.Owned()
	}
	err = readProject(ctx, d, g, tree, prev, &in)
	if u, ok := errors.AsType[*plan.UnmappedError](err); ok {
		res.Diagnostics = projectUnmapped(u)
		planned.plan = res
		return then(planned)
	}
	if err != nil {
		return err
	}
	for _, a := range adapters {
		if m := a.Project.MCP; m != nil {
			file := m.Real
			if in.Isolated[a.ID] {
				file = m.Isolated
			}
			planned.homeMCP = filepath.Join(home, filepath.FromSlash(file))
		}
	}
	for _, k := range shared.OwnedKeys(planned.homeMCP) {
		if len(k) > 1 && k[1] == mcpKey && !ownsKey(record, planned.homeMCP, k) {
			in.Shared = append(in.Shared, k)
		}
	}
	p, err := plan.BuildProject(in)
	if err != nil {
		return err
	}
	res.Ops, res.Skipped = append(res.Ops, p.Ops...), append(res.Skipped, p.Skipped...)
	res.Notices = append(res.Notices, modNotices(p.Ops, project)...)
	res.Exclude.Entries, res.Warnings = append(res.Exclude.Entries, p.Exclude...), append(res.Warnings, p.Warnings...)
	res.Refusals = append(res.Refusals, p.Refusals...)
	planned.plan, planned.in, planned.kept = res, in, p.Kept
	if err := planned.markerOp(); err != nil {
		return err
	}
	return then(planned)
}

// modNotices say, for each mod the plan installs, when Claude Code loads
// it (HARNESS-NOTES): only from the top of the project, after the user
// trusts the workspace, and from a version that has mods.
func modNotices(ops []plan.ProjectOp, project string) []string {
	var names []string
	for _, op := range ops {
		rest, ok := strings.CutPrefix(op.Source, harness.ModsSource)
		if !ok || op.Kind != string(harness.ProjectMods) || op.Action == plan.Remove {
			continue
		}
		if name, _, _ := strings.Cut(rest, "/"); !slices.Contains(names, name) {
			names = append(names, name)
		}
	}
	var out []string
	for _, name := range names {
		out = append(out, fmt.Sprintf("the mod %s loads in Claude Code %s or later, in a terminal, only in a session started at %s, and only after you accept the workspace trust dialog there", name, harness.ModsMinClaude, project))
	}
	return out
}

// CodeMarkerMismatch is the warning of a project marker that does not say
// what the record under the home directory says, which wins.
const CodeMarkerMismatch = "marker-mismatch"

// markerOp gives the op of the project marker what it writes, the pack,
// variant and profiles applied and the record's id, which leaves it
// unchanged when it holds that already; a marker that holds something
// else is warned about, since the record under the home directory wins.
func (p *projectPlanned) markerOp() error {
	data, err := state.EncodeMarker(state.Marker{Schema: state.MarkerSchema, ID: p.scope.id, Pack: p.pack, Variant: p.plan.Variant, Profiles: p.plan.Profiles})
	if err != nil {
		return err
	}
	for i, op := range p.plan.Ops {
		if op.Kind != plan.KindMarker {
			continue
		}
		op.Data, op.After = data, contentHash(data)
		if cur, ok := p.in.Disk[op.Target]; ok {
			op.Action = plan.Replace
			if cur.Hash == op.After {
				op.Action = plan.Unchanged
			} else if p.prev != nil {
				p.plan.Warnings = append(p.plan.Warnings, plan.Warning{Code: CodeMarkerMismatch, Path: op.Target,
					Message: fmt.Sprintf("%s does not say what the record %s says; the record wins, and the apply rewrites the marker", op.Target, p.plan.Record)})
			}
		}
		p.plan.Ops[i] = op
	}
	return nil
}

// ownsKey reports whether the record r owns the key k of file.
func ownsKey(r *state.Project, file string, k []string) bool {
	if r == nil {
		return false
	}
	return slices.ContainsFunc(r.Files, func(f state.ProjectFile) bool {
		return f.Target == file && slices.ContainsFunc(f.Keys, func(o plan.OwnedKey) bool { return slices.Equal(o.Path, k) })
	})
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
// with the directories of the items dotai owns whole,
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
	// An item dotai owns whole, such as a mod, is skipped when git tracks
	// any file in its directory.
	whole, err := plan.ProjectDirs(*in)
	if err != nil {
		return err
	}
	for _, dir := range whole {
		if rel, err := filepath.Rel(in.Project, dir); err == nil && filepath.IsLocal(rel) {
			rels = append(rels, filepath.ToSlash(rel)+"/")
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
