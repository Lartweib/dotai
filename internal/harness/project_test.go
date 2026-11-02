package harness_test

import (
	"reflect"
	"slices"
	"testing"

	"github.com/lartweib/dotai/internal/harness"
)

// TestProjectTargets checks the local target of each kind per harness
// against the table of decision 0041.
func TestProjectTargets(t *testing.T) {
	tests := []struct {
		harness, source string
		target          string
		kind            harness.ProjectKind
		item            string
		byKey           bool
	}{
		{"claudecode", ".claude/rules/web.md", ".claude/rules/web.md", harness.ProjectRules, "rule web", false},
		{"claudecode", ".claude/agents/webdev.md", ".claude/agents/webdev.md", harness.ProjectAgents, "agent webdev", false},
		{"claudecode", ".claude/skills/lint/SKILL.md", ".claude/skills/lint/SKILL.md", harness.ProjectSkills, "skill lint", false},
		{"claudecode", ".claude/skills/lint/ref/a.md", ".claude/skills/lint/ref/a.md", harness.ProjectSkills, "skill lint", false},
		{"claudecode", ".claude/commands/deploy.md", ".claude/commands/deploy.md", harness.ProjectCommands, "command deploy", false},
		{"claudecode", ".claude/settings.json", ".claude/settings.local.json", harness.ProjectSettings, "", true},
		{"antigravity-cli", ".agents/rules/web.md", ".agents/rules/web.md", harness.ProjectRules, "rule web", false},
		{"antigravity-cli", ".agents/agents/webdev.md", ".agents/agents/webdev.md", harness.ProjectAgents, "agent webdev", false},
		{"antigravity-cli", ".agents/skills/deploy/SKILL.md", ".agents/skills/deploy/SKILL.md", harness.ProjectSkills, "skill deploy", false},
		{"antigravity-cli", ".agents/hooks.json", ".agents/hooks.json", harness.ProjectHooks, "", true},
		{"antigravity-cli", ".agents/mcp_config.json", ".agents/mcp_config.json", harness.ProjectMCP, "", true},
		{"opencode", ".opencode/memories/web.md", ".opencode/memories/web.md", harness.ProjectRules, "rule web", false},
		{"opencode", ".opencode/agents/webdev.md", ".opencode/agents/webdev.md", harness.ProjectAgents, "agent webdev", false},
		{"opencode", ".opencode/skills/lint/SKILL.md", ".opencode/skills/lint/SKILL.md", harness.ProjectSkills, "skill lint", false},
		{"opencode", ".opencode/commands/deploy.md", ".opencode/commands/deploy.md", harness.ProjectCommands, "command deploy", false},
		{"opencode", ".opencode/plugins/rulesync-hooks.js", ".opencode/plugins/rulesync-hooks.js", harness.ProjectHooks, "", false},
		{"opencode", "opencode.jsonc", ".opencode/opencode.json", harness.ProjectSettings, "", true},
	}
	for _, tt := range tests {
		t.Run(tt.source, func(t *testing.T) {
			got, ok := adapter(t, tt.harness).Project.Locate(tt.source)
			if !ok {
				t.Fatalf("Locate(%q) found nothing", tt.source)
			}
			if got.Target != tt.target || got.Kind != tt.kind || got.Item != tt.item || got.ByKey != tt.byKey {
				t.Errorf("Locate(%q) = %+v, want %s, %s, %q, by key %v", tt.source, got, tt.target, tt.kind, tt.item, tt.byKey)
			}
		})
	}
}

// TestProjectNeverTargets checks that the files decision 0041 leaves to the
// user or the team are never a target of any harness, and that a file of
// another harness is not this one's.
func TestProjectNeverTargets(t *testing.T) {
	for _, a := range harness.All() {
		for _, source := range []string{"AGENTS.md", "CLAUDE.md", "CLAUDE.local.md", ".mcp.json", "opencode.json", ".claude/settings.local.json", "GEMINI.md"} {
			if got, ok := a.Project.Locate(source); ok {
				t.Errorf("%s: Locate(%q) = %+v, want no target", a.ID, source, got)
			}
		}
	}
	if _, ok := adapter(t, "claudecode").Project.Locate(".agents/rules/web.md"); ok {
		t.Error("Claude Code locates a file of agy")
	}
}

func TestProjectKeysOfSettingsFiles(t *testing.T) {
	tests := []struct {
		harness, source string
		want            []harness.Members
	}{
		{"claudecode", ".claude/settings.json", []harness.Members{{Path: []string{"env"}}, {Path: []string{"hooks"}, Item: harness.ItemHook}, {Path: []string{"permissions"}}}},
		{"antigravity-cli", ".agents/hooks.json", []harness.Members{{Path: []string{"rulesync"}, Item: harness.ItemHook}}},
		{"antigravity-cli", ".agents/mcp_config.json", []harness.Members{{Path: []string{"mcpServers"}, Item: harness.ItemServer}}},
		{"opencode", "opencode.jsonc", []harness.Members{{Path: []string{"mcp", "servers"}, Item: harness.ItemServer}}},
	}
	for _, tt := range tests {
		got, _ := adapter(t, tt.harness).Project.Locate(tt.source)
		if !reflect.DeepEqual(got.Each, tt.want) {
			t.Errorf("%s %s: Each = %+v, want %+v", tt.harness, tt.source, got.Each, tt.want)
		}
	}
}

// TestProjectLayoutSpecials checks what is not a staged file: Claude Code's
// import of AGENTS.md and its MCP servers in its home file, agy's missing
// settings slot, OpenCode reading the skills of the other harnesses, and
// which same-name item wins across scopes (decision 0023).
func TestProjectLayoutSpecials(t *testing.T) {
	cc, agy, oc := adapter(t, "claudecode"), adapter(t, "antigravity-cli"), adapter(t, "opencode")
	if got := cc.Project.ImportFile("minimal-pack"); got != ".claude/rules/minimal-pack.md" {
		t.Errorf("ImportFile() = %q", got)
	}
	if cc.Project.ImportLine != "@../../AGENTS.md" {
		t.Errorf("ImportLine = %q", cc.Project.ImportLine)
	}
	if agy.Project.ImportFile("p") != "" || oc.Project.ImportFile("p") != "" {
		t.Error("agy or OpenCode imports AGENTS.md, which they read themselves")
	}
	m := cc.Project.MCP
	if m == nil || m.From != ".mcp.json" || m.Servers != "mcpServers" || m.Projects != "projects" ||
		m.Isolated != ".dotai/isolated/claude/.claude.json" || m.Real != ".claude.json" {
		t.Errorf("MCP = %+v, want .mcp.json into the projects key of .claude.json", m)
	}
	if agy.Project.MCP != nil || oc.Project.MCP != nil {
		t.Error("only Claude Code keeps its project MCP servers outside the project")
	}
	if !slices.Contains(agy.Project.NoSlot, harness.ProjectSettings) || len(cc.Project.NoSlot)+len(oc.Project.NoSlot) != 0 {
		t.Errorf("NoSlot = %v, %v, %v, want settings for agy only", cc.Project.NoSlot, agy.Project.NoSlot, oc.Project.NoSlot)
	}
	if !reflect.DeepEqual(oc.Project.AlsoReads, []string{".claude/skills/", ".agents/skills/"}) || len(cc.Project.AlsoReads)+len(agy.Project.AlsoReads) != 0 {
		t.Errorf("AlsoReads = %v", oc.Project.AlsoReads)
	}
	wins := map[string]map[harness.ProjectKind]bool{}
	for _, a := range []harness.Adapter{cc, agy, oc} {
		wins[a.ID] = map[harness.ProjectKind]bool{}
		for _, u := range a.Project.UserItems {
			if len(u.Dirs) == 0 {
				t.Errorf("%s %s: no user directory", a.ID, u.Kind)
			}
			wins[a.ID][u.Kind] = u.ProjectWins
		}
	}
	want := map[string]map[harness.ProjectKind]bool{
		"claudecode":      {harness.ProjectSkills: false, harness.ProjectAgents: true},
		"antigravity-cli": {harness.ProjectSkills: true, harness.ProjectAgents: true},
		"opencode":        {harness.ProjectSkills: true, harness.ProjectAgents: true},
	}
	if !reflect.DeepEqual(wins, want) {
		t.Errorf("project wins = %v, want %v", wins, want)
	}
}

func TestAllClonesTheProjectLayout(t *testing.T) {
	a := harness.All()
	a[0].Project.Paths[0].To = "changed"
	a[0].Project.Paths[4].Each[0].Path[0] = "changed"
	a[0].Project.UserItems[0].Dirs[0] = "changed"
	a[0].Project.MCP.From = "changed"
	b := harness.All()
	if b[0].Project.Paths[0].To == "changed" || b[0].Project.Paths[4].Each[0].Path[0] == "changed" ||
		b[0].Project.UserItems[0].Dirs[0] == "changed" || b[0].Project.MCP.From == "changed" {
		t.Error("All() shares the project layout between calls")
	}
}
