package harness_test

import (
	"testing"

	"github.com/lartweib/dotai/internal/harness"
)

func TestKindOfTellsTheKindOfAStagedUserFile(t *testing.T) {
	want := map[string]harness.ProjectKind{
		".claude/CLAUDE.md":                                      harness.ProjectRules,
		".claude/rules/tmux.md":                                  harness.ProjectRules,
		".claude/agents/reviewer.md":                             harness.ProjectAgents,
		".claude/skills/commit-message/SKILL.md":                 harness.ProjectSkills,
		".claude/commands/review.md":                             harness.ProjectCommands,
		".claude/settings.json":                                  harness.ProjectSettings,
		".claude/unknown.txt":                                    "",
		".gemini/GEMINI.md":                                      harness.ProjectRules,
		".gemini/config/rules/tmux.md":                           harness.ProjectRules,
		".gemini/config/agents/reviewer.md":                      harness.ProjectAgents,
		".gemini/antigravity-cli/skills/commit-message/SKILL.md": harness.ProjectSkills,
		".gemini/antigravity-cli/settings.json":                  harness.ProjectSettings,
		".config/opencode/AGENTS.md":                             harness.ProjectRules,
		".config/opencode/memories/tmux.md":                      harness.ProjectRules,
		".config/opencode/agents/reviewer.md":                    harness.ProjectAgents,
		".config/opencode/skills/commit-message/SKILL.md":        harness.ProjectSkills,
		".config/opencode/opencode.jsonc":                        harness.ProjectSettings,
		".config/opencode/commands/review.md":                    harness.ProjectCommands,
	}
	adapters, err := harness.Lookup([]string{"claudecode", "antigravity-cli", "opencode"})
	if err != nil {
		t.Fatal(err)
	}
	for staged, kind := range want {
		got := harness.ProjectKind("")
		for _, a := range adapters {
			if k := a.KindOf(staged); k != "" {
				got = k
			}
		}
		if got != kind {
			t.Errorf("KindOf(%q) = %q, want %q", staged, got, kind)
		}
	}
}

func TestStagedOfFindsTheStagedPathOfAnInstalledFile(t *testing.T) {
	adapters, err := harness.Lookup([]string{"claudecode", "antigravity-cli", "opencode"})
	if err != nil {
		t.Fatal(err)
	}
	byID := map[string]harness.Adapter{}
	for _, a := range adapters {
		byID[a.ID] = a
	}
	tests := []struct {
		id        string
		mode      harness.Mode
		installed string
		want      string
	}{
		{"claudecode", harness.Isolated, ".dotai/isolated/claude/agents/reviewer.md", ".claude/agents/reviewer.md"},
		{"claudecode", harness.Replace, ".claude/rules/tmux.md", ".claude/rules/tmux.md"},
		{"antigravity-cli", harness.Isolated, ".dotai/isolated/gemini/config/skills/x/SKILL.md", ".gemini/antigravity-cli/skills/x/SKILL.md"},
		{"opencode", harness.Isolated, ".dotai/isolated/opencode/config/opencode/memories/a.md", ".config/opencode/memories/a.md"},
		{"opencode", harness.Isolated, ".dotai/tools/hello.sh", ""},
	}
	for _, tt := range tests {
		if got := byID[tt.id].StagedOf(tt.mode, tt.installed); got != tt.want {
			t.Errorf("%s StagedOf(%s, %q) = %q, want %q", tt.id, tt.mode, tt.installed, got, tt.want)
		}
	}
}
