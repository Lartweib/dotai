package app_test

import (
	"os"
	"strings"
	"testing"

	"github.com/lartweib/dotai/internal/app"
)

// TestTwoWorktreesShareTheirMCPEntryAndRestoreInEitherOrder applies the
// same profile in the main worktree of a real git repository and in a
// linked one, which share Claude Code's projects entry and the exclude
// file, and restores them in either order: ~/.claude.json, the exclude
// file and the list of projects end as they were before the first apply,
// the file removed when the first apply created it.
func TestTwoWorktreesShareTheirMCPEntryAndRestoreInEitherOrder(t *testing.T) {
	needGit(t)
	for _, tt := range []struct {
		name       string
		claudeJSON string // "" for none
		steps      []string
	}{
		{"main first, no .claude.json", "", []string{"apply main", "apply linked", "restore main", "restore linked"}},
		{"linked first, a .claude.json", `{"numStartups": 3}`, []string{"apply main", "apply linked", "restore linked", "restore main"}},
		{"main, both, main again", "", []string{"apply main", "apply linked", "apply main", "restore main", "restore linked"}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			root, home, _ := projectDirs(t)
			main, linked := at(root, "main"), at(root, "linked")
			if err := os.MkdirAll(main, 0o755); err != nil {
				t.Fatal(err)
			}
			git(t, main, "init", "-q")
			writeFile(t, at(main, "README.md"), "# site\n")
			git(t, main, "add", ".")
			git(t, main, "commit", "-q", "-m", "init")
			git(t, main, "worktree", "add", "-q", linked)
			if tt.claudeJSON != "" {
				writeFile(t, at(home, ".claude.json"), tt.claudeJSON)
			}
			homeBefore := homeFiles(t, home)
			exclude := at(main, ".git/info/exclude")
			excludeBefore, err := os.ReadFile(exclude)
			if err != nil {
				t.Fatal(err)
			}
			dirs := map[string]string{"main": main, "linked": linked}
			for _, step := range tt.steps {
				verb, which, _ := strings.Cut(step, " ")
				if verb == "apply" {
					if _, err := applyWith(t, newClockFS(t), app.OSGit{}, profileTree, dirs[which], home, nil); err != nil {
						t.Fatalf("%s: %v", step, err)
					}
					continue
				}
				if _, err := restoreProject(t, app.OSGit{}, dirs[which], home, nil); err != nil {
					t.Fatalf("%s: %v", step, err)
				}
			}
			if got := homeFiles(t, home); !mapsEqual(got, homeBefore) {
				t.Errorf("home = %v\nwant %v", got, homeBefore)
			}
			if got, _ := os.ReadFile(exclude); string(got) != string(excludeBefore) {
				t.Errorf("exclude = %q\nwant %q", got, excludeBefore)
			}
			if _, err := os.Stat(at(home, ".dotai/projects.json")); !os.IsNotExist(err) {
				t.Errorf("projects.json = %v, want it gone", err)
			}
		})
	}
}

func mapsEqual(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if w, ok := b[k]; !ok || w != v {
			return false
		}
	}
	return true
}
