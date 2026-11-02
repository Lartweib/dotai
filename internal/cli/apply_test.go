package cli_test

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/lartweib/dotai/internal/app"
	"github.com/lartweib/dotai/internal/cli"
	"github.com/lartweib/dotai/internal/cli/output"
	"github.com/lartweib/dotai/internal/plan"
)

// projectPlan is a project plan with fixed paths, so that its output reads
// the same on every OS.
var projectPlan = app.ProjectPlan{
	Pack:    &app.PackSummary{Name: "minimal-pack", Version: "0.1.0", Harnesses: []string{"claudecode", "antigravity-cli", "opencode"}, OptionalHarnesses: []string{}, Profiles: []app.ProfileSummary{}, Variants: []string{}, SharedSkills: []string{}, Layers: []app.LayerSummary{}},
	Variant: "standalone", Profiles: []string{"web"}, Project: "/work/site", Record: "/work/site/.dotai/state.json",
	Harnesses: []string{"claudecode", "antigravity-cli", "opencode"},
	Ops: []app.ProjectOp{
		{Action: plan.Create, Harness: "claudecode", Kind: "mcp", Source: ".mcp.json", Target: "/home/u/.claude.json",
			Keys: []app.ProjectKey{{Key: []string{"projects", "/work/site", "mcpServers", "browser"}, Action: plan.Create}}},
		{Action: plan.Create, Harness: "antigravity-cli", Kind: "hooks", Source: ".agents/hooks.json", Target: "/work/site/.agents/hooks.json",
			Keys: []app.ProjectKey{{Key: []string{"rulesync", "SessionStart"}, Action: plan.Create}}},
		{Action: plan.Conflict, Harness: "antigravity-cli", Kind: "rules", Source: ".agents/rules/web.md", Target: "/work/site/.agents/rules/web.md", Before: "aa", After: "bb", Keys: []app.ProjectKey{}},
		{Action: plan.Create, Harness: "claudecode", Kind: "rules", Source: ".claude/rules/web.md", Target: "/work/site/.claude/rules/web.md", After: "bb", Keys: []app.ProjectKey{}},
		{Action: plan.Merge, Harness: "claudecode", Kind: "settings", Source: ".claude/settings.json", Target: "/work/site/.claude/settings.local.json", Before: "cc",
			Keys: []app.ProjectKey{{Key: []string{"permissions", "allow"}, Action: plan.Merge}, {Key: []string{"model"}, Action: plan.Unchanged}}},
		{Action: plan.Create, Kind: "record", Target: "/work/site/.dotai/state.json", Keys: []app.ProjectKey{}},
		{Action: plan.Unchanged, Harness: "opencode", Kind: "rules", Source: ".opencode/memories/web.md", Target: "/work/site/.opencode/memories/web.md", Before: "bb", After: "bb", Keys: []app.ProjectKey{}},
	},
	Skipped: []app.SkippedItem{
		{Harness: "claudecode", Kind: "skills", Item: "skill lint", Target: "/work/site/.claude/skills/lint/SKILL.md", Reason: app.SkipTracked},
		{Harness: "opencode", Kind: "hooks", Item: "hook sessionStart: echo hi", Target: "/work/site/.opencode/plugins/rulesync-hooks.js", Reason: app.SkipTracked},
	},
	Exclude: app.ExcludePlan{File: "/work/site/.git/info/exclude", Entries: []string{"/.dotai/", "/.agents/hooks.json", "/.claude/rules/web.md"}},
	Warnings: []app.ProjectWarning{
		{Code: plan.CodeScopeClash, Harness: "claudecode", Item: "skill lint", Path: "/home/u/.claude/skills/lint", Message: "claudecode loads the user-level skill lint from /home/u/.claude/skills/lint instead of the project's"},
		{Code: plan.CodeNoLocalSlot, Harness: "antigravity-cli", Message: "antigravity-cli has no project settings file, so the permissions of the profiles are not installed for it"},
	},
	Notices:     []string{},
	Diagnostics: []app.Diagnostic{},
}

func TestRunApplyDryRun(t *testing.T) {
	t.Setenv("DOTAI_CACHE_DIR", "cache")
	wantCache, err := filepath.Abs("cache")
	if err != nil {
		t.Fatal(err)
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for _, tt := range []struct {
		name, golden, dir string
		args              []string
		want              app.ApplyOptions
		wantProgress      bool
	}{
		{name: "text", args: []string{"apply", examplePack, "--profile", "web", "--dry-run"}, golden: "apply-dry-run.txt.golden", dir: examplePack,
			want: app.ApplyOptions{Profiles: []string{"web"}}, wantProgress: true},
		{name: "json", args: []string{"apply", "--profile", "web,docs", "--variant", "tmux", "--dry-run", "--json"}, golden: "apply-dry-run.json.golden",
			want: app.ApplyOptions{Profiles: []string{"web", "docs"}, Variant: "tmux"}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			env := testEnv(&stdout, &stderr)
			env.PlanProject = func(ctx context.Context, cacheDir, dir, project, gotHome string, opts app.ApplyOptions, progress app.Progress) (app.ProjectPlan, error) {
				if ctx == nil || cacheDir != wantCache || dir != tt.dir || project != cwd || gotHome != home || !reflect.DeepEqual(opts, tt.want) || (progress != nil) != tt.wantProgress {
					t.Errorf("PlanProject(%q, %q, %q, %q, %+v, progress %v), want the cache, %q, the current directory, the home, %+v and progress %v",
						cacheDir, dir, project, gotHome, opts, progress != nil, tt.dir, tt.want, tt.wantProgress)
				}
				return projectPlan, nil
			}
			if code := cli.Run(tt.args, env); code != cli.ExitOK || stderr.Len() != 0 {
				t.Errorf("exit code = %d, stderr = %q, want 0 and empty", code, stderr.String())
			}
			assertGolden(t, filepath.Join("testdata", tt.golden), stdout.Bytes())
		})
	}
}

func TestRunApplyUsageErrors(t *testing.T) {
	for _, tt := range []struct {
		name string
		args []string
		want string
	}{
		{name: "without --dry-run", args: []string{"apply", "--profile", "web", "--json"}, want: "not available yet"},
		{name: "without a profile", args: []string{"apply", "--dry-run", "--json"}, want: "--profile"},
		{name: "two paths", args: []string{"apply", "a", "b", "--profile", "web", "--dry-run", "--json"}, want: "arg"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var stdout bytes.Buffer
			env := testEnv(&stdout, &bytes.Buffer{})
			env.PlanProject = func(context.Context, string, string, string, string, app.ApplyOptions, app.Progress) (app.ProjectPlan, error) {
				t.Error("PlanProject called on a usage error")
				return app.ProjectPlan{}, nil
			}
			if code := cli.Run(tt.args, env); code != cli.ExitUsage {
				t.Errorf("exit code = %d, want %d", code, cli.ExitUsage)
			}
			if e := decodeOne(t, stdout.Bytes()); e.OK || e.Command != "apply" || e.Error == nil || e.Error.Code != output.CodeUsage || !strings.Contains(e.Error.Message, tt.want) {
				t.Errorf("envelope = %+v, want a usage error about %s", e, tt.want)
			}
		})
	}
}

func TestRunApplyRefusesTheHomeDirectory(t *testing.T) {
	var stdout bytes.Buffer
	env := testEnv(&stdout, &bytes.Buffer{})
	env.PlanProject = func(context.Context, string, string, string, string, app.ApplyOptions, app.Progress) (app.ProjectPlan, error) {
		return app.ProjectPlan{}, &app.ProjectRefusedError{Dir: "/home/u", Why: "the home directory"}
	}
	if code := cli.Run([]string{"apply", "--profile", "web", "--dry-run", "--json"}, env); code != cli.ExitInternal {
		t.Errorf("exit code = %d, want %d", code, cli.ExitInternal)
	}
	if e := decodeOne(t, stdout.Bytes()); e.OK || e.Error == nil || e.Error.Code != output.CodeProjectRefused || !strings.Contains(e.Error.Message, "/home/u is the home directory") {
		t.Errorf("envelope = %+v, want project-refused", e)
	}
}

func TestRunApplyInvalidPack(t *testing.T) {
	invalid := projectPlan
	invalid.Ops, invalid.Skipped, invalid.Warnings = []app.ProjectOp{}, []app.SkippedItem{}, []app.ProjectWarning{}
	invalid.Diagnostics = []app.Diagnostic{{File: "dotai.yaml", Code: "layer-selection", Message: `profile "nope" is not defined`}}
	var stdout, stderr bytes.Buffer
	env := testEnv(&stdout, &stderr)
	env.PlanProject = func(context.Context, string, string, string, string, app.ApplyOptions, app.Progress) (app.ProjectPlan, error) {
		return invalid, nil
	}
	if code := cli.Run([]string{"apply", "p", "--profile", "nope", "--dry-run"}, env); code != cli.ExitInvalid || stderr.Len() != 0 || !strings.HasPrefix(stdout.String(), "✗ minimal-pack 0.1.0 · 1 problem") {
		t.Errorf("exit code = %d, stdout = %q, stderr = %q, want the report and %d", code, stdout.String(), stderr.String(), cli.ExitInvalid)
	}
}

func TestRunApplyOutsideARepository(t *testing.T) {
	pending := projectPlan
	pending.Exclude = app.ExcludePlan{Pending: true, Entries: []string{"/.dotai/"}}
	var stdout bytes.Buffer
	env := testEnv(&stdout, &bytes.Buffer{})
	env.PlanProject = func(context.Context, string, string, string, string, app.ApplyOptions, app.Progress) (app.ProjectPlan, error) {
		return pending, nil
	}
	if code := cli.Run([]string{"apply", "--profile", "web", "--dry-run"}, env); code != cli.ExitOK {
		t.Fatalf("exit code = %d", code)
	}
	if !strings.Contains(stdout.String(), "\nexclude · pending, not in a git repository\n  /.dotai/\n") {
		t.Errorf("stdout = %q, want the pending entries", stdout.String())
	}
}
