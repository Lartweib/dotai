package plan_test

import (
	"errors"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/lartweib/dotai/internal/harness"
	"github.com/lartweib/dotai/internal/pack"
	"github.com/lartweib/dotai/internal/plan"
)

// exampleTree is the user-scope tree that dotai stage writes for the
// example pack with rulesync 41.0.0.
var exampleTree = []string{
	".claude/CLAUDE.md",
	".claude/agents/reviewer.md",
	".claude/rules/standalone.md",
	".claude/settings.json",
	".claude/skills/commit-message/SKILL.md",
	".config/opencode/AGENTS.md",
	".config/opencode/agents/reviewer.md",
	".config/opencode/memories/standalone.md",
	".config/opencode/opencode.jsonc",
	".config/opencode/skills/commit-message/SKILL.md",
	".dotai/tools/hello.sh",
	".gemini/GEMINI.md",
	".gemini/antigravity-cli/settings.json",
	".gemini/antigravity-cli/skills/commit-message/SKILL.md",
	".gemini/config/agents/reviewer.md",
	".gemini/config/rules/standalone.md",
	".rulesync/rules/overview.md",
	".rulesync/rules/standalone.md",
	".rulesync/skills/commit-message/SKILL.md",
	".rulesync/subagents/reviewer.md",
}

func adapters(t *testing.T, ids ...string) []harness.Adapter {
	t.Helper()
	a, err := harness.Lookup(ids)
	if err != nil {
		t.Fatal(err)
	}
	return a
}

func staged(paths ...string) []plan.File {
	var files []plan.File
	for _, p := range paths {
		files = append(files, plan.File{Path: p, Hash: "new:" + p})
	}
	return files
}

// under joins slash path rel to home with the OS separator.
func under(home, rel string) string { return filepath.Join(home, filepath.FromSlash(rel)) }

func TestBuildMapsEachHarness(t *testing.T) {
	tests := []struct {
		source, harness, target string // target relative to the home directory
	}{
		{".claude/CLAUDE.md", "claudecode", ".dotai/isolated/claude/CLAUDE.md"},
		{".claude/skills/commit-message/SKILL.md", "claudecode", ".dotai/isolated/claude/skills/commit-message/SKILL.md"},
		{".gemini/GEMINI.md", "antigravity-cli", ".dotai/isolated/gemini/GEMINI.md"},
		{".gemini/antigravity-cli/settings.json", "antigravity-cli", ".dotai/isolated/gemini/antigravity-cli/settings.json"},
		{".config/opencode/AGENTS.md", "opencode", ".dotai/isolated/opencode/config/opencode/AGENTS.md"},
		{".config/opencode/opencode.jsonc", "opencode", ".dotai/isolated/opencode/config/opencode/opencode.jsonc"},
		{".dotai/tools/hello.sh", "", ".dotai/tools/hello.sh"},
		{".dotai/tools/lib/util.sh", "", ".dotai/tools/lib/util.sh"},
	}
	home := t.TempDir()
	for _, tt := range tests {
		t.Run(tt.source, func(t *testing.T) {
			got, err := plan.Build(plan.Input{Home: home, Adapters: adapters(t, pack.Harnesses...), Files: staged(tt.source)})
			if err != nil {
				t.Fatalf("Build() error = %v", err)
			}
			want := []plan.Op{{Action: plan.Create, Scope: plan.ScopeUser, Harness: tt.harness, Source: tt.source, Target: under(home, tt.target), After: "new:" + tt.source}}
			if !reflect.DeepEqual(got.Ops, want) {
				t.Errorf("Build() = %+v, want %+v", got.Ops, want)
			}
		})
	}
}

func TestBuildActions(t *testing.T) {
	home := t.TempDir()
	target := under(home, ".dotai/isolated/claude/CLAUDE.md")
	tests := []struct {
		name   string
		disk   map[string]string
		action plan.Action
		before string
	}{
		{"missing", nil, plan.Create, ""},
		{"other content", map[string]string{target: "old"}, plan.Replace, "old"},
		{"same content", map[string]string{target: "new:.claude/CLAUDE.md"}, plan.Unchanged, "new:.claude/CLAUDE.md"},
		{"another file only", map[string]string{under(home, ".claude/CLAUDE.md"): "old"}, plan.Create, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := plan.Build(plan.Input{Home: home, Adapters: adapters(t, "claudecode"), Files: staged(".claude/CLAUDE.md"), Disk: tt.disk})
			if err != nil {
				t.Fatalf("Build() error = %v", err)
			}
			if len(got.Ops) != 1 || got.Ops[0].Action != tt.action || got.Ops[0].Before != tt.before || got.Ops[0].After != "new:.claude/CLAUDE.md" {
				t.Errorf("Build() = %+v, want one %s with Before %q", got.Ops, tt.action, tt.before)
			}
		})
	}
}

// TestBuildExampleTree checks that every file the example pack stages is
// planned, except rulesync's sources, in target order.
func TestBuildExampleTree(t *testing.T) {
	home := t.TempDir()
	files := staged(exampleTree...)
	got, err := plan.Build(plan.Input{Home: home, Adapters: adapters(t, pack.Harnesses...), Files: files})
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	if len(got.Ops) != len(exampleTree)-4 {
		t.Errorf("Build() has %d ops, want one per file outside .rulesync/ (%d)", len(got.Ops), len(exampleTree)-4)
	}
	if !slices.IsSortedFunc(got.Ops, func(a, b plan.Op) int { return strings.Compare(a.Target, b.Target) }) {
		t.Errorf("ops are not in target order: %+v", got.Ops)
	}
	ids := slices.Clone(pack.Harnesses)
	slices.Reverse(ids)
	slices.Reverse(files)
	again, err := plan.Build(plan.Input{Home: home, Adapters: adapters(t, ids...), Files: files})
	if err != nil || !reflect.DeepEqual(again, got) {
		t.Errorf("Build() of the same input in another order = %+v, %v, want %+v", again, err, got)
	}
}

func TestBuildKeepsExec(t *testing.T) {
	home := t.TempDir()
	files := []plan.File{{Path: ".dotai/tools/hello.sh", Hash: "h", Exec: true}, {Path: ".dotai/tools/hello.conf", Hash: "c"}}
	got, err := plan.Build(plan.Input{Home: home, Files: files})
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	exec := map[string]bool{}
	for _, op := range got.Ops {
		exec[op.Source] = op.Exec
	}
	if want := map[string]bool{".dotai/tools/hello.sh": true, ".dotai/tools/hello.conf": false}; !reflect.DeepEqual(exec, want) {
		t.Errorf("Exec by source = %v, want %v", exec, want)
	}
}

// TestBuildUnmapped checks that a staged file no adapter of the pack maps
// is an error naming every such file, and that nothing is planned.
func TestBuildUnmapped(t *testing.T) {
	files := staged(".claude/CLAUDE.md", "notes.md", ".gemini/GEMINI.md", ".claudex/a.md", ".claude", ".dotai/state.json", ".rulesync/rules/a.md")
	got, err := plan.Build(plan.Input{Home: t.TempDir(), Adapters: adapters(t, "claudecode"), Files: files})
	var u *plan.UnmappedError
	if !errors.As(err, &u) {
		t.Fatalf("Build() = %+v, %v, want an UnmappedError", got, err)
	}
	if want := []string{".claude", ".claudex/a.md", ".dotai/state.json", ".gemini/GEMINI.md", "notes.md"}; !reflect.DeepEqual(u.Paths, want) {
		t.Errorf("Paths = %v, want %v", u.Paths, want)
	}
	if want := "no harness of the pack installs the staged files .claude, .claudex/a.md, .dotai/state.json, .gemini/GEMINI.md, notes.md"; err.Error() != want {
		t.Errorf("Error() = %q, want %q", err, want)
	}
	if len(got.Ops) != 0 {
		t.Errorf("Build() planned %+v with an error", got.Ops)
	}
}

func TestBuildRejectsBadInput(t *testing.T) {
	tests := []struct {
		name  string
		input plan.Input
		want  string
	}{
		{"relative home", plan.Input{Home: "home", Files: staged(".dotai/tools/a")}, `home directory "home" is not absolute`},
		{"path outside the tree", plan.Input{Home: t.TempDir(), Files: staged(".dotai/tools/../../x")}, `staged path ".dotai/tools/../../x" is not a slash path inside the tree`},
		{"absolute path", plan.Input{Home: t.TempDir(), Files: staged("/etc/x")}, `staged path "/etc/x" is not a slash path inside the tree`},
		{"backslash", plan.Input{Home: t.TempDir(), Files: staged(`.dotai/tools/..\..\x`)}, `staged path ".dotai/tools/..\\..\\x" is not a slash path inside the tree`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := plan.Build(tt.input); err == nil || err.Error() != tt.want {
				t.Errorf("Build() error = %v, want %q", err, tt.want)
			}
		})
	}
}
