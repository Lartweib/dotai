package app_test

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/lartweib/dotai/internal/app"
	"github.com/lartweib/dotai/internal/plan"
	"github.com/lartweib/dotai/internal/rulesync"
	"github.com/lartweib/dotai/internal/state"
)

// profileTree is what a fake rulesync writes into the project scope: a
// profile with a rule, a skill, an MCP server and settings, as rulesync
// 41.0.0 renders it for every harness.
var profileTree = map[string]string{
	".claude/rules/web.md":         "# web\n",
	".claude/skills/lint/SKILL.md": "lint\n",
	".claude/settings.json":        `{"model":"sonnet"}`,
	".mcp.json":                    `{"mcpServers":{"browser":{"command":"npx"}}}`,
	".agents/rules/web.md":         "# web\n",
	".agents/mcp_config.json":      `{"mcpServers":{"browser":{"command":"npx"}}}`,
	".opencode/memories/web.md":    "# web\n",
	"opencode.jsonc":               `{"instructions":[".opencode/memories/web.md"]}`,
}

// projectRulesync is a rulesync that renders profileTree into the project
// scope and nothing at user level.
type projectRulesync struct{ t *testing.T }

func (f projectRulesync) fetch(_ context.Context, _, version string, _ app.Progress) (rulesync.Result, error) {
	return rulesync.Result{Version: version}, nil
}

func (f projectRulesync) run(_ context.Context, _ rulesync.Result, _ []string, jobs ...rulesync.Job) error {
	for _, j := range jobs {
		if j.Global {
			continue
		}
		for name, data := range profileTree {
			writeFile(f.t, filepath.Join(j.Dir, filepath.FromSlash(name)), data)
		}
	}
	return nil
}

// fakeGit is a repository at the top of the project whose index holds
// tracked, or no repository when outside is set.
type fakeGit struct {
	outside bool
	tracked []string
	asked   []string
}

func (g *fakeGit) Repo(_ context.Context, dir string) (*app.Repo, error) {
	if g.outside {
		return nil, nil
	}
	return &app.Repo{Exclude: filepath.Join(dir, ".git", "info", "exclude")}, nil
}

func (g *fakeGit) Tracked(_ context.Context, _ string, paths []string) (map[string]bool, error) {
	g.asked = paths
	out := map[string]bool{}
	for _, p := range paths {
		if slices.Contains(g.tracked, p) {
			out[p] = true
		}
	}
	return out, nil
}

func planProject(t *testing.T, g app.GitPort, dir, project, home string) (app.ProjectPlan, error) {
	t.Helper()
	f := projectRulesync{t}
	return app.PlanProjectWith(context.Background(), app.OSInstallFS{}, g, f.fetch, f.run, dir, project, home, app.ApplyOptions{Profiles: []string{"web"}})
}

// projectDirs are a project and a home directory, each empty, and the
// temporary directory, so that a test sees anything the plan leaves.
func projectDirs(t *testing.T) (project, home, tmp string) {
	t.Helper()
	home, tmp = installHome(t)
	return t.TempDir(), home, tmp
}

func TestPlanProjectListsEachTargetAndWritesNothing(t *testing.T) {
	project, home, tmp := projectDirs(t)
	g := &fakeGit{tracked: []string{".claude/skills/lint/SKILL.md"}}
	p, err := planProject(t, g, examplePack, project, home)
	if err != nil || len(p.Diagnostics) != 0 {
		t.Fatalf("PlanProject() = %+v, %v", p, err)
	}
	if p.Pack == nil || p.Pack.Name != "minimal-pack" || p.Variant != "standalone" || !reflect.DeepEqual(p.Profiles, []string{"web"}) ||
		p.Project != project || p.Record != at(project, ".dotai/state.json") || !reflect.DeepEqual(p.Harnesses, []string{"claudecode", "antigravity-cli", "opencode"}) {
		t.Errorf("header = %+v", p)
	}
	var got []string
	for _, op := range p.Ops {
		got = append(got, string(op.Action)+" "+op.Harness+" "+op.Target)
	}
	want := []string{
		"create claudecode " + at(home, ".claude.json"),
		"create antigravity-cli " + at(project, ".agents/mcp_config.json"),
		"create antigravity-cli " + at(project, ".agents/rules/web.md"),
		"create claudecode " + at(project, ".claude/rules/web.md"),
		"create claudecode " + at(project, ".claude/settings.local.json"),
		"create  " + at(project, ".dotai/state.json"),
		"create opencode " + at(project, ".opencode/memories/web.md"),
		"create opencode " + at(project, ".opencode/opencode.json"),
	}
	slices.Sort(want)
	slices.Sort(got)
	if !reflect.DeepEqual(got, want) {
		t.Errorf("ops =\n%q\nwant\n%q", got, want)
	}
	if want := []app.SkippedItem{{Harness: "claudecode", Kind: "skills", Item: "skill lint", Target: at(project, ".claude/skills/lint/SKILL.md"), Reason: app.SkipTracked}}; !reflect.DeepEqual(p.Skipped, want) {
		t.Errorf("skipped = %+v, want %+v", p.Skipped, want)
	}
	if !slices.Contains(g.asked, ".claude/skills/lint/SKILL.md") || slices.ContainsFunc(g.asked, func(p string) bool { return strings.Contains(p, ".claude.json") }) {
		t.Errorf("git was asked about %q, want the project targets only", g.asked)
	}
	wantExclude := app.ExcludePlan{File: at(project, ".git/info/exclude"), Entries: []string{
		"/.dotai/", "/.agents/mcp_config.json", "/.agents/rules/web.md", "/.claude/rules/web.md", "/.claude/settings.local.json",
		"/.opencode/memories/web.md", "/.opencode/opencode.json",
	}}
	if !reflect.DeepEqual(p.Exclude, wantExclude) {
		t.Errorf("exclude = %+v, want %+v", p.Exclude, wantExclude)
	}
	if got := tree(t, project); len(got) != 0 {
		t.Errorf("project = %v, want it untouched", got)
	}
	if got := tree(t, home); len(got) != 0 {
		t.Errorf("home = %v, want it untouched", got)
	}
	if entries, _ := os.ReadDir(tmp); len(entries) != 0 {
		t.Errorf("temporary directory holds %v, want the staging removed", entries)
	}
}

func TestPlanProjectOutsideARepository(t *testing.T) {
	project, home, _ := projectDirs(t)
	p, err := planProject(t, &fakeGit{outside: true}, examplePack, project, home)
	if err != nil {
		t.Fatal(err)
	}
	if !p.Exclude.Pending || p.Exclude.File != "" || len(p.Exclude.Entries) == 0 {
		t.Errorf("exclude = %+v, want the entries pending", p.Exclude)
	}
	if !slices.ContainsFunc(p.Warnings, func(w app.ProjectWarning) bool { return w.Code == plan.CodeExcludePending }) {
		t.Errorf("warnings = %+v, want exclude-pending", p.Warnings)
	}
}

func TestPlanProjectRefusesTheHomeDirectoryAndARoot(t *testing.T) {
	project, home, _ := projectDirs(t)
	_ = project
	root := string(filepath.Separator)
	if v := filepath.VolumeName(home); v != "" {
		root = v + root
	}
	for _, tt := range []struct{ dir, why string }{{home, "the home directory"}, {root, "the root of"}} {
		_, err := planProject(t, &fakeGit{}, examplePack, tt.dir, home)
		if r, ok := errors.AsType[*app.ProjectRefusedError](err); !ok || !strings.HasPrefix(r.Why, tt.why) {
			t.Errorf("PlanProject(%s) error = %v, want a refusal for %s", tt.dir, err, tt.why)
		}
	}
	if got := tree(t, home); len(got) != 0 {
		t.Errorf("home = %v, want it untouched", got)
	}
}

func TestPlanProjectUsesThePackOfTheInstallState(t *testing.T) {
	project, home, _ := projectDirs(t)
	if _, err := planProject(t, &fakeGit{}, "", project, home); !errors.As(err, new(*app.UsageError)) {
		t.Fatalf("PlanProject() without a pack or a state: error = %v, want a usage error", err)
	}
	abs, err := filepath.Abs(examplePack)
	if err != nil {
		t.Fatal(err)
	}
	writeState(t, at(home, ".dotai/state.json"), state.State{Schema: state.Schema, Pack: state.Pack{Name: "minimal-pack", Version: "0.1.0", Path: abs},
		Variant: "tmux", Mode: app.ModeIsolated, Harnesses: []string{"claudecode", "antigravity-cli", "opencode"}, Files: []state.File{}})
	p, err := planProject(t, &fakeGit{}, "", project, home)
	if err != nil || len(p.Diagnostics) != 0 {
		t.Fatalf("PlanProject() = %+v, %v", p, err)
	}
	if p.Variant != "tmux" {
		t.Errorf("variant = %q, want the one the install state records", p.Variant)
	}
	// Claude Code runs isolated, so its project MCP servers go to the
	// .claude.json of its isolated directory.
	if !slices.ContainsFunc(p.Ops, func(op app.ProjectOp) bool { return op.Target == at(home, ".dotai/isolated/claude/.claude.json") }) {
		t.Errorf("ops = %+v, want the isolated .claude.json", p.Ops)
	}
}

func TestPlanProjectImportsAgentsMDOnlyWhenNoClaudeMDDoes(t *testing.T) {
	project, home, _ := projectDirs(t)
	writeFile(t, at(project, "AGENTS.md"), "# team\n")
	importOp := func(p app.ProjectPlan) bool {
		return slices.ContainsFunc(p.Ops, func(op app.ProjectOp) bool { return op.Target == at(project, ".claude/rules/minimal-pack.md") })
	}
	p, err := planProject(t, &fakeGit{}, examplePack, project, home)
	if err != nil || !importOp(p) {
		t.Fatalf("PlanProject() = %+v, %v, want the import of AGENTS.md", p.Ops, err)
	}
	writeFile(t, at(project, "CLAUDE.md"), "intro\n@AGENTS.md\n")
	if p, err = planProject(t, &fakeGit{}, examplePack, project, home); err != nil || importOp(p) {
		t.Errorf("PlanProject() = %+v, %v, want no import beside a CLAUDE.md that imports AGENTS.md", p.Ops, err)
	}
}

func TestPlanProjectComparesTheExistingTargets(t *testing.T) {
	project, home, _ := projectDirs(t)
	writeFile(t, at(project, ".claude/rules/web.md"), "# web\n")
	writeFile(t, at(project, ".agents/rules/web.md"), "# mine\n")
	writeFile(t, at(project, ".claude/settings.local.json"), `{"model":"opus","theme":"dark"}`)
	p, err := planProject(t, &fakeGit{}, examplePack, project, home)
	if err != nil {
		t.Fatal(err)
	}
	actions := map[string]plan.Action{}
	for _, op := range p.Ops {
		actions[op.Target] = op.Action
	}
	if actions[at(project, ".claude/rules/web.md")] != plan.Unchanged || actions[at(project, ".agents/rules/web.md")] != plan.Conflict ||
		actions[at(project, ".claude/settings.local.json")] != plan.Merge {
		t.Errorf("actions = %v, want unchanged, conflict and merge", actions)
	}
}

func TestPlanProjectRefusesALinkOnTheWay(t *testing.T) {
	project, home, _ := projectDirs(t)
	makeLink(t, t.TempDir(), at(project, ".claude"))
	_, err := planProject(t, &fakeGit{}, examplePack, project, home)
	if _, ok := errors.AsType[*app.NotRegularError](err); !ok {
		t.Errorf("PlanProject() error = %v, want the link refused", err)
	}
}

// git runs git in dir for a test, with a fixed identity.
func git(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.com", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.com",
		"GIT_CONFIG_GLOBAL="+filepath.Join(dir, ".no-global"), "GIT_CONFIG_NOSYSTEM=1")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func TestOSGitInARepository(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not on PATH")
	}
	t.Setenv("GIT_CONFIG_GLOBAL", filepath.Join(t.TempDir(), "none"))
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	repo := t.TempDir()
	git(t, repo, "init", "-q")
	writeFile(t, at(repo, "site/.claude/rules/web.md"), "team\n")
	writeFile(t, at(repo, "site/a [x].md"), "team\n")
	writeFile(t, at(repo, "site/.claude/rules/other.md"), "untracked\n")
	git(t, repo, "add", "site/.claude/rules/web.md", "site/a [x].md")
	git(t, repo, "commit", "-q", "-m", "init")
	project := at(repo, "site")
	g := app.OSGit{}
	r, err := g.Repo(context.Background(), project)
	if err != nil || r == nil {
		t.Fatalf("Repo() = %v, %v", r, err)
	}
	got, err := os.Stat(r.Exclude)
	want, err2 := os.Stat(at(repo, ".git/info/exclude"))
	if r.Prefix != "site/" || !filepath.IsAbs(r.Exclude) || err != nil || err2 != nil || !os.SameFile(got, want) {
		t.Errorf("Repo() = %+v (%v, %v), want the prefix site/ and the exclude file of the repository", r, err, err2)
	}
	tracked, err := g.Tracked(context.Background(), project, []string{".claude/rules/web.md", ".claude/rules/other.md", "a [x].md", "a ?x].md", "missing.md"})
	if err != nil {
		t.Fatal(err)
	}
	if want := map[string]bool{".claude/rules/web.md": true, "a [x].md": true}; !reflect.DeepEqual(tracked, want) {
		t.Errorf("Tracked() = %v, want %v, pathspecs read literally", tracked, want)
	}
	if entries, err := os.ReadDir(at(repo, ".git")); err != nil || slices.ContainsFunc(entries, func(e os.DirEntry) bool { return e.Name() == "index.lock" }) {
		t.Errorf("the repository holds %v, %v", entries, err)
	}
}

func TestOSGitOutsideARepository(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not on PATH")
	}
	t.Setenv("GIT_CEILING_DIRECTORIES", filepath.Dir(t.TempDir()))
	r, err := app.OSGit{}.Repo(context.Background(), t.TempDir())
	if err != nil || r != nil {
		t.Errorf("Repo() = %+v, %v, want no repository", r, err)
	}
}

func TestOSGitWithoutGit(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	dir := t.TempDir()
	if r, err := (app.OSGit{}).Repo(context.Background(), dir); err != nil || r != nil {
		t.Errorf("Repo() without git or .git = %+v, %v, want no repository", r, err)
	}
	if err := os.Mkdir(at(dir, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	_, err := app.OSGit{}.Repo(context.Background(), at(dir, "sub"))
	if m, ok := errors.AsType[*app.GitMissingError](err); !ok || m.Git != at(dir, ".git") {
		t.Errorf("Repo() error = %v, want git missing in a repository", err)
	}
}
