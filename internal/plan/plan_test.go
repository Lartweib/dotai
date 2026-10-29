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
	const hash = "new:.claude/CLAUDE.md"
	tests := []struct {
		name       string
		exec       bool // the staged file is executable
		disk       map[string]plan.Current
		ignoreExec bool
		action     plan.Action
		before     string
		beforeExec bool
	}{
		{name: "missing", action: plan.Create},
		{name: "another file only", disk: map[string]plan.Current{under(home, ".claude/CLAUDE.md"): {Hash: "old"}}, action: plan.Create},
		{name: "other content", disk: map[string]plan.Current{target: {Hash: "old"}}, action: plan.Replace, before: "old"},
		{name: "same content", disk: map[string]plan.Current{target: {Hash: hash}}, action: plan.Unchanged, before: hash},
		{name: "same content and exec", exec: true, disk: map[string]plan.Current{target: {Hash: hash, Exec: true}}, action: plan.Unchanged, before: hash, beforeExec: true},
		{name: "target gained exec", disk: map[string]plan.Current{target: {Hash: hash, Exec: true}}, action: plan.Replace, before: hash, beforeExec: true},
		{name: "target lost exec", exec: true, disk: map[string]plan.Current{target: {Hash: hash}}, action: plan.Replace, before: hash},
		{name: "exec ignored", exec: true, disk: map[string]plan.Current{target: {Hash: hash}}, ignoreExec: true, action: plan.Unchanged, before: hash},
		{name: "exec ignored, other content", disk: map[string]plan.Current{target: {Hash: "old", Exec: true}}, ignoreExec: true, action: plan.Replace, before: "old", beforeExec: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			files := []plan.File{{Path: ".claude/CLAUDE.md", Hash: hash, Exec: tt.exec}}
			got, err := plan.Build(plan.Input{Home: home, Adapters: adapters(t, "claudecode"), Files: files, Disk: tt.disk, IgnoreExec: tt.ignoreExec})
			if err != nil {
				t.Fatalf("Build() error = %v", err)
			}
			want := plan.Op{Action: tt.action, Scope: plan.ScopeUser, Harness: "claudecode", Source: ".claude/CLAUDE.md", Target: target,
				Exec: tt.exec, Before: tt.before, BeforeExec: tt.beforeExec, After: hash}
			if len(got.Ops) != 1 || got.Ops[0] != want {
				t.Errorf("Build() = %+v, want [%+v]", got.Ops, want)
			}
		})
	}
}

// TestBuildExampleTree checks the target of every file the example pack
// stages, except rulesync's sources, and that the plan does not depend on
// the order of its input.
func TestBuildExampleTree(t *testing.T) {
	home := t.TempDir()
	files := staged(exampleTree...)
	got, err := plan.Build(plan.Input{Home: home, Adapters: adapters(t, pack.Harnesses...), Files: files})
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	want := []string{
		".dotai/isolated/claude/CLAUDE.md",
		".dotai/isolated/claude/agents/reviewer.md",
		".dotai/isolated/claude/rules/standalone.md",
		".dotai/isolated/claude/settings.json",
		".dotai/isolated/claude/skills/commit-message/SKILL.md",
		".dotai/isolated/gemini/GEMINI.md",
		".dotai/isolated/gemini/antigravity-cli/settings.json",
		".dotai/isolated/gemini/antigravity-cli/skills/commit-message/SKILL.md",
		".dotai/isolated/gemini/config/agents/reviewer.md",
		".dotai/isolated/gemini/config/rules/standalone.md",
		".dotai/isolated/opencode/config/opencode/AGENTS.md",
		".dotai/isolated/opencode/config/opencode/agents/reviewer.md",
		".dotai/isolated/opencode/config/opencode/memories/standalone.md",
		".dotai/isolated/opencode/config/opencode/opencode.jsonc",
		".dotai/isolated/opencode/config/opencode/skills/commit-message/SKILL.md",
		".dotai/tools/hello.sh",
	}
	var targets []string
	for _, op := range got.Ops {
		rel, err := filepath.Rel(home, op.Target)
		if err != nil {
			t.Fatal(err)
		}
		targets = append(targets, filepath.ToSlash(rel))
	}
	if !reflect.DeepEqual(targets, want) {
		t.Errorf("targets = %q, want %q", targets, want)
	}
	if !slices.IsSortedFunc(got.Ops, func(a, b plan.Op) int { return strings.Compare(a.Target, b.Target) }) {
		t.Errorf("ops are not in target order: %+v", got.Ops)
	}
	if len(slices.Compact(slices.Clone(targets))) != len(targets) {
		t.Errorf("targets are not unique: %q", targets)
	}
	ids := slices.Clone(pack.Harnesses)
	slices.Reverse(ids)
	slices.Reverse(files)
	again, err := plan.Build(plan.Input{Home: home, Adapters: adapters(t, ids...), Files: files})
	if err != nil || !reflect.DeepEqual(again, got) {
		t.Errorf("Build() of the same input in another order = %+v, %v, want %+v", again, err, got)
	}
}

// TestLocate checks that Locate gives the target Build plans, so that a
// Disk keyed by it is found.
func TestLocate(t *testing.T) {
	home := t.TempDir()
	all := adapters(t, pack.Harnesses...)
	for _, source := range exampleTree {
		if strings.HasPrefix(source, ".rulesync/") {
			continue
		}
		h, target, ok := plan.Locate(home, all, source)
		if !ok {
			t.Errorf("Locate(%q) found no target", source)
			continue
		}
		got, err := plan.Build(plan.Input{Home: home, Adapters: all, Files: staged(source), Disk: map[string]plan.Current{target: {Hash: "new:" + source}}})
		if err != nil || len(got.Ops) != 1 || got.Ops[0].Target != target || got.Ops[0].Harness != h || got.Ops[0].Action != plan.Unchanged {
			t.Errorf("Build() of %s = %+v, %v, want one unchanged op at %s for %q", source, got.Ops, err, target, h)
		}
	}
	if _, _, ok := plan.Locate(home, all, "notes.md"); ok {
		t.Error("Locate(notes.md) found a target")
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

// TestBuildRejectsBadInput checks the start of each error; the rest names
// a target under a temporary directory.
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
		{"duplicate path", plan.Input{Home: t.TempDir(), Files: staged(".dotai/tools/a", ".dotai/tools/a")}, `staged files .dotai/tools/a and .dotai/tools/a install to the same target `},
		{"duplicate harness", plan.Input{Home: t.TempDir(), Adapters: slices.Concat(adapters(t, "claudecode"), adapters(t, "claudecode"))}, `harness "claudecode" is given twice`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := plan.Build(tt.input); err == nil || !strings.HasPrefix(err.Error(), tt.want) {
				t.Errorf("Build() error = %v, want it to start with %q", err, tt.want)
			}
		})
	}
}

// TestBuildRejectsWindowsNames checks that a path Windows cannot store is
// rejected on every OS, so that a pack installs the same everywhere.
func TestBuildRejectsWindowsNames(t *testing.T) {
	tests := []struct {
		path string
		ok   bool
	}{
		{".dotai/tools/a:b", false},
		{".dotai/tools/CON", false},
		{".dotai/tools/con.txt", false},
		{".dotai/tools/Aux.md", false},
		{".dotai/tools/nul", false},
		{".dotai/tools/prn/x.sh", false},
		{".dotai/tools/COM1.log", false},
		{".dotai/tools/lpt9", false},
		{".dotai/tools/console.md", true},
		{".dotai/tools/conf.d/a", true},
		{".dotai/tools/COM10", true},
		{".dotai/tools/lpt", true},
	}
	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			_, err := plan.Build(plan.Input{Home: t.TempDir(), Files: staged(tt.path)})
			if tt.ok != (err == nil) {
				t.Errorf("Build() error = %v, want ok %v", err, tt.ok)
			}
			if want := "is not a valid file name on Windows"; err != nil && !strings.Contains(err.Error(), want) {
				t.Errorf("Build() error = %v, want it to contain %q", err, want)
			}
		})
	}
}
