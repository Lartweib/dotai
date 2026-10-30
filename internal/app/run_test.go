package app_test

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
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
		if err := os.MkdirAll(filepath.Join(home, filepath.FromSlash(harness.IsolatedDir), a.Dir), 0o755); err != nil {
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
		{name: "agy not supported yet", home: func(t *testing.T) string { return t.TempDir() }, id: "antigravity-cli", usage: true,
			wantInErr: []string{"antigravity-cli is not supported yet", "only claudecode"}},
		{name: "opencode not supported yet", home: func(t *testing.T) string { return t.TempDir() }, id: "opencode", usage: true,
			wantInErr: []string{"opencode is not supported yet"}},
		{name: "no install state", home: func(t *testing.T) string { return t.TempDir() }, id: "claudecode",
			wantInErr: []string{"no install state", "dotai install"}},
		{name: "pending journal", home: func(t *testing.T) string {
			return runHome(t, app.ModeIsolated, []string{"claudecode"}, pending)
		}, id: "claudecode", wantInErr: []string{"was interrupted", "dotai install", "dotai restore"}},
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
