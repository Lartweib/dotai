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

	"github.com/lartweib/dotai/internal/harness"
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

// fakeHarness prints what it was launched with as JSON: its arguments,
// the current directory and each variable of the isolated launches that is
// set, with how many times its name appears in any case; then it exits
// with the code in FAKE_HARNESS_EXIT. It is built as claude, agy and
// opencode.
const fakeHarness = `package main

import (
	"encoding/json"
	"os"
	"strconv"
	"strings"
)

func main() {
	cwd, _ := os.Getwd()
	env, counts := map[string]string{}, map[string]int{}
	for _, name := range []string{"CLAUDE_CONFIG_DIR", "XDG_CONFIG_HOME", "XDG_DATA_HOME", "XDG_STATE_HOME", "XDG_CACHE_HOME",
		"OPENCODE_TEST_HOME", "OPENCODE_DISABLE_CLAUDE_CODE"} {
		if v, ok := os.LookupEnv(name); ok {
			env[name] = v
		}
		for _, kv := range os.Environ() {
			if k, _, _ := strings.Cut(kv, "="); strings.EqualFold(k, name) {
				counts[name]++
			}
		}
	}
	json.NewEncoder(os.Stdout).Encode(map[string]any{"args": os.Args[1:], "cwd": cwd, "env": env, "counts": counts})
	code, _ := strconv.Atoi(os.Getenv("FAKE_HARNESS_EXIT"))
	os.Exit(code)
}
`

// launched is what the fake harness prints.
type launched struct {
	Args   []string          `json:"args"`
	Cwd    string            `json:"cwd"`
	Env    map[string]string `json:"env"`
	Counts map[string]int    `json:"counts"`
}

// buildFakes builds the fake harness as claude, agy and opencode into a
// new directory, which it returns, so that they are the only ones on a
// PATH made of it.
func buildFakes(t *testing.T) string {
	t.Helper()
	bin := t.TempDir()
	for _, name := range []string{"claude", "agy", "opencode"} {
		if runtime.GOOS == "windows" {
			name += ".exe"
		}
		buildFake(t, filepath.Join(bin, name))
	}
	return bin
}

// buildFake builds the fake harness at dst.
func buildFake(t *testing.T, dst string) {
	t.Helper()
	src := t.TempDir()
	if err := os.WriteFile(filepath.Join(src, "main.go"), []byte(fakeHarness), 0o644); err != nil {
		t.Fatal(err)
	}
	build := exec.Command("go", "build", "-o", dst, "main.go")
	build.Dir = src
	build.Env = append(os.Environ(), "GOFLAGS=", "GO111MODULE=off")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("building the fake %s: %v\n%s", dst, err, out)
	}
}

// launch decodes what the fake harness printed.
func launch(t *testing.T, stdout string) launched {
	t.Helper()
	var got launched
	if err := json.Unmarshal([]byte(stdout), &got); err != nil {
		t.Fatalf("stdout = %q: %v", stdout, err)
	}
	return got
}

// installedHome is a temporary home with an isolated install of
// harnesses, Claude Code when there is none, as dotai install records it.
func installedHome(t *testing.T, pending *state.Pending, harnesses ...string) string {
	t.Helper()
	if len(harnesses) == 0 {
		harnesses = []string{"claudecode"}
	}
	home := t.TempDir()
	s := state.State{
		Schema: state.Schema, Pack: state.Pack{Name: "minimal-pack", Version: "0.1.0", Path: filepath.Join(home, "pack")}, Mode: "isolated",
		InstalledAt: time.Date(2026, 10, 30, 10, 0, 0, 0, time.UTC), Harnesses: harnesses, Files: []state.File{}, Pending: pending,
	}
	data, err := state.Encode(s)
	if err != nil {
		t.Fatal(err)
	}
	adapters, err := harness.Lookup(harnesses)
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range adapters {
		if err := os.MkdirAll(filepath.Join(home, ".dotai", "isolated", a.Dir), 0o755); err != nil {
			t.Fatal(err)
		}
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
	bin := buildFakes(t)
	home := installedHome(t, nil)
	cwd := t.TempDir()
	args := []string{"--model", "opus", "-p", "hi there", "--json", "--", "x"}
	for _, code := range []int{0, 3} {
		exit, stdout, stderr := dotai(t, home, bin, cwd, []string{"FAKE_HARNESS_EXIT=" + string(rune('0'+code))}, append([]string{"run", "claudecode", "--"}, args...)...)
		if exit != code {
			t.Fatalf("exit code = %d, want the harness's %d; stderr = %q", exit, code, stderr)
		}
		got := launch(t, stdout)
		if want := filepath.Join(home, ".dotai", "isolated", "claude"); got.Env["CLAUDE_CONFIG_DIR"] != want || got.Counts["CLAUDE_CONFIG_DIR"] != 1 {
			t.Errorf("CLAUDE_CONFIG_DIR = %q (%d times), want only %s", got.Env["CLAUDE_CONFIG_DIR"], got.Counts["CLAUDE_CONFIG_DIR"], want)
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
	bin := buildFakes(t)
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
		{name: "opencode not installed", home: installedHome(t, nil), path: bin, args: []string{"run", "opencode"}, want: 1, inErr: "opencode is not installed"},
		{name: "unknown harness", home: installedHome(t, nil), path: bin, args: []string{"run", "codex"}, want: 2, inErr: `unknown harness "codex"`},
		{name: "json", home: installedHome(t, nil), path: bin, args: []string{"--json", "run", "claudecode"}, want: 2, inErr: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			exit, stdout, stderr := dotai(t, tt.home, tt.path, t.TempDir(), nil, tt.args...)
			if exit != tt.want || !strings.Contains(stderr, tt.inErr) {
				t.Errorf("exit code = %d, stderr = %q, want %d and %q", exit, stderr, tt.want, tt.inErr)
			}
			if strings.Contains(stdout, `"args"`) {
				t.Errorf("stdout = %q, want the harness not launched", stdout)
			}
		})
	}
}

func TestRunLaunchesAgyIsolated(t *testing.T) {
	bin := buildFakes(t)
	home := installedHome(t, nil, "antigravity-cli")
	args := []string{"agents", "-p", "hi there"}
	exit, stdout, stderr := dotai(t, home, bin, t.TempDir(), nil, append([]string{"run", "antigravity-cli", "--"}, args...)...)
	if exit != 0 {
		t.Fatalf("exit code = %d; stderr = %q", exit, stderr)
	}
	dir := filepath.Join(home, ".dotai", "isolated", "gemini")
	if got := launch(t, stdout); !slices.Equal(got.Args, append([]string{"--gemini_dir=" + dir}, args...)) {
		t.Errorf("args = %q, want --gemini_dir=%s before %q", got.Args, dir, args)
	}
	for _, line := range []string{
		"dotai: antigravity-cli · partial isolation · --gemini_dir=" + dir + "\n",
		"dotai: login: keeps your login\n",
		"dotai: leaks: authentication is shared through the OS keyring\n",
	} {
		if !strings.Contains(stderr, line) {
			t.Errorf("stderr = %q, want the line %q", stderr, line)
		}
	}
	exit, stdout, stderr = dotai(t, home, bin, t.TempDir(), nil, "run", "antigravity-cli", "--gemini_dir=/elsewhere")
	if exit != 2 || stdout != "" || !strings.Contains(stderr, "sets --gemini_dir itself") {
		t.Errorf("with the user's --gemini_dir: exit code = %d, stdout = %q, stderr = %q, want a usage error and nothing launched", exit, stdout, stderr)
	}
}

func TestRunLaunchesOpenCodeIsolated(t *testing.T) {
	bin := buildFakes(t)
	home := installedHome(t, nil, "opencode")
	extra := []string{"XDG_CONFIG_HOME=" + filepath.Join(home, "inherited"), "OPENCODE_DISABLE_CLAUDE_CODE=0", "OPENCODE_TEST_HOME=" + home}
	exit, stdout, stderr := dotai(t, home, bin, t.TempDir(), extra, "run", "opencode", "run", "hi there")
	if exit != 0 {
		t.Fatalf("exit code = %d; stderr = %q", exit, stderr)
	}
	dir := filepath.Join(home, ".dotai", "isolated", "opencode")
	got := launch(t, stdout)
	if want := []string{"run", "--standalone", "hi there"}; !slices.Equal(got.Args, want) {
		t.Errorf("args = %q, want %q", got.Args, want)
	}
	want := map[string]string{
		"XDG_CONFIG_HOME": filepath.Join(dir, "config"), "XDG_DATA_HOME": filepath.Join(dir, "data"),
		"XDG_STATE_HOME": filepath.Join(dir, "state"), "XDG_CACHE_HOME": filepath.Join(dir, "cache"),
		"OPENCODE_TEST_HOME": filepath.Join(dir, "home"), "OPENCODE_DISABLE_CLAUDE_CODE": "1",
	}
	for name, value := range want {
		if got.Env[name] != value || got.Counts[name] != 1 {
			t.Errorf("%s = %q (%d times), want only %q", name, got.Env[name], got.Counts[name], value)
		}
	}
	if _, ok := got.Env["CLAUDE_CONFIG_DIR"]; !ok {
		t.Error("CLAUDE_CONFIG_DIR was dropped, want the rest of the environment kept")
	}
	if info, err := os.Stat(filepath.Join(dir, "home")); err != nil || !info.IsDir() {
		t.Errorf("the home of OPENCODE_TEST_HOME: %v, want a directory", err)
	}
	for _, line := range []string{
		"dotai: opencode · partial isolation · XDG_CONFIG_HOME=" + filepath.Join(dir, "config"),
		", OPENCODE_TEST_HOME=" + filepath.Join(dir, "home") + ", OPENCODE_DISABLE_CLAUDE_CODE=1\n",
		"dotai: login: you must log in again in the isolated configuration\n",
		"dotai: leaks: provider credentials do not carry over\n",
		"dotai: note: --standalone added: ",
	} {
		if !strings.Contains(stderr, line) {
			t.Errorf("stderr = %q, want %q", stderr, line)
		}
	}
	exit, stdout, _ = dotai(t, home, bin, t.TempDir(), nil, "run", "opencode", "debug", "paths")
	if got := launch(t, stdout); exit != 0 || !slices.Equal(got.Args, []string{"debug", "paths"}) {
		t.Errorf("debug paths: exit code = %d, args = %q, want them untouched", exit, got.Args)
	}
}

// TestRunLaunchesTheExecutableOfOpenCodesShim lays out OpenCode as npm
// installs it on Windows: opencode.cmd, which runs the opencode.exe in
// node_modules next to it.
func TestRunLaunchesTheExecutableOfOpenCodesShim(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("batch shims are a Windows lookup")
	}
	npm := t.TempDir()
	if err := os.WriteFile(filepath.Join(npm, "opencode.cmd"), []byte("@exit /b 9"), 0o755); err != nil {
		t.Fatal(err)
	}
	buildFake(t, filepath.Join(npm, "node_modules", "@opencode", "cli", "bin", "opencode.exe"))
	home := installedHome(t, nil, "opencode")
	exit, stdout, stderr := dotai(t, home, npm, t.TempDir(), nil, "run", "opencode", "--", "models", "a&b")
	if exit != 0 {
		t.Fatalf("exit code = %d, stderr = %q, want the fake run instead of the batch file", exit, stderr)
	}
	if got := launch(t, stdout); !slices.Equal(got.Args, []string{"models", "--standalone", "a&b"}) {
		t.Errorf("args = %q, want models --standalone a&b", got.Args)
	}
	if strings.Contains(stderr, "batch file") {
		t.Errorf("stderr = %q, want no note about a batch file", stderr)
	}
}
