package app_test

import (
	"cmp"
	"os"
	"path/filepath"
	"slices"
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
	if _, err := app.Stage(ctx, app.NewRulesync(cache, nil), examplePack, out, app.Selection{Profiles: []string{"web"}}, false); err != nil {
		t.Fatalf("Stage() error = %v", err)
	}
	got := tree(t, out)
	for _, want := range []string{
		"user/.claude/CLAUDE.md",
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
}
