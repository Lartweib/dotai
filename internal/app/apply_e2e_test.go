package app_test

import (
	"cmp"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/lartweib/dotai/internal/app"
	"github.com/lartweib/dotai/internal/plan"
)

// TestApplyAndRestoreProjectWithRealRulesync applies a profile with the
// pinned rulesync to a git repository that holds the user's own files,
// and checks the acceptance of roadmap row 10-3: git status stays as it
// was, foreign content is kept, a second apply is not the first and
// writes nothing, and restore --project leaves the repository, its
// exclude file and the home directory as before the first apply. It needs
// the network and git, so it runs only with DOTAI_E2E=1.
func TestApplyAndRestoreProjectWithRealRulesync(t *testing.T) {
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
		"mcp.jsonc":            `{"mcpServers": {"browser": {"type": "stdio", "command": "npx", "args": ["browser-mcp"]}}}`,
		"permissions.jsonc":    `{"permission": {"bash": {"npm test": "allow"}}}`,
	} {
		writeFile(t, filepath.Join(web, filepath.FromSlash(name)), data)
	}
	project, home, _ := projectDirs(t)
	git(t, project, "init", "-q")
	writeFile(t, at(project, "AGENTS.md"), "# team rules\n")
	git(t, project, "add", ".")
	git(t, project, "commit", "-q", "-m", "team files")
	// The user's own local settings, which the user excludes.
	writeFile(t, at(project, ".claude/settings.local.json"), "{\n  \"theme\": \"dark\"\n}\n")
	exclude := at(project, ".git/info/exclude")
	data, err := os.ReadFile(exclude)
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, exclude, string(data)+"/.claude/settings.local.json\n")
	writeFile(t, at(home, ".claude.json"), `{"numStartups": 3}`)
	status := func() string {
		t.Helper()
		cmd := exec.Command("git", "status", "--porcelain", "--untracked-files=all")
		cmd.Dir = project
		out, err := cmd.Output()
		if err != nil {
			t.Fatal(err)
		}
		return string(out)
	}
	before, homeBefore := files(t, project), homeFiles(t, home)
	if got := status(); got != "" {
		t.Fatalf("git status before = %q, want it clean", got)
	}
	opts := app.ApplyOptions{Profiles: []string{"web"}}
	res, err := app.ApplyWithRulesync(context.Background(), app.OSGit{}, cache, dir, project, home, opts)
	if err != nil || len(res.Diagnostics) != 0 || !res.FirstApply || res.Backup == nil {
		t.Fatalf("Apply() = first %v, backup %v, %+v, %v", res.FirstApply, res.Backup, res.Diagnostics, err)
	}
	if got := status(); got != "" {
		t.Errorf("git status after the apply = %q, want it clean", got)
	}
	settings, _ := os.ReadFile(at(project, ".claude/settings.local.json"))
	if !strings.Contains(string(settings), `"theme": "dark"`) || !strings.Contains(string(settings), "npm test") {
		t.Errorf("settings.local.json = %s, want the user's key and the profile's permission", settings)
	}
	claudeJSON, _ := os.ReadFile(at(home, ".claude.json"))
	if v, ok, _ := plan.GetKey(claudeJSON, []string{"numStartups"}); !ok || string(v) != "3" {
		t.Errorf("~/.claude.json = %s, want its own keys kept", claudeJSON)
	}
	if _, ok, _ := plan.GetKey(claudeJSON, []string{"projects", filepath.ToSlash(project), "mcpServers", "browser"}); !ok {
		t.Errorf("~/.claude.json = %s, want the profile's server for the project", claudeJSON)
	}
	if got, _ := os.ReadFile(at(project, "AGENTS.md")); string(got) != before["AGENTS.md"] {
		t.Errorf("AGENTS.md = %q, want it untouched", got)
	}
	again, err := app.ApplyWithRulesync(context.Background(), app.OSGit{}, cache, dir, project, home, opts)
	if err != nil || again.FirstApply || again.Backup != nil {
		t.Errorf("second Apply() = first %v, backup %v, %v, want not the first and nothing written", again.FirstApply, again.Backup, err)
	}
	if _, err := app.RestoreProjectWith(context.Background(), app.OSInstallFS{}, app.OSGit{}, project, home, nil); err != nil {
		t.Fatalf("RestoreProject() error = %v", err)
	}
	after := files(t, project)
	for k := range after {
		if strings.HasPrefix(k, ".git/") && k != ".git/info/exclude" {
			delete(after, k)
		}
	}
	for k := range before {
		if strings.HasPrefix(k, ".git/") && k != ".git/info/exclude" {
			delete(before, k)
		}
	}
	if !reflect.DeepEqual(after, before) {
		t.Errorf("project after the restore = %v, want %v", after, before)
	}
	if got := homeFiles(t, home); !reflect.DeepEqual(got, homeBefore) {
		t.Errorf("home after the restore = %v, want %v", got, homeBefore)
	}
	if got := status(); got != "" {
		t.Errorf("git status after the restore = %q, want it clean", got)
	}
}
