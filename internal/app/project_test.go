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
	"time"

	"github.com/lartweib/dotai/internal/app"
	"github.com/lartweib/dotai/internal/envelope"
	"github.com/lartweib/dotai/internal/pack"
	"github.com/lartweib/dotai/internal/plan"
	"github.com/lartweib/dotai/internal/rulesync"
	"github.com/lartweib/dotai/internal/state"
)

// profileTree is what a fake rulesync writes into the project scope: a
// profile with a rule, a skill, an MCP server and settings, as rulesync
// 43.9.2 renders it for every harness.
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

func (f projectRulesync) run(_ context.Context, _ rulesync.Result, targets []string, jobs ...rulesync.Job) error {
	for _, j := range jobs {
		if j.Global {
			continue
		}
		for name, data := range profileTree {
			if !rendersFor(name, targets) {
				continue
			}
			writeFile(f.t, filepath.Join(j.Dir, filepath.FromSlash(name)), data)
		}
	}
	return nil
}

// fakeGit is a repository at the top of the project whose index holds
// tracked, or no repository when outside is set; repos gives the
// repository of a directory instead, and fail the error git gives there.
type fakeGit struct {
	outside bool
	tracked []string
	asked   []string
	repos   map[string]*app.Repo
	fail    map[string]error
	files   []string // what the index holds
	// filesAsked and repoAsked count the calls of Files and Repo.
	filesAsked, repoAsked int
}

func (g *fakeGit) Files(context.Context, string) ([]string, error) {
	g.filesAsked++
	return g.files, nil
}

func (g *fakeGit) Repo(_ context.Context, dir string) (*app.Repo, error) {
	g.repoAsked++
	if err := g.fail[dir]; err != nil {
		return nil, err
	}
	if r, ok := g.repos[dir]; ok {
		return r, nil
	}
	if g.outside {
		return nil, nil
	}
	return &app.Repo{Top: dir, Main: dir, Exclude: filepath.Join(dir, ".git", "info", "exclude")}, nil
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
	chooseEveryHarness(t, home)
	g := &fakeGit{tracked: []string{".claude/skills/lint/SKILL.md"}}
	p, err := planProject(t, g, examplePack, project, home)
	if err != nil || len(p.Diagnostics) != 0 {
		t.Fatalf("PlanProject() = %+v, %v", p, err)
	}
	if p.Pack == nil || p.Pack.Name != "minimal-pack" || p.Variant != "standalone" || !reflect.DeepEqual(p.Profiles, []string{"web"}) ||
		p.Project != project || p.Marker != at(project, ".dotai/state.json") || !strings.HasPrefix(p.Record, at(home, ".dotai/projects")+string(filepath.Separator)) || !reflect.DeepEqual(p.Harnesses, []string{"claudecode", "antigravity-cli", "opencode"}) {
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
		"create claudecode " + at(project, ".claude/skills/hello/.claude-plugin/plugin.json"),
		"create claudecode " + at(project, ".claude/skills/hello/hooks/hooks.json"),
		"create claudecode " + at(project, ".claude/skills/hello/hooks/register.js"),
		"create  " + at(project, ".dotai/state.json"),
		"create  " + at(project, ".dotai/onboarding.json"),
		"create opencode " + at(project, ".opencode/memories/web.md"),
		"create opencode " + at(project, ".opencode/opencode.json"),
	}
	slices.Sort(want)
	slices.Sort(got)
	if !reflect.DeepEqual(got, want) {
		t.Errorf("ops =\n%q\nwant\n%q", got, want)
	}
	if want := []plan.Skipped{{Harness: "claudecode", Kind: "skills", Item: "skill lint", Target: at(project, ".claude/skills/lint/SKILL.md"), Reason: plan.SkipTracked}}; !reflect.DeepEqual(p.Skipped, want) {
		t.Errorf("skipped = %+v, want %+v", p.Skipped, want)
	}
	if !slices.Contains(g.asked, ".claude/skills/lint/SKILL.md") || slices.ContainsFunc(g.asked, func(p string) bool { return strings.Contains(p, ".claude.json") }) {
		t.Errorf("git was asked about %q, want the project targets only", g.asked)
	}
	wantExclude := app.ExcludePlan{File: at(project, ".git/info/exclude"), Entries: []string{
		"/.dotai/", "/.agents/mcp_config.json", "/.agents/rules/web.md", "/.claude/rules/web.md", "/.claude/settings.local.json",
		"/.claude/skills/hello/", "/.opencode/memories/web.md", "/.opencode/opencode.json",
	}}
	if !reflect.DeepEqual(p.Exclude, wantExclude) {
		t.Errorf("exclude = %+v, want %+v", p.Exclude, wantExclude)
	}
	if got := tree(t, project); len(got) != 0 {
		t.Errorf("project = %v, want it untouched", got)
	}
	if got := tree(t, home); !reflect.DeepEqual(got, []string{".dotai/state.json"}) {
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
	if !slices.ContainsFunc(p.Warnings, func(w plan.Warning) bool { return w.Code == plan.CodeExcludePending }) {
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
	if !slices.ContainsFunc(p.Ops, func(op plan.ProjectOp) bool { return op.Target == at(home, ".dotai/isolated/claude/.claude.json") }) {
		t.Errorf("ops = %+v, want the isolated .claude.json", p.Ops)
	}
}

func TestPlanProjectImportsAgentsMDOnlyWhenNoClaudeMDDoes(t *testing.T) {
	project, home, _ := projectDirs(t)
	writeFile(t, at(project, "AGENTS.md"), "# team\n")
	importOp := func(p app.ProjectPlan) bool {
		return slices.ContainsFunc(p.Ops, func(op plan.ProjectOp) bool { return op.Target == at(project, ".claude/rules/minimal-pack.md") })
	}
	p, err := planProject(t, &fakeGit{}, examplePack, project, home)
	if err != nil || !importOp(p) {
		t.Fatalf("PlanProject() = %+v, %v, want the import of AGENTS.md", p.Ops, err)
	}
	writeFile(t, at(project, "CLAUDE.md"), "intro\n@AGENTS.md\n")
	if p, err = planProject(t, &fakeGit{}, examplePack, project, home); err != nil || importOp(p) {
		t.Errorf("PlanProject() = %+v, %v, want no import beside a CLAUDE.md that imports AGENTS.md", p.Ops, err)
	}
	if err := os.Remove(at(project, "CLAUDE.md")); err != nil {
		t.Fatal(err)
	}
	writeFile(t, at(project, ".claude/CLAUDE.md"), "@../AGENTS.md\n")
	if p, err = planProject(t, &fakeGit{}, examplePack, project, home); err != nil || importOp(p) {
		t.Errorf("PlanProject() = %+v, %v, want no import beside a .claude/CLAUDE.md that imports AGENTS.md", p.Ops, err)
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

// needGit skips a test without git, and keeps the user's git
// configuration out of it.
func needGit(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not on PATH")
	}
	t.Setenv("GIT_CONFIG_GLOBAL", filepath.Join(t.TempDir(), "none"))
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
}

// samePlace fails unless a and b are the same existing file or directory,
// however each is spelled, as git may resolve a link or a short name.
func samePlace(t *testing.T, what, a, b string) {
	t.Helper()
	fa, err := os.Stat(a)
	fb, err2 := os.Stat(b)
	if err != nil || err2 != nil || !os.SameFile(fa, fb) {
		t.Errorf("%s = %s, want %s (%v, %v)", what, a, b, err, err2)
	}
}

func TestOSGitFromASubdirectory(t *testing.T) {
	needGit(t)
	repo := t.TempDir()
	git(t, repo, "init", "-q")
	writeFile(t, at(repo, "site/x.md"), "x\n")
	r, err := app.OSGit{}.Repo(context.Background(), at(repo, "site"))
	if err != nil || r == nil {
		t.Fatalf("Repo() = %v, %v", r, err)
	}
	// A repository without a commit is one too.
	samePlace(t, "top", r.Top, repo)
	samePlace(t, "main", r.Main, repo)
	samePlace(t, "exclude", r.Exclude, at(repo, ".git/info/exclude"))
	for _, p := range []string{r.Top, r.Main, r.Exclude} {
		if !filepath.IsAbs(p) || strings.Contains(p, "/") && filepath.Separator != '/' {
			t.Errorf("%s is not an absolute path with the OS separator", p)
		}
	}
}

func TestOSGitInALinkedWorktree(t *testing.T) {
	needGit(t)
	main := t.TempDir()
	git(t, main, "init", "-q")
	writeFile(t, at(main, "a.md"), "a\n")
	git(t, main, "add", "a.md")
	git(t, main, "commit", "-q", "-m", "init")
	wt := filepath.Join(t.TempDir(), "wt")
	git(t, main, "worktree", "add", "-q", wt)
	writeFile(t, at(wt, "sub/b.md"), "b\n")
	r, err := app.OSGit{}.Repo(context.Background(), at(wt, "sub"))
	if err != nil || r == nil {
		t.Fatalf("Repo() = %v, %v", r, err)
	}
	// The worktree is the project; Claude Code keys its MCP servers by the
	// main worktree, and the exclude file is the common one.
	samePlace(t, "top", r.Top, wt)
	samePlace(t, "main", r.Main, main)
	samePlace(t, "exclude", r.Exclude, at(main, ".git/info/exclude"))
}

func TestOSGitTracked(t *testing.T) {
	needGit(t)
	repo := t.TempDir()
	git(t, repo, "init", "-q")
	writeFile(t, at(repo, ".claude/rules/web.md"), "team\n")
	writeFile(t, at(repo, "a [x].md"), "team\n")
	writeFile(t, at(repo, ".claude/rules/other.md"), "untracked\n")
	writeFile(t, at(repo, ".agents/rules/Team.md"), "team\n")
	git(t, repo, "add", ".claude/rules/web.md", "a [x].md", ".agents/rules/Team.md")
	git(t, repo, "commit", "-q", "-m", "init")
	asked := []string{".claude/rules/web.md", ".claude/rules/other.md", "a [x].md", "a ?x].md", "missing.md", ".agents/rules/team.md"}
	tracked, err := app.OSGit{}.Tracked(context.Background(), repo, asked)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{".claude/rules/web.md": true, "a [x].md": true}
	// Where the file system ignores case, team.md is Team.md, which git
	// tracks, so it is never written.
	if app.FoldCase {
		want[".agents/rules/team.md"] = true
	}
	if !reflect.DeepEqual(tracked, want) {
		t.Errorf("Tracked() = %v, want %v, by the paths asked, pathspecs read literally", tracked, want)
	}
	if _, err := os.Stat(at(repo, ".git/index.lock")); err == nil {
		t.Error("git left an index lock")
	}
}

func TestOSGitTrackedDirectory(t *testing.T) {
	needGit(t)
	repo := t.TempDir()
	git(t, repo, "init", "-q")
	writeFile(t, at(repo, ".claude/skills/panel/README.md"), "team\n")
	writeFile(t, at(repo, ".claude/skills/other/x.md"), "untracked\n")
	git(t, repo, "add", ".claude/skills/panel/README.md")
	git(t, repo, "commit", "-q", "-m", "init")
	tracked, err := app.OSGit{}.Tracked(context.Background(), repo, []string{".claude/skills/panel/", ".claude/skills/other/", ".claude/skills/pan/", ".claude/skills/panel/hooks/hooks.json"})
	if err != nil {
		t.Fatal(err)
	}
	if want := map[string]bool{".claude/skills/panel/": true}; !reflect.DeepEqual(tracked, want) {
		t.Errorf("Tracked() = %v, want %v: a directory asked with a trailing slash is tracked when git tracks any file in it", tracked, want)
	}
}

func TestOSGitIgnoresTheVariablesThatPointGitElsewhere(t *testing.T) {
	needGit(t)
	repo := t.TempDir()
	git(t, repo, "init", "-q")
	writeFile(t, at(repo, "a.md"), "a\n")
	git(t, repo, "add", "a.md")
	git(t, repo, "commit", "-q", "-m", "init")
	other := t.TempDir()
	git(t, other, "init", "-q")
	t.Setenv("GIT_DIR", at(other, ".git"))
	t.Setenv("GIT_WORK_TREE", other)
	t.Setenv("GIT_INDEX_FILE", at(other, "index"))
	t.Setenv("GIT_CONFIG_COUNT", "1")
	t.Setenv("GIT_CONFIG_KEY_0", "core.bogus")
	t.Setenv("GIT_CONFIG_VALUE_0", "1")
	t.Setenv("LANG", "de_DE.UTF-8")
	t.Setenv("LC_ALL", "de_DE.UTF-8")
	r, err := app.OSGit{}.Repo(context.Background(), repo)
	if err != nil || r == nil {
		t.Fatalf("Repo() = %v, %v", r, err)
	}
	samePlace(t, "top", r.Top, repo)
	tracked, err := app.OSGit{}.Tracked(context.Background(), repo, []string{"a.md"})
	if err != nil || !tracked["a.md"] {
		t.Errorf("Tracked() = %v, %v, want a.md of this repository", tracked, err)
	}
	if r, err := (app.OSGit{}).Repo(context.Background(), t.TempDir()); err != nil || r != nil {
		t.Errorf("Repo() outside = %+v, %v, want no repository in any locale", r, err)
	}
}

func TestPlanProjectRunsFromTheTopOfTheWorktree(t *testing.T) {
	needGit(t)
	_, home, _ := projectDirs(t)
	main := t.TempDir()
	git(t, main, "init", "-q")
	writeFile(t, at(main, "README.md"), "r\n")
	git(t, main, "add", "README.md")
	git(t, main, "commit", "-q", "-m", "init")
	wt := filepath.Join(t.TempDir(), "wt")
	git(t, main, "worktree", "add", "-q", wt)
	for _, tt := range []struct{ name, cwd, top string }{
		{"subdirectory", at(main, "docs/deep"), main},
		{"linked worktree", at(wt, "src"), wt},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if err := os.MkdirAll(tt.cwd, 0o755); err != nil {
				t.Fatal(err)
			}
			p, err := planProject(t, app.OSGit{}, examplePack, tt.cwd, home)
			if err != nil {
				t.Fatal(err)
			}
			samePlace(t, "project", p.Project, tt.top)
			samePlace(t, "exclude", p.Exclude.File, at(main, ".git/info/exclude"))
			var key []string
			for _, op := range p.Ops {
				if op.Kind == "mcp" && op.Harness == "claudecode" {
					key = op.Keys[0].Path
				} else if rel, err := filepath.Rel(p.Project, op.Target); err != nil || !filepath.IsLocal(rel) {
					t.Errorf("%s is outside the project %s", op.Target, p.Project)
				}
			}
			if len(key) != 4 {
				t.Fatalf("ops = %+v, want Claude Code's MCP key", p.Ops)
			}
			samePlace(t, "MCP key", filepath.FromSlash(key[1]), main)
			if strings.Contains(key[1], `\`) {
				t.Errorf("MCP key %q, want forward slashes", key[1])
			}
		})
	}
}

func TestPlanProjectRefusesALinkToTheHomeDirectoryOrARoot(t *testing.T) {
	_, home, _ := projectDirs(t)
	toHome := filepath.Join(t.TempDir(), "home")
	makeLink(t, home, toHome)
	root := string(filepath.Separator)
	if v := filepath.VolumeName(home); v != "" {
		root = v + root
	}
	toRoot := filepath.Join(t.TempDir(), "root")
	makeLink(t, root, toRoot)
	for _, tt := range []struct{ dir, why string }{{toHome, "the home directory"}, {toRoot, "the root of"}} {
		_, err := planProject(t, &fakeGit{outside: true}, examplePack, tt.dir, home)
		if r, ok := errors.AsType[*app.ProjectRefusedError](err); !ok || !strings.HasPrefix(r.Why, tt.why) {
			t.Errorf("PlanProject(%s) error = %v, want a refusal for %s", tt.dir, err, tt.why)
		}
	}
}

func TestOSGitOutsideARepository(t *testing.T) {
	needGit(t)
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

// TestPlanProjectFollowsTheInstallsChoiceOfAPreviewHarness checks an
// apply's selection (decision 0047): by default it leaves out a detected
// preview harness, with a notice that says to choose it at install; it
// follows a saved choice that names it, with harness-preview; and a pack
// that requires it is refused with harness-refused, whose message points
// at dotai install --harness, since apply takes no --harness.
func TestPlanProjectFollowsTheInstallsChoiceOfAPreviewHarness(t *testing.T) {
	project, home, _ := projectDirs(t)
	p, err := planProject(t, &fakeGit{}, examplePack, project, home)
	if err != nil || len(p.Diagnostics) != 0 {
		t.Fatalf("PlanProject() = %+v, %v", p, err)
	}
	if slices.Contains(p.Harnesses, "opencode") || !slices.ContainsFunc(p.Notices, func(n string) bool {
		return strings.Contains(n, "the preview harness opencode is not managed") && strings.Contains(n, "dotai install --harness")
	}) {
		t.Errorf("harnesses %v, notices %q, want OpenCode left out with a notice naming dotai install --harness", p.Harnesses, p.Notices)
	}
	if slices.ContainsFunc(p.Warnings, func(w plan.Warning) bool { return w.Code == app.CodeHarnessPreview }) {
		t.Errorf("warnings %+v, want no harness-preview", p.Warnings)
	}

	chooseEveryHarness(t, home)
	p, err = planProject(t, &fakeGit{}, examplePack, project, home)
	if err != nil || len(p.Diagnostics) != 0 {
		t.Fatalf("PlanProject() = %+v, %v", p, err)
	}
	if !slices.Contains(p.Harnesses, "opencode") || !slices.ContainsFunc(p.Warnings, func(w plan.Warning) bool { return w.Code == app.CodeHarnessPreview && w.Harness == "opencode" }) {
		t.Errorf("harnesses %v, warnings %+v, want OpenCode with harness-preview", p.Harnesses, p.Warnings)
	}
	assertManagedPreview(t, p, "opencode")

	// An install from before decision 0047 saved no choice and managed
	// OpenCode: the apply keeps it managed.
	_, home3, _ := projectDirs(t)
	s := state.New(state.Pack{Name: "minimal-pack", Version: "0.1.0"}, "standalone", app.ModeReplace, time.Date(2026, 10, 29, 10, 0, 0, 0, time.UTC), pack.Harnesses, "", nil)
	data, err := state.Encode(s)
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, at(home3, ".dotai/state.json"), string(data))
	p, err = planProject(t, &fakeGit{}, examplePack, project, home3)
	if err != nil || len(p.Diagnostics) != 0 {
		t.Fatalf("PlanProject() = %+v, %v", p, err)
	}
	assertManagedPreview(t, p, "opencode")

	dir := copyPack(t)
	editPack(t, dir, "dotai.yaml", "# requires: [claudecode]", "requires: [opencode]")
	_, home2, _ := projectDirs(t)
	_, err = planProject(t, &fakeGit{}, dir, project, home2)
	refused, ok := errors.AsType[*app.HarnessRefusedError](err)
	if !ok || len(refused.Refusals) != 1 || refused.Refusals[0].Code != app.CodeHarnessPreviewRequired || !strings.Contains(err.Error(), "choose it with dotai install --harness, then apply again") {
		t.Fatalf("PlanProject() error = %v, want harness-preview-required pointing at dotai install --harness", err)
	}
	if code, _ := envelope.Classify(err); code != envelope.CodeHarnessRefused {
		t.Errorf("envelope code %q, want %q", code, envelope.CodeHarnessRefused)
	}
}

// TestPlanProjectListsEveryHarnessWithItsState checks that an apply plan
// lists every harness dotai knows with its state, as an install plan does
// (decision 0047): a detected preview harness the install's choice leaves
// out is not managed, with its status and reason; a compatible harness
// that is not installed is not managed and not detected; and a harness the
// pack is not compatible with comes after the compatible ones, never
// managed, with its detection. harnesses still names those planned.
func TestPlanProjectListsEveryHarnessWithItsState(t *testing.T) {
	type st struct {
		id                                      string
		compatible, required, detected, managed bool
		status                                  string
	}
	states := func(p app.ProjectPlan) []st {
		var got []st
		for _, hs := range p.HarnessStates {
			got = append(got, st{hs.ID, hs.Compatible, hs.Required, hs.Detected, hs.Managed, hs.Status})
			if hs.Config == "" || (hs.Status == "preview") != (hs.StatusReason != "") {
				t.Errorf("harness %+v, want its config and a reason for a preview only", hs)
			}
		}
		return got
	}
	f := projectRulesync{t}
	opts := app.ApplyOptions{Profiles: []string{"web"}}

	project, home, _ := projectDirs(t)
	h := app.NewHost(found(at(home, "bin"), "claude", "opencode"), nil, "")
	p, err := app.PlanProjectHost(context.Background(), app.OSInstallFS{}, h, &fakeGit{}, f.fetch, f.run, examplePack, project, home, opts)
	if err != nil || len(p.Diagnostics) != 0 {
		t.Fatalf("PlanProject() = %+v, %v", p, err)
	}
	want := []st{
		{"claudecode", true, false, true, true, "supported"},
		{"antigravity-cli", true, false, false, false, "supported"},
		{"opencode", true, false, true, false, "preview"},
	}
	if got := states(p); !reflect.DeepEqual(got, want) {
		t.Errorf("harness states =\n%+v\nwant\n%+v", got, want)
	}
	if !reflect.DeepEqual(p.Harnesses, []string{"claudecode"}) {
		t.Errorf("harnesses = %v, want only the planned one", p.Harnesses)
	}

	dir := copyPack(t)
	editPack(t, dir, "dotai.yaml", "harnesses: [claudecode, antigravity-cli, opencode]", "harnesses: [claudecode, opencode]")
	editPack(t, dir, "base/dotai/settings.yaml", "antigravity-cli:", "unused:")
	editPack(t, dir, "base/dotai/settings.yaml", "unused:\n  statusLine: { type: command, command: \"${DOTAI_TOOL:hello}\" }\n", "")
	project, home, _ = projectDirs(t)
	h = app.NewHost(found(at(home, "bin"), "claude", "agy"), nil, "")
	p, err = app.PlanProjectHost(context.Background(), app.OSInstallFS{}, h, &fakeGit{}, f.fetch, f.run, dir, project, home, opts)
	if err != nil || len(p.Diagnostics) != 0 {
		t.Fatalf("PlanProject() = %+v, %v", p, err)
	}
	want = []st{
		{"claudecode", true, false, true, true, "supported"},
		{"opencode", true, false, false, false, "preview"},
		{"antigravity-cli", false, false, true, false, "supported"},
	}
	if got := states(p); !reflect.DeepEqual(got, want) {
		t.Errorf("harness states =\n%+v\nwant\n%+v", got, want)
	}
}

// assertManagedPreview checks that the harness states of p list id as a
// compatible preview harness, with its reason, that the apply manages.
func assertManagedPreview(t *testing.T, p app.ProjectPlan, id string) {
	t.Helper()
	i := slices.IndexFunc(p.HarnessStates, func(hs app.HarnessState) bool { return hs.ID == id })
	if i < 0 {
		t.Fatalf("harness states %+v, want %s", p.HarnessStates, id)
	}
	if hs := p.HarnessStates[i]; !hs.Compatible || !hs.Managed || hs.Status != "preview" || hs.StatusReason == "" || !slices.Contains(p.Harnesses, id) {
		t.Errorf("harness state %+v, harnesses %v, want %s managed as a preview harness with its reason", hs, p.Harnesses, id)
	}
}
