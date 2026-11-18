package app_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/lartweib/dotai/internal/app"
	"github.com/lartweib/dotai/internal/merge"
	"github.com/lartweib/dotai/internal/plan"
)

// TestDotaiBinaryPrefersTheSameFileOnPath checks which binary the
// session-start hook names: the dotai on PATH when it is the very file
// that runs, as a package manager's link is, which outlives an upgrade,
// and otherwise the binary that runs.
func TestDotaiBinaryPrefersTheSameFileOnPath(t *testing.T) {
	dir := t.TempDir()
	exe := filepath.Join(dir, "cellar", "1.0", "dotai")
	writeFile(t, exe, "binary")
	linked := filepath.Join(dir, "bin", "dotai")
	if err := os.MkdirAll(filepath.Dir(linked), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(exe, linked); err != nil {
		t.Skipf("no hard link here: %v", err)
	}
	other := filepath.Join(dir, "other", "dotai")
	writeFile(t, other, "binary")
	running := func() (string, error) { return exe, nil }
	tests := []struct {
		name  string
		found string
		err   error
		want  string
	}{
		{name: "the same file on PATH", found: linked, want: linked},
		{name: "another dotai on PATH", found: other, want: exe},
		{name: "none on PATH", err: errors.New("not found"), want: exe},
		{name: "a relative path on PATH", found: filepath.Join("bin", "dotai"), want: exe},
		{name: "a dotai on PATH that is gone", found: filepath.Join(dir, "gone", "dotai"), want: exe},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			look := func(name string) (string, error) {
				if name != "dotai" {
					t.Errorf("looked up %q, want dotai", name)
				}
				return tt.found, tt.err
			}
			if got, err := app.DotaiBinaryWith(look, running); err != nil || got != tt.want {
				t.Errorf("DotaiBinary() = %q, %v, want %q", got, err, tt.want)
			}
		})
	}
	if _, err := app.DotaiBinaryWith(func(string) (string, error) { return linked, nil }, func() (string, error) { return "", errors.New("unknown") }); err == nil {
		t.Errorf("DotaiBinary() with no running binary = nil error, want it told")
	}
}

func TestProfilesSkill(t *testing.T) {
	got := string(app.ProfilesSkill(app.PackSummary{Name: "acme", Version: "1.2.0", Profiles: []app.ProfileSummary{
		{Name: "web", Description: "Web projects\nwith a toolchain.", Hints: []string{"package.json", "astro.config.*"}, Mods: []string{"panel"}},
		{Name: "godot", Description: "Godot games.", Hints: []string{}, Mods: []string{}},
	}}))
	front, body, ok := strings.Cut(strings.TrimPrefix(got, "---\n"), "\n---\n")
	if !ok || !strings.HasPrefix(got, "---\n") {
		t.Fatalf("skill has no frontmatter:\n%s", got)
	}
	if !strings.HasPrefix(front, "name: dotai-profiles\ndescription: ") || strings.Count(front, "\n") != 1 {
		t.Errorf("frontmatter = %q, want the name and a one-line description", front)
	}
	// The description is always in the context: at most 60 tokens, about
	// four characters each (decision 0045).
	if d := strings.TrimPrefix(front, "name: dotai-profiles\ndescription: "); len(d) > 240 {
		t.Errorf("description is %d characters, want at most 240: %s", len(d), d)
	}
	for _, want := range []string{
		"`dotai apply --profile <a,b> --dry-run`", "`dotai apply --profile <a,b>`", "`--yes`", "`dotai onboarding dismiss`",
		"Never commit", "`.dotai/state.json`", "README, a license or ignore files",
		"### web\n\nWeb projects with a toolchain.\n\n- Hints: `package.json`, `astro.config.*`\n- Mods: `panel`\n",
		"### godot\n\nGodot games.\n",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("skill has no %q:\n%s", want, body)
		}
	}
	if strings.Index(body, "### web") > strings.Index(body, "### godot") {
		t.Errorf("profiles are not in declaration order:\n%s", body)
	}
	if none := string(app.ProfilesSkill(app.PackSummary{Name: "empty"})); !strings.Contains(none, "The pack has no profiles") {
		t.Errorf("skill of a pack without profiles =\n%s", none)
	}
}

func TestHookCommand(t *testing.T) {
	tests := []struct {
		name, bin, goos, harness, want string
		err                            bool
	}{
		{name: "claude quotes with forward slashes", bin: `C:\Users\Ana Lopez\bin\dotai.exe`, goos: "windows", harness: "claudecode",
			want: `"C:/Users/Ana Lopez/bin/dotai.exe" onboarding hook --harness claudecode`},
		{name: "opencode quotes", bin: "/home/ana/bin/dotai", goos: "linux", harness: "opencode", want: `"/home/ana/bin/dotai" onboarding hook --harness opencode`},
		{name: "agy takes it bare in its own separators", bin: `C:/Users/ana/bin/dotai.exe`, goos: "windows", harness: "antigravity-cli",
			want: `C:\Users\ana\bin\dotai.exe onboarding hook --harness antigravity-cli`},
		{name: "agy bare elsewhere", bin: "/usr/local/bin/dotai", goos: "linux", harness: "antigravity-cli", want: "/usr/local/bin/dotai onboarding hook --harness antigravity-cli"},
		{name: "agy cannot take a space", bin: `C:\Users\Ana Lopez\dotai.exe`, goos: "windows", harness: "antigravity-cli", err: true},
		{name: "agy cannot take an operator", bin: "/opt/a&b/dotai", goos: "linux", harness: "antigravity-cli", err: true},
		{name: "a quote breaks the quotes", bin: `/opt/a"b/dotai`, goos: "linux", harness: "claudecode", err: true},
		{name: "a percent on Windows", bin: `C:\%x%\dotai.exe`, goos: "windows", harness: "claudecode", err: true},
		{name: "a network path", bin: `\\server\share\dotai.exe`, goos: "windows", harness: "opencode", err: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := app.HookCommand(tt.bin, tt.goos, tt.harness)
			if (err != nil) != tt.err || got != tt.want {
				t.Errorf("HookCommand() = %q, %v, want %q (error %v)", got, err, tt.want, tt.err)
			}
		})
	}
}

// stagedUser reads the file name of the user scope of the staging in out.
func stagedUser(t *testing.T, out, name string) ([]byte, bool) {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(out, "user", filepath.FromSlash(name)))
	if errors.Is(err, os.ErrNotExist) {
		return nil, false
	}
	if err != nil {
		t.Fatal(err)
	}
	return data, true
}

// sessionStart is the sessionStart entries of each block of the hooks
// file data, by block.
func sessionStart(t *testing.T, data []byte) map[string][]map[string]string {
	t.Helper()
	var doc map[string]json.RawMessage
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatal(err)
	}
	out := map[string][]map[string]string{}
	for k, v := range doc {
		var block struct {
			Hooks map[string][]map[string]string `json:"hooks"`
		}
		if json.Unmarshal(v, &block) == nil && block.Hooks != nil {
			out[k] = block.Hooks["sessionStart"]
		}
	}
	return out
}

func TestStageAddsDotaiOnboarding(t *testing.T) {
	bin := filepath.Join(t.TempDir(), "bin", "dotai")
	fr := quiet(t)
	fr.tools = app.ToolsIn(t.TempDir(), t.TempDir())
	fr.tools.Onboarding = &app.OnboardingBinary{Path: bin}
	out := filepath.Join(t.TempDir(), "staging")
	res, err := fr.stage(examplePack, out, app.Selection{}, false)
	if err != nil || len(res.Diagnostics) > 0 {
		t.Fatalf("Stage() = %+v, %v", res, err)
	}
	if len(res.Warnings) > 0 {
		t.Errorf("warnings = %v, want none", res.Warnings)
	}
	skill, ok := stagedUser(t, out, merge.ProfilesSkillFile)
	if !ok || !bytes.Contains(skill, []byte("### web\n\nWeb projects with a JavaScript toolchain.")) {
		t.Errorf("skill = %s, %v, want the example pack's profiles", skill, ok)
	}
	i := slices.IndexFunc(res.Files, func(f app.StagedFile) bool { return f.Path == merge.ProfilesSkillFile })
	if i < 0 || res.Files[i].Generator != merge.GeneratorDotai || res.Files[i].Scope != app.ScopeUser {
		t.Errorf("files = %+v, want the skill in the user scope, made by dotai", res.Files)
	}
	hooks, ok := stagedUser(t, out, merge.HooksFile)
	if !ok {
		t.Fatalf("no hooks file staged")
	}
	got := sessionStart(t, hooks)
	quoted := `"` + filepath.ToSlash(bin) + `" onboarding hook --harness `
	bare := bin + " onboarding hook --harness antigravity-cli"
	if want := []map[string]string{{"matcher": "startup", "command": quoted + "claudecode"}}; !equalEntries(got["claudecode"], want) {
		t.Errorf("claudecode sessionStart = %v, want %v", got["claudecode"], want)
	}
	if want := []map[string]string{{"command": quoted + "opencode"}}; !equalEntries(got["opencode"], want) {
		t.Errorf("opencode sessionStart = %v, want %v", got["opencode"], want)
	}
	if want := []map[string]string{{"command": bare}}; !equalEntries(got["antigravity-cli"], want) {
		t.Errorf("agy sessionStart = %v, want %v", got["antigravity-cli"], want)
	}
}

func equalEntries(a, b []map[string]string) bool {
	return slices.EqualFunc(a, b, func(x, y map[string]string) bool {
		if len(x) != len(y) {
			return false
		}
		for k, v := range x {
			if y[k] != v {
				return false
			}
		}
		return true
	})
}

func TestStageSkipsAHookTheHarnessCannotRun(t *testing.T) {
	fr := quiet(t)
	fr.tools = app.ToolsIn(t.TempDir(), t.TempDir())
	fr.tools.Onboarding = &app.OnboardingBinary{Path: filepath.Join(t.TempDir(), "with space", "dotai")}
	out := filepath.Join(t.TempDir(), "staging")
	res, err := fr.stage(examplePack, out, app.Selection{}, false)
	if err != nil || len(res.Diagnostics) > 0 {
		t.Fatalf("Stage() = %+v, %v", res, err)
	}
	if len(res.Warnings) != 1 || res.Warnings[0].Code != app.CodeOnboardingHookSkipped || res.Warnings[0].Harness != "antigravity-cli" {
		t.Errorf("warnings = %+v, want agy's hook skipped", res.Warnings)
	}
	hooks, _ := stagedUser(t, out, merge.HooksFile)
	got := sessionStart(t, hooks)
	if _, ok := got["antigravity-cli"]; ok || len(got["claudecode"]) != 1 || len(got["opencode"]) != 1 {
		t.Errorf("sessionStart = %v, want Claude Code's and OpenCode's only", got)
	}
	if _, ok := stagedUser(t, out, merge.ProfilesSkillFile); !ok {
		t.Errorf("no skill staged: agy still gets it")
	}
}

func TestStageUnknownBinarySkipsEveryHook(t *testing.T) {
	fr := quiet(t)
	fr.tools = app.ToolsIn(t.TempDir(), t.TempDir())
	fr.tools.Onboarding = &app.OnboardingBinary{Err: errors.New("no executable")}
	out := filepath.Join(t.TempDir(), "staging")
	res, err := fr.stage(examplePack, out, app.Selection{}, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Warnings) != 3 || !strings.Contains(res.Warnings[0].Message, "no executable") {
		t.Errorf("warnings = %+v, want each harness's hook skipped", res.Warnings)
	}
	if _, ok := stagedUser(t, out, merge.HooksFile); ok {
		t.Errorf("a hooks file was staged without a hook")
	}
}

func TestStageCustomOnboardingAddsNothing(t *testing.T) {
	dir := copyPack(t)
	writeFile(t, filepath.Join(dir, "variants", "tmux", ".rulesync", "skills", "setup", "SKILL.md"), "---\nname: setup\ndescription: Sets up.\n---\n")
	manifest, err := os.ReadFile(filepath.Join(dir, "dotai.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(dir, "dotai.yaml"), string(manifest)+"onboarding:\n  custom: variants/tmux/.rulesync/skills/setup\n")
	fr := quiet(t)
	fr.tools = app.ToolsIn(t.TempDir(), t.TempDir())
	fr.tools.Onboarding = &app.OnboardingBinary{Path: "/bin/dotai"}
	out := filepath.Join(t.TempDir(), "staging")
	res, err := fr.stage(dir, out, app.Selection{}, false)
	if err != nil || len(res.Diagnostics) > 0 {
		t.Fatalf("Stage() = %+v, %v", res, err)
	}
	want := plan.Warning{Code: app.CodeOnboardingReplaced, Item: "variants/tmux/.rulesync/skills/setup/SKILL.md"}
	if len(res.Warnings) != 1 || res.Warnings[0].Code != want.Code || res.Warnings[0].Item != want.Item ||
		!strings.Contains(res.Warnings[0].Message, "minimal-pack") || !strings.Contains(res.Warnings[0].Message, "only with the variant tmux") {
		t.Errorf("warnings = %+v, want %+v naming the pack and the variant", res.Warnings, want)
	}
	if _, ok := stagedUser(t, out, merge.ProfilesSkillFile); ok {
		t.Errorf("the profiling skill was staged for a pack that replaces it")
	}
	if _, ok := stagedUser(t, out, merge.HooksFile); ok {
		t.Errorf("a hook was staged for a pack that replaces the onboarding")
	}
}

func TestPlanInstallWarnsAboutTheOnboarding(t *testing.T) {
	home, _ := installHome(t)
	bin := filepath.Join(t.TempDir(), "with space", "dotai")
	h := app.WithExe(app.EveryHarness, func() (string, error) { return bin, nil })
	p, err := app.PlanInstallHost(context.Background(), app.OSInstallFS{}, h, quiet(t).fetch, quiet(t).run, examplePack, home, app.InstallOptions{})
	if err != nil || len(p.Diagnostics) > 0 {
		t.Fatalf("PlanInstall() = %+v, %v", p, err)
	}
	if len(p.Warnings) != 1 || p.Warnings[0].Code != app.CodeOnboardingHookSkipped || p.Warnings[0].Harness != "antigravity-cli" {
		t.Errorf("warnings = %+v, want agy's hook skipped", p.Warnings)
	}
	// A host whose binary is unknown to the test adds no onboarding, and
	// the plan's warnings are an empty list, never null.
	p, err = app.PlanInstallHost(context.Background(), app.OSInstallFS{}, app.EveryHarness, quiet(t).fetch, quiet(t).run, examplePack, home, app.InstallOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if data, _ := json.Marshal(p); !bytes.Contains(data, []byte(`"warnings":[]`)) {
		t.Errorf("plan = %s, want warnings []", data)
	}
}
