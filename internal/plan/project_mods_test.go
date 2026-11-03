package plan_test

import (
	"maps"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/lartweib/dotai/internal/plan"
)

// modFiles are the staged files of the mod panel, as dotai stage writes
// them into the project tree.
var modFiles = []string{"mods/panel/.claude-plugin/plugin.json", "mods/panel/hooks/hooks.json", "mods/panel/hooks/register.js"}

// modInput is projectInput with the mod panel staged.
func modInput(t *testing.T) plan.ProjectInput {
	t.Helper()
	in := projectInput(t)
	for _, p := range modFiles {
		in.Files = append(in.Files, plan.File{Path: p, Hash: "new:" + p, Data: []byte(p)})
	}
	return in
}

// modOps are the ops of p under .claude/skills/panel/, by target relative
// to the project.
func modOps(p plan.Placement, project string) map[string]plan.ProjectOp {
	out := map[string]plan.ProjectOp{}
	for target, op := range opsByTarget(p, project) {
		if strings.HasPrefix(target, ".claude/skills/panel/") {
			out[target] = op
		}
	}
	return out
}

var modTargets = []string{".claude/skills/panel/.claude-plugin/plugin.json", ".claude/skills/panel/hooks/hooks.json", ".claude/skills/panel/hooks/register.js"}

func TestBuildProjectInstallsAModUnderClaudeSkills(t *testing.T) {
	in := modInput(t)
	p, err := plan.BuildProject(in)
	if err != nil {
		t.Fatal(err)
	}
	ops := modOps(p, in.Project)
	if got := slices.Sorted(maps.Keys(ops)); !reflect.DeepEqual(got, modTargets) {
		t.Fatalf("mod targets = %v, want %v", got, modTargets)
	}
	for i, target := range modTargets {
		op := ops[target]
		if op.Action != plan.Create || op.Harness != "claudecode" || op.Kind != "mods" || op.Source != modFiles[i] || op.Owns == nil || !op.Owns.Created {
			t.Errorf("%s = %+v, want a claudecode mods create of %s that dotai owns", target, op, modFiles[i])
		}
		if op.Owns != nil && op.Owns.Exclude != "/.claude/skills/panel/" {
			t.Errorf("%s is excluded by %q, want the mod's directory", target, op.Owns.Exclude)
		}
	}
	if !slices.Contains(p.Exclude, "/.claude/skills/panel/") || slices.ContainsFunc(p.Exclude, func(e string) bool {
		return strings.HasPrefix(e, "/.claude/skills/panel/") && e != "/.claude/skills/panel/"
	}) {
		t.Errorf("exclude = %v, want the mod's directory alone", p.Exclude)
	}
	var slots []string
	for _, w := range p.Warnings {
		if w.Code == plan.CodeNoLocalSlot && w.Item == "mods" {
			slots = append(slots, w.Harness+": "+w.Message)
		}
	}
	want := []string{
		"antigravity-cli: antigravity-cli has no local place for project mods, so those of the profiles are not installed for it",
		"opencode: opencode has no local place for project mods, so those of the profiles are not installed for it",
	}
	if !reflect.DeepEqual(slots, want) {
		t.Errorf("no-local-slot warnings = %q, want %q", slots, want)
	}
}

func TestBuildProjectSkipsAModWhenGitTracksAnythingInIt(t *testing.T) {
	in := modInput(t)
	dir := filepath.Join(in.Project, ".claude", "skills", "panel")
	in.Tracked[dir] = true // git tracks a file there that the mod does not ship
	p, err := plan.BuildProject(in)
	if err != nil {
		t.Fatal(err)
	}
	if ops := modOps(p, in.Project); len(ops) != 0 {
		t.Errorf("mod ops = %v, want none", slices.Sorted(maps.Keys(ops)))
	}
	var got []plan.Skipped
	for _, sk := range p.Skipped {
		if sk.Kind == "mods" {
			got = append(got, sk)
		}
	}
	want := []plan.Skipped{{Harness: "claudecode", Kind: "mods", Item: "mod panel", Target: dir, Reason: plan.SkipTracked}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("skipped = %+v, want %+v", got, want)
	}
	if slices.ContainsFunc(p.Exclude, func(e string) bool { return strings.HasPrefix(e, "/.claude/skills/panel") }) {
		t.Errorf("exclude = %v, want nothing of the skipped mod", p.Exclude)
	}
}

func TestBuildProjectPlansAModOverAFolderOfItsName(t *testing.T) {
	in := modInput(t)
	dir := filepath.Join(in.Project, ".claude", "skills", "panel")
	in.Exists[dir] = true // someone's folder, or what a restore left of dotai's
	in.Disk[filepath.Join(dir, "hooks", "hooks.json")] = plan.Current{Hash: "new:mods/panel/hooks/hooks.json"}
	in.Disk[filepath.Join(dir, "hooks", "register.js")] = plan.Current{Hash: "other"}
	p, err := plan.BuildProject(in)
	if err != nil {
		t.Fatal(err)
	}
	ops := modOps(p, in.Project)
	want := map[string]plan.Action{
		".claude/skills/panel/.claude-plugin/plugin.json": plan.Create,    // missing: created, no consent needed
		".claude/skills/panel/hooks/hooks.json":           plan.Unchanged, // holds the same already
		".claude/skills/panel/hooks/register.js":          plan.Conflict,  // holds something else: overwritten only with consent
	}
	for target, action := range want {
		if ops[target].Action != action {
			t.Errorf("%s = %s, want %s", target, ops[target].Action, action)
		}
	}
	var got []string
	for _, e := range p.Exclude {
		if strings.HasPrefix(e, "/.claude/skills/panel") {
			got = append(got, e)
		}
	}
	if want := []string{"/.claude/skills/panel/.claude-plugin/plugin.json"}; !reflect.DeepEqual(got, want) {
		t.Errorf("exclude = %v, want the file the apply creates in the existing folder", got)
	}
}

func TestBuildProjectKeepsAModAnEarlierApplyInstalled(t *testing.T) {
	in := modInput(t)
	dir := filepath.Join(in.Project, ".claude", "skills", "panel")
	in.Exists[dir] = true
	in.Owned = map[string]plan.ProjectOwned{}
	for i, target := range modTargets {
		abs := filepath.Join(in.Project, filepath.FromSlash(target))
		in.Disk[abs] = plan.Current{Hash: "new:" + modFiles[i]}
		in.Owned[abs] = plan.ProjectOwned{Harness: "claudecode", Kind: "mods", Hash: "new:" + modFiles[i], Created: true, Exclude: "/.claude/skills/panel/"}
	}
	p, err := plan.BuildProject(in)
	if err != nil {
		t.Fatal(err)
	}
	for target, op := range modOps(p, in.Project) {
		if op.Action != plan.Unchanged || op.Owns == nil || op.Owns.Exclude != "/.claude/skills/panel/" {
			t.Errorf("%s = %+v, want unchanged and still dotai's", target, op)
		}
	}
	if !slices.Contains(p.Exclude, "/.claude/skills/panel/") {
		t.Errorf("exclude = %v, want the mod's directory kept", p.Exclude)
	}

	// A later apply whose profiles no longer name the mod removes it.
	in.Files = slices.DeleteFunc(in.Files, func(f plan.File) bool { return strings.HasPrefix(f.Path, "mods/") })
	p, err = plan.BuildProject(in)
	if err != nil {
		t.Fatal(err)
	}
	ops := modOps(p, in.Project)
	for _, target := range modTargets {
		if ops[target].Action != plan.Remove {
			t.Errorf("%s = %+v, want remove", target, ops[target])
		}
	}
	if slices.Contains(p.Exclude, "/.claude/skills/panel/") {
		t.Errorf("exclude = %v, want the dropped mod gone", p.Exclude)
	}
}

func TestBuildProjectWithoutClaudeCodeLeavesTheModsOut(t *testing.T) {
	in := modInput(t)
	in.Adapters = adapters(t, "antigravity-cli", "opencode")
	in.Files = slices.DeleteFunc(in.Files, func(f plan.File) bool { return strings.HasPrefix(f.Path, ".claude/") || f.Path == ".mcp.json" })
	p, err := plan.BuildProject(in)
	if err != nil {
		t.Fatalf("BuildProject() = %v, want the mods left out, not unmapped", err)
	}
	if ops := modOps(p, in.Project); len(ops) != 0 {
		t.Errorf("mod ops = %v, want none", ops)
	}
	var slots []string
	for _, w := range p.Warnings {
		if w.Code == plan.CodeNoLocalSlot && w.Item == "mods" {
			slots = append(slots, w.Harness)
		}
	}
	if !reflect.DeepEqual(slots, []string{"antigravity-cli", "opencode"}) {
		t.Errorf("no-local-slot for mods = %v, want each planned harness, since none takes them", slots)
	}
}

func TestProjectDirsAreTheModsDirectories(t *testing.T) {
	in := modInput(t)
	got, err := plan.ProjectDirs(in)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{filepath.Join(in.Project, ".claude", "skills", "panel")}; !reflect.DeepEqual(got, want) {
		t.Errorf("ProjectDirs() = %v, want %v", got, want)
	}
}
