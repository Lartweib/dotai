package plan_test

import (
	"errors"
	"maps"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/lartweib/dotai/internal/plan"
)

// projectTree is the project tree that dotai stage writes for a profile
// with a rule, a skill, a subagent, a command, an MCP server, hooks,
// permissions and settings, with rulesync 41.0.0 (checked on Windows,
// 2026-11-02). Each settings or configuration file holds its content.
var projectTree = map[string]string{
	".agents/agents/webdev.md":            "",
	".agents/hooks.json":                  `{"rulesync":{"SessionStart":[{"type":"command","command":"echo hi"}],"Stop":[{"type":"command","command":"echo bye"}]}}`,
	".agents/mcp_config.json":             `{"mcpServers":{"browser":{"command":"npx","args":["browser-mcp"]}}}`,
	".agents/rules/web.md":                "",
	".agents/skills/deploy/SKILL.md":      "",
	".agents/skills/lint/SKILL.md":        "",
	".claude/agents/webdev.md":            "",
	".claude/commands/deploy.md":          "",
	".claude/rules/web.md":                "",
	".claude/settings.json":               `{"$schema":"x","hooks":{"SessionStart":[{"hooks":[{"type":"command","command":"echo hi"}]}]},"permissions":{"allow":["Bash(npm test)"]},"model":"sonnet"}`,
	".claude/skills/lint/SKILL.md":        "",
	".dotai/tools/check.sh":               "",
	".mcp.json":                           `{"mcpServers":{"browser":{"type":"stdio","command":"npx","args":["browser-mcp"]}}}`,
	".opencode/agents/webdev.md":          "",
	".opencode/commands/deploy.md":        "",
	".opencode/memories/web.md":           "",
	".opencode/plugins/rulesync-hooks.js": "",
	".opencode/skills/lint/SKILL.md":      "",
	".rulesync/hooks.jsonc":               `{"version":1,"hooks":{"sessionStart":[{"command":"echo hi"}],"stop":[{"command":"echo bye"}]},"opencode":{"hooks":{"stop":[{"command":"echo oc"}]}}}`,
	".rulesync/permissions.jsonc":         `{"permission":{"bash":{"npm test":"allow"}}}`,
	".rulesync/rules/web.md":              "",
	"opencode.jsonc":                      `{"mcp":{"servers":{"browser":{"type":"local","command":["npx","browser-mcp"]}}},"permissions":[],"instructions":[".opencode/memories/web.md"],"model":"m"}`,
}

// projectInput is the input of a fresh plan of projectTree into /work/site,
// at the top of a git repository.
func projectInput(t *testing.T) plan.ProjectInput {
	t.Helper()
	root := filepath.Join(string(filepath.Separator)+"work", "site")
	home := filepath.Join(string(filepath.Separator)+"home", "u")
	if v := filepath.VolumeName(t.TempDir()); v != "" {
		root, home = v+root, v+home
	}
	in := plan.ProjectInput{
		Project: root, Home: home, Repo: true, Pack: "minimal-pack",
		Adapters: adapters(t, "claudecode", "antigravity-cli", "opencode"),
		Disk:     map[string]plan.Current{}, Content: map[string][]byte{}, Tracked: map[string]bool{}, Exists: map[string]bool{},
	}
	for _, p := range slices.Sorted(maps.Keys(projectTree)) {
		in.Files = append(in.Files, plan.File{Path: p, Hash: "new:" + p, Data: []byte(projectTree[p])})
	}
	return in
}

// opsByTarget is the ops of p by their target relative to base, with
// forward slashes, or by their absolute target outside it.
func opsByTarget(p plan.ProjectPlan, base string) map[string]plan.ProjectOp {
	out := map[string]plan.ProjectOp{}
	for _, op := range p.Ops {
		key := op.Target
		if rel, err := filepath.Rel(base, op.Target); err == nil && filepath.IsLocal(rel) {
			key = filepath.ToSlash(rel)
		}
		out[key] = op
	}
	return out
}

func TestBuildProjectPlacesEachKindInItsLocalTarget(t *testing.T) {
	in := projectInput(t)
	p, err := plan.BuildProject(in)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string][3]string{ // harness, kind, source
		".claude/agents/webdev.md":            {"claudecode", "agents", ".claude/agents/webdev.md"},
		".claude/commands/deploy.md":          {"claudecode", "commands", ".claude/commands/deploy.md"},
		".claude/rules/web.md":                {"claudecode", "rules", ".claude/rules/web.md"},
		".claude/settings.local.json":         {"claudecode", "settings", ".claude/settings.json"},
		".claude/skills/lint/SKILL.md":        {"claudecode", "skills", ".claude/skills/lint/SKILL.md"},
		".agents/agents/webdev.md":            {"antigravity-cli", "agents", ".agents/agents/webdev.md"},
		".agents/hooks.json":                  {"antigravity-cli", "hooks", ".agents/hooks.json"},
		".agents/mcp_config.json":             {"antigravity-cli", "mcp", ".agents/mcp_config.json"},
		".agents/rules/web.md":                {"antigravity-cli", "rules", ".agents/rules/web.md"},
		".agents/skills/deploy/SKILL.md":      {"antigravity-cli", "skills", ".agents/skills/deploy/SKILL.md"},
		".agents/skills/lint/SKILL.md":        {"antigravity-cli", "skills", ".agents/skills/lint/SKILL.md"},
		".opencode/agents/webdev.md":          {"opencode", "agents", ".opencode/agents/webdev.md"},
		".opencode/commands/deploy.md":        {"opencode", "commands", ".opencode/commands/deploy.md"},
		".opencode/memories/web.md":           {"opencode", "rules", ".opencode/memories/web.md"},
		".opencode/opencode.json":             {"opencode", "settings", "opencode.jsonc"},
		".opencode/plugins/rulesync-hooks.js": {"opencode", "hooks", ".opencode/plugins/rulesync-hooks.js"},
		".opencode/skills/lint/SKILL.md":      {"opencode", "skills", ".opencode/skills/lint/SKILL.md"},
		".dotai/tools/check.sh":               {"", "tools", ".dotai/tools/check.sh"},
		".dotai/state.json":                   {"", "record", ""},
	}
	got := opsByTarget(p, in.Project)
	claudeJSON := filepath.Join(in.Home, ".claude.json")
	if len(got) != len(want)+1 {
		t.Errorf("ops = %v, want %d", slices.Sorted(maps.Keys(got)), len(want)+1)
	}
	for target, w := range want {
		op, ok := got[target]
		if !ok {
			t.Errorf("no op for %s", target)
			continue
		}
		if op.Action != plan.Create || op.Harness != w[0] || op.Kind != w[1] || op.Source != w[2] {
			t.Errorf("%s = %+v, want create %v", target, op, w)
		}
	}
	for _, never := range []string{"CLAUDE.md", "AGENTS.md", "CLAUDE.local.md", ".mcp.json", ".claude/settings.json", "opencode.jsonc", "opencode.json"} {
		if _, ok := got[never]; ok {
			t.Errorf("%s is a target", never)
		}
	}
	mcp := got[claudeJSON]
	project := filepath.ToSlash(in.Project)
	if mcp.Action != plan.Create || mcp.Harness != "claudecode" || mcp.Kind != "mcp" || mcp.Source != ".mcp.json" ||
		!reflect.DeepEqual(mcp.Keys, []plan.ProjectKey{{Path: []string{"projects", project, "mcpServers", "browser"}, Action: plan.Create}}) {
		t.Errorf("~/.claude.json = %+v, want the server under projects[%q].mcpServers", mcp, project)
	}
	keys := func(target string) []string {
		var out []string
		for _, k := range got[target].Keys {
			out = append(out, string(k.Action)+" "+strings.Join(k.Path, "."))
		}
		return out
	}
	for target, w := range map[string][]string{
		".claude/settings.local.json": {"create $schema", "create hooks.SessionStart", "create permissions.allow", "create model"},
		".agents/hooks.json":          {"create rulesync.SessionStart", "create rulesync.Stop"},
		".agents/mcp_config.json":     {"create mcpServers.browser"},
		".opencode/opencode.json":     {"create mcp.servers.browser", "create permissions", "create instructions", "create model"},
	} {
		if g := keys(target); !reflect.DeepEqual(g, w) {
			t.Errorf("%s keys = %v, want %v", target, g, w)
		}
	}
	if len(p.Skipped) != 0 {
		t.Errorf("skipped = %+v, want none", p.Skipped)
	}
}

func TestBuildProjectExcludesEveryCreatedFile(t *testing.T) {
	in := projectInput(t)
	p, err := plan.BuildProject(in)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"/.dotai/",
		"/.agents/agents/webdev.md", "/.agents/hooks.json", "/.agents/mcp_config.json", "/.agents/rules/web.md",
		"/.agents/skills/deploy/", "/.agents/skills/lint/",
		"/.claude/agents/webdev.md", "/.claude/commands/deploy.md", "/.claude/rules/web.md", "/.claude/settings.local.json", "/.claude/skills/lint/",
		"/.opencode/agents/webdev.md", "/.opencode/commands/deploy.md", "/.opencode/memories/web.md", "/.opencode/opencode.json",
		"/.opencode/plugins/rulesync-hooks.js", "/.opencode/skills/lint/",
	}
	if !reflect.DeepEqual(p.Exclude, want) {
		t.Errorf("exclude = %q, want %q", p.Exclude, want)
	}
	if slices.ContainsFunc(p.Warnings, func(w plan.Warning) bool { return w.Code == plan.CodeExcludePending }) {
		t.Errorf("warnings = %+v, want no pending exclude in a repository", p.Warnings)
	}
}

func TestBuildProjectExcludeIsRelativeToTheRepository(t *testing.T) {
	in := projectInput(t)
	in.Prefix = "site/"
	in.Files = []plan.File{{Path: ".claude/rules/a [x] b.md", Hash: "h"}}
	p, err := plan.BuildProject(in)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"/site/.dotai/", `/site/.claude/rules/a \[x] b.md`}; !reflect.DeepEqual(p.Exclude, want) {
		t.Errorf("exclude = %q, want %q", p.Exclude, want)
	}
}

func TestBuildProjectOutsideARepositoryKeepsTheEntriesPending(t *testing.T) {
	in := projectInput(t)
	in.Repo = false
	in.Files = []plan.File{{Path: ".claude/rules/web.md", Hash: "h"}}
	p, err := plan.BuildProject(in)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"/.dotai/", "/.claude/rules/web.md"}; !reflect.DeepEqual(p.Exclude, want) {
		t.Errorf("exclude = %q, want %q relative to the project", p.Exclude, want)
	}
	if len(p.Warnings) != 1 || p.Warnings[0].Code != plan.CodeExcludePending {
		t.Errorf("warnings = %+v, want exclude-pending", p.Warnings)
	}
}

func TestBuildProjectSkipsEachItemGitTracks(t *testing.T) {
	in := projectInput(t)
	at := func(rel string) string { return filepath.Join(in.Project, filepath.FromSlash(rel)) }
	for _, rel := range []string{".claude/skills/lint/SKILL.md", ".agents/hooks.json", ".claude/settings.local.json", ".opencode/plugins/rulesync-hooks.js", ".agents/rules/web.md"} {
		in.Tracked[at(rel)] = true
	}
	p, err := plan.BuildProject(in)
	if err != nil {
		t.Fatal(err)
	}
	type item struct{ harness, item, target string }
	var got []item
	for _, s := range p.Skipped {
		got = append(got, item{s.Harness, s.Item, s.Target})
	}
	want := []item{
		{"claudecode", "key $schema", at(".claude/settings.local.json")},
		{"claudecode", "hook SessionStart: echo hi", at(".claude/settings.local.json")},
		{"claudecode", "key permissions.allow", at(".claude/settings.local.json")},
		{"claudecode", "key model", at(".claude/settings.local.json")},
		{"claudecode", "skill lint", at(".claude/skills/lint/SKILL.md")},
		{"antigravity-cli", "hook SessionStart: echo hi", at(".agents/hooks.json")},
		{"antigravity-cli", "hook Stop: echo bye", at(".agents/hooks.json")},
		{"antigravity-cli", "rule web", at(".agents/rules/web.md")},
		{"opencode", "hook sessionStart: echo hi", at(".opencode/plugins/rulesync-hooks.js")},
		{"opencode", "hook stop: echo oc", at(".opencode/plugins/rulesync-hooks.js")},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("skipped =\n%v\nwant\n%v", got, want)
	}
	ops := opsByTarget(p, in.Project)
	for _, rel := range []string{".claude/skills/lint/SKILL.md", ".agents/hooks.json", ".claude/settings.local.json", ".opencode/plugins/rulesync-hooks.js", ".agents/rules/web.md"} {
		if op, ok := ops[rel]; ok {
			t.Errorf("%s is tracked but planned: %+v", rel, op)
		}
		if slices.Contains(p.Exclude, "/"+rel) {
			t.Errorf("%s is tracked but excluded", rel)
		}
	}
	if slices.Contains(p.Exclude, "/.claude/skills/lint/") {
		t.Error("the tracked skill is excluded")
	}
}

func TestBuildProjectComparesExistingTargets(t *testing.T) {
	in := projectInput(t)
	at := func(rel string) string { return filepath.Join(in.Project, filepath.FromSlash(rel)) }
	in.Disk[at(".claude/rules/web.md")] = plan.Current{Hash: "new:.claude/rules/web.md"}
	in.Disk[at(".claude/agents/webdev.md")] = plan.Current{Hash: "someone else's"}
	in.Disk[at(".claude/settings.local.json")] = plan.Current{Hash: "x"}
	in.Content[at(".claude/settings.local.json")] = []byte(`{
  // the user's own
  "model": "opus",
  "permissions": {"allow": ["Bash(npm test)"]},
  "theme": "dark"
}`)
	in.Disk[at(".dotai/state.json")] = plan.Current{Hash: "record"}
	claudeJSON := filepath.Join(in.Home, ".claude.json")
	in.Disk[claudeJSON] = plan.Current{Hash: "y"}
	in.Content[claudeJSON] = []byte(`{"projects":{"` + filepath.ToSlash(in.Project) + `":{"mcpServers":{"browser":{"type":"stdio","command":"npx","args":["browser-mcp"]}}}}}`)
	p, err := plan.BuildProject(in)
	if err != nil {
		t.Fatal(err)
	}
	ops := opsByTarget(p, in.Project)
	if op := ops[".claude/rules/web.md"]; op.Action != plan.Unchanged {
		t.Errorf("same content = %+v, want unchanged", op)
	}
	if op := ops[".claude/agents/webdev.md"]; op.Action != plan.Conflict || op.Before != "someone else's" {
		t.Errorf("other content = %+v, want a conflict", op)
	}
	settings := ops[".claude/settings.local.json"]
	wantKeys := []plan.ProjectKey{
		{Path: []string{"$schema"}, Action: plan.Create},
		{Path: []string{"hooks", "SessionStart"}, Action: plan.Create},
		{Path: []string{"permissions", "allow"}, Action: plan.Unchanged},
		{Path: []string{"model"}, Action: plan.Merge},
	}
	if settings.Action != plan.Merge || !reflect.DeepEqual(settings.Keys, wantKeys) {
		t.Errorf("settings = %+v, want merge with %+v", settings, wantKeys)
	}
	if op := ops[claudeJSON]; op.Action != plan.Unchanged || op.Keys[0].Action != plan.Unchanged {
		t.Errorf("~/.claude.json = %+v, want unchanged", op)
	}
	if op := ops[".dotai/state.json"]; op.Action != plan.Replace {
		t.Errorf("record = %+v, want replace", op)
	}
	for _, rel := range []string{".claude/rules/web.md", ".claude/agents/webdev.md", ".claude/settings.local.json"} {
		if slices.Contains(p.Exclude, "/"+rel) {
			t.Errorf("%s exists but is excluded as created", rel)
		}
	}
}

func TestBuildProjectRefusesASettingsFileItCannotEdit(t *testing.T) {
	in := projectInput(t)
	target := filepath.Join(in.Project, ".agents", "mcp_config.json")
	in.Disk[target] = plan.Current{Hash: "x"}
	in.Content[target] = []byte(`[1, 2]`)
	p, err := plan.BuildProject(in)
	if err != nil {
		t.Fatal(err)
	}
	if op := opsByTarget(p, in.Project)[".agents/mcp_config.json"]; op.Action != plan.Conflict || len(op.Keys) != 0 {
		t.Errorf("op = %+v, want a conflict", op)
	}
}

func TestBuildProjectImportsAgentsMD(t *testing.T) {
	in := projectInput(t)
	in.ImportAgents = true
	in.Adapters = adapters(t, "claudecode", "opencode")
	in.Files = slices.DeleteFunc(in.Files, func(f plan.File) bool { return strings.HasPrefix(f.Path, ".agents/") })
	p, err := plan.BuildProject(in)
	if err != nil {
		t.Fatal(err)
	}
	op, ok := opsByTarget(p, in.Project)[".claude/rules/minimal-pack.md"]
	if !ok || op.Action != plan.Create || op.Harness != "claudecode" || op.Kind != "rules" || op.Source != "" || op.After != plan.ImportHash("@../../AGENTS.md") {
		t.Errorf("import = %+v, want the pack's rules file that imports AGENTS.md", op)
	}
	if !slices.Contains(p.Exclude, "/.claude/rules/minimal-pack.md") {
		t.Errorf("exclude = %v, want the import file", p.Exclude)
	}
	in.ImportAgents = false
	p, _ = plan.BuildProject(in)
	if _, ok := opsByTarget(p, in.Project)[".claude/rules/minimal-pack.md"]; ok {
		t.Error("the import is planned while a CLAUDE.md imports AGENTS.md")
	}
}

func TestBuildProjectPutsClaudeCodeMCPInTheIsolatedFile(t *testing.T) {
	in := projectInput(t)
	in.Isolated = map[string]bool{"claudecode": true}
	p, err := plan.BuildProject(in)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := opsByTarget(p, in.Project)[filepath.Join(in.Home, ".dotai", "isolated", "claude", ".claude.json")]; !ok {
		t.Errorf("ops = %+v, want the isolated .claude.json", p.Ops)
	}
}

func TestBuildProjectNamesSameNameClashes(t *testing.T) {
	in := projectInput(t)
	probes, err := plan.ProjectProbes(in)
	if err != nil {
		t.Fatal(err)
	}
	userSkill := filepath.Join(in.Home, ".claude", "skills", "lint")
	agySkill := filepath.Join(in.Home, ".gemini", "config", "skills", "deploy")
	ocAgent := filepath.Join(in.Home, ".config", "opencode", "agents", "webdev.md")
	for _, p := range []string{userSkill, agySkill, ocAgent} {
		if !slices.Contains(probes, p) {
			t.Fatalf("probes = %v, want %s", probes, p)
		}
		in.Exists[p] = true
	}
	p, err := plan.BuildProject(in)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, w := range p.Warnings {
		got = append(got, w.Code+" "+w.Harness+" "+w.Item+" "+w.Path)
	}
	want := []string{
		"scope-clash claudecode skill lint " + userSkill,
		"scope-clash antigravity-cli skill deploy " + agySkill,
		// OpenCode reads the user's Claude Code skills too.
		"scope-clash opencode skill lint " + userSkill,
		"scope-clash opencode agent webdev " + ocAgent,
		// OpenCode also reads the skill the plan installs for Claude Code
		// and agy.
		"read-twice opencode skill lint " + filepath.Join(in.Project, ".claude", "skills", "lint"),
		"read-twice opencode skill lint " + filepath.Join(in.Project, ".agents", "skills", "lint"),
		"no-local-slot antigravity-cli  ",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("warnings =\n%q\nwant\n%q", got, want)
	}
	if !strings.Contains(p.Warnings[0].Message, "instead of the project's") || !strings.Contains(p.Warnings[1].Message, "instead of the user-level one") {
		t.Errorf("messages = %q, %q: want who wins", p.Warnings[0].Message, p.Warnings[1].Message)
	}
}

func TestBuildProjectRejectsWhatNoHarnessInstalls(t *testing.T) {
	in := projectInput(t)
	in.Files = append(in.Files, plan.File{Path: "CLAUDE.md", Hash: "h"}, plan.File{Path: "AGENTS.md", Hash: "h"})
	_, err := plan.BuildProject(in)
	if u, ok := errors.AsType[*plan.UnmappedError](err); !ok || !reflect.DeepEqual(u.Paths, []string{"AGENTS.md", "CLAUDE.md"}) {
		t.Fatalf("BuildProject() error = %v, want CLAUDE.md and AGENTS.md unmapped", err)
	}
	in = projectInput(t)
	in.Adapters = adapters(t, "claudecode")
	if _, err := plan.BuildProject(in); !errors.As(err, new(*plan.UnmappedError)) {
		t.Errorf("BuildProject() error = %v, want the files of the other harnesses unmapped", err)
	}
}

func TestBuildProjectRejectsARelativeProject(t *testing.T) {
	in := projectInput(t)
	in.Project = "site"
	if _, err := plan.BuildProject(in); err == nil {
		t.Error("BuildProject() accepted a relative project directory")
	}
}
