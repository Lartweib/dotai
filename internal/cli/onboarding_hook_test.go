package cli_test

import (
	"bytes"
	"context"
	"errors"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/lartweib/dotai/internal/cli"
)

func TestRunOnboardingHook(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	const line = "dotai: this repository is not set up."
	tests := []struct {
		name string
		args []string
		hook func(ctx context.Context) (string, error)
		want string
	}{
		{name: "plain text for Claude Code", args: []string{"--harness", "claudecode"}, hook: func(context.Context) (string, error) { return line, nil }, want: line + "\n"},
		{name: "plain text for OpenCode", args: []string{"--harness", "opencode"}, hook: func(context.Context) (string, error) { return line, nil }, want: line + "\n"},
		{name: "JSON that agy reads", args: []string{"--harness", "antigravity-cli"}, hook: func(context.Context) (string, error) { return `say "hi"`, nil },
			want: `{"injectSteps":[{"systemMessage":{"systemMessage":"say \"hi\""}}]}` + "\n"},
		{name: "silence", args: []string{"--harness", "antigravity-cli"}, hook: func(context.Context) (string, error) { return "", nil }},
		{name: "an error is silent", args: []string{"--harness", "claudecode"}, hook: func(context.Context) (string, error) { return line, errors.New("broken") }},
		{name: "past the time bound is silent", args: []string{"--harness", "claudecode"}, hook: func(ctx context.Context) (string, error) {
			<-ctx.Done()
			time.Sleep(50 * time.Millisecond)
			return line, nil
		}},
		{name: "an envelope with --json", args: []string{"--harness", "claudecode", "--json"}, hook: func(context.Context) (string, error) { return line, nil },
			want: "{\n  \"schema\": \"dotai.cli/v1\",\n  \"command\": \"onboarding hook\",\n  \"ok\": true,\n  \"data\": {\n    \"line\": \"" + line + "\"\n  }\n}\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			env := testEnv(&stdout, &stderr)
			env.OnboardingHook = func(ctx context.Context, gotCwd, gotHome string) (string, error) {
				if gotCwd != cwd || gotHome != home {
					t.Errorf("OnboardingHook(%q, %q), want the current and the home directory", gotCwd, gotHome)
				}
				return tt.hook(ctx)
			}
			start := time.Now()
			code := cli.Run(append([]string{"onboarding", "hook"}, tt.args...), env)
			if code != cli.ExitOK || stdout.String() != tt.want || stderr.Len() != 0 {
				t.Errorf("exit code = %d, stdout = %q, stderr = %q, want 0, %q and nothing", code, stdout.String(), stderr.String(), tt.want)
			}
			if d := time.Since(start); d > time.Second {
				t.Errorf("the hook took %v, want it bounded", d)
			}
		})
	}
}

// TestRunOnboardingHookWaitsForTheHookItStopped checks the bound of 1 s:
// the hook's ctx ends before it, and the command waits for the hook to
// return, as the git it runs is killed, so that no process is left behind.
func TestRunOnboardingHookWaitsForTheHookItStopped(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("USERPROFILE", os.Getenv("HOME"))
	var stdout, stderr bytes.Buffer
	env := testEnv(&stdout, &stderr)
	var returned atomic.Bool
	var bound time.Duration
	start := time.Now()
	env.OnboardingHook = func(ctx context.Context, _, _ string) (string, error) {
		deadline, _ := ctx.Deadline()
		bound = deadline.Sub(start)
		<-ctx.Done()
		time.Sleep(50 * time.Millisecond) // git, killed, exits
		returned.Store(true)
		return "dotai: late", nil
	}
	code := cli.Run([]string{"onboarding", "hook", "--harness", "claudecode"}, env)
	if code != cli.ExitOK || stdout.Len() != 0 {
		t.Errorf("exit code = %d, stdout = %q, want 0 and silence", code, stdout.String())
	}
	if !returned.Load() {
		t.Errorf("the command returned before the hook it stopped")
	}
	if bound <= 0 || bound > time.Second {
		t.Errorf("the hook's ctx ends after %v, want within 1 s", bound)
	}
}

// TestRunOnboardingHookIgnoresAnUnknownHarness checks that a harness the
// hook does not know gets nothing on stdout, a note on stderr, and exit 0,
// without the hook running: a session never breaks on it.
func TestRunOnboardingHookIgnoresAnUnknownHarness(t *testing.T) {
	var stdout, stderr bytes.Buffer
	env := testEnv(&stdout, &stderr)
	env.OnboardingHook = func(context.Context, string, string) (string, error) {
		t.Error("the hook ran for an unknown harness")
		return "dotai: line", nil
	}
	code := cli.Run([]string{"onboarding", "hook", "--harness", "claude\ncode"}, env)
	if code != cli.ExitOK || stdout.Len() != 0 || !strings.Contains(stderr.String(), "unknown harness") || strings.Count(stderr.String(), "\n") != 1 {
		t.Errorf("exit code = %d, stdout = %q, stderr = %q, want 0, nothing and one note", code, stdout.String(), stderr.String())
	}
}
