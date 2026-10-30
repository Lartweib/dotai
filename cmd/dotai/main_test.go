package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/lartweib/dotai/internal/state"
)

// TestMain runs dotai itself when the test binary is started as dotai, so
// that the tests can launch the real command with its real launcher.
func TestMain(m *testing.M) {
	if os.Getenv("DOTAI_TEST_AS_DOTAI") == "1" {
		main()
		return
	}
	os.Exit(m.Run())
}

// fakeClaude prints what it was launched with as JSON and exits with the
// code in FAKE_CLAUDE_EXIT.
const fakeClaude = `package main

import (
	"encoding/json"
	"os"
	"strconv"
)

func main() {
	cwd, _ := os.Getwd()
	dir, set := os.LookupEnv("CLAUDE_CONFIG_DIR")
	n := 0
	for _, kv := range os.Environ() {
		if len(kv) >= 18 && (kv[:18] == "CLAUDE_CONFIG_DIR=" || kv[:18] == "claude_config_dir=") {
			n++
		}
	}
	json.NewEncoder(os.Stdout).Encode(map[string]any{"configDir": dir, "set": set, "count": n, "args": os.Args[1:], "cwd": cwd})
	code, _ := strconv.Atoi(os.Getenv("FAKE_CLAUDE_EXIT"))
	os.Exit(code)
}
`

// launched is what the fake claude prints.
type launched struct {
	ConfigDir string   `json:"configDir"`
	Set       bool     `json:"set"`
	Count     int      `json:"count"`
	Args      []string `json:"args"`
	Cwd       string   `json:"cwd"`
}

// buildFakeClaude builds the fake claude into a new directory, which it
// returns, so that it is the only claude on a PATH made of it.
func buildFakeClaude(t *testing.T) string {
	t.Helper()
	src, bin := t.TempDir(), t.TempDir()
	if err := os.WriteFile(filepath.Join(src, "main.go"), []byte(fakeClaude), 0o644); err != nil {
		t.Fatal(err)
	}
	name := "claude"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	build := exec.Command("go", "build", "-o", filepath.Join(bin, name), "main.go")
	build.Dir = src
	build.Env = append(os.Environ(), "GOFLAGS=", "GO111MODULE=off")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("building the fake claude: %v\n%s", err, out)
	}
	return bin
}

// installedHome is a temporary home with an isolated install of Claude
// Code, as dotai install records it.
func installedHome(t *testing.T, pending *state.Pending) string {
	t.Helper()
	home := t.TempDir()
	s := state.State{
		Schema: state.Schema, Pack: state.Pack{Name: "minimal-pack", Version: "0.1.0", Path: filepath.Join(home, "pack")}, Mode: "isolated",
		InstalledAt: time.Date(2026, 10, 30, 10, 0, 0, 0, time.UTC), Harnesses: []string{"claudecode"}, Files: []state.File{}, Pending: pending,
	}
	data, err := state.Encode(s)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(home, ".dotai", "isolated", "claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".dotai", "state.json"), data, 0o600); err != nil {
		t.Fatal(err)
	}
	return home
}

// dotai runs this test binary as dotai with args in cwd, home as the home
// directory and path as the whole PATH, with an inherited
// CLAUDE_CONFIG_DIR and extra variables, and gives its exit code, stdout
// and stderr.
func dotai(t *testing.T, home, path, cwd string, extra []string, args ...string) (int, string, string) {
	t.Helper()
	cmd := exec.Command(os.Args[0], args...)
	cmd.Dir = cwd
	cmd.Env = append(os.Environ(), "DOTAI_TEST_AS_DOTAI=1", "HOME="+home, "USERPROFILE="+home, "PATH="+path,
		"CLAUDE_CONFIG_DIR="+filepath.Join(home, "inherited"))
	cmd.Env = append(cmd.Env, extra...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	if exit, ok := errors.AsType[*exec.ExitError](err); ok {
		return exit.ExitCode(), stdout.String(), stderr.String()
	}
	if err != nil {
		t.Fatal(err)
	}
	return 0, stdout.String(), stderr.String()
}

func TestRunLaunchesClaudeCodeIsolated(t *testing.T) {
	bin := buildFakeClaude(t)
	home := installedHome(t, nil)
	cwd := t.TempDir()
	args := []string{"--model", "opus", "-p", "hi there", "--json", "--", "x"}
	for _, code := range []int{0, 3} {
		exit, stdout, stderr := dotai(t, home, bin, cwd, []string{"FAKE_CLAUDE_EXIT=" + string(rune('0'+code))}, append([]string{"run", "claudecode", "--"}, args...)...)
		if exit != code {
			t.Fatalf("exit code = %d, want the harness's %d; stderr = %q", exit, code, stderr)
		}
		var got launched
		if err := json.Unmarshal([]byte(stdout), &got); err != nil {
			t.Fatalf("stdout = %q: %v", stdout, err)
		}
		if want := filepath.Join(home, ".dotai", "isolated", "claude"); !got.Set || got.ConfigDir != want || got.Count != 1 {
			t.Errorf("CLAUDE_CONFIG_DIR = %q (set %t, %d times), want only %s", got.ConfigDir, got.Set, got.Count, want)
		}
		if !slices.Equal(got.Args, args) {
			t.Errorf("args = %q, want %q", got.Args, args)
		}
		if a, errA := os.Stat(got.Cwd); errA != nil {
			t.Errorf("cwd %q: %v", got.Cwd, errA)
		} else if b, _ := os.Stat(cwd); !os.SameFile(a, b) {
			t.Errorf("cwd = %q, want %s", got.Cwd, cwd)
		}
		// The new login is verified on Windows only.
		login := "dotai: login: you may need to log in again in the isolated configuration (not verified on this OS)\n"
		if runtime.GOOS == "windows" {
			login = "dotai: login: you must log in again in the isolated configuration\n"
		}
		for _, line := range []string{
			"dotai: claudecode · partial isolation · CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".dotai", "isolated", "claude") + "\n",
			login,
			"dotai: leaks: ~/.claude/CLAUDE.md still loads when the working directory is under the home directory\n",
		} {
			if !strings.Contains(stderr, line) {
				t.Errorf("stderr = %q, want the line %q", stderr, line)
			}
		}
	}
}

func TestRunFailsBeforeLaunching(t *testing.T) {
	bin := buildFakeClaude(t)
	tests := []struct {
		name  string
		home  string
		path  string
		args  []string
		want  int
		inErr string
	}{
		{name: "no install state", home: t.TempDir(), path: bin, args: []string{"run", "claudecode"}, want: 1, inErr: "there is no install state"},
		{name: "pending journal", home: installedHome(t, &state.Pending{Backup: "20261030T100000.000Z_install", Ops: []state.PendingOp{}}), path: bin,
			args: []string{"run", "claudecode"}, want: 1, inErr: "was interrupted"},
		{name: "claude missing on PATH", home: installedHome(t, nil), path: t.TempDir(), args: []string{"run", "claudecode"}, want: 1, inErr: "claude was not found on PATH"},
		{name: "opencode", home: installedHome(t, nil), path: bin, args: []string{"run", "opencode"}, want: 2, inErr: "not supported yet"},
		{name: "json", home: installedHome(t, nil), path: bin, args: []string{"--json", "run", "claudecode"}, want: 2, inErr: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			exit, stdout, stderr := dotai(t, tt.home, tt.path, t.TempDir(), nil, tt.args...)
			if exit != tt.want || !strings.Contains(stderr, tt.inErr) {
				t.Errorf("exit code = %d, stderr = %q, want %d and %q", exit, stderr, tt.want, tt.inErr)
			}
			if strings.Contains(stdout, "configDir") {
				t.Errorf("stdout = %q, want the harness not launched", stdout)
			}
		})
	}
}
