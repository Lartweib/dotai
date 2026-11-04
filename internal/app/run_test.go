package app_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/lartweib/dotai/internal/app"
	"github.com/lartweib/dotai/internal/harness"
	"github.com/lartweib/dotai/internal/state"
)

// runHome is a temporary home with an isolated install of harnesses: the
// install state and each harness's isolated directory.
func runHome(t *testing.T, mode string, harnesses []string, pending *state.Pending) string {
	t.Helper()
	home := t.TempDir()
	s := state.State{
		Schema: state.Schema, Pack: state.Pack{Name: "minimal-pack", Version: "0.1.0", Path: filepath.Join(home, "pack")},
		Mode: mode, InstalledAt: time.Date(2026, 10, 30, 10, 0, 0, 0, time.UTC), Harnesses: harnesses, Files: []state.File{}, Pending: pending,
	}
	data, err := state.Encode(s)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(at(home, ".dotai"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(at(home, ".dotai/state.json"), data, 0o600); err != nil {
		t.Fatal(err)
	}
	adapters, err := harness.Lookup(harnesses)
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range adapters {
		if err := os.MkdirAll(filepath.Join(home, filepath.FromSlash(harness.IsolatedDir), a.Dir, filepath.FromSlash(a.Install)), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return home
}

// lookPathAt finds every command in dir.
func lookPathAt(dir string) func(string) (string, error) {
	return func(name string) (string, error) { return filepath.Join(dir, name), nil }
}

func TestPlanRunClaudeCode(t *testing.T) {
	home := runHome(t, app.ModeIsolated, []string{"claudecode", "opencode"}, nil)
	bin := t.TempDir()
	environ := []string{"PATH=" + bin, "CLAUDE_CONFIG_DIR=/somewhere/else", "KEEP=1"}
	args := []string{"--model", "opus", "-p", "hi there", "--", "x"}
	p, err := app.PlanRunWith(home, "claudecode", args, environ, lookPathAt(bin), runtime.GOOS)
	if err != nil {
		t.Fatal(err)
	}
	dir := at(home, ".dotai/isolated/claude")
	if p.Harness.ID != "claudecode" || p.Harness.Level != "partial" || p.Harness.Dir != dir {
		t.Errorf("Harness = %+v, want claudecode's isolation at %s", p.Harness, dir)
	}
	if p.Command != "claude" || p.Program != filepath.Join(bin, "claude") {
		t.Errorf("Command, Program = %q, %q, want claude found in %s", p.Command, p.Program, bin)
	}
	if !reflect.DeepEqual(p.Args, args) {
		t.Errorf("Args = %q, want %q untouched", p.Args, args)
	}
	want := []string{"PATH=" + bin, "KEEP=1", "CLAUDE_CONFIG_DIR=" + dir}
	if !reflect.DeepEqual(p.Env, want) {
		t.Errorf("Env = %q, want %q", p.Env, want)
	}
}

func TestPlanRunAgy(t *testing.T) {
	home := runHome(t, app.ModeIsolated, []string{"antigravity-cli"}, nil)
	bin := t.TempDir()
	environ := []string{"PATH=" + bin, "KEEP=1"}
	args := []string{"agents", "-p", "hi there"}
	p, err := app.PlanRunWith(home, "antigravity-cli", args, environ, lookPathAt(bin), runtime.GOOS)
	if err != nil {
		t.Fatal(err)
	}
	dir := at(home, ".dotai/isolated/gemini")
	if p.Harness.ID != "antigravity-cli" || p.Harness.Dir != dir || p.Command != "agy" || p.Program != filepath.Join(bin, "agy") {
		t.Errorf("Harness, Command, Program = %+v, %q, %q, want agy's isolation at %s and agy found in %s", p.Harness, p.Command, p.Program, dir, bin)
	}
	if want := append([]string{"--gemini_dir=" + dir}, args...); !reflect.DeepEqual(p.Args, want) {
		t.Errorf("Args = %q, want %q: the flag in one argument, first", p.Args, want)
	}
	if !reflect.DeepEqual(p.Env, environ) {
		t.Errorf("Env = %q, want %q untouched", p.Env, environ)
	}
}

// TestPlanRunRefusesTheFlagItSets checks that a --gemini_dir of the user's,
// in any form agy's flags take, is refused before anything is read.
func TestPlanRunRefusesTheFlagItSets(t *testing.T) {
	home := t.TempDir()
	for _, arg := range []string{"--gemini_dir", "--gemini_dir=/x", "-gemini_dir", "-gemini_dir=/x"} {
		_, err := app.PlanRunWith(home, "antigravity-cli", []string{"-p", "hi", arg}, nil, lookPathAt(home), runtime.GOOS)
		var usage *app.UsageError
		if !errors.As(err, &usage) || !strings.Contains(err.Error(), "--gemini_dir") || !strings.Contains(err.Error(), fmt.Sprintf("%q", arg)) {
			t.Errorf("argument %q: error = %v, want a usage error that names it", arg, err)
		}
	}
	for _, arg := range []string{"--gemini_dirx", "gemini_dir", "--gemini"} {
		if _, err := app.PlanRunWith(home, "antigravity-cli", []string{arg}, nil, lookPathAt(home), runtime.GOOS); err == nil || strings.Contains(err.Error(), "--gemini_dir") {
			t.Errorf("argument %q: error = %v, want only the missing install", arg, err)
		}
	}
}

func TestPlanRunReplacesTheInheritedPointerInAnyCaseOnWindows(t *testing.T) {
	home := runHome(t, app.ModeIsolated, []string{"claudecode"}, nil)
	p, err := app.PlanRunWith(home, "claudecode", nil, []string{"claude_config_dir=x", "Path=y"}, lookPathAt(home), "windows")
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"Path=y", "CLAUDE_CONFIG_DIR=" + at(home, ".dotai/isolated/claude")}; !reflect.DeepEqual(p.Env, want) {
		t.Errorf("Env = %q, want %q", p.Env, want)
	}
}

func TestPlanRunKeepsAVariableWithAnotherCaseOutsideWindows(t *testing.T) {
	home := runHome(t, app.ModeIsolated, []string{"claudecode"}, nil)
	p, err := app.PlanRunWith(home, "claudecode", nil, []string{"claude_config_dir=x"}, lookPathAt(home), "linux")
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"claude_config_dir=x", "CLAUDE_CONFIG_DIR=" + at(home, ".dotai/isolated/claude")}; !reflect.DeepEqual(p.Env, want) {
		t.Errorf("Env = %q, want %q", p.Env, want)
	}
}

func TestPlanRunErrors(t *testing.T) {
	pending := &state.Pending{Backup: "20261030T100000.000Z_install", Ops: []state.PendingOp{}}
	missing := func(string) (string, error) { return "", exec.ErrNotFound }
	tests := []struct {
		name      string
		home      func(t *testing.T) string
		id        string
		lookPath  func(string) (string, error)
		usage     bool
		wantInErr []string
	}{
		{name: "unknown harness", home: func(t *testing.T) string { return t.TempDir() }, id: "codex", usage: true,
			wantInErr: []string{`unknown harness "codex"`, "claudecode, antigravity-cli, opencode"}},
		{name: "no install state", home: func(t *testing.T) string { return t.TempDir() }, id: "claudecode",
			wantInErr: []string{"no install state", "dotai install"}},
		{name: "pending journal", home: func(t *testing.T) string {
			return runHome(t, app.ModeIsolated, []string{"claudecode"}, pending)
		}, id: "claudecode", wantInErr: []string{"was interrupted", "dotai install", "dotai restore"}},
		{name: "a replace install", home: func(t *testing.T) string { return runHome(t, app.ModeReplace, []string{"claudecode"}, nil) }, id: "claudecode",
			wantInErr: []string{"records a replace install", "start it directly (claude)", "without dotai run"}},
		{name: "another mode", home: func(t *testing.T) string { return runHome(t, "shared", []string{"claudecode"}, nil) }, id: "claudecode",
			wantInErr: []string{`mode "shared"`, "dotai install"}},
		{name: "harness not installed", home: func(t *testing.T) string { return runHome(t, app.ModeIsolated, []string{"opencode"}, nil) }, id: "claudecode",
			wantInErr: []string{"claudecode is not installed", "opencode", "dotai install"}},
		{name: "isolated directory missing", home: func(t *testing.T) string {
			home := runHome(t, app.ModeIsolated, []string{"claudecode"}, nil)
			if err := os.Remove(at(home, ".dotai/isolated/claude")); err != nil {
				t.Fatal(err)
			}
			return home
		}, id: "claudecode", wantInErr: []string{"isolated", "claude", "dotai install"}},
		{name: "claude only through a relative PATH entry", home: func(t *testing.T) string { return runHome(t, app.ModeIsolated, []string{"claudecode"}, nil) },
			id: "claudecode", lookPath: func(string) (string, error) { return filepath.Join("bin", "claude"), exec.ErrDot },
			wantInErr: []string{"relative PATH entry", filepath.Join("bin", "claude"), "remove it from PATH"}},
		{name: "claude missing on PATH", home: func(t *testing.T) string { return runHome(t, app.ModeIsolated, []string{"claudecode"}, nil) },
			id: "claudecode", lookPath: missing, wantInErr: []string{"claude was not found on PATH"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			home := tt.home(t)
			lookPath := tt.lookPath
			if lookPath == nil {
				lookPath = lookPathAt(home)
			}
			_, err := app.PlanRunWith(home, tt.id, nil, nil, lookPath, runtime.GOOS)
			if err == nil {
				t.Fatal("PlanRun() error = nil")
			}
			var usage *app.UsageError
			if errors.As(err, &usage) != tt.usage {
				t.Errorf("PlanRun() error = %v, usage error = %t, want %t", err, !tt.usage, tt.usage)
			}
			for _, want := range tt.wantInErr {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("PlanRun() error = %q, want it to contain %q", err, want)
				}
			}
		})
	}
}

func TestPlanRunWritesNothing(t *testing.T) {
	home := t.TempDir()
	if _, err := app.PlanRunWith(home, "claudecode", nil, nil, lookPathAt(home), runtime.GOOS); err == nil {
		t.Fatal("PlanRun() error = nil without an install")
	}
	if entries, err := os.ReadDir(home); err != nil || len(entries) != 0 {
		t.Errorf("home holds %v (%v), want nothing created", entries, err)
	}
}

func TestPlanRunRefusesALinkedIsolatedDirectory(t *testing.T) {
	home := runHome(t, app.ModeIsolated, []string{"claudecode"}, nil)
	dir := at(home, ".dotai/isolated/claude")
	if err := os.Remove(dir); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(t.TempDir(), dir); err != nil {
		t.Skipf("cannot create a link: %v", err)
	}
	_, err := app.PlanRunWith(home, "claudecode", nil, nil, lookPathAt(home), runtime.GOOS)
	var notRegular *app.NotRegularError
	if !errors.As(err, &notRegular) || notRegular.Path != dir {
		t.Errorf("PlanRun() error = %v, want %s refused as a link", err, dir)
	}
}

// lookPathFiles finds the commands in found, by name, and no other.
func lookPathFiles(found map[string]string) func(string) (string, error) {
	return func(name string) (string, error) {
		if p, ok := found[name]; ok {
			return p, nil
		}
		return "", exec.ErrNotFound
	}
}

func TestPlanRunPrefersTheExecutableOverABatchShimOnWindows(t *testing.T) {
	home := runHome(t, app.ModeIsolated, []string{"claudecode"}, nil)
	for _, shim := range []string{`C:\npm\claude.cmd`, `C:\npm\CLAUDE.BAT`} {
		found := map[string]string{"claude": shim, "claude.exe": `C:\local\bin\claude.exe`}
		p, err := app.PlanRunWith(home, "claudecode", []string{"a&b"}, nil, lookPathFiles(found), "windows")
		if err != nil {
			t.Fatal(err)
		}
		if p.Program != found["claude.exe"] || len(p.Notes) != 0 {
			t.Errorf("Program = %q, Notes = %q, want %s and no note", p.Program, p.Notes, found["claude.exe"])
		}
	}
}

func TestPlanRunLaunchesAnOnlyBatchShimWithANote(t *testing.T) {
	home := runHome(t, app.ModeIsolated, []string{"claudecode"}, nil)
	found := map[string]string{"claude": `C:\npm\claude.cmd`}
	p, err := app.PlanRunWith(home, "claudecode", []string{"-p", "hi there"}, nil, lookPathFiles(found), "windows")
	if err != nil {
		t.Fatal(err)
	}
	if p.Program != found["claude"] || len(p.Notes) != 1 || !strings.Contains(p.Notes[0], "Terminate batch job") || !strings.Contains(p.Notes[0], found["claude"]) {
		t.Errorf("Program = %q, Notes = %q, want the shim with a note about cmd's prompt", p.Program, p.Notes)
	}
	for _, arg := range []string{"a&b", "50%", `say "hi"`, "a|b", "x^y", "wow!", "a>b", "a<b", "two\nlines"} {
		_, err := app.PlanRunWith(home, "claudecode", []string{"-p", arg}, nil, lookPathFiles(found), "windows")
		if err == nil || !strings.Contains(err.Error(), "batch file") || !strings.Contains(err.Error(), fmt.Sprintf("%q", arg)) {
			t.Errorf("argument %q: error = %v, want a refusal that names the batch file and the argument", arg, err)
		}
	}
}

func TestPlanRunKeepsABatchNameOutsideWindows(t *testing.T) {
	home := runHome(t, app.ModeIsolated, []string{"claudecode"}, nil)
	found := map[string]string{"claude": "/opt/claude.cmd", "claude.exe": "/opt/claude.exe"}
	p, err := app.PlanRunWith(home, "claudecode", []string{"a&b"}, nil, lookPathFiles(found), "linux")
	if err != nil || p.Program != found["claude"] || len(p.Notes) != 0 {
		t.Errorf("PlanRun() = %q, %q, %v, want %s as found", p.Program, p.Notes, err, found["claude"])
	}
}

// TestPlanRunFindsTheExecutableOnPath uses the real PATH lookup with fake
// files: a claude.cmd shim in an earlier directory and claude.exe later.
func TestPlanRunFindsTheExecutableOnPath(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("batch shims are a Windows lookup")
	}
	home := runHome(t, app.ModeIsolated, []string{"claudecode"}, nil)
	npm, local := t.TempDir(), t.TempDir()
	for _, f := range []string{filepath.Join(npm, "claude.cmd"), filepath.Join(local, "claude.exe")} {
		if err := os.WriteFile(f, nil, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", npm+string(os.PathListSeparator)+local)
	p, err := app.PlanRunWith(home, "claudecode", nil, nil, exec.LookPath, "windows")
	if err != nil || p.Program != filepath.Join(local, "claude.exe") {
		t.Errorf("PlanRun() = %q, %v, want %s", p.Program, err, filepath.Join(local, "claude.exe"))
	}
	if err := os.Remove(filepath.Join(local, "claude.exe")); err != nil {
		t.Fatal(err)
	}
	p, err = app.PlanRunWith(home, "claudecode", nil, nil, exec.LookPath, "windows")
	if err != nil || p.Program != filepath.Join(npm, "claude.cmd") || len(p.Notes) != 1 {
		t.Errorf("PlanRun() = %q, %q, %v, want the shim %s with a note", p.Program, p.Notes, err, filepath.Join(npm, "claude.cmd"))
	}
}

func TestPlanRunRefusesWhileAnotherDotaiHoldsTheLock(t *testing.T) {
	home := runHome(t, app.ModeIsolated, []string{"claudecode"}, nil)
	lock := at(home, ".dotai/lock")
	if err := os.WriteFile(lock, []byte("pid 4242\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := app.PlanRunWith(home, "claudecode", nil, nil, lookPathAt(home), runtime.GOOS)
	var locked *app.LockedError
	if !errors.As(err, &locked) || locked.PID != 4242 || !strings.Contains(err.Error(), "an install or restore is running") {
		t.Errorf("PlanRun() error = %v, want the lock held by process 4242 named", err)
	}
	if _, err := os.Stat(lock); err != nil {
		t.Errorf("the lock of the other dotai: %v, want it left", err)
	}
}

func TestPlanRunReleasesTheLockBeforeLaunching(t *testing.T) {
	home := runHome(t, app.ModeIsolated, []string{"claudecode"}, nil)
	if _, err := app.PlanRunWith(home, "claudecode", nil, nil, lookPathAt(home), runtime.GOOS); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(at(home, ".dotai"))
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.Name() != "state.json" && e.Name() != "isolated" {
			t.Errorf("%s left in .dotai, want only the state and the isolated root", e.Name())
		}
	}
}

// TestPlanRunShowsTheIsolationOfTheOS checks Claude Code's login and leaks
// per OS: the new login is verified on Windows only.
func TestPlanRunShowsTheIsolationOfTheOS(t *testing.T) {
	home := runHome(t, app.ModeIsolated, []string{"claudecode"}, nil)
	tests := []struct {
		goos, relogin string
		leaks         int
	}{
		{"windows", "yes", 2},
		{"linux", "unknown", 1},
		{"darwin", "unknown", 1},
	}
	for _, tt := range tests {
		p, err := app.PlanRunWith(home, "claudecode", nil, nil, lookPathAt(home), tt.goos)
		if err != nil {
			t.Fatal(err)
		}
		if p.Harness.Relogin != tt.relogin || len(p.Harness.Leaks) != tt.leaks {
			t.Errorf("%s: relogin = %q, leaks = %q, want %q and %d leaks", tt.goos, p.Harness.Relogin, p.Harness.Leaks, tt.relogin, tt.leaks)
		}
	}
}

func TestPlanRunOpenCode(t *testing.T) {
	home := runHome(t, app.ModeIsolated, []string{"opencode"}, nil)
	bin := t.TempDir()
	environ := []string{"PATH=" + bin, "XDG_CONFIG_HOME=/elsewhere", "OPENCODE_TEST_HOME=/x", "OPENCODE_DISABLE_CLAUDE_CODE=0", "KEEP=1"}
	p, err := app.PlanRunWith(home, "opencode", []string{"run", "hi"}, environ, lookPathAt(bin), runtime.GOOS)
	if err != nil {
		t.Fatal(err)
	}
	dir := at(home, ".dotai/isolated/opencode")
	if p.Command != "opencode" || p.Program != filepath.Join(bin, "opencode") {
		t.Errorf("Command, Program = %q, %q, want opencode found in %s", p.Command, p.Program, bin)
	}
	want := []string{"PATH=" + bin, "KEEP=1",
		"XDG_CONFIG_HOME=" + filepath.Join(dir, "config"), "XDG_DATA_HOME=" + filepath.Join(dir, "data"),
		"XDG_STATE_HOME=" + filepath.Join(dir, "state"), "XDG_CACHE_HOME=" + filepath.Join(dir, "cache"),
		"OPENCODE_TEST_HOME=" + filepath.Join(dir, "home"), "OPENCODE_DISABLE_CLAUDE_CODE=1"}
	if !reflect.DeepEqual(p.Env, want) {
		t.Errorf("Env = %q, want %q", p.Env, want)
	}
	if want := []string{"OPENCODE_DISABLE_CLAUDE_CODE=1"}; !reflect.DeepEqual(p.Vars, want) {
		t.Errorf("Vars = %q, want %q", p.Vars, want)
	}
	if want := []string{"run", "--standalone", "hi"}; !reflect.DeepEqual(p.Args, want) {
		t.Errorf("Args = %q, want %q", p.Args, want)
	}
	if len(p.Notes) != 1 || !strings.Contains(p.Notes[0], "--standalone") || !strings.Contains(p.Notes[0], "private server") {
		t.Errorf("Notes = %q, want one about --standalone", p.Notes)
	}
	// OpenCode's background service starts in its home, which must exist.
	if entries, err := os.ReadDir(filepath.Join(dir, "home")); err != nil || len(entries) != 0 {
		t.Errorf("the home directory of OPENCODE_TEST_HOME: %v, %v, want it created empty", entries, err)
	}
}

func TestPlanRunReplacesOpenCodeVariablesInAnyCaseOnWindows(t *testing.T) {
	home := runHome(t, app.ModeIsolated, []string{"opencode"}, nil)
	environ := []string{"xdg_cache_home=x", "Opencode_Disable_Claude_Code=0", "opencode_test_home=y", "Path=z"}
	p, err := app.PlanRunWith(home, "opencode", nil, environ, lookPathAt(home), "windows")
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Env) != 7 || p.Env[0] != "Path=z" {
		t.Errorf("Env = %q, want Path and the six variables of OpenCode's launch only", p.Env)
	}
}

// TestPlanRunAddsStandalone checks where --standalone goes: right after
// the global flags and the subcommands of a command that takes it, and
// nowhere else; a command dotai does not know gets a warning instead.
func TestPlanRunAddsStandalone(t *testing.T) {
	home := runHome(t, app.ModeIsolated, []string{"opencode"}, nil)
	project := t.TempDir()
	tests := []struct {
		args, want []string
		warn       bool
	}{
		{args: nil, want: []string{"--standalone"}},
		{args: []string{"-c"}, want: []string{"--standalone", "-c"}},
		{args: []string{project, "--prompt", "hi"}, want: []string{"--standalone", project, "--prompt", "hi"}},
		{args: []string{"run", "hi", "there"}, want: []string{"run", "--standalone", "hi", "there"}},
		{args: []string{"api", "skill.list"}, want: []string{"api", "--standalone", "skill.list"}},
		{args: []string{"models"}, want: []string{"models", "--standalone"}},
		{args: []string{"session", "list", "-n", "5"}, want: []string{"session", "list", "--standalone", "-n", "5"}},
		{args: []string{"auth", "login"}, want: []string{"auth", "login", "--standalone"}},
		{args: []string{"auth", "logout"}, want: []string{"auth", "logout", "--standalone"}},
		{args: []string{"session"}, want: []string{"session"}},
		{args: []string{"debug", "paths"}, want: []string{"debug", "paths"}},
		{args: []string{"debug", "config"}, want: []string{"debug", "config"}},
		{args: []string{"serve"}, want: []string{"serve"}},
		{args: []string{"run", "--standalone", "hi"}, want: []string{"run", "--standalone", "hi"}},
		{args: []string{"run", "--server", "http://127.0.0.1:1", "hi"}, want: []string{"run", "--server", "http://127.0.0.1:1", "hi"}},
		{args: []string{"--server=http://127.0.0.1:1"}, want: []string{"--server=http://127.0.0.1:1"}},
		{args: []string{"run", "--", "--server"}, want: []string{"run", "--standalone", "--", "--server"}},
		// Global flags before the subcommand, with their values.
		{args: []string{"--print-logs", "mcp", "list"}, want: []string{"--print-logs", "mcp", "list"}},
		{args: []string{"--log-level", "DEBUG", "serve"}, want: []string{"--log-level", "DEBUG", "serve"}},
		{args: []string{"--log-level", "DEBUG", "run", "hi"}, want: []string{"--log-level", "DEBUG", "run", "--standalone", "hi"}},
		{args: []string{"--log-level=debug", "models"}, want: []string{"--log-level=debug", "models", "--standalone"}},
		{args: []string{"--print-logs"}, want: []string{"--print-logs", "--standalone"}},
		// A word that is neither a subcommand nor a directory.
		{args: []string{"newcommand", "x"}, want: []string{"newcommand", "x"}, warn: true},
		{args: []string{"--print-logs", "newcommand"}, want: []string{"--print-logs", "newcommand"}, warn: true},
		{args: []string{"session", "newsub"}, want: []string{"session", "newsub"}, warn: true},
	}
	for _, tt := range tests {
		t.Run(strings.Join(tt.args, " "), func(t *testing.T) {
			p, err := app.PlanRunWith(home, "opencode", tt.args, nil, lookPathAt(home), runtime.GOOS)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(p.Args, tt.want) {
				t.Errorf("Args = %q, want %q", p.Args, tt.want)
			}
			if added := len(p.Args) > len(tt.args); added != (len(p.Notes) == 1) {
				t.Errorf("Notes = %q, want a note exactly when --standalone is added", p.Notes)
			}
			if tt.warn != (len(p.Warnings) == 1) || tt.warn && (!strings.Contains(p.Warnings[0], "--standalone") || !strings.Contains(p.Warnings[0], "background service")) {
				t.Errorf("Warnings = %q, want a warning about the background service: %t", p.Warnings, tt.warn)
			}
		})
	}
}

// TestPlanRunDropsClaudeCodeSessionAndPluginVariables checks that the
// variables that would load the user's own plugins or shell setup, or
// carry a parent Claude Code session into the isolated one, are removed and
// named in a note, and that the credential variables are kept and named,
// without their values, in a warning.
func TestPlanRunDropsClaudeCodeSessionAndPluginVariables(t *testing.T) {
	home := runHome(t, app.ModeIsolated, []string{"claudecode"}, nil)
	dropped := []string{"CLAUDE_CODE_PLUGIN_DIRS=/p", "CLAUDE_CODE_PLUGIN_SEED_DIR=/s", "CLAUDE_CODE_PLUGIN_CACHE_DIR=/c", "CLAUDE_ENV_FILE=/e.sh",
		"CLAUDE_CODE_SESSION_ID=abc", "claudecode=1", "CLAUDE_CODE_CHILD_SESSION=1"}
	kept := []string{"CLAUDE_CODE_OAUTH_TOKEN=secret-oauth", "ANTHROPIC_API_KEY=secret-key", "KEEP=1"}
	p, err := app.PlanRunWith(home, "claudecode", nil, append(slices.Clone(dropped), kept...), lookPathAt(home), "windows")
	if err != nil {
		t.Fatal(err)
	}
	for _, kv := range dropped {
		if slices.Contains(p.Env, kv) {
			t.Errorf("Env has %q, want it removed", kv)
		}
	}
	for _, kv := range kept {
		if !slices.Contains(p.Env, kv) {
			t.Errorf("Env = %q, want %q kept", p.Env, kv)
		}
	}
	if len(p.Notes) != 1 || !strings.Contains(p.Notes[0], "CLAUDE_CODE_PLUGIN_DIRS, CLAUDE_CODE_PLUGIN_SEED_DIR, CLAUDE_CODE_PLUGIN_CACHE_DIR, CLAUDE_ENV_FILE, CLAUDE_CODE_SESSION_ID, claudecode, CLAUDE_CODE_CHILD_SESSION") {
		t.Errorf("Notes = %q, want one naming the removed variables", p.Notes)
	}
	if len(p.Warnings) != 1 || !strings.Contains(p.Warnings[0], "CLAUDE_CODE_OAUTH_TOKEN, ANTHROPIC_API_KEY") ||
		strings.Contains(p.Warnings[0], "ANTHROPIC_AUTH_TOKEN") || strings.Contains(p.Warnings[0], "secret") {
		t.Errorf("Warnings = %q, want one naming the credential variables that are set, without their values", p.Warnings)
	}
	p, err = app.PlanRunWith(home, "claudecode", nil, []string{"KEEP=1"}, lookPathAt(home), "linux")
	if err != nil || len(p.Notes) != 0 || len(p.Warnings) != 0 {
		t.Errorf("without them: Notes = %q, Warnings = %q, %v, want none", p.Notes, p.Warnings, err)
	}
}

// TestPlanRunDropsVariablesThatPointOpenCodeElsewhere checks that an
// inherited configuration or database of OpenCode's is removed, in any
// case on Windows, and named in a note.
func TestPlanRunDropsVariablesThatPointOpenCodeElsewhere(t *testing.T) {
	home := runHome(t, app.ModeIsolated, []string{"opencode"}, nil)
	environ := []string{"OPENCODE_CONFIG=/x.json", "opencode_config_dir=/x", "OPENCODE_DB=/x.db", "OPENCODE_CONFIG_CONTENT={}", "KEEP=1"}
	p, err := app.PlanRunWith(home, "opencode", []string{"debug", "paths"}, environ, lookPathAt(home), "windows")
	if err != nil {
		t.Fatal(err)
	}
	for _, kv := range p.Env {
		if name, _, _ := strings.Cut(kv, "="); strings.HasPrefix(strings.ToUpper(name), "OPENCODE_CONFIG") || strings.EqualFold(name, "OPENCODE_DB") {
			t.Errorf("Env has %q, want it removed", kv)
		}
	}
	if !slices.Contains(p.Env, "KEEP=1") {
		t.Errorf("Env = %q, want the rest kept", p.Env)
	}
	if len(p.Notes) != 1 || !strings.Contains(p.Notes[0], "OPENCODE_CONFIG, opencode_config_dir, OPENCODE_DB, OPENCODE_CONFIG_CONTENT") {
		t.Errorf("Notes = %q, want one naming the removed variables", p.Notes)
	}
	p, err = app.PlanRunWith(home, "opencode", []string{"debug", "paths"}, []string{"opencode_config_dir=/x"}, lookPathAt(home), "linux")
	if err != nil || !slices.Contains(p.Env, "opencode_config_dir=/x") || len(p.Notes) != 0 {
		t.Errorf("on linux: Env = %q, Notes = %q, %v, want a variable of another case kept", p.Env, p.Notes, err)
	}
}

// TestPlanRunRefusesAMissingInstallDirectory checks the directory the
// harness's files install to inside its isolated directory, config/opencode
// for OpenCode.
func TestPlanRunRefusesAMissingInstallDirectory(t *testing.T) {
	home := runHome(t, app.ModeIsolated, []string{"opencode"}, nil)
	dir := at(home, ".dotai/isolated/opencode/config/opencode")
	if err := os.Remove(dir); err != nil {
		t.Fatal(err)
	}
	_, err := app.PlanRunWith(home, "opencode", nil, nil, lookPathAt(home), runtime.GOOS)
	if err == nil || !strings.Contains(err.Error(), dir) || !strings.Contains(err.Error(), "dotai install") {
		t.Errorf("PlanRun() error = %v, want %s named as missing", err, dir)
	}
	if _, err := os.Stat(at(home, ".dotai/isolated/opencode/home")); err == nil {
		t.Error("the home of OPENCODE_TEST_HOME was created for a launch that was refused")
	}
	if err := os.WriteFile(dir, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	_, err = app.PlanRunWith(home, "opencode", nil, nil, lookPathAt(home), runtime.GOOS)
	var notRegular *app.NotRegularError
	if !errors.As(err, &notRegular) || notRegular.Path != dir {
		t.Errorf("PlanRun() error = %v, want the file %s refused", err, dir)
	}
}

func TestPlanRunRefusesALinkedOpenCodeHome(t *testing.T) {
	home := runHome(t, app.ModeIsolated, []string{"opencode"}, nil)
	dir := at(home, ".dotai/isolated/opencode/home")
	if err := os.Symlink(t.TempDir(), dir); err != nil {
		t.Skipf("cannot create a link: %v", err)
	}
	_, err := app.PlanRunWith(home, "opencode", nil, nil, lookPathAt(home), runtime.GOOS)
	var notRegular *app.NotRegularError
	if !errors.As(err, &notRegular) || notRegular.Path != dir {
		t.Errorf("PlanRun() error = %v, want %s refused as a link", err, dir)
	}
}

// TestPlanRunLaunchesTheExecutableOfOpenCodesShim checks that npm's
// opencode.cmd gives way to the opencode.exe it runs, next to it in
// node_modules, before an opencode.exe further on PATH.
func TestPlanRunLaunchesTheExecutableOfOpenCodesShim(t *testing.T) {
	home := runHome(t, app.ModeIsolated, []string{"opencode"}, nil)
	npm := t.TempDir()
	shim := filepath.Join(npm, "opencode.cmd")
	exe := filepath.Join(npm, "node_modules", "@opencode", "cli", "bin", "opencode.exe")
	found := map[string]string{"opencode": shim, "opencode.exe": `C:\other\opencode.exe`}
	p, err := app.PlanRunWith(home, "opencode", []string{"run", "a&b"}, nil, lookPathFiles(found), "windows")
	if err != nil || p.Program != found["opencode.exe"] {
		t.Errorf("without the shim's executable: Program = %q, %v, want %s on PATH", p.Program, err, found["opencode.exe"])
	}
	if err := os.MkdirAll(filepath.Dir(exe), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(exe, nil, 0o755); err != nil {
		t.Fatal(err)
	}
	p, err = app.PlanRunWith(home, "opencode", []string{"run", "a&b"}, nil, lookPathFiles(found), "windows")
	if err != nil || p.Program != exe || len(p.Notes) != 1 {
		t.Errorf("Program = %q, Notes = %q, %v, want %s with the --standalone note only", p.Program, p.Notes, err, exe)
	}
	// Outside Windows, a .cmd is no batch file.
	p, err = app.PlanRunWith(home, "opencode", nil, nil, lookPathFiles(found), "linux")
	if err != nil || p.Program != shim {
		t.Errorf("on linux: Program = %q, %v, want %s as found", p.Program, err, shim)
	}
}

// TestPlanRunOpenCodeWithSharedSkills checks the launch of an install whose
// variant shares the user's skills (decision 0036): OpenCode keeps the real
// home directory, where it reads ~/.agents/skills, so OPENCODE_TEST_HOME is
// not set, and one inherited is removed; its empty home is not made.
func TestPlanRunOpenCodeWithSharedSkills(t *testing.T) {
	home := runHome(t, app.ModeIsolated, []string{"opencode"}, nil)
	s := readJSON(t, at(home, ".dotai/state.json"), state.Decode)
	s.SharedSkills = true
	data, err := state.Encode(s)
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, at(home, ".dotai/state.json"), string(data))
	environ := []string{"PATH=" + home, "OPENCODE_TEST_HOME=/x", "KEEP=1"}
	p, err := app.PlanRunWith(home, "opencode", []string{"run", "hi"}, environ, lookPathAt(home), "linux")
	if err != nil {
		t.Fatal(err)
	}
	dir := at(home, ".dotai/isolated/opencode")
	want := []string{"PATH=" + home, "KEEP=1",
		"XDG_CONFIG_HOME=" + filepath.Join(dir, "config"), "XDG_DATA_HOME=" + filepath.Join(dir, "data"),
		"XDG_STATE_HOME=" + filepath.Join(dir, "state"), "XDG_CACHE_HOME=" + filepath.Join(dir, "cache"), "OPENCODE_DISABLE_CLAUDE_CODE=1"}
	if !reflect.DeepEqual(p.Env, want) {
		t.Errorf("Env = %q, want %q", p.Env, want)
	}
	if slices.ContainsFunc(p.Harness.Pointers, func(ptr app.PointerPlan) bool { return ptr.Name == "OPENCODE_TEST_HOME" }) {
		t.Errorf("Pointers = %+v, want no OPENCODE_TEST_HOME", p.Harness.Pointers)
	}
	if !slices.ContainsFunc(p.Harness.Leaks, func(l string) bool { return strings.Contains(l, "as the variant shares skills") }) {
		t.Errorf("Leaks = %q, want the shared home named", p.Harness.Leaks)
	}
	if _, err := os.Lstat(filepath.Join(dir, "home")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("the empty home was made: %v, want it left out", err)
	}
}

// TestPlanRunOpenCodeNamesOnlyTheWalkUpItReaches checks that the leak of
// OpenCode's walk-up is shown only for a working directory under the home
// directory, and names only the files of the home directory it reaches
// that exist.
func TestPlanRunOpenCodeNamesOnlyTheWalkUpItReaches(t *testing.T) {
	walkUp := func(leaks []string) []string {
		var out []string
		for _, l := range leaks {
			if strings.Contains(l, "every directory above") {
				out = append(out, l)
			}
		}
		return out
	}
	home := runHome(t, app.ModeIsolated, []string{"opencode"}, nil)
	under := filepath.Join(home, "work", "site")
	if err := os.MkdirAll(under, 0o755); err != nil {
		t.Fatal(err)
	}
	p, err := app.PlanRunAt(home, t.TempDir(), "opencode", []string{"run", "hi"}, nil, lookPathAt(home), "linux")
	if err != nil || len(walkUp(p.Harness.Leaks)) != 0 {
		t.Errorf("outside the home directory: leaks = %q, %v, want no walk-up leak", p.Harness.Leaks, err)
	}
	p, err = app.PlanRunAt(home, under, "opencode", []string{"run", "hi"}, nil, lookPathAt(home), "linux")
	if err != nil || len(walkUp(p.Harness.Leaks)) != 0 {
		t.Errorf("under a home without those files: leaks = %q, %v, want no walk-up leak", p.Harness.Leaks, err)
	}
	if err := os.MkdirAll(at(home, ".agents/skills"), 0o755); err != nil {
		t.Fatal(err)
	}
	want := []string{"AGENTS.md, .agents and .opencode load from the working directory and every directory above it, so under your home directory these still load: ~/.agents/skills"}
	p, err = app.PlanRunAt(home, under, "opencode", []string{"run", "hi"}, nil, lookPathAt(home), "linux")
	if got := walkUp(p.Harness.Leaks); err != nil || !reflect.DeepEqual(got, want) {
		t.Errorf("under a home with ~/.agents/skills: walk-up leaks = %q, %v, want %q", got, err, want)
	}
	writeFile(t, at(home, "AGENTS.md"), "# mine\n")
	want = []string{"AGENTS.md, .agents and .opencode load from the working directory and every directory above it, so under your home directory these still load: ~/.agents/skills and ~/AGENTS.md"}
	p, err = app.PlanRunAt(home, home, "opencode", []string{"run", "hi"}, nil, lookPathAt(home), "linux")
	if got := walkUp(p.Harness.Leaks); err != nil || !reflect.DeepEqual(got, want) {
		t.Errorf("in the home directory: walk-up leaks = %q, %v, want %q", got, err, want)
	}
}

// TestUnderHome checks the working directory test of dotai run: below the
// home directory or the home directory itself, without case only on
// Windows and macOS, never on another drive, and through a link.
func TestUnderHome(t *testing.T) {
	for _, tt := range []struct {
		name, goos, home, cwd string
		want                  bool
	}{
		{"the home directory", "linux", "/home/ana", "/home/ana", true},
		{"below it", "linux", "/home/ana", "/home/ana/work/site", true},
		{"a sibling with the same prefix", "linux", "/home/ana", "/home/anabel", false},
		{"above it", "linux", "/home/ana", "/home", false},
		{"other case on Linux", "linux", "/home/ana", "/home/Ana/work", false},
		{"other case on macOS", "darwin", "/Users/ana", "/users/ANA/work", true},
		{"other case on Windows", "windows", `C:\Users\Ana`, `c:\users\ana\work`, true},
		{"another drive", "windows", `C:\Users\Ana`, `D:\Users\Ana\work`, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if runtime.GOOS != "windows" && strings.Contains(tt.home, `\`) {
				t.Skip("Windows paths")
			}
			if got := app.UnderHome(tt.goos, filepath.FromSlash(tt.home), filepath.FromSlash(tt.cwd)); got != tt.want {
				t.Errorf("UnderHome(%s, %s, %s) = %v, want %v", tt.goos, tt.home, tt.cwd, got, tt.want)
			}
		})
	}
	home, other := t.TempDir(), t.TempDir()
	link := filepath.Join(other, "link")
	if err := os.Symlink(home, link); err != nil {
		t.Skipf("no link: %v", err)
	}
	if !app.UnderHome(runtime.GOOS, home, link) {
		t.Errorf("a link to the home directory is not under it")
	}
}

// TestPlanRunSaysWhetherTheHomeInstructionsAreExcluded checks that the
// leak of Claude Code's walk-up says the home directory's instruction
// files are excluded only when the isolated settings list every pattern
// an install writes, and otherwise that they still load and to install
// again, as after an install from before claudeMdExcludes.
func TestPlanRunSaysWhetherTheHomeInstructionsAreExcluded(t *testing.T) {
	home := runHome(t, app.ModeIsolated, []string{"claudecode"}, nil)
	settings := at(home, ".dotai/isolated/claude/settings.json")
	patterns, ok := app.HomePatterns([]string{filepath.ToSlash(home)}, []string{"CLAUDE.md", "CLAUDE.local.md", ".claude/CLAUDE.md", ".claude/rules/**"})
	if !ok {
		t.Fatal("no patterns")
	}
	list := func(p []string) string {
		data, err := json.Marshal(map[string]any{"model": "sonnet", "claudeMdExcludes": p})
		if err != nil {
			t.Fatal(err)
		}
		return string(data)
	}
	for _, tt := range []struct {
		name, settings string // "" for no settings file
		excluded       bool
	}{
		{"no settings file", "", false},
		{"settings without the key", `{"model": "sonnet"}`, false},
		{"some patterns only", list(patterns[:2]), false},
		{"not JSON", "{", false},
		{"every pattern, with the pack's", list(append([]string{"**/vendor/CLAUDE.md"}, patterns...)), true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if err := os.Remove(settings); err != nil && !errors.Is(err, os.ErrNotExist) {
				t.Fatal(err)
			}
			if tt.settings != "" {
				writeFile(t, settings, tt.settings)
			}
			p, err := app.PlanRunWith(home, "claudecode", nil, nil, lookPathAt(home), "linux")
			if err != nil || len(p.Harness.Leaks) == 0 {
				t.Fatalf("PlanRun() = %+v, %v", p.Harness.Leaks, err)
			}
			leak := p.Harness.Leaks[0]
			if got := strings.Contains(leak, "which the isolated settings exclude with claudeMdExcludes"); got != tt.excluded {
				t.Errorf("leak = %q, want excluded %v", leak, tt.excluded)
			}
			if !tt.excluded && !strings.Contains(leak, "run dotai install again") {
				t.Errorf("leak = %q, want it to say to install again", leak)
			}
		})
	}
}
