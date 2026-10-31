package cli_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lartweib/dotai/internal/app"
	"github.com/lartweib/dotai/internal/cli"
	"github.com/lartweib/dotai/internal/cli/output"
	"github.com/lartweib/dotai/internal/plan"
)

// installPlan is a plan with fixed paths, so that its output reads the
// same on every OS.
var installPlan = app.InstallPlan{
	Pack:    &app.PackSummary{Name: "minimal-pack", Version: "0.1.0", Harnesses: []string{"claudecode", "antigravity-cli", "opencode"}, Profiles: []string{}, Variants: []string{}, SharedSkills: []string{}, Layers: []app.LayerSummary{}},
	Variant: "standalone", Mode: app.ModeIsolated, Root: "/home/u/.dotai/isolated",
	Harnesses: []app.HarnessPlan{
		{ID: "claudecode", Level: "partial", Dir: "/home/u/.dotai/isolated/claude", Relogin: "unknown",
			Pointers: []app.PointerPlan{{Mechanism: "env", Name: "CLAUDE_CONFIG_DIR", Dir: "/home/u/.dotai/isolated/claude"}},
			Leaks:    []string{"~/.claude/CLAUDE.md still loads when the working directory is under the home directory"}, Tools: []app.ToolCommand{}},
		{ID: "antigravity-cli", Level: "partial", Dir: "/home/u/.dotai/isolated/gemini", Relogin: "no",
			Pointers: []app.PointerPlan{{Mechanism: "flag", Name: "--gemini_dir", Dir: "/home/u/.dotai/isolated/gemini"}},
			Leaks:    []string{"authentication is shared through the OS keyring"},
			Tools:    []app.ToolCommand{{Key: "statusLine", Tool: "hello", Command: `sh /home/u/.dotai/tools/hello.sh`}}},
		{ID: "opencode", Level: "partial", Dir: "/home/u/.dotai/isolated/opencode", Relogin: "yes",
			Pointers: []app.PointerPlan{
				{Mechanism: "env", Name: "XDG_CONFIG_HOME", Dir: "/home/u/.dotai/isolated/opencode/config"},
				{Mechanism: "env", Name: "XDG_DATA_HOME", Dir: "/home/u/.dotai/isolated/opencode/data"},
			},
			Leaks: []string{}, Tools: []app.ToolCommand{}},
	},
	Ops: []app.InstallOp{
		{Action: plan.Create, Harness: "claudecode", Source: ".claude/CLAUDE.md", Target: "/home/u/.dotai/isolated/claude/CLAUDE.md", After: "aa", Keys: []plan.KeyChange{}},
		{Action: plan.Replace, Harness: "claudecode", Source: ".claude/settings.json", Target: "/home/u/.dotai/isolated/claude/settings.json", Before: "bb", After: "b2",
			Keys: []plan.KeyChange{{Key: "env.DOTAI_TERMINAL", Action: plan.Create}, {Key: "model", Action: plan.Replace}}},
		{Action: plan.Replace, Harness: "antigravity-cli", Source: ".gemini/GEMINI.md", Target: "/home/u/.dotai/isolated/gemini/GEMINI.md", Before: "cc", After: "dd", Keys: []plan.KeyChange{}},
		{Action: plan.Create, Source: ".dotai/tools/hello.sh", Target: "/home/u/.dotai/tools/hello.sh", Exec: true, After: "ee", Keys: []plan.KeyChange{}},
	},
	Diagnostics: []app.Diagnostic{},
}

func TestRunInstallDryRun(t *testing.T) {
	t.Setenv("DOTAI_CACHE_DIR", "cache")
	wantCache, err := filepath.Abs("cache")
	if err != nil {
		t.Fatal(err)
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	for _, tt := range []struct {
		name         string
		args         []string
		golden       string
		wantVariant  string
		wantProgress bool
	}{
		{name: "text", args: []string{"install", examplePack, "--dry-run"}, golden: "install-dry-run.txt.golden", wantProgress: true},
		{name: "json", args: []string{"install", examplePack, "--dry-run", "--variant", "tmux", "--json"}, golden: "install-dry-run.json.golden", wantVariant: "tmux"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			env := testEnv(&stdout, &stderr)
			env.PlanInstall = func(ctx context.Context, cacheDir, dir, variant, gotHome string, progress app.Progress) (app.InstallPlan, error) {
				if ctx == nil || cacheDir != wantCache || dir != examplePack || variant != tt.wantVariant || gotHome != home || (progress != nil) != tt.wantProgress {
					t.Errorf("PlanInstall(%q, %q, %q, %q, progress %v), want the cache, the pack, %q, the home and progress %v",
						cacheDir, dir, variant, gotHome, progress != nil, tt.wantVariant, tt.wantProgress)
				}
				return installPlan, nil
			}
			if code := cli.Run(tt.args, env); code != cli.ExitOK || stderr.Len() != 0 {
				t.Errorf("exit code = %d, stderr = %q, want 0 and empty", code, stderr.String())
			}
			assertGolden(t, filepath.Join("testdata", tt.golden), stdout.Bytes())
		})
	}
}

func TestRunInstallJSONShape(t *testing.T) {
	var stdout bytes.Buffer
	env := testEnv(&stdout, &bytes.Buffer{})
	env.PlanInstall = func(context.Context, string, string, string, string, app.Progress) (app.InstallPlan, error) {
		return installPlan, nil
	}
	if code := cli.Run([]string{"install", "--dry-run", "--json"}, env); code != cli.ExitOK {
		t.Fatalf("exit code = %d", code)
	}
	var e struct {
		Command string `json:"command"`
		Data    struct {
			Mode      string                       `json:"mode"`
			Root      string                       `json:"root"`
			Harnesses []map[string]json.RawMessage `json:"harnesses"`
			Ops       []map[string]json.RawMessage `json:"ops"`
		} `json:"data"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &e); err != nil {
		t.Fatal(err)
	}
	if e.Command != "install" || e.Data.Mode != "isolated" || e.Data.Root == "" || len(e.Data.Harnesses) != 3 || len(e.Data.Ops) != 4 {
		t.Fatalf("envelope = %+v, want install with the plan", e)
	}
	for _, key := range []string{"id", "level", "dir", "pointers", "relogin", "leaks"} {
		if _, ok := e.Data.Harnesses[0][key]; !ok {
			t.Errorf("harness has no %q: %v", key, e.Data.Harnesses[0])
		}
	}
	for _, key := range []string{"action", "harness", "source", "target", "exec", "before", "after", "beforeExec"} {
		if _, ok := e.Data.Ops[3][key]; !ok {
			t.Errorf("op has no %q: %v", key, e.Data.Ops[3])
		}
	}
}

func TestRunInstallUsageErrors(t *testing.T) {
	for _, tt := range []struct {
		name string
		args []string
		want string
	}{
		{name: "with a profile", args: []string{"install", examplePack, "--dry-run", "--profile", "web", "--json"}, want: "profile"},
		{name: "two paths", args: []string{"install", "a", "b", "--dry-run", "--json"}, want: "arg"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var stdout bytes.Buffer
			env := testEnv(&stdout, &bytes.Buffer{})
			env.PlanInstall = func(context.Context, string, string, string, string, app.Progress) (app.InstallPlan, error) {
				t.Error("PlanInstall called on a usage error")
				return app.InstallPlan{}, nil
			}
			env.Install = func(context.Context, string, string, string, string, app.Progress, app.Warn) (app.InstallResult, error) {
				t.Error("Install called on a usage error")
				return app.InstallResult{}, nil
			}
			if code := cli.Run(tt.args, env); code != cli.ExitUsage {
				t.Errorf("exit code = %d, want %d", code, cli.ExitUsage)
			}
			if e := decodeOne(t, stdout.Bytes()); e.OK || e.Command != "install" || e.Error == nil || e.Error.Code != output.CodeUsage || !strings.Contains(e.Error.Message, tt.want) {
				t.Errorf("envelope = %+v, want a usage error about %s", e, tt.want)
			}
		})
	}
	// A profile is refused without --dry-run too.
	var stderr bytes.Buffer
	if code := cli.Run([]string{"install", examplePack, "--profile", "web"}, testEnv(&bytes.Buffer{}, &stderr)); code != cli.ExitUsage || !strings.Contains(stderr.String(), "profile") {
		t.Errorf("exit code = %d, stderr = %q, want a usage error about the profile", code, stderr.String())
	}
}

func TestRunInstallInvalidPack(t *testing.T) {
	invalid := app.InstallPlan{
		Pack: &app.PackSummary{Name: "p", Version: "1.0.0"}, Mode: app.ModeIsolated, Root: "/home/u/.dotai/isolated",
		Harnesses: []app.HarnessPlan{}, Ops: []app.InstallOp{},
		Diagnostics: []app.Diagnostic{{File: "dotai.yaml", Code: "layer-selection", Message: "variant \"nope\" is not defined"}},
	}
	for _, jsonOut := range []bool{false, true} {
		var stdout, stderr bytes.Buffer
		env := testEnv(&stdout, &stderr)
		env.PlanInstall = func(context.Context, string, string, string, string, app.Progress) (app.InstallPlan, error) {
			return invalid, nil
		}
		args := []string{"install", "p", "--dry-run", "--variant", "nope"}
		if jsonOut {
			args = append(args, "--json")
		}
		if code := cli.Run(args, env); code != cli.ExitInvalid || stderr.Len() != 0 {
			t.Errorf("json %v: exit code = %d, stderr = %q, want %d and empty", jsonOut, code, stderr.String(), cli.ExitInvalid)
		}
		if !jsonOut && !strings.HasPrefix(stdout.String(), "✗ p 1.0.0 · 1 problem") {
			t.Errorf("stdout = %q, want the diagnostics report", stdout.String())
		}
		if jsonOut {
			if e := decodeOne(t, stdout.Bytes()); e.OK || e.Error == nil || e.Error.Code != output.CodeInvalid {
				t.Errorf("envelope = %+v, want invalid", e)
			}
		}
	}
}

func TestRunInstallFailure(t *testing.T) {
	var stdout, stderr bytes.Buffer
	env := testEnv(&stdout, &stderr)
	env.PlanInstall = func(context.Context, string, string, string, string, app.Progress) (app.InstallPlan, error) {
		return app.InstallPlan{}, &app.NotRegularError{Path: "/home/u/.dotai/tools"}
	}
	if code := cli.Run([]string{"install", "--dry-run"}, env); code != cli.ExitInternal || stdout.Len() != 0 || !strings.Contains(stderr.String(), "/home/u/.dotai/tools") {
		t.Errorf("exit code = %d, stdout = %q, stderr = %q, want an internal error naming the path", code, stdout.String(), stderr.String())
	}
}

func TestRunInstallWithoutAHomeDirectory(t *testing.T) {
	t.Setenv("HOME", "")
	t.Setenv("USERPROFILE", "")
	t.Setenv("home", "") // Plan 9
	t.Setenv("DOTAI_CACHE_DIR", t.TempDir())
	var stderr bytes.Buffer
	env := testEnv(&bytes.Buffer{}, &stderr)
	env.PlanInstall = func(context.Context, string, string, string, string, app.Progress) (app.InstallPlan, error) {
		t.Error("PlanInstall called without a home directory")
		return app.InstallPlan{}, errors.New("unreachable")
	}
	if code := cli.Run([]string{"install", "--dry-run"}, env); code != cli.ExitInternal || !strings.Contains(stderr.String(), "home directory") {
		t.Errorf("exit code = %d, stderr = %q, want an internal error about the home directory", code, stderr.String())
	}
}

// installResult is an install of installPlan that also removed a file the
// pack dropped.
var installResult = func() app.InstallResult {
	p := installPlan
	p.Ops = append(append([]app.InstallOp{}, installPlan.Ops[:2]...),
		app.InstallOp{Action: plan.Remove, Harness: "claudecode", Target: "/home/u/.dotai/isolated/claude/rules/old.md", Before: "ff", Keys: []plan.KeyChange{}})
	p.Ops = append(p.Ops, installPlan.Ops[2:]...)
	return app.InstallResult{
		InstallPlan: p,
		Backup:      &app.BackupRef{ID: "20261029T101500.000Z_install", Path: "/home/u/.dotai/backups/20261029T101500.000Z_install"},
		State:       "/home/u/.dotai/state.json",
		Warnings:    []string{},
	}
}()

func TestRunInstall(t *testing.T) {
	t.Setenv("DOTAI_CACHE_DIR", "cache")
	wantCache, err := filepath.Abs("cache")
	if err != nil {
		t.Fatal(err)
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	for _, tt := range []struct {
		name         string
		args         []string
		golden       string
		wantVariant  string
		wantProgress bool
	}{
		{name: "text", args: []string{"install", examplePack}, golden: "install.txt.golden", wantProgress: true},
		{name: "json", args: []string{"install", examplePack, "--variant", "tmux", "--json"}, golden: "install.json.golden", wantVariant: "tmux"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			env := testEnv(&stdout, &stderr)
			env.PlanInstall = func(context.Context, string, string, string, string, app.Progress) (app.InstallPlan, error) {
				t.Error("PlanInstall called without --dry-run")
				return app.InstallPlan{}, nil
			}
			env.Install = func(ctx context.Context, cacheDir, dir, variant, gotHome string, progress app.Progress, warn app.Warn) (app.InstallResult, error) {
				if ctx == nil || cacheDir != wantCache || dir != examplePack || variant != tt.wantVariant || gotHome != home || (progress != nil) != tt.wantProgress {
					t.Errorf("Install(%q, %q, %q, %q, progress %v), want the cache, the pack, %q, the home and progress %v",
						cacheDir, dir, variant, gotHome, progress != nil, tt.wantVariant, tt.wantProgress)
				}
				return installResult, nil
			}
			if code := cli.Run(tt.args, env); code != cli.ExitOK || stderr.Len() != 0 {
				t.Errorf("exit code = %d, stderr = %q, want 0 and empty", code, stderr.String())
			}
			assertGolden(t, filepath.Join("testdata", tt.golden), stdout.Bytes())
		})
	}
}

func TestRunInstallJSONShapeWithoutDryRun(t *testing.T) {
	var stdout bytes.Buffer
	env := testEnv(&stdout, &bytes.Buffer{})
	env.Install = func(context.Context, string, string, string, string, app.Progress, app.Warn) (app.InstallResult, error) {
		return installResult, nil
	}
	if code := cli.Run([]string{"install", "--json"}, env); code != cli.ExitOK {
		t.Fatalf("exit code = %d", code)
	}
	var e struct {
		Command string `json:"command"`
		Data    struct {
			Mode   string                       `json:"mode"`
			Ops    []map[string]json.RawMessage `json:"ops"`
			Backup *struct {
				ID   string `json:"id"`
				Path string `json:"path"`
			} `json:"backup"`
			State string `json:"state"`
		} `json:"data"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &e); err != nil {
		t.Fatal(err)
	}
	if e.Command != "install" || e.Data.Mode != "isolated" || len(e.Data.Ops) != 5 || e.Data.Backup == nil ||
		e.Data.Backup.ID != installResult.Backup.ID || e.Data.Backup.Path != installResult.Backup.Path || e.Data.State != installResult.State {
		t.Errorf("envelope = %+v, want the plan with the backup and the state", e)
	}
	if string(e.Data.Ops[2]["action"]) != `"remove"` || string(e.Data.Ops[2]["source"]) != `""` {
		t.Errorf("op 2 = %v, want a remove without a source", e.Data.Ops[2])
	}
}

func TestRunInstallThatWroteNothing(t *testing.T) {
	var stdout bytes.Buffer
	env := testEnv(&stdout, &bytes.Buffer{})
	res := app.InstallResult{InstallPlan: installPlan, State: "/home/u/.dotai/state.json"}
	env.Install = func(context.Context, string, string, string, string, app.Progress, app.Warn) (app.InstallResult, error) {
		return res, nil
	}
	if code := cli.Run([]string{"install"}, env); code != cli.ExitOK || !strings.HasSuffix(stdout.String(), "installed\nbackup: none, no file was written\nstate:  /home/u/.dotai/state.json\n") {
		t.Errorf("exit code = %d, stdout = %q, want no backup", code, stdout.String())
	}
	stdout.Reset()
	if code := cli.Run([]string{"install", "--json"}, env); code != cli.ExitOK || !strings.Contains(stdout.String(), `"backup": null`) {
		t.Errorf("exit code = %d, stdout = %s, want a null backup", code, stdout.String())
	}
}

func TestRunInstallInvalidPackWithoutDryRun(t *testing.T) {
	var stdout, stderr bytes.Buffer
	env := testEnv(&stdout, &stderr)
	env.Install = func(context.Context, string, string, string, string, app.Progress, app.Warn) (app.InstallResult, error) {
		return app.InstallResult{InstallPlan: app.InstallPlan{
			Pack: &app.PackSummary{Name: "p", Version: "1.0.0"}, Mode: app.ModeIsolated, Harnesses: []app.HarnessPlan{}, Ops: []app.InstallOp{},
			Diagnostics: []app.Diagnostic{{File: "dotai.yaml", Code: "layer-selection", Message: "variant \"nope\" is not defined"}},
		}}, nil
	}
	if code := cli.Run([]string{"install", "p", "--variant", "nope", "--json"}, env); code != cli.ExitInvalid || stderr.Len() != 0 {
		t.Errorf("exit code = %d, stderr = %q, want %d and empty", code, stderr.String(), cli.ExitInvalid)
	}
	if e := decodeOne(t, stdout.Bytes()); e.OK || e.Error == nil || e.Error.Code != output.CodeInvalid {
		t.Errorf("envelope = %+v, want invalid", e)
	}
}

func TestRunInstallFailureWithoutDryRun(t *testing.T) {
	for _, err := range []error{
		&app.LockedError{Path: "/home/u/.dotai/lock"},
		&app.InstallError{Cause: &app.ChangedError{Path: "/home/u/.dotai/tools/hello.sh"}, Backup: "/home/u/.dotai/backups/x"},
		&plan.EditedError{Targets: []string{"/home/u/.dotai/isolated/claude/CLAUDE.md"}},
	} {
		var stdout, stderr bytes.Buffer
		env := testEnv(&stdout, &stderr)
		env.Install = func(context.Context, string, string, string, string, app.Progress, app.Warn) (app.InstallResult, error) {
			return app.InstallResult{}, err
		}
		if code := cli.Run([]string{"install"}, env); code != cli.ExitInternal || stdout.Len() != 0 || !strings.Contains(stderr.String(), err.Error()) {
			t.Errorf("exit code = %d, stdout = %q, stderr = %q, want an internal error with %q", code, stdout.String(), stderr.String(), err)
		}
	}
}

// TestRunInstallWarnsBeforeItWrites checks that the warning to close a
// harness is printed when the install gives it, before it writes and so
// before the plan, once, while the other warnings follow the summary; with
// --json it is only in data.
func TestRunInstallWarnsBeforeItWrites(t *testing.T) {
	const closing = "close claudecode before installing: a harness that is running may rewrite its settings file while dotai writes its keys there"
	res := installResult
	res.Warnings = []string{closing, "older backups could not be pruned: busy"}
	for _, jsonOut := range []bool{false, true} {
		var out bytes.Buffer // stdout and stderr, in the order they are written
		env := testEnv(&out, &out)
		env.Install = func(_ context.Context, _, _, _, _ string, _ app.Progress, warn app.Warn) (app.InstallResult, error) {
			if (warn == nil) != jsonOut {
				t.Errorf("json %v: warn = %v, want one only for text", jsonOut, warn != nil)
			}
			if warn != nil {
				warn(closing)
			}
			return res, nil
		}
		args := []string{"install"}
		if jsonOut {
			args = append(args, "--json")
		}
		if code := cli.Run(args, env); code != cli.ExitOK {
			t.Fatalf("json %v: exit code = %d", jsonOut, code)
		}
		got := out.String()
		if jsonOut {
			if !strings.Contains(got, closing) || strings.Contains(got, "warning: ") {
				t.Errorf("output = %s, want the warning in data only", got)
			}
			continue
		}
		if !strings.HasPrefix(got, "warning: "+closing+"\nInstall plan") || strings.Count(got, closing) != 1 ||
			!strings.HasSuffix(got, "state:  /home/u/.dotai/state.json\nwarning: older backups could not be pruned: busy\n") {
			t.Errorf("output = %q, want the close warning first and once, and the other one after the summary", got)
		}
	}
}

func TestRunInstallWithWarnings(t *testing.T) {
	res := installResult
	res.Warnings = []string{"older backups could not be pruned: busy"}
	for _, jsonOut := range []bool{false, true} {
		var stdout, stderr bytes.Buffer
		env := testEnv(&stdout, &stderr)
		env.Install = func(context.Context, string, string, string, string, app.Progress, app.Warn) (app.InstallResult, error) {
			return res, nil
		}
		args := []string{"install"}
		if jsonOut {
			args = append(args, "--json")
		}
		code := cli.Run(args, env)
		if code != cli.ExitOK {
			t.Errorf("json %v: exit code = %d, want 0: the install succeeded", jsonOut, code)
		}
		if !jsonOut && (stderr.String() != "warning: older backups could not be pruned: busy\n" || !strings.Contains(stdout.String(), "state:  /home/u/.dotai/state.json")) {
			t.Errorf("stdout = %q, stderr = %q, want the summary and the warning", stdout.String(), stderr.String())
		}
		if jsonOut && (!strings.Contains(stdout.String(), `"warnings": [`) || !strings.Contains(stdout.String(), "could not be pruned") || stderr.Len() != 0) {
			t.Errorf("stdout = %s, stderr = %q, want the warning in data", stdout.String(), stderr.String())
		}
	}
}

// TestRunInstallDryRunWithSharedSkills checks the plan of a variant that
// shares the user's skills (decision 0036): the shared skills and what is
// skipped in the header, each link with where it points, and agy's copies
// as files.
func TestRunInstallDryRunWithSharedSkills(t *testing.T) {
	t.Setenv("DOTAI_CACHE_DIR", "cache")
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	p := installPlan
	p.Variant = "shared"
	p.SharedSkills = &app.SharedSkillsPlan{Dir: "/home/u/.agents/skills", Found: true, Skills: []string{"commit-message", "linked", "review"}, Skipped: []app.SkippedSkill{
		{Name: "notes", Reason: app.ReasonNotASkill},
		{Name: "commit-message", Harness: "claudecode", Reason: app.ReasonPackSkill},
		{Name: "commit-message", Harness: "antigravity-cli", Reason: app.ReasonPackSkill},
		{Name: "linked", Harness: "antigravity-cli", Reason: app.ReasonNotCopyable, File: "data/cache"},
	}}
	p.Ops = []app.InstallOp{
		{Action: plan.Create, Harness: "claudecode", Source: ".claude/skills/review", Target: "/home/u/.dotai/isolated/claude/skills/review",
			After: "aa", Link: "/home/u/.agents/skills/review", Keys: []plan.KeyChange{}},
		{Action: plan.Remove, Harness: "claudecode", Target: "/home/u/.dotai/isolated/claude/skills/gone", Before: "bb", BeforeLink: "/home/u/.agents/skills/gone", Keys: []plan.KeyChange{}},
		{Action: plan.Unchanged, Harness: "antigravity-cli", Source: ".gemini/antigravity-cli/skills/review/SKILL.md", Target: "/home/u/.dotai/isolated/gemini/config/skills/review/SKILL.md",
			Before: "cc", After: "cc", Keys: []plan.KeyChange{}},
	}
	var stdout, stderr bytes.Buffer
	env := testEnv(&stdout, &stderr)
	env.PlanInstall = func(context.Context, string, string, string, string, app.Progress) (app.InstallPlan, error) {
		return p, nil
	}
	if code := cli.Run([]string{"install", examplePack, "--dry-run", "--variant", "shared"}, env); code != cli.ExitOK {
		t.Errorf("exit code = %d, stderr = %q, want 0", code, stderr.String())
	}
	assertGolden(t, filepath.Join("testdata", "install-dry-run-shared.txt.golden"), stdout.Bytes())

	p.SharedSkills = &app.SharedSkillsPlan{Dir: "/home/u/.agents/skills", Skills: []string{}, Skipped: []app.SkippedSkill{}}
	p.Ops = []app.InstallOp{}
	stdout.Reset()
	if code := cli.Run([]string{"install", examplePack, "--dry-run", "--variant", "shared"}, env); code != cli.ExitOK {
		t.Errorf("exit code = %d, want 0", code)
	}
	if want := "shared skills: /home/u/.agents/skills is missing, so there is nothing to share\n"; !strings.Contains(stdout.String(), want) {
		t.Errorf("stdout = %q, want it to contain %q", stdout.String(), want)
	}
}
