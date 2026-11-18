package app_test

import (
	"cmp"
	"context"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/lartweib/dotai/internal/app"
	"github.com/lartweib/dotai/internal/plan"
)

// TestPlanProjectWithRealRulesync plans a profile with a rule, a skill, a
// command, an MCP server, hooks and permissions into a git repository that
// tracks agy's hooks file, with the pinned rulesync. It needs the network
// and git, so it runs only with DOTAI_E2E=1.
func TestPlanProjectWithRealRulesync(t *testing.T) {
	if os.Getenv("DOTAI_E2E") != "1" {
		t.Skip("set DOTAI_E2E=1 to run rulesync")
	}
	needGit(t)
	cache, err := filepath.Abs(cmp.Or(os.Getenv("DOTAI_CACHE_DIR"), t.TempDir()))
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(t.TempDir(), "pack")
	if err := os.CopyFS(dir, os.DirFS(examplePack)); err != nil {
		t.Fatal(err)
	}
	web := filepath.Join(dir, "profiles", "web", ".rulesync")
	for name, data := range map[string]string{
		"skills/lint/SKILL.md": "---\nname: lint\ndescription: Lint the project.\n---\nRun the linter.\n",
		"commands/deploy.md":   "---\ndescription: Deploy.\n---\nDeploy it.\n",
		"mcp.jsonc":            `{"mcpServers": {"browser": {"type": "stdio", "command": "npx", "args": ["browser-mcp"]}}}`,
		"hooks.jsonc":          `{"version": 1, "hooks": {"sessionStart": [{"command": "echo hi"}]}}`,
		"permissions.jsonc":    `{"permission": {"bash": {"npm test": "allow"}}}`,
	} {
		writeFile(t, filepath.Join(web, filepath.FromSlash(name)), data)
	}
	project, home, _ := projectDirs(t)
	chooseEveryHarness(t, home) // OpenCode, a preview harness, too
	git(t, project, "init", "-q")
	writeFile(t, at(project, ".agents/hooks.json"), "{}\n")
	git(t, project, "add", ".agents/hooks.json")
	git(t, project, "commit", "-q", "-m", "team hooks")
	p, err := app.PlanProjectWithRulesync(context.Background(), app.OSGit{}, cache, dir, project, home, app.ApplyOptions{Profiles: []string{"web"}})
	if err != nil || len(p.Diagnostics) != 0 {
		t.Fatalf("PlanProject() = %+v, %v", p.Diagnostics, err)
	}
	targets := map[string]plan.Action{}
	for _, op := range p.Ops {
		targets[op.Target] = op.Action
	}
	for _, rel := range []string{
		".claude/rules/web.md", ".claude/skills/lint/SKILL.md", ".claude/commands/deploy.md", ".claude/settings.local.json",
		".agents/rules/web.md", ".agents/skills/lint/SKILL.md", ".agents/skills/deploy/SKILL.md", ".agents/mcp_config.json",
		".opencode/memories/web.md", ".opencode/skills/lint/SKILL.md", ".opencode/commands/deploy.md", ".opencode/plugins/rulesync-hooks-project.js", ".opencode/opencode.json",
		".dotai/state.json",
	} {
		if targets[at(project, rel)] != plan.Create {
			t.Errorf("%s = %q, want create; ops = %v", rel, targets[at(project, rel)], targets)
		}
	}
	if targets[at(home, ".claude.json")] != plan.Create {
		t.Errorf("~/.claude.json = %q, want create", targets[at(home, ".claude.json")])
	}
	for _, never := range []string{"CLAUDE.md", "AGENTS.md", ".mcp.json", ".claude/settings.json", "opencode.jsonc", ".agents/hooks.json"} {
		if _, ok := targets[at(project, never)]; ok {
			t.Errorf("%s is planned", never)
		}
	}
	want := []plan.Skipped{{Harness: "antigravity-cli", Kind: "hooks", Item: "hook SessionStart: echo hi", Target: at(project, ".agents/hooks.json"), Reason: plan.SkipTracked}}
	if !reflect.DeepEqual(p.Skipped, want) {
		t.Errorf("skipped = %+v, want %+v", p.Skipped, want)
	}
	if p.Exclude.Pending || !slices.Contains(p.Exclude.Entries, "/.claude/skills/lint/") || slices.Contains(p.Exclude.Entries, "/.agents/hooks.json") {
		t.Errorf("exclude = %+v", p.Exclude)
	}
	if got := tree(t, project); slices.ContainsFunc(got, func(p string) bool { return p != ".agents/hooks.json" && !strings.HasPrefix(p, ".git/") }) {
		t.Errorf("project = %v, want only what git holds", got)
	}
}
