package app_test

import (
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/lartweib/dotai/internal/app"
	"github.com/lartweib/dotai/internal/plan"
)

// homeExcludes are the claudeMdExcludes an isolated install writes for
// home: the instructions Claude Code finds in the real home directory.
func homeExcludes(home string) []any {
	h := filepath.ToSlash(home)
	return []any{h + "/CLAUDE.md", h + "/CLAUDE.local.md", h + "/.claude/CLAUDE.md", h + "/.claude/rules/**"}
}

// TestInstallExcludesTheHomeInstructionsFromIsolatedClaudeCode checks
// that an isolated install owns claudeMdExcludes in Claude Code's
// settings, naming the instructions of the real home directory, which
// Claude Code would otherwise load from the directories above the working
// directory.
func TestInstallExcludesTheHomeInstructionsFromIsolatedClaudeCode(t *testing.T) {
	home, _ := installHome(t)
	res, err := installVariant(t, newClockFS(t), examplePack, "", home)
	if err != nil {
		t.Fatalf("Install() error = %v", err)
	}
	got := rawSettingsOf(t, at(home, claudeSettings))
	if want := homeExcludes(home); !reflect.DeepEqual(got["claudeMdExcludes"], want) {
		t.Errorf("claudeMdExcludes = %v, want %v", got["claudeMdExcludes"], want)
	}
	if got["model"] != "sonnet" {
		t.Errorf("settings = %v, want the pack's keys too", got)
	}
	var op app.InstallOp
	for _, o := range res.Ops {
		if o.Target == at(home, claudeSettings) {
			op = o
		}
	}
	if !slices.Contains(op.Keys, plan.KeyChange{Key: "claudeMdExcludes", Action: plan.Create}) {
		t.Errorf("settings op = %+v, want claudeMdExcludes created as a key dotai owns", op)
	}
}

// TestInstallMergesThePackClaudeMdExcludes checks that a list the pack
// sets is kept, first, and the home patterns are added to it as a union
// (decision 0040).
func TestInstallMergesThePackClaudeMdExcludes(t *testing.T) {
	home, _ := installHome(t)
	dir := copyPack(t)
	own := filepath.ToSlash(home) + "/CLAUDE.md"
	editPack(t, dir, "base/dotai/settings.yaml", "  model: sonnet\n", "  model: sonnet\n  claudeMdExcludes: [\"**/vendor/CLAUDE.md\", \""+own+"\"]\n")
	if _, err := installVariant(t, newClockFS(t), dir, "", home); err != nil {
		t.Fatalf("Install() error = %v", err)
	}
	got := rawSettingsOf(t, at(home, claudeSettings))["claudeMdExcludes"]
	want := append([]any{"**/vendor/CLAUDE.md"}, homeExcludes(home)...)
	if !reflect.DeepEqual(got, want) {
		t.Errorf("claudeMdExcludes = %v, want %v", got, want)
	}
}

// TestPlanInstallWritesNoClaudeMdExcludesForAGlobHome checks that a home
// directory whose path is itself a glob pattern gets no patterns, which
// would match other files, and a notice instead.
func TestPlanInstallWritesNoClaudeMdExcludesForAGlobHome(t *testing.T) {
	base, _ := installHome(t)
	home := filepath.Join(base, "user[1]")
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	p, err := quiet(t).planInstall(copyPackWithoutToolCommand(t), "", home)
	if err != nil || len(p.Diagnostics) != 0 {
		t.Fatalf("PlanInstall() = %+v, %v", p, err)
	}
	for _, op := range p.Ops {
		if slices.ContainsFunc(op.Keys, func(k plan.KeyChange) bool { return k.Key == "claudeMdExcludes" }) {
			t.Errorf("op %+v plans claudeMdExcludes", op)
		}
	}
	if !slices.ContainsFunc(p.Notices, func(n string) bool { return strings.Contains(n, "claudeMdExcludes") && strings.Contains(n, home) }) {
		t.Errorf("notices = %q, want one about the glob home", p.Notices)
	}
}

// TestPlanReplaceWritesNoClaudeMdExcludes checks that a replace install,
// where Claude Code reads the real home directory as its own, excludes
// nothing.
func TestPlanReplaceWritesNoClaudeMdExcludes(t *testing.T) {
	home, h := realHome(t)
	p, err := planReplace(t, home, h, "")
	if err != nil || len(p.Diagnostics) != 0 {
		t.Fatalf("PlanInstall(replace) = %+v, %v", p, err)
	}
	for _, op := range p.Ops {
		for _, k := range op.Keys {
			if k.Key == "claudeMdExcludes" {
				t.Errorf("op %+v plans claudeMdExcludes", op)
			}
		}
	}
}
