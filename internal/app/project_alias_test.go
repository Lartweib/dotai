package app_test

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/lartweib/dotai/internal/app"
	"github.com/lartweib/dotai/internal/plan"
)

// linkedParent is dir reached through a link to its parent directory: a
// symbolic link, or on Windows a junction.
func linkedParent(t *testing.T, dir string) string {
	t.Helper()
	link := filepath.Join(t.TempDir(), "alias")
	makeLink(t, filepath.Dir(dir), link)
	return filepath.Join(link, filepath.Base(dir))
}

// TestApplyAndRestoreThroughAnotherSpellingOfTheProject applies a profile
// to a real git repository from another spelling of its path, as a user
// whose repository sits under a link or an 8.3 short name runs dotai, and
// restores it from that spelling again. Git gives the top of the worktree
// resolved, so the project, every target in it and Claude Code's projects
// key are spelled that one way, the way Claude Code finds the repository;
// the restore finds the same record and leaves the project and the home
// as they were.
func TestApplyAndRestoreThroughAnotherSpellingOfTheProject(t *testing.T) {
	needGit(t)
	for name, alias := range projectAliases() {
		t.Run(name, func(t *testing.T) {
			_, home, _ := projectDirs(t)
			real := filepath.Join(t.TempDir(), "the parent", "the project")
			if err := os.MkdirAll(real, 0o755); err != nil {
				t.Fatal(err)
			}
			git(t, real, "init", "-q")
			writeFile(t, at(real, "README.md"), "# site\n")
			git(t, real, "add", ".")
			git(t, real, "commit", "-q", "-m", "init")
			cwd := alias(t, real)
			top := app.ResolvePath(real)
			if top == cwd {
				t.Fatalf("alias %s is spelled as the project itself", cwd)
			}
			p, err := planProject(t, app.OSGit{}, examplePack, cwd, home)
			if err != nil || len(p.Diagnostics) != 0 {
				t.Fatalf("PlanProject() = %+v, %v", p.Diagnostics, err)
			}
			if p.Project != top {
				t.Errorf("project = %s, want %s", p.Project, top)
			}
			var key []string
			for _, op := range p.Ops {
				if op.Kind == "mcp" && op.Harness == "claudecode" {
					key = op.Keys[0].Path
				} else if !strings.HasPrefix(op.Target, top+string(filepath.Separator)) {
					t.Errorf("target %s, want it under %s", op.Target, top)
				}
			}
			if want := []string{"projects", filepath.ToSlash(top), "mcpServers", "browser"}; !reflect.DeepEqual(key, want) {
				t.Errorf("Claude Code's MCP key = %q, want %q", key, want)
			}

			before, homeBefore := worktreeFiles(t, real), homeFiles(t, home)
			if _, err := applyWith(t, newClockFS(t), app.OSGit{}, profileTree, cwd, home, nil); err != nil {
				t.Fatalf("Apply() error = %v", err)
			}
			claudeJSON, _ := os.ReadFile(at(home, ".claude.json"))
			projects, _, _ := plan.GetKey(claudeJSON, []string{"projects"})
			if _, ok, _ := plan.GetKey(projects, []string{filepath.ToSlash(top), "mcpServers", "browser"}); !ok || strings.Count(string(projects), "mcpServers") != 1 {
				t.Errorf("~/.claude.json projects = %s, want the server under %s alone", projects, filepath.ToSlash(top))
			}
			if _, err := os.Stat(at(real, ".claude/rules/web.md")); err != nil {
				t.Errorf("the project's rule: %v", err)
			}
			if _, err := restoreProject(t, app.OSGit{}, cwd, home, nil); err != nil {
				t.Fatalf("RestoreProject() error = %v", err)
			}
			if got := worktreeFiles(t, real); !reflect.DeepEqual(got, before) {
				t.Errorf("project after the restore = %v, want %v", got, before)
			}
			if got := homeFiles(t, home); !reflect.DeepEqual(got, homeBefore) {
				t.Errorf("home after the restore = %v, want %v", got, homeBefore)
			}
		})
	}
}
