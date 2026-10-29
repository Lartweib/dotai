package app_test

import (
	"errors"
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
	p, err := app.PlanRunWith(home, "claudecode", args, environ, lookPathAt(bin))
	if err != nil {
		t.Fatal(err)
	}
	dir := at(home, ".dotai/isolated/claude")
	if p.Harness.ID != "claudecode" || p.Harness.Level != "partial" || !p.Harness.Relogin || len(p.Harness.Leaks) != 2 || p.Harness.Dir != dir {
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
	if runtime.GOOS != "windows" {
		t.Skip("environment names are case-sensitive outside Windows")
	}
	home := runHome(t, app.ModeIsolated, []string{"claudecode"}, nil)
	p, err := app.PlanRunWith(home, "claudecode", nil, []string{"claude_config_dir=x", "Path=y"}, lookPathAt(home))
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"Path=y", "CLAUDE_CONFIG_DIR=" + at(home, ".dotai/isolated/claude")}; !reflect.DeepEqual(p.Env, want) {
		t.Errorf("Env = %q, want %q", p.Env, want)
	}
}

func TestPlanRunKeepsAVariableWithAnotherCaseOutsideWindows(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("environment names are case-insensitive on Windows")
	}
	home := runHome(t, app.ModeIsolated, []string{"claudecode"}, nil)
	p, err := app.PlanRunWith(home, "claudecode", nil, []string{"claude_config_dir=x"}, lookPathAt(home))
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
		{name: "claude missing on PATH", home: func(t *testing.T) string { return runHome(t, app.ModeIsolated, []string{"claudecode"}, nil) },
			id: "claudecode", lookPath: missing, wantInErr: []string{"claude", "PATH"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			home := tt.home(t)
			lookPath := tt.lookPath
			if lookPath == nil {
				lookPath = lookPathAt(home)
			}
			_, err := app.PlanRunWith(home, tt.id, nil, nil, lookPath)
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
	if _, err := app.PlanRunWith(home, "claudecode", nil, nil, lookPathAt(home)); err == nil {
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
	_, err := app.PlanRunWith(home, "claudecode", nil, nil, lookPathAt(home))
	var notRegular *app.NotRegularError
	if !errors.As(err, &notRegular) || notRegular.Path != dir {
		t.Errorf("PlanRun() error = %v, want %s refused as a link", err, dir)
	}
}
