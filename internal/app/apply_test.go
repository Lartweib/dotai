package app_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/lartweib/dotai/internal/app"
	"github.com/lartweib/dotai/internal/pack"
	"github.com/lartweib/dotai/internal/plan"
	"github.com/lartweib/dotai/internal/rulesync"
	"github.com/lartweib/dotai/internal/state"
)

// treeRulesync is a rulesync that renders tree into the project scope.
type treeRulesync struct {
	t    *testing.T
	tree map[string]string
}

func (f *treeRulesync) fetch(_ context.Context, _, version string, _ app.Progress) (rulesync.Result, error) {
	return rulesync.Result{Version: version}, nil
}

func (f *treeRulesync) run(_ context.Context, _ rulesync.Result, targets []string, jobs ...rulesync.Job) error {
	for _, j := range jobs {
		if !j.Global {
			for name, data := range f.tree {
				if rendersFor(name, targets) {
					writeFile(f.t, filepath.Join(j.Dir, filepath.FromSlash(name)), data)
				}
			}
		}
	}
	return nil
}

// rendersFor reports whether rulesync renders the project file name for
// one of targets, as it renders a harness's files only for its target: a
// path under .opencode or opencode.json(c) for OpenCode, under .agents for
// agy, and any other for Claude Code.
func rendersFor(name string, targets []string) bool {
	switch {
	case strings.HasPrefix(name, ".opencode/") || strings.HasPrefix(name, "opencode.json"):
		return slices.Contains(targets, "opencode")
	case strings.HasPrefix(name, ".agents/"):
		return slices.Contains(targets, "antigravity-cli")
	}
	return slices.Contains(targets, "claudecode")
}

// chooseEveryHarness saves in home the install state of a replace install
// of the example pack whose --harness choice is every harness, so that an
// apply, which writes into each harness's own configuration as with no
// install, follows that choice and manages OpenCode, a preview harness,
// too (decision 0047).
func chooseEveryHarness(t *testing.T, home string) {
	t.Helper()
	s := state.New(state.Pack{Name: "minimal-pack", Version: "0.1.0"}, "standalone", app.ModeReplace, time.Date(2026, 10, 29, 10, 0, 0, 0, time.UTC), pack.Harnesses, "", nil)
	s.Selection = slices.Clone(pack.Harnesses)
	data, err := state.Encode(s)
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, at(home, ".dotai/state.json"), string(data))
}

// applyRepo is a project at the top of a git repository whose exclude
// file holds a line of the user's, and an empty home directory.
func applyRepo(t *testing.T) (project, home string, g *fakeGit) {
	t.Helper()
	project, home, _ = projectDirs(t)
	writeFile(t, at(project, ".git/info/exclude"), "# the user's own\n*.log\n")
	return project, home, &fakeGit{}
}

func applyWith(t *testing.T, d app.ExecuteFS, g app.GitPort, tree map[string]string, project, home string, consent app.ApplyConsent) (app.ApplyResult, error) {
	t.Helper()
	f := &treeRulesync{t: t, tree: tree}
	return app.ApplyWith(context.Background(), d, g, f.fetch, f.run, examplePack, project, home, app.ApplyOptions{Profiles: []string{"web"}}, consent)
}

// files is every file under dir with its content.
func files(t *testing.T, dir string) map[string]string {
	t.Helper()
	out := map[string]string{}
	for _, rel := range tree(t, dir) {
		data, err := os.ReadFile(at(dir, rel))
		if err != nil {
			t.Fatal(err)
		}
		out[rel] = string(data)
	}
	return out
}

// worktreeFiles is files of the git worktree dir, its exclude file
// included and the rest of .git left out: git's background maintenance
// adds and removes files there, such as objects/maintenance.lock, while
// the walk reads them.
func worktreeFiles(t *testing.T, dir string) map[string]string {
	t.Helper()
	gitDir := filepath.Join(dir, ".git")
	out := map[string]string{}
	err := filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		switch {
		case err != nil:
			return err
		case p == gitDir:
			return filepath.SkipDir
		case d.IsDir():
			return nil
		}
		data, err := os.ReadFile(p)
		rel, _ := filepath.Rel(dir, p)
		out[filepath.ToSlash(rel)] = string(data)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(gitDir, "info", "exclude"))
	if err != nil {
		t.Fatal(err)
	}
	out[".git/info/exclude"] = string(data)
	return out
}

func yes(app.ProjectPlan) (bool, error) { return true, nil }

func TestApplyWritesThePlanAndASecondApplyIsNotTheFirst(t *testing.T) {
	project, home, g := applyRepo(t)
	chooseEveryHarness(t, home)
	res, err := applyWith(t, newClockFS(t), g, profileTree, project, home, nil)
	if err != nil {
		t.Fatalf("Apply() error = %v", err)
	}
	if !res.FirstApply || res.Backup == nil {
		t.Errorf("Apply() = first %v, backup %v, want the first apply with a backup", res.FirstApply, res.Backup)
	}
	got := files(t, project)
	for _, rel := range []string{".claude/rules/web.md", ".claude/skills/lint/SKILL.md", ".agents/rules/web.md", ".agents/mcp_config.json", ".opencode/memories/web.md", ".opencode/opencode.json", ".claude/settings.local.json", ".dotai/state.json"} {
		if _, ok := got[rel]; !ok {
			t.Errorf("project lacks %s: %v", rel, slices.Sorted(func(yield func(string) bool) {
				for k := range got {
					if !yield(k) {
						return
					}
				}
			}))
		}
	}
	for _, rel := range []string{"CLAUDE.md", "AGENTS.md", ".mcp.json", ".claude/settings.json", "opencode.jsonc"} {
		if _, ok := got[rel]; ok {
			t.Errorf("project has %s, which an apply never writes", rel)
		}
	}
	exclude := got[".git/info/exclude"]
	if !strings.HasPrefix(exclude, "# the user's own\n*.log\n# >>> dotai "+project) || !strings.Contains(exclude, "\n/.dotai/\n") || !strings.Contains(exclude, "\n/.claude/rules/web.md\n") {
		t.Errorf("exclude = %q, want the user's lines, then dotai's block", exclude)
	}
	claudeJSON, err := os.ReadFile(at(home, ".claude.json"))
	if v, ok, _ := plan.GetKey(claudeJSON, []string{"projects", filepath.ToSlash(project), "mcpServers", "browser", "command"}); err != nil || !ok || string(v) != `"npx"` {
		t.Errorf("~/.claude.json = %s, %v, want the server under the project's entry", claudeJSON, err)
	}
	r := readJSON(t, res.Record, func(data []byte) (state.Project, error) { return state.DecodeProject(data, project) })
	if r.Pending != nil || len(r.Backups) != 1 || r.FirstApply.IsZero() || !slices.Contains(r.Exclude.Entries, "/.dotai/") {
		t.Errorf("record = %+v, want one backup, the first apply and the exclude entries", r)
	}
	if i := slices.IndexFunc(r.Files, func(f state.ProjectFile) bool { return f.Target == at(home, ".claude.json") }); i < 0 || len(r.Files[i].Keys) != 1 {
		t.Errorf("record files = %+v, want the server owned in ~/.claude.json", r.Files)
	}
	list := readJSON(t, at(home, ".dotai/projects.json"), state.DecodeProjects)
	if len(list.Projects) != 1 || list.Projects[0].Project != project || list.Projects[0].File != at(home, ".claude.json") {
		t.Errorf("projects list = %+v, want the project's server", list)
	}

	again, err := applyWith(t, newClockFS(t), g, profileTree, project, home, nil)
	if err != nil {
		t.Fatalf("second Apply() error = %v", err)
	}
	if again.FirstApply || again.Backup != nil {
		t.Errorf("second Apply() = first %v, backup %v, want not the first, and nothing written", again.FirstApply, again.Backup)
	}
	for _, op := range again.Ops {
		if op.Action != plan.Unchanged {
			t.Errorf("second Apply() op %+v, want everything unchanged", op)
		}
	}
	if !reflect.DeepEqual(files(t, project), got) {
		t.Error("the second apply changed the project")
	}
}

func TestApplyKeepsForeignContent(t *testing.T) {
	project, home, g := applyRepo(t)
	settings := "{\n  // mine\n  \"theme\": \"dark\",\n  \"permissions\": {\"allow\": [\"Bash(ls)\"]}\n}\n"
	writeFile(t, at(project, ".claude/settings.local.json"), settings)
	writeFile(t, at(home, ".claude.json"), `{"numStartups": 3, "projects": {"/other": {"mcpServers": {"x": {}}}}}`)
	before := files(t, project)
	if _, err := applyWith(t, newClockFS(t), g, profileTree, project, home, nil); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(at(project, ".claude/settings.local.json"))
	if v, _, _ := plan.GetKey(data, []string{"permissions", "allow"}); !strings.Contains(string(data), "// mine") || !strings.Contains(string(data), `"theme": "dark"`) || string(v) != `["Bash(ls)"]` {
		t.Errorf("settings.local.json = %s, want the user's keys and comments kept", data)
	}
	home1, _ := os.ReadFile(at(home, ".claude.json"))
	if v, ok, _ := plan.GetKey(home1, []string{"numStartups"}); !ok || string(v) != "3" {
		t.Errorf("~/.claude.json = %s, want Claude Code's own keys kept", home1)
	}
	if v, ok, _ := plan.GetKey(home1, []string{"projects", "/other", "mcpServers", "x"}); !ok || string(v) != "{}" {
		t.Errorf("~/.claude.json = %s, want another project's entry kept", home1)
	}
	if strings.Contains(files(t, project)[".git/info/exclude"], "/.claude/settings.local.json") {
		t.Error("exclude lists settings.local.json, which was the user's")
	}
	_ = before
}

func TestApplyNeedsConsentForAConflict(t *testing.T) {
	project, home, g := applyRepo(t)
	writeFile(t, at(project, ".claude/rules/web.md"), "# the user's\n")
	before, homeBefore := files(t, project), files(t, home)
	_, err := applyWith(t, newClockFS(t), g, profileTree, project, home, nil)
	var ce *app.ApplyConsentError
	if !errors.As(err, &ce) || ce.Files != 1 || !strings.Contains(err.Error(), "--yes") {
		t.Fatalf("Apply() error = %v, want consent required for 1 file", err)
	}
	if !reflect.DeepEqual(files(t, project), before) || !reflect.DeepEqual(files(t, home), homeBefore) {
		t.Error("a refused apply wrote something")
	}
	res, err := applyWith(t, newClockFS(t), g, profileTree, project, home, yes)
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(at(project, ".claude/rules/web.md")); string(got) != "# web\n" {
		t.Errorf("web.md = %q, want the profile's with consent", got)
	}
	if got := files(t, res.Backup.Path)["files/0"]; got != "# the user's\n" {
		t.Errorf("backup = %v, want a copy of the user's file", files(t, res.Backup.Path))
	}
}

func TestApplyRefusesAForeignMCPServer(t *testing.T) {
	project, home, g := applyRepo(t)
	writeFile(t, at(home, ".claude.json"), `{"projects":{"`+filepath.ToSlash(project)+`":{"mcpServers":{"browser":{"command":"mine"}}}}}`)
	before := files(t, project)
	_, err := applyWith(t, newClockFS(t), g, profileTree, project, home, yes)
	var re *app.ApplyRefusedError
	if !errors.As(err, &re) || re.Plan.Refusals[0].Code != plan.CodeMCPServerForeign {
		t.Fatalf("Apply() error = %v, want a refusal of the foreign server", err)
	}
	if !reflect.DeepEqual(files(t, project), before) {
		t.Error("a refused apply wrote something")
	}
}

func TestApplyRemovesWhatADroppedProfileGave(t *testing.T) {
	project, home, g := applyRepo(t)
	if _, err := applyWith(t, newClockFS(t), g, profileTree, project, home, nil); err != nil {
		t.Fatal(err)
	}
	smaller := map[string]string{".claude/rules/web.md": "# web\n", ".claude/settings.json": `{"model":"sonnet"}`}
	res, err := applyWith(t, newClockFS(t), g, smaller, project, home, nil)
	if err != nil {
		t.Fatal(err)
	}
	got := files(t, project)
	for _, rel := range []string{".claude/skills/lint/SKILL.md", ".agents/rules/web.md", ".opencode/memories/web.md", ".agents/mcp_config.json"} {
		if _, ok := got[rel]; ok {
			t.Errorf("%s is still there after its profile was dropped (ops %+v)", rel, res.Ops)
		}
	}
	if _, err := os.Lstat(at(project, ".agents")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf(".agents = %v, want the directory dotai created removed", err)
	}
	claudeJSON, _ := os.ReadFile(at(home, ".claude.json"))
	if _, ok, _ := plan.GetKey(claudeJSON, []string{"projects", filepath.ToSlash(project)}); ok {
		t.Errorf("~/.claude.json = %s, want the project's entry dotai created removed", claudeJSON)
	}
	if strings.Contains(got[".git/info/exclude"], "/.agents/rules/web.md") || !strings.Contains(got[".git/info/exclude"], "/.claude/rules/web.md") {
		t.Errorf("exclude = %q, want the dropped entries removed and the kept ones kept", got[".git/info/exclude"])
	}
	if _, err := os.Stat(at(home, ".dotai/projects.json")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("projects.json = %v, want it removed with no key left", err)
	}
}

func TestApplyOutsideARepositoryKeepsTheExcludeEntriesPending(t *testing.T) {
	project, home, _ := projectDirs(t)
	g := &fakeGit{outside: true}
	res, err := applyWith(t, newClockFS(t), g, profileTree, project, home, nil)
	if err != nil {
		t.Fatal(err)
	}
	r := readJSON(t, res.Record, func(data []byte) (state.Project, error) { return state.DecodeProject(data, project) })
	if !r.Exclude.Pending || r.Exclude.File != "" || !slices.Contains(r.Exclude.Entries, "/.claude/rules/web.md") {
		t.Errorf("record exclude = %+v, want the entries pending", r.Exclude)
	}
	// Once it is a repository, the next apply writes them.
	writeFile(t, at(project, ".git/info/exclude"), "")
	if _, err := applyWith(t, newClockFS(t), &fakeGit{}, profileTree, project, home, nil); err != nil {
		t.Fatal(err)
	}
	if got := files(t, project)[".git/info/exclude"]; !strings.Contains(got, "/.claude/rules/web.md") {
		t.Errorf("exclude = %q, want the pending entries written", got)
	}
}

// failAtFS fails the write of one target, after the apply wrote others.
type failAtFS struct {
	*clockFS
	fail string
}

func (f *failAtFS) WriteIn(root, name string, data []byte, mode os.FileMode) error {
	if filepath.Join(root, filepath.FromSlash(name)) == f.fail {
		return errDisk
	}
	return f.clockFS.WriteIn(root, name, data, mode)
}

func TestApplyRollsBackEverythingOnAFailure(t *testing.T) {
	project, home, g := applyRepo(t)
	chooseEveryHarness(t, home)
	writeFile(t, at(project, ".claude/settings.local.json"), `{"theme":"dark"}`)
	writeFile(t, at(home, ".claude.json"), `{"numStartups":3}`)
	before, homeBefore := files(t, project), files(t, home)
	_, err := applyWith(t, &failAtFS{clockFS: newClockFS(t), fail: at(project, ".opencode/opencode.json")}, g, profileTree, project, home, nil)
	var ae *app.ApplyError
	if !errors.As(err, &ae) || ae.Rollback != nil {
		t.Fatalf("Apply() error = %v, want an apply rolled back", err)
	}
	if got := files(t, project); !reflect.DeepEqual(got, before) {
		t.Errorf("project = %v, want it as before %v", got, before)
	}
	got := files(t, home)
	delete(got, ".dotai/lock")
	for k := range got {
		if strings.HasPrefix(k, ".dotai/projects/") {
			delete(got, k) // the backup, which is kept
		}
	}
	if !reflect.DeepEqual(got, homeBefore) {
		t.Errorf("home = %v, want it as before %v", got, homeBefore)
	}
}

// loseKeysFS writes the home file back as it was right after dotai writes
// it, as a harness that rewrites it would, times times.
type loseKeysFS struct {
	*clockFS
	target string
	times  int
}

func (f *loseKeysFS) WriteIn(root, name string, data []byte, mode os.FileMode) error {
	if filepath.Join(root, filepath.FromSlash(name)) == f.target && f.times > 0 {
		f.times--
		old, _ := os.ReadFile(f.target)
		if err := f.clockFS.WriteIn(root, name, data, mode); err != nil {
			return err
		}
		return os.WriteFile(f.target, old, 0o600)
	}
	return f.clockFS.WriteIn(root, name, data, mode)
}

func TestApplyWritesTheMCPServersAgainWhenAHarnessLostThem(t *testing.T) {
	for _, tt := range []struct {
		times int
		lost  bool
	}{{1, false}, {2, true}} {
		project, home, g := applyRepo(t)
		writeFile(t, at(home, ".claude.json"), `{"numStartups":3}`)
		res, err := applyWith(t, &loseKeysFS{clockFS: newClockFS(t), target: at(home, ".claude.json"), times: tt.times}, g, profileTree, project, home, nil)
		if err != nil {
			t.Fatal(err)
		}
		lost := slices.ContainsFunc(res.Warnings, func(w plan.Warning) bool { return w.Code == app.CodeMCPKeysLost })
		data, _ := os.ReadFile(at(home, ".claude.json"))
		_, held, _ := plan.GetKey(data, []string{"projects", filepath.ToSlash(project), "mcpServers", "browser"})
		if lost != tt.lost || held == tt.lost {
			t.Errorf("lost %d times: warning %v, server held %v, want warning %v", tt.times, lost, held, tt.lost)
		}
	}
}

// failRecordFS fails the final record and every removal, so that the
// rollback fails too and the journal stays, as when an apply is killed.
type failRecordFS struct {
	*clockFS
	project string
	journal bool
}

func (f *failRecordFS) WriteIn(root, name string, data []byte, mode os.FileMode) error {
	if name == "record.json" {
		if f.journal {
			return errDisk
		}
		f.journal = true
	}
	return f.clockFS.WriteIn(root, name, data, mode)
}

func (*failRecordFS) RemoveIn(string, string) error { return errors.New("locked by another program") }

func TestApplyRollsBackAnApplyThatStoppedMidway(t *testing.T) {
	project, home, g := applyRepo(t)
	before := files(t, project)
	_, err := applyWith(t, &failRecordFS{clockFS: newClockFS(t), project: project}, g, profileTree, project, home, nil)
	var ae *app.ApplyError
	if !errors.As(err, &ae) || ae.Rollback == nil {
		t.Fatalf("Apply() error = %v, want a failed rollback", err)
	}
	if _, err := planProject(t, g, examplePack, project, home); err == nil || !strings.Contains(err.Error(), "stopped midway") {
		t.Errorf("dry run over the journal: error = %v, want it refused", err)
	}
	res, err := applyWith(t, newClockFS(t), g, profileTree, project, home, nil)
	if err != nil || !res.FirstApply {
		t.Fatalf("next Apply() = first %v, %v, want the journal rolled back and a first apply", res.FirstApply, err)
	}
	_ = before
}
