package app_test

import (
	"cmp"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/lartweib/dotai/internal/app"
)

// TestStageWithRealRulesync downloads the pinned rulesync, or takes it from
// DOTAI_CACHE_DIR, and stages the example pack with it. It needs the
// network, so it runs only with DOTAI_E2E=1.
func TestStageWithRealRulesync(t *testing.T) {
	if os.Getenv("DOTAI_E2E") != "1" {
		t.Skip("set DOTAI_E2E=1 to run rulesync")
	}
	cache, err := filepath.Abs(cmp.Or(os.Getenv("DOTAI_CACHE_DIR"), t.TempDir()))
	if err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(t.TempDir(), "staging")
	tools := app.ToolsIn(t.TempDir(), t.TempDir())
	if _, err := app.Stage(context.Background(), cache, examplePack, out, app.Selection{Profiles: []string{"web"}}, tools, false, nil); err != nil {
		t.Fatalf("Stage() error = %v", err)
	}
	got := tree(t, out)
	for _, want := range []string{
		"user/.claude/CLAUDE.md",
		"user/.dotai/tools/hello.sh",
		"user/.dotai/tools/hello.ps1",
		"user/.claude/agents/reviewer.md",
		"user/.claude/skills/commit-message/SKILL.md",
		"user/.gemini/GEMINI.md",
		"user/.config/opencode/AGENTS.md",
		"project/.claude/rules/web.md",
		"project/.opencode/memories/web.md",
	} {
		if !slices.Contains(got, want) {
			t.Errorf("staging has no %s; tree = %v", want, got)
		}
	}
	claude, err := os.ReadFile(filepath.Join(out, "user", ".claude", "CLAUDE.md"))
	if want := filepath.ToSlash(tools.User) + "/hello.sh"; err != nil || !strings.Contains(string(claude), want) {
		t.Errorf("CLAUDE.md = %q, %v, want it to name %s", claude, err, want)
	}
	// agy's statusLine runs the tool hello (decision 0037), unquoted.
	statusLine := `sh ` + filepath.ToSlash(tools.User) + `/hello.sh`
	if runtime.GOOS == "windows" {
		statusLine = `powershell.exe -NoProfile -ExecutionPolicy Bypass -File ` + tools.User + `\hello.ps1`
	}
	command, err := json.Marshal(statusLine)
	if err != nil {
		t.Fatal(err)
	}
	for name, want := range map[string]string{
		".claude/settings.json": "{\n  \"$schema\": \"https://json.schemastore.org/claude-code-settings.json\",\n" +
			"  \"permissions\": {\n    \"allow\": [\n      \"Bash(git diff *)\",\n      \"Bash(git status)\"\n    ],\n" +
			"    \"deny\": [\n      \"Bash(git push --force *)\",\n      \"Read(.env)\"\n    ]\n  },\n  \"model\": \"sonnet\"\n}\n",
		".gemini/antigravity-cli/settings.json": "{\n  \"permissions\": {\n    \"allow\": [\n      \"command(git diff)\",\n      \"command(git status)\"\n    ],\n" +
			"    \"deny\": [\n      \"command(git push --force)\",\n      \"read_file(.env)\"\n    ]\n  },\n" +
			"  \"statusLine\": {\n    \"type\": \"command\",\n    \"command\": " + string(command) + "\n  }\n}\n",
	} {
		if got, err := os.ReadFile(filepath.Join(out, "user", filepath.FromSlash(name))); err != nil || string(got) != want {
			t.Errorf("%s = %q, %v, want %q", name, got, err, want)
		}
	}
}

// TestStageMergesListFilesWithRealRulesync stages a copy of the example
// pack whose base and tmux variant both have hooks and permissions
// (decision 0040), and checks that rulesync renders the entries of both
// layers into each harness. It runs only with DOTAI_E2E=1.
func TestStageMergesListFilesWithRealRulesync(t *testing.T) {
	if os.Getenv("DOTAI_E2E") != "1" {
		t.Skip("set DOTAI_E2E=1 to run rulesync")
	}
	cache, err := filepath.Abs(cmp.Or(os.Getenv("DOTAI_CACHE_DIR"), t.TempDir()))
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(t.TempDir(), "pack")
	if err := os.CopyFS(dir, os.DirFS(examplePack)); err != nil {
		t.Fatal(err)
	}
	for name, data := range map[string]string{
		"base/.rulesync/hooks.jsonc":          `{"version": 1, "hooks": {"preToolUse": [{"matcher": "Bash", "command": "echo base-hook"}]}}`,
		"variants/tmux/.rulesync/hooks.jsonc": `{"version": 1, "hooks": {"preToolUse": [{"matcher": "Bash", "command": "echo base-hook"}, {"matcher": "Bash", "command": "echo tmux-hook"}]}}`,
	} {
		if err := os.WriteFile(filepath.Join(dir, filepath.FromSlash(name)), []byte(data), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	out := filepath.Join(t.TempDir(), "staging")
	tools := app.ToolsIn(t.TempDir(), t.TempDir())
	if _, err := app.Stage(context.Background(), cache, dir, out, app.Selection{Variant: "tmux"}, tools, false, nil); err != nil {
		t.Fatalf("Stage() error = %v", err)
	}
	for name, want := range map[string][]string{
		".rulesync/permissions.jsonc":                {`"git status": "allow"`, `"tmux *": "allow"`},
		".claude/settings.json":                      {`"Bash(git status)"`, `"Bash(tmux *)"`, `"Bash(git push --force *)"`, `"echo base-hook"`, `"echo tmux-hook"`},
		".gemini/antigravity-cli/settings.json":      {`"command(git status)"`, `"command(tmux)"`},
		".gemini/config/hooks.json":                  {`"echo base-hook"`, `"echo tmux-hook"`},
		".config/opencode/opencode.jsonc":            {`"resource": "git status"`, `"resource": "tmux *"`},
		".config/opencode/plugins/rulesync-hooks.js": {"echo base-hook", "echo tmux-hook"},
	} {
		got, err := os.ReadFile(filepath.Join(out, "user", filepath.FromSlash(name)))
		if err != nil {
			t.Errorf("staging has no user/%s: %v", name, err)
			continue
		}
		for _, w := range want {
			if !strings.Contains(string(got), w) {
				t.Errorf("user/%s has no %s:\n%s", name, w, got)
			}
		}
		if n := strings.Count(string(got), "echo base-hook"); n > 1 {
			t.Errorf("user/%s runs the base hook %d times, want once:\n%s", name, n, got)
		}
	}
}
