package cli_test

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/lartweib/dotai/internal/app"
	"github.com/lartweib/dotai/internal/cli"
	"github.com/lartweib/dotai/internal/envelope"
	"github.com/lartweib/dotai/internal/plan"
)

// projectPlan is a project plan with fixed paths, so that its output reads
// the same on every OS.
var projectPlan = app.ProjectPlan{
	Pack:    &app.PackSummary{Name: "minimal-pack", Version: "0.1.0", Harnesses: []string{"claudecode", "antigravity-cli", "opencode"}, Requires: []string{}, Profiles: []app.ProfileSummary{}, Variants: []string{}, SharedSkills: []string{}, Layers: []app.LayerSummary{}},
	Variant: "standalone", Profiles: []string{"web"}, Project: "/work/site", Record: "/home/u/.dotai/projects/0123456789abcdef/record.json", Marker: "/work/site/.dotai/state.json",
	Harnesses: []string{"claudecode", "antigravity-cli", "opencode"},
	HarnessStates: []app.HarnessState{
		{ID: "claudecode", Compatible: true, Status: "supported", Managed: true, Detection: app.Detection{Detected: true, Binary: "/usr/bin/claude", Config: "/home/u/.claude", ConfigFound: true}},
		{ID: "antigravity-cli", Compatible: true, Status: "supported", Managed: true, Detection: app.Detection{Detected: true, Binary: "/usr/bin/agy", Config: "/home/u/.gemini"}},
		{ID: "opencode", Compatible: true, Status: "preview", StatusReason: "the hooks plugin hangs", Managed: true, Detection: app.Detection{Detected: true, Config: "/home/u/.config/opencode", ConfigFound: true}},
	},
	Ops: []plan.ProjectOp{
		{Action: plan.Create, Harness: "claudecode", Kind: "mcp", Source: ".mcp.json", Target: "/home/u/.claude.json",
			Keys: []plan.ProjectKey{{Path: []string{"projects", "/work/site", "mcpServers", "browser"}, Action: plan.Create}}},
		{Action: plan.Create, Harness: "antigravity-cli", Kind: "hooks", Source: ".agents/hooks.json", Target: "/work/site/.agents/hooks.json",
			Keys: []plan.ProjectKey{{Path: []string{"rulesync", "SessionStart"}, Action: plan.Create}}},
		{Action: plan.Conflict, Harness: "antigravity-cli", Kind: "rules", Source: ".agents/rules/web.md", Target: "/work/site/.agents/rules/web.md", Before: "aa", After: "bb", Keys: []plan.ProjectKey{}},
		{Action: plan.Replace, Harness: "claudecode", Kind: "commands", Source: ".claude/commands/deploy.md", Target: "/work/site/.claude/commands/deploy.md", Before: "ff", After: "gg", Keys: []plan.ProjectKey{}},
		{Action: plan.Create, Harness: "claudecode", Kind: "rules", Source: ".claude/rules/web.md", Target: "/work/site/.claude/rules/web.md", After: "bb", Keys: []plan.ProjectKey{}},
		{Action: plan.Merge, Harness: "claudecode", Kind: "settings", Source: ".claude/settings.json", Target: "/work/site/.claude/settings.local.json", Before: "cc",
			Keys: []plan.ProjectKey{{Path: []string{"permissions", "allow"}, Action: plan.Merge}, {Path: []string{"model"}, Action: plan.Unchanged}}},
		{Action: plan.Create, Harness: "claudecode", Kind: "mods", Source: "mods/hello/hooks/hooks.json", Target: "/work/site/.claude/skills/hello/hooks/hooks.json", After: "dd", Keys: []plan.ProjectKey{}},
		{Action: plan.Create, Harness: "claudecode", Kind: "mods", Source: "mods/hello/hooks/register.js", Target: "/work/site/.claude/skills/hello/hooks/register.js", After: "ee", Keys: []plan.ProjectKey{}},
		{Action: plan.Create, Kind: "marker", Target: "/work/site/.dotai/state.json", Keys: []plan.ProjectKey{}},
		{Action: plan.Unchanged, Harness: "opencode", Kind: "rules", Source: ".opencode/memories/web.md", Target: "/work/site/.opencode/memories/web.md", Before: "bb", After: "bb", Keys: []plan.ProjectKey{}},
	},
	Skipped: []plan.Skipped{
		{Harness: "claudecode", Kind: "skills", Item: "skill lint", Target: "/work/site/.claude/skills/lint/SKILL.md", Reason: plan.SkipTracked},
		{Harness: "opencode", Kind: "hooks", Item: "hook sessionStart: echo hi", Target: "/work/site/.opencode/plugins/rulesync-hooks.js", Reason: plan.SkipTracked},
	},
	Exclude: app.ExcludePlan{File: "/work/site/.git/info/exclude", Entries: []string{"/.dotai/", "/.agents/hooks.json", "/.claude/rules/web.md", "/.claude/skills/hello/"}},
	Warnings: []plan.Warning{
		{Code: plan.CodeScopeClash, Harness: "claudecode", Item: "skill lint", Path: "/home/u/.claude/skills/lint", Message: "claudecode loads the user-level skill lint from /home/u/.claude/skills/lint instead of the project's"},
		{Code: plan.CodeNoLocalSlot, Harness: "antigravity-cli", Message: "antigravity-cli has no project settings file, so the permissions of the profiles are not installed for it"},
		{Code: plan.CodeNoLocalSlot, Harness: "opencode", Item: "mods", Message: "opencode has no local place for project mods, so those of the profiles are not installed for it"},
	},
	Refusals:    []plan.Warning{},
	FirstApply:  true,
	Notices:     []string{"the mod hello loads in Claude Code 2.1.287 or later, in a terminal, only in a session started at /work/site, and only after you accept the workspace trust dialog there"},
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

// TestRunApplyDryRunShowsAHarnessItDoesNotPlan checks the text of a
// harness the apply does not plan (decision 0047), as an install prints
// it: a preview harness the install's choice leaves out, a compatible one
// that is not installed and one the pack is not compatible with, each with
// its state and detection, among the harnesses it plans.
func TestRunApplyDryRunShowsAHarnessItDoesNotPlan(t *testing.T) {
	p := projectPlan
	p.Harnesses = []string{"claudecode"}
	p.HarnessStates = []app.HarnessState{p.HarnessStates[0],
		{ID: "opencode", Compatible: true, Status: "preview", StatusReason: "r", Detection: app.Detection{Detected: true, Config: "/home/u/.config/opencode", ConfigFound: true}},
		{ID: "antigravity-cli", Compatible: true, Required: true, Status: "supported", Detection: app.Detection{Config: "/home/u/.gemini"}},
		{ID: "x", Status: "supported", Detection: app.Detection{Detected: true, Binary: "/usr/bin/x", Config: "/home/u/.x", ConfigFound: true}},
	}
	p.Ops = slices.DeleteFunc(slices.Clone(p.Ops), func(op plan.ProjectOp) bool { return op.Harness != "claudecode" && op.Harness != "" })
	p.Skipped = []plan.Skipped{}
	var stdout bytes.Buffer
	env := testEnv(&stdout, &bytes.Buffer{})
	env.PlanProject = func(context.Context, string, string, string, string, app.ApplyOptions, app.Progress) (app.ProjectPlan, error) {
		return p, nil
	}
	if code := cli.Run([]string{"apply", "--profile", "web", "--dry-run"}, env); code != cli.ExitOK {
		t.Fatalf("exit code = %d", code)
	}
	out := stdout.String()
	for _, want := range []string{
		"\nclaudecode\n  create     /home/u/.claude.json\n",
		"\nopencode · preview, not managed\n  binary: not found on PATH\n  config: /home/u/.config/opencode\n",
		"\nantigravity-cli · required, not managed\n  binary: not found on PATH\n  config: /home/u/.gemini (missing)\n",
		"\nx · not compatible with the pack, not managed\n  binary: /usr/bin/x\n",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("stdout = %q, want it to contain %q", out, want)
		}
	}
	if i, j := strings.Index(out, "\nopencode ·"), strings.Index(out, "\nx ·"); i < 0 || j < i {
		t.Errorf("stdout = %q, want the harnesses in the order of the plan", out)
	}
}

func TestRunApplyUsageErrors(t *testing.T) {
	for _, tt := range []struct {
		name string
		args []string
		want string
	}{
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
			if e := decodeOne(t, stdout.Bytes()); e.OK || e.Command != "apply" || e.Error == nil || e.Error.Code != envelope.CodeUsage || !strings.Contains(e.Error.Message, tt.want) {
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
	if e := decodeOne(t, stdout.Bytes()); e.OK || e.Error == nil || e.Error.Code != envelope.CodeProjectRefused || !strings.Contains(e.Error.Message, "/home/u is the home directory") {
		t.Errorf("envelope = %+v, want project-refused", e)
	}
}

func TestRunApplyInvalidPack(t *testing.T) {
	invalid := projectPlan
	invalid.Ops, invalid.Skipped, invalid.Warnings = []plan.ProjectOp{}, []plan.Skipped{}, []plan.Warning{}
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

func TestRunApply(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("DOTAI_CACHE_DIR", "cache")
	for _, tt := range []struct {
		name, golden string
		args         []string
		first        bool
		backup       *app.BackupRef
	}{
		{name: "text", args: []string{"apply", "--profile", "web"}, golden: "apply.txt.golden", first: true,
			backup: &app.BackupRef{ID: "20261102T101500.000Z_apply", Path: "/home/u/.dotai/projects/0123456789abcdef/backups/20261102T101500.000Z_apply"}},
		{name: "second", args: []string{"apply", "--profile", "web"}, golden: "apply-again.txt.golden"},
		{name: "json", args: []string{"apply", "--profile", "web", "--json"}, golden: "apply.json.golden", first: true,
			backup: &app.BackupRef{ID: "20261102T101500.000Z_apply", Path: "/home/u/.dotai/projects/0123456789abcdef/backups/20261102T101500.000Z_apply"}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			env := testEnv(&stdout, &stderr)
			env.Apply = func(ctx context.Context, _, _, _, _ string, opts app.ApplyOptions, _ app.Progress, consent app.ApplyConsent) (app.ApplyResult, error) {
				if ok, err := consent(projectPlan); ok || err != nil {
					t.Errorf("consent without --yes or a terminal = %v, %v, want false", ok, err)
				}
				p := projectPlan
				p.FirstApply = tt.first
				return app.ApplyResult{ProjectPlan: p, Backup: tt.backup}, nil
			}
			if code := cli.Run(tt.args, env); code != cli.ExitOK || stderr.Len() != 0 {
				t.Errorf("exit code = %d, stderr = %q, want 0 and empty", code, stderr.String())
			}
			assertGolden(t, filepath.Join("testdata", tt.golden), stdout.Bytes())
		})
	}
}

func TestRunApplyRefusals(t *testing.T) {
	refused := projectPlan
	refused.Refusals = []plan.Warning{{Code: plan.CodeMCPServerForeign, Harness: "claudecode", Item: "MCP server browser", Path: "/home/u/.claude.json", Message: "/home/u/.claude.json already has an MCP server browser"}}
	for _, tt := range []struct {
		name string
		err  error
		code string
	}{
		{name: "consent", err: &app.ApplyConsentError{Plan: projectPlan, Files: 1}, code: envelope.CodeConsent},
		{name: "refused", err: &app.ApplyRefusedError{Plan: refused}, code: envelope.CodeApplyRefused},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var stdout bytes.Buffer
			env := testEnv(&stdout, &bytes.Buffer{})
			env.Apply = func(context.Context, string, string, string, string, app.ApplyOptions, app.Progress, app.ApplyConsent) (app.ApplyResult, error) {
				return app.ApplyResult{}, tt.err
			}
			if code := cli.Run([]string{"apply", "--profile", "web", "--json"}, env); code != cli.ExitInternal {
				t.Errorf("exit code = %d, want %d", code, cli.ExitInternal)
			}
			e := decodeOne(t, stdout.Bytes())
			if e.OK || e.Error == nil || e.Error.Code != tt.code || !strings.Contains(stdout.String(), `"data": {`) {
				t.Errorf("envelope = %+v, want %s with the plan", e, tt.code)
			}
		})
	}
	// In text, the plan explains the refusal, and the error goes to stderr.
	var stdout, stderr bytes.Buffer
	env := testEnv(&stdout, &stderr)
	env.Apply = func(context.Context, string, string, string, string, app.ApplyOptions, app.Progress, app.ApplyConsent) (app.ApplyResult, error) {
		return app.ApplyResult{}, &app.ApplyRefusedError{Plan: refused}
	}
	if code := cli.Run([]string{"apply", "--profile", "web"}, env); code != cli.ExitInternal ||
		!strings.Contains(stdout.String(), "\nrefused\n  /home/u/.claude.json already has an MCP server browser\n") || !strings.Contains(stdout.String(), "refused, the apply wrote nothing") ||
		!strings.Contains(stderr.String(), "the apply wrote nothing") {
		t.Errorf("exit code = %d, stdout = %q, stderr = %q, want the plan with its refusal, and the error", code, stdout.String(), stderr.String())
	}
}

func TestRunApplyAsksOnATerminal(t *testing.T) {
	for _, answer := range []bool{true, false} {
		var stdout bytes.Buffer
		env := testEnv(&stdout, &bytes.Buffer{})
		env.Prompt = func(context.Context, string) (bool, error) { return answer, nil }
		env.Apply = func(_ context.Context, _, _, _, _ string, _ app.ApplyOptions, _ app.Progress, consent app.ApplyConsent) (app.ApplyResult, error) {
			if ok, _ := consent(projectPlan); !ok {
				return app.ApplyResult{}, &app.ApplyConsentError{Plan: projectPlan, Files: 1}
			}
			return app.ApplyResult{ProjectPlan: projectPlan}, nil
		}
		code := cli.Run([]string{"apply", "--profile", "web"}, env)
		if answer && code != cli.ExitOK || !answer && code != cli.ExitInternal || !strings.Contains(stdout.String(), "nothing was written yet") {
			t.Errorf("answer %v: exit code = %d, stdout = %q", answer, code, stdout.String())
		}
	}
	var stdout bytes.Buffer
	env := testEnv(&stdout, &bytes.Buffer{})
	env.Apply = func(_ context.Context, _, _, _, _ string, _ app.ApplyOptions, _ app.Progress, consent app.ApplyConsent) (app.ApplyResult, error) {
		if ok, _ := consent(projectPlan); !ok {
			t.Error("consent with --yes = false, want true")
		}
		return app.ApplyResult{ProjectPlan: projectPlan}, nil
	}
	if code := cli.Run([]string{"apply", "--profile", "web", "--yes"}, env); code != cli.ExitOK {
		t.Errorf("--yes: exit code = %d", code)
	}
}

func TestRunApplyRefusesAnInvalidRecord(t *testing.T) {
	var stdout bytes.Buffer
	env := testEnv(&stdout, &bytes.Buffer{})
	env.Apply = func(context.Context, string, string, string, string, app.ApplyOptions, app.Progress, app.ApplyConsent) (app.ApplyResult, error) {
		return app.ApplyResult{}, &app.RecordInvalidError{Path: "/home/u/.dotai/projects/0123456789abcdef/record.json", Why: "owns /home/u/.ssh/authorized_keys"}
	}
	if code := cli.Run([]string{"apply", "--profile", "web", "--json"}, env); code != cli.ExitInternal {
		t.Errorf("exit code = %d, want %d", code, cli.ExitInternal)
	}
	if e := decodeOne(t, stdout.Bytes()); e.Error == nil || e.Error.Code != envelope.CodeRecordInvalid {
		t.Errorf("envelope = %+v, want record-invalid", e)
	}
}
