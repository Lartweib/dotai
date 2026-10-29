package app_test

import (
	"cmp"
	"context"
	"os"
	"path/filepath"
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
	for name, want := range map[string]string{
		".claude/settings.json":                 "{\n  \"model\": \"sonnet\"\n}\n",
		".gemini/antigravity-cli/settings.json": "{\n  \"statusLine\": {\n    \"type\": \"command\",\n    \"command\": \"" + filepath.ToSlash(tools.User) + "/hello.sh\"\n  }\n}\n",
	} {
		if got, err := os.ReadFile(filepath.Join(out, "user", filepath.FromSlash(name))); err != nil || string(got) != want {
			t.Errorf("%s = %q, %v, want %q", name, got, err, want)
		}
	}
}
