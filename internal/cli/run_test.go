package cli_test

import (
	"bytes"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/lartweib/dotai/internal/app"
	"github.com/lartweib/dotai/internal/cli"
	"github.com/lartweib/dotai/internal/envelope"
)

// runPlan is Claude Code's launch with fixed paths, so that the output
// reads the same on every OS.
var runPlan = app.RunPlan{
	Harness: app.HarnessPlan{
		ID: "claudecode", Level: "partial", Dir: "/home/u/.dotai/isolated/claude", Relogin: "yes",
		Pointers: []app.PointerPlan{{Mechanism: "env", Name: "CLAUDE_CONFIG_DIR", Dir: "/home/u/.dotai/isolated/claude"}},
		Leaks:    []string{"the CLAUDE.md, .claude/CLAUDE.md and CLAUDE.local.md of each directory above the working directory still load, but for your ~/CLAUDE.md, ~/CLAUDE.local.md, ~/.claude/CLAUDE.md and ~/.claude/rules, which the isolated settings exclude with claudeMdExcludes", "credentials do not carry over"},
	},
	Command: "claude", Program: "/usr/bin/claude", Env: []string{"CLAUDE_CONFIG_DIR=/home/u/.dotai/isolated/claude"},
}

// fakeRun plans res and launches it with exit code, recording how it was
// called.
type fakeRun struct {
	res      app.RunPlan
	err      error
	code     int
	home, id string
	args     []string
	planned  bool
	launched *app.RunPlan
}

func (f *fakeRun) env(stdout, stderr *bytes.Buffer) cli.Env {
	env := testEnv(stdout, stderr)
	env.PlanRun = func(home, id string, args []string) (app.RunPlan, error) {
		f.planned, f.home, f.id, f.args = true, home, id, args
		if f.err != nil {
			return app.RunPlan{}, f.err
		}
		res := f.res
		res.Args = args
		return res, nil
	}
	env.Launch = func(p app.RunPlan) (int, error) {
		f.launched = &p
		return f.code, nil
	}
	return env
}

func TestRunRunLaunchesAndForwardsTheExitCode(t *testing.T) {
	home := restoreHome(t)
	f := &fakeRun{res: runPlan, code: 7}
	var stdout, stderr bytes.Buffer
	code := cli.Run([]string{"run", "claudecode", "--", "--model", "opus", "-p", "hi there"}, f.env(&stdout, &stderr))
	if code != 7 {
		t.Errorf("exit code = %d, want the harness's 7", code)
	}
	if f.home != home || f.id != "claudecode" {
		t.Errorf("PlanRun(%q, %q), want the home %s and claudecode", f.home, f.id, home)
	}
	if want := []string{"--model", "opus", "-p", "hi there"}; !reflect.DeepEqual(f.args, want) || f.launched == nil || !reflect.DeepEqual(f.launched.Args, want) {
		t.Errorf("args = %q, launched = %+v, want %q passed through", f.args, f.launched, want)
	}
	if stdout.Len() != 0 {
		t.Errorf("stdout = %q, want nothing: it belongs to the harness", stdout.String())
	}
	want := "dotai: claudecode · partial isolation · CLAUDE_CONFIG_DIR=/home/u/.dotai/isolated/claude\n" +
		"dotai: login: you must log in again in the isolated configuration\n" +
		"dotai: leaks: the CLAUDE.md, .claude/CLAUDE.md and CLAUDE.local.md of each directory above the working directory still load, but for your ~/CLAUDE.md, ~/CLAUDE.local.md, ~/.claude/CLAUDE.md and ~/.claude/rules, which the isolated settings exclude with claudeMdExcludes\n" +
		"dotai: leaks: credentials do not carry over\n"
	if stderr.String() != want {
		t.Errorf("stderr = %q, want %q", stderr.String(), want)
	}
}

// TestRunRunStderrLines checks the launch lines dotai run prints on stderr
// for plans that differ from runPlan in one way: the isolation level and
// pointers, the login, the leaks, the notes and the warnings.
func TestRunRunStderrLines(t *testing.T) {
	const (
		exact = iota
		contains
		suffix
	)
	tests := []struct {
		name   string
		mutate func(p *app.RunPlan)
		id     string
		match  int
		want   []string
	}{
		{
			name: "the notes come after the leaks",
			mutate: func(p *app.RunPlan) {
				p.Notes = []string{`claude resolves to the batch file C:\npm\claude.cmd: Ctrl-C may show cmd's prompt`}
			},
			id: "claudecode", match: suffix,
			want: []string{"dotai: leaks: credentials do not carry over\ndotai: note: claude resolves to the batch file C:\\npm\\claude.cmd: Ctrl-C may show cmd's prompt\n"},
		},
		{
			name:   "the lines without relogin or leaks",
			mutate: func(p *app.RunPlan) { p.Harness.Relogin, p.Harness.Leaks = "no", []string{} },
			id:     "claudecode", match: contains,
			want: []string{"dotai: login: keeps your login\n", "dotai: leaks: nothing known\n"},
		},
		{
			// A flag pointer reads as the one argument the harness gets.
			name: "a flag pointer as it is passed",
			mutate: func(p *app.RunPlan) {
				p.Harness = app.HarnessPlan{
					ID: "antigravity-cli", Level: "partial", Dir: "/home/u/.dotai/isolated/gemini", Relogin: "no",
					Pointers: []app.PointerPlan{{Mechanism: "flag", Name: "--gemini_dir", Dir: "/home/u/.dotai/isolated/gemini"}},
					Leaks:    []string{"authentication is shared through the OS keyring"},
				}
			},
			id: "antigravity-cli", match: exact,
			want: []string{"dotai: antigravity-cli · partial isolation · --gemini_dir=/home/u/.dotai/isolated/gemini\n" +
				"dotai: login: keeps your login\n" +
				"dotai: leaks: authentication is shared through the OS keyring\n"},
		},
		{
			name:   "a login that is unknown",
			mutate: func(p *app.RunPlan) { p.Harness.Relogin = "unknown" },
			id:     "claudecode", match: contains,
			want: []string{"dotai: login: you may need to log in again in the isolated configuration (not verified on this OS)\n"},
		},
		{
			// OpenCode's first line ends with the variables set to a fixed
			// value, and the note about --standalone follows.
			name: "the fixed variables with the pointers",
			mutate: func(p *app.RunPlan) {
				p.Harness = app.HarnessPlan{
					ID: "opencode", Level: "partial", Dir: "/home/u/.dotai/isolated/opencode", Relogin: "yes",
					Pointers: []app.PointerPlan{{Mechanism: "env", Name: "XDG_CONFIG_HOME", Dir: "/home/u/.dotai/isolated/opencode/config"}},
					Leaks:    []string{"provider credentials do not carry over"},
				}
				p.Vars = []string{"OPENCODE_DISABLE_CLAUDE_CODE=1"}
				p.Notes = []string{"--standalone added: OpenCode runs a private server"}
			},
			id: "opencode", match: exact,
			want: []string{"dotai: opencode · partial isolation · XDG_CONFIG_HOME=/home/u/.dotai/isolated/opencode/config, OPENCODE_DISABLE_CLAUDE_CODE=1\n" +
				"dotai: login: you must log in again in the isolated configuration\n" +
				"dotai: leaks: provider credentials do not carry over\n" +
				"dotai: note: --standalone added: OpenCode runs a private server\n"},
		},
		{
			name: "the warnings come after the notes",
			mutate: func(p *app.RunPlan) {
				p.Notes = []string{"a note"}
				p.Warnings = []string{`"newcommand" is not a command known to take --standalone`}
			},
			id: "claudecode", match: suffix,
			want: []string{"dotai: note: a note\ndotai: warning: \"newcommand\" is not a command known to take --standalone\n"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			restoreHome(t)
			p := runPlan
			tt.mutate(&p)
			f := &fakeRun{res: p}
			var stderr bytes.Buffer
			if code := cli.Run([]string{"run", tt.id}, f.env(&bytes.Buffer{}, &stderr)); code != cli.ExitOK {
				t.Errorf("exit code = %d, want %d", code, cli.ExitOK)
			}
			got := stderr.String()
			for _, want := range tt.want {
				switch {
				case tt.match == exact && got != want:
					t.Errorf("stderr = %q, want %q", got, want)
				case tt.match == contains && !strings.Contains(got, want):
					t.Errorf("stderr = %q, want it to contain %q", got, want)
				case tt.match == suffix && !strings.HasSuffix(got, want):
					t.Errorf("stderr = %q, want it to end with %q", got, want)
				}
			}
		})
	}
}

func TestRunRunPassesArgumentsWithoutADash(t *testing.T) {
	tests := []struct {
		args []string
		want []string
	}{
		{args: []string{"run", "claudecode", "-p", "hi", "--json", "--help"}, want: []string{"-p", "hi", "--json", "--help"}},
		{args: []string{"run", "claudecode"}, want: []string{}},
		{args: []string{"run", "claudecode", "--", "--", "x"}, want: []string{"--", "x"}},
		{args: []string{"run", "--", "claudecode", "--", "x"}, want: []string{"x"}},
		{args: []string{"run", "claudecode", "x", "--", "y"}, want: []string{"x", "--", "y"}},
	}
	for _, tt := range tests {
		t.Run(strings.Join(tt.args, " "), func(t *testing.T) {
			restoreHome(t)
			f := &fakeRun{res: runPlan}
			var stdout bytes.Buffer
			if code := cli.Run(tt.args, f.env(&stdout, &bytes.Buffer{})); code != cli.ExitOK {
				t.Fatalf("exit code = %d, want %d", code, cli.ExitOK)
			}
			if !reflect.DeepEqual(f.args, tt.want) {
				t.Errorf("args = %q, want %q", f.args, tt.want)
			}
			if stdout.Len() != 0 {
				t.Errorf("stdout = %q, want nothing", stdout.String())
			}
		})
	}
}

func TestRunRunUsageErrors(t *testing.T) {
	tests := []struct {
		args []string
		json bool
	}{
		{args: []string{"run"}},
		{args: []string{"run", "--json", "claudecode"}, json: true},
		{args: []string{"--json", "run", "claudecode"}, json: true},
		{args: []string{"run", "--bogus", "claudecode"}},
	}
	for _, tt := range tests {
		t.Run(strings.Join(tt.args, " "), func(t *testing.T) {
			restoreHome(t)
			f := &fakeRun{res: runPlan}
			var stdout, stderr bytes.Buffer
			if code := cli.Run(tt.args, f.env(&stdout, &stderr)); code != cli.ExitUsage {
				t.Errorf("exit code = %d, want %d", code, cli.ExitUsage)
			}
			if f.planned || f.launched != nil {
				t.Error("PlanRun or Launch called on a usage error")
			}
			if !tt.json {
				return
			}
			e := decodeOne(t, stdout.Bytes())
			if e.OK || e.Command != "run" || e.Error == nil || e.Error.Code != envelope.CodeUsage || !strings.Contains(e.Error.Message, "--json") {
				t.Errorf("envelope = %+v, want a usage error about --json", e)
			}
		})
	}
}

func TestRunRunFailures(t *testing.T) {
	restoreHome(t)
	tests := []struct {
		name string
		f    *fakeRun
		args []string
		want int
	}{
		{name: "plan fails", f: &fakeRun{err: errors.New("there is no install state")}, args: []string{"run", "claudecode", "--json"}, want: cli.ExitInternal},
		{name: "launch fails", f: &fakeRun{res: runPlan}, args: []string{"run", "claudecode"}, want: cli.ExitInternal},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			env := tt.f.env(&stdout, &stderr)
			if tt.name == "launch fails" {
				env.Launch = func(app.RunPlan) (int, error) { return 0, errors.New("launching /usr/bin/claude: exec format error") }
			}
			if code := cli.Run(tt.args, env); code != tt.want {
				t.Errorf("exit code = %d, want %d", code, tt.want)
			}
			if stdout.Len() != 0 || !strings.HasPrefix(stderr.String(), "Error: ") && !strings.Contains(stderr.String(), "\nError: ") {
				t.Errorf("stdout = %q, stderr = %q, want the error as text on stderr only, even with --json after the harness", stdout.String(), stderr.String())
			}
		})
	}
}

// TestRunRunReal plans with the real use case in a home with no install:
// the harness checks and the missing state, without launching anything.
func TestRunRunReal(t *testing.T) {
	restoreHome(t)
	tests := []struct {
		harness string
		want    int
		inErr   string
	}{
		{harness: "antigravity-cli", want: cli.ExitInternal, inErr: "no install state"},
		{harness: "opencode", want: cli.ExitInternal, inErr: "no install state"},
		{harness: "agy", want: cli.ExitUsage, inErr: `unknown harness "agy"`},
		{harness: "claudecode", want: cli.ExitInternal, inErr: "no install state"},
	}
	for _, tt := range tests {
		t.Run(tt.harness, func(t *testing.T) {
			var stderr bytes.Buffer
			env := testEnv(&bytes.Buffer{}, &stderr)
			env.PlanRun = app.PlanRun
			env.Launch = func(app.RunPlan) (int, error) {
				t.Error("Launch called")
				return 0, nil
			}
			if code := cli.Run([]string{"run", tt.harness}, env); code != tt.want || !strings.Contains(stderr.String(), tt.inErr) {
				t.Errorf("exit code = %d, stderr = %q, want %d and %q", code, stderr.String(), tt.want, tt.inErr)
			}
		})
	}
}
