package app_test

import (
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/lartweib/dotai/internal/app"
	"github.com/lartweib/dotai/internal/plan"
)

// homeExcludes are the claudeMdExcludes an isolated install writes for
// home: the instructions Claude Code finds in the real home directory, for
// each form of it (app.HomeForms), so that a temporary home reached
// through a link or an 8.3 short name, as on macOS and CI runners, gets
// both.
func homeExcludes(home string) []any {
	return claudePatterns(app.HomeForms(home)...)
}

// homeExcludesOf are the claudeMdExcludes of one form of a home directory,
// with forward slashes.
func homeExcludesOf(form string) []any {
	h := filepath.ToSlash(form)
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
	got := settingsOf(t, at(home, claudeSettings))
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
	// The pattern of the first form, so the union keeps the order of homeExcludes.
	own := homeExcludes(home)[0].(string)
	editPack(t, dir, "base/dotai/settings.yaml", "  model: sonnet\n", "  model: sonnet\n  claudeMdExcludes: [\"**/vendor/CLAUDE.md\", \""+own+"\"]\n")
	if _, err := installVariant(t, newClockFS(t), dir, "", home); err != nil {
		t.Fatalf("Install() error = %v", err)
	}
	got := settingsOf(t, at(home, claudeSettings))["claudeMdExcludes"]
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

// claudePatterns are the claudeMdExcludes of each form of a home
// directory, in order.
func claudePatterns(forms ...string) []any {
	var out []any
	for _, f := range forms {
		out = append(out, homeExcludesOf(f)...)
	}
	return out
}

// TestInstallExcludesTheHomeInItsOnDiskCase checks that the patterns name
// the home directory as it is on disk, since Claude Code matches them with
// case, and as given too when that differs.
func TestInstallExcludesTheHomeInItsOnDiskCase(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("a home directory given in another case resolves only on Windows")
	}
	base, _ := installHome(t)
	onDisk := filepath.Join(base, "Home")
	if err := os.Mkdir(onDisk, 0o755); err != nil {
		t.Fatal(err)
	}
	home := filepath.Join(base, "home")
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	if _, err := installVariant(t, newClockFS(t), examplePack, "", home); err != nil {
		t.Fatalf("Install() error = %v", err)
	}
	got, _ := settingsOf(t, at(home, claudeSettings))["claudeMdExcludes"].([]any)
	if want := homeExcludes(home); !reflect.DeepEqual(got, want) {
		t.Errorf("claudeMdExcludes = %v, want %v", got, want)
	}
	// The on-disk form first, then the home as given, whatever form of the
	// temporary directory it is under.
	if len(got) != 8 || !strings.HasSuffix(got[0].(string), "/Home/CLAUDE.md") || !strings.HasSuffix(got[4].(string), "/home/CLAUDE.md") {
		t.Errorf("claudeMdExcludes = %v, want the home in its on-disk case, then as given", got)
	}
}

// TestHomePatternsRefuseGlobCharacters checks that a form of the home
// directory with a character a glob pattern reads, extglob ones included,
// gives no patterns.
func TestHomePatternsRefuseGlobCharacters(t *testing.T) {
	paths := []string{"CLAUDE.md"}
	if got, ok := app.HomePatterns([]string{"C:/Users/ana", "C:/users/ana"}, paths); !ok || !reflect.DeepEqual(got, []string{"C:/Users/ana/CLAUDE.md", "C:/users/ana/CLAUDE.md"}) {
		t.Errorf("HomePatterns() = %q, %v, want one pattern per form", got, ok)
	}
	for _, c := range []string{"*", "?", "[", "]", "{", "}", "(", ")", "+", "@", "!", `\`} {
		if got, ok := app.HomePatterns([]string{"C:/Users/ana", "C:/Users/a" + c + "na"}, paths); ok || got != nil {
			t.Errorf("HomePatterns() with %q = %q, %v, want none", c, got, ok)
		}
	}
}

// TestInstallRefusesAPackClaudeMdExcludesThatIsNotAList checks that a pack
// value the home patterns cannot join fails the install with a clear
// error.
func TestInstallRefusesAPackClaudeMdExcludesThatIsNotAList(t *testing.T) {
	home, _ := installHome(t)
	dir := copyPack(t)
	editPack(t, dir, "base/dotai/settings.yaml", "  model: sonnet\n", "  model: sonnet\n  claudeMdExcludes: \"**/vendor/CLAUDE.md\"\n")
	_, err := installVariant(t, newClockFS(t), dir, "", home)
	if err == nil || !strings.Contains(err.Error(), "claudeMdExcludes must be a list of patterns") {
		t.Fatalf("Install() error = %v, want one that says claudeMdExcludes must be a list", err)
	}
	if _, err := os.Stat(at(home, claudeSettings)); !os.IsNotExist(err) {
		t.Errorf("settings = %v, want nothing installed", err)
	}
}

// TestInstallAgainLeavesTheExcludesAlone checks that a second install of
// the same pack finds the patterns as it wrote them: nothing to write and
// no backup.
func TestInstallAgainLeavesTheExcludesAlone(t *testing.T) {
	home, _ := installHome(t)
	d := newClockFS(t)
	if _, err := installVariant(t, d, examplePack, "", home); err != nil {
		t.Fatal(err)
	}
	res, err := installVariant(t, d, examplePack, "", home)
	if err != nil {
		t.Fatal(err)
	}
	for _, op := range res.Ops {
		if op.Action != plan.Unchanged {
			t.Errorf("op %+v, want unchanged", op)
		}
	}
	if res.Backup != nil {
		t.Errorf("backup = %+v, want none", res.Backup)
	}
	if got := settingsOf(t, at(home, claudeSettings))["claudeMdExcludes"]; !reflect.DeepEqual(got, homeExcludes(home)) {
		t.Errorf("claudeMdExcludes = %v, want each pattern once", got)
	}
}
