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
		// agy moves antigravity-cli/skills to config/skills on its first run.
		{".gemini/antigravity-cli/skills/commit-message/SKILL.md", "antigravity-cli", ".dotai/isolated/gemini/config/skills/commit-message/SKILL.md"},
		{".gemini/antigravity-cli/skillsx/a.md", "antigravity-cli", ".dotai/isolated/gemini/antigravity-cli/skillsx/a.md"},
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
			if len(got.Ops) != 1 || !reflect.DeepEqual(got.Ops[0], want) {
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
		".dotai/isolated/gemini/config/agents/reviewer.md",
		".dotai/isolated/gemini/config/rules/standalone.md",
		".dotai/isolated/gemini/config/skills/commit-message/SKILL.md",
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
		{"agy skill staged at both paths", plan.Input{Home: t.TempDir(), Adapters: adapters(t, "antigravity-cli"),
			Files: staged(".gemini/antigravity-cli/skills/a/SKILL.md", ".gemini/config/skills/a/SKILL.md")},
			`staged files .gemini/antigravity-cli/skills/a/SKILL.md and .gemini/config/skills/a/SKILL.md install to the same target `},
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
		{".dotai/tools/a\x00b", false},
		{".dotai/tools/a\x1fb", false},
		{".dotai/tools/a<b", false},
		{".dotai/tools/a>b", false},
		{`.dotai/tools/a"b`, false},
		{".dotai/tools/a|b", false},
		{".dotai/tools/a?b", false},
		{".dotai/tools/a*b", false},
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

// TestBuildRefusesAHarnessWithoutIsolation checks decision 0018: a harness
// whose isolation level is none does not offer the isolated mode.
func TestBuildRefusesAHarnessWithoutIsolation(t *testing.T) {
	a := adapters(t, "claudecode")
	a[0].Isolation.Level = harness.None
	_, err := plan.Build(plan.Input{Home: t.TempDir(), Adapters: a, Files: staged(".claude/CLAUDE.md")})
	if want := `harness "claudecode" does not offer the isolated mode`; err == nil || !strings.Contains(err.Error(), want) {
		t.Errorf("Build() error = %v, want it to contain %q", err, want)
	}
}

// TestBuildRemovesOwnedTargets checks that a target an earlier install
// owned and the pack no longer stages is removed when it is still there,
// in target order with the rest, and that a staged one is not.
func TestBuildRemovesOwnedTargets(t *testing.T) {
	home := t.TempDir()
	kept := under(home, ".dotai/isolated/claude/CLAUDE.md")
	dropped := under(home, ".dotai/isolated/claude/agents/old.md")
	tool := under(home, ".dotai/tools/old.sh")
	gone := under(home, ".dotai/isolated/claude/rules/gone.md")
	in := plan.Input{
		Home: home, Adapters: adapters(t, "claudecode"), Files: staged(".claude/CLAUDE.md"),
		Owned: map[string]plan.Owned{
			kept:    {Harness: "claudecode", Hash: "old"},
			dropped: {Harness: "claudecode", Hash: "a"},
			tool:    {Hash: "t", Exec: true},
			gone:    {Harness: "claudecode", Hash: "g"}, // deleted since: nothing to remove
		},
		Disk: map[string]plan.Current{kept: {Hash: "old"}, dropped: {Hash: "a"}, tool: {Hash: "t", Exec: true}},
	}
	got, err := plan.Build(in)
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	want := []plan.Op{
		{Action: plan.Replace, Scope: plan.ScopeUser, Harness: "claudecode", Source: ".claude/CLAUDE.md", Target: kept, Before: "old", After: "new:.claude/CLAUDE.md"},
		{Action: plan.Remove, Scope: plan.ScopeUser, Harness: "claudecode", Target: dropped, Before: "a"},
		{Action: plan.Remove, Scope: plan.ScopeUser, Target: tool, Before: "t", BeforeExec: true},
	}
	if !reflect.DeepEqual(got.Ops, want) {
		t.Errorf("Build() = %+v, want %+v", got.Ops, want)
	}
}

// TestBuildRefusesEditedOwnedTargets checks that an owned target whose
// content or exec bit differs from the ownership record is an error
// naming every such target, whether the pack still stages it or not.
func TestBuildRefusesEditedOwnedTargets(t *testing.T) {
	home := t.TempDir()
	staged1 := under(home, ".dotai/isolated/claude/CLAUDE.md")
	dropped := under(home, ".dotai/isolated/claude/agents/old.md")
	tests := []struct {
		name       string
		disk       map[string]plan.Current
		ignoreExec bool
		edited     []string
	}{
		{name: "as recorded", disk: map[string]plan.Current{staged1: {Hash: "s"}, dropped: {Hash: "d"}}},
		{name: "content", disk: map[string]plan.Current{staged1: {Hash: "x"}, dropped: {Hash: "y"}}, edited: []string{staged1, dropped}},
		{name: "exec bit", disk: map[string]plan.Current{staged1: {Hash: "s", Exec: true}, dropped: {Hash: "d"}}, edited: []string{staged1}},
		{name: "exec bit ignored", disk: map[string]plan.Current{staged1: {Hash: "s", Exec: true}, dropped: {Hash: "d"}}, ignoreExec: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			in := plan.Input{
				Home: home, Adapters: adapters(t, "claudecode"), Files: staged(".claude/CLAUDE.md"), IgnoreExec: tt.ignoreExec,
				Owned: map[string]plan.Owned{staged1: {Harness: "claudecode", Hash: "s"}, dropped: {Harness: "claudecode", Hash: "d"}},
				Disk:  tt.disk,
			}
			got, err := plan.Build(in)
			var e *plan.EditedError
			if tt.edited == nil {
				if err != nil {
					t.Errorf("Build() error = %v, want none", err)
				}
				return
			}
			if !errors.As(err, &e) || !reflect.DeepEqual(e.Targets, tt.edited) || len(got.Ops) != 0 {
				t.Fatalf("Build() = %+v, %v, want an EditedError naming %v", got.Ops, err, tt.edited)
			}
			for _, target := range tt.edited {
				if !strings.Contains(err.Error(), target) {
					t.Errorf("Error() = %q, want it to name %s", err, target)
				}
			}
		})
	}
}

// TestBuildRefusesOwnedTargetsOutsideDotai checks that an ownership record
// cannot make the plan remove a file outside the directories an install
// writes to.
func TestBuildRefusesOwnedTargetsOutsideDotai(t *testing.T) {
	home := t.TempDir()
	for _, rel := range []string{".claude/CLAUDE.md", ".dotai/state.json", ".dotai/isolated", ".dotai/toolsx/a", "../x"} {
		t.Run(rel, func(t *testing.T) {
			target := under(home, rel)
			in := plan.Input{Home: home, Owned: map[string]plan.Owned{target: {Hash: "h"}}, Disk: map[string]plan.Current{target: {Hash: "h"}}}
			if _, err := plan.Build(in); err == nil || !strings.Contains(err.Error(), "is not under") {
				t.Errorf("Build() error = %v, want an owned target outside the install directories", err)
			}
		})
	}
}

// TestBuildResolvesAnInterruptedInstall checks the ownership of the
// targets an install that stopped midway was writing: a content in Also is
// dotai's as much as Hash, and a content in Foreign means the file is not
// dotai's, so it is neither an edit nor removed. Plan.Owned gives every
// target dotai owns on disk.
func TestBuildResolvesAnInterruptedInstall(t *testing.T) {
	home := t.TempDir()
	written := under(home, ".dotai/isolated/claude/agents/a.md") // replaced before the stop
	pending := under(home, ".dotai/isolated/claude/agents/b.md") // not replaced yet
	foreign := under(home, ".dotai/isolated/claude/agents/c.md") // a file dotai never owned, not replaced yet
	edited := under(home, ".dotai/isolated/claude/agents/d.md")
	owned := map[string]plan.Owned{
		written: {Harness: "claudecode", Hash: "a0", Also: []plan.Current{{Hash: "a1"}}},
		pending: {Harness: "claudecode", Hash: "b0", Also: []plan.Current{{Hash: "b1"}}},
		foreign: {Harness: "claudecode", Hash: "c1", Foreign: []plan.Current{{Hash: "c0"}}},
	}
	disk := map[string]plan.Current{written: {Hash: "a1"}, pending: {Hash: "b0"}, foreign: {Hash: "c0"}}
	got, err := plan.Build(plan.Input{Home: home, Adapters: adapters(t, "claudecode"), Owned: owned, Disk: disk})
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	want := []plan.Op{
		{Action: plan.Remove, Scope: plan.ScopeUser, Harness: "claudecode", Target: written, Before: "a1"},
		{Action: plan.Remove, Scope: plan.ScopeUser, Harness: "claudecode", Target: pending, Before: "b0"},
	}
	if !reflect.DeepEqual(got.Ops, want) {
		t.Errorf("Build() = %+v, want %+v", got.Ops, want)
	}
	if want := map[string]plan.Current{written: {Hash: "a1"}, pending: {Hash: "b0"}}; !reflect.DeepEqual(got.Owned, want) {
		t.Errorf("Owned = %+v, want %+v", got.Owned, want)
	}
	owned[edited] = plan.Owned{Hash: "d0", Also: []plan.Current{{Hash: "d1"}}, Foreign: []plan.Current{{Hash: "d2"}}}
	disk[edited] = plan.Current{Hash: "dx"}
	if _, err := plan.Build(plan.Input{Home: home, Owned: owned, Disk: disk}); !errors.As(err, new(*plan.EditedError)) {
		t.Errorf("Build() error = %v, want the edited target", err)
	}
}

// TestEditedErrorWording checks that the message agrees in number with the
// files it names.
func TestEditedErrorWording(t *testing.T) {
	one := (&plan.EditedError{Targets: []string{"/h/a"}}).Error()
	if want := "the installed file /h/a was changed after dotai installed it, and dotai does not overwrite or remove it: " +
		"move it away or put back what dotai installed, then install again"; one != want {
		t.Errorf("Error() = %q, want %q", one, want)
	}
	two := (&plan.EditedError{Targets: []string{"/h/a", "/h/b"}}).Error()
	if want := "the installed files /h/a, /h/b were changed after dotai installed them, and dotai does not overwrite or remove them: " +
		"move them away or put back what dotai installed, then install again"; two != want {
		t.Errorf("Error() = %q, want %q", two, want)
	}
}

// TestMovedFrom checks the targets on the old side of an adapter's move,
// where an older install put agy's skills.
func TestMovedFrom(t *testing.T) {
	home := t.TempDir()
	for rel, want := range map[string]bool{
		".dotai/isolated/gemini/antigravity-cli/skills/a/SKILL.md": true,
		".dotai/isolated/gemini/antigravity-cli/skills":            false,
		".dotai/isolated/gemini/antigravity-cli/settings.json":     false,
		".dotai/isolated/gemini/config/skills/a/SKILL.md":          false,
		".dotai/isolated/claude/skills/a/SKILL.md":                 false,
	} {
		if got := plan.MovedFrom(home, under(home, rel)); got != want {
			t.Errorf("MovedFrom(%s) = %t, want %t", rel, got, want)
		}
	}
}

// TestBuildPlansLinks checks that a staged link (decision 0036) is planned
// like a file whose content is its destination: created when missing, left
// unchanged when it already points there, removed when an earlier install
// owned it and the pack no longer stages it, and refused as edited when it
// points elsewhere.
func TestBuildPlansLinks(t *testing.T) {
	home := t.TempDir()
	dest := func(name string) string { return under(home, ".agents/skills/"+name) }
	link := func(name string) string { return under(home, ".dotai/isolated/claude/skills/"+name) }
	files := []plan.File{
		{Path: ".claude/skills/new", Link: dest("new")},
		{Path: ".claude/skills/same", Link: dest("same")},
		{Path: ".claude/skills/foreign", Link: dest("foreign")},
	}
	owned := map[string]plan.Owned{
		link("same"): {Harness: "claudecode", Hash: plan.LinkHash(dest("same"))},
		link("gone"): {Harness: "claudecode", Hash: plan.LinkHash(dest("gone"))},
	}
	disk := map[string]plan.Current{
		link("same"):    plan.LinkCurrent(dest("same")),
		link("gone"):    plan.LinkCurrent(dest("gone")),
		link("foreign"): plan.LinkCurrent(under(home, "elsewhere")), // a link dotai did not make
	}
	got, err := plan.Build(plan.Input{Home: home, Adapters: adapters(t, "claudecode"), Files: files, Owned: owned, Disk: disk})
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	want := []plan.Op{
		{Action: plan.Replace, Scope: plan.ScopeUser, Harness: "claudecode", Source: ".claude/skills/foreign", Target: link("foreign"),
			Before: plan.LinkHash(under(home, "elsewhere")), BeforeLink: under(home, "elsewhere"), After: plan.LinkHash(dest("foreign")), Link: dest("foreign")},
		{Action: plan.Remove, Scope: plan.ScopeUser, Harness: "claudecode", Target: link("gone"), Before: plan.LinkHash(dest("gone")), BeforeLink: dest("gone")},
		{Action: plan.Create, Scope: plan.ScopeUser, Harness: "claudecode", Source: ".claude/skills/new", Target: link("new"), After: plan.LinkHash(dest("new")), Link: dest("new")},
		{Action: plan.Unchanged, Scope: plan.ScopeUser, Harness: "claudecode", Source: ".claude/skills/same", Target: link("same"),
			Before: plan.LinkHash(dest("same")), BeforeLink: dest("same"), After: plan.LinkHash(dest("same")), Link: dest("same")},
	}
	if !reflect.DeepEqual(got.Ops, want) {
		t.Errorf("Build() =\n%+v\nwant\n%+v", got.Ops, want)
	}

	disk[link("same")] = plan.LinkCurrent(under(home, "changed"))
	if _, err := plan.Build(plan.Input{Home: home, Adapters: adapters(t, "claudecode"), Files: files, Owned: owned, Disk: disk}); !errors.As(err, new(*plan.EditedError)) {
		t.Errorf("Build() error = %v, want an owned link that points elsewhere refused as edited", err)
	}
}

func TestBuildRefusesARelativeLink(t *testing.T) {
	home := t.TempDir()
	files := []plan.File{{Path: ".claude/skills/a", Link: "relative/a"}}
	if _, err := plan.Build(plan.Input{Home: home, Adapters: adapters(t, "claudecode"), Files: files}); err == nil || !strings.Contains(err.Error(), "not absolute") {
		t.Errorf("Build() error = %v, want a link whose destination is not absolute refused", err)
	}
}

func TestLinkHash(t *testing.T) {
	if a, b := plan.LinkHash("/h/.agents/skills/a"), plan.LinkHash("/h/.agents/skills/b"); len(a) != 64 || a == b {
		t.Errorf("LinkHash() = %q, %q, want two SHA-256 in hex", a, b)
	}
	if c := plan.LinkCurrent("/h/a"); c != (plan.Current{Hash: plan.LinkHash("/h/a"), Link: "/h/a"}) {
		t.Errorf("LinkCurrent() = %+v", c)
	}
}

func TestBuildRefusesToReplaceAFileWithALink(t *testing.T) {
	home := t.TempDir()
	target := under(home, ".dotai/isolated/claude/skills/a")
	for name, tt := range map[string]struct {
		file plan.File
		disk plan.Current
		link bool
	}{
		"a file where a link goes": {plan.File{Path: ".claude/skills/a", Link: under(home, ".agents/skills/a")}, plan.Current{Hash: "f"}, true},
		"a link where a file goes": {plan.File{Path: ".claude/skills/a", Hash: "f"}, plan.LinkCurrent(under(home, "x")), false},
	} {
		t.Run(name, func(t *testing.T) {
			in := plan.Input{Home: home, Adapters: adapters(t, "claudecode"), Files: []plan.File{tt.file}, Disk: map[string]plan.Current{target: tt.disk}}
			_, err := plan.Build(in)
			var e *plan.KindError
			if !errors.As(err, &e) || e.Target != target || e.Link != tt.link {
				t.Errorf("Build() error = %v, want a kind error on %s", err, target)
			}
		})
	}
}
