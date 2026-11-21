package cli_test

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/lartweib/dotai/internal/app"
	"github.com/lartweib/dotai/internal/cli"
)

// TestInstallAndRestoreWireTheirSteps checks that the commands give the use
// case a step reporter and print what it reports on stderr, and print nothing
// there with --json: a use case here reports through the context as the real
// ones do, so that a command that does not wire the reporter fails.
func TestInstallAndRestoreWireTheirSteps(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("USERPROFILE", t.TempDir())
	t.Setenv("DOTAI_CACHE_DIR", t.TempDir())
	report := func(ctx context.Context) {
		app.ReportStep(ctx, app.Step{Phase: app.PhaseWipe, N: 1, Total: 2, Item: "~/.claude/plugins"})
		app.ReportStep(ctx, app.Step{Phase: app.PhaseWipe, N: 2, Total: 2, Item: "~/.claude/skills"})
	}
	run := func(args ...string) (int, string, string) {
		var stdout, stderr bytes.Buffer
		env := testEnv(&stdout, &stderr)
		env.Install = func(ctx context.Context, _, _, _ string, _ app.InstallOptions, _ app.Progress, _ app.Warn, _ app.Consent) (app.InstallResult, error) {
			report(ctx)
			return app.InstallResult{}, nil
		}
		env.Restore = func(ctx context.Context, _, _ string, _ app.RestoreOptions, _ app.Warn, _ func(app.RestoreResult) (bool, error)) (app.RestoreResult, error) {
			report(ctx)
			return app.RestoreResult{}, nil
		}
		code := cli.Run(args, env)
		return code, stdout.String(), stderr.String()
	}
	for _, cmd := range [][]string{{"install", t.TempDir()}, {"restore"}} {
		code, stdout, stderr := run(cmd...)
		if want := "wiping 1/2 ~/.claude/plugins\nwiping 2/2 ~/.claude/skills\ndone\n"; code != cli.ExitOK || stderr != want {
			t.Errorf("%s: exit code = %d, stderr = %q, stdout = %q, want stderr %q", cmd[0], code, stderr, stdout, want)
		}
		code, stdout, stderr = run(append(cmd, "--json")...)
		if code != cli.ExitOK || stderr != "" || !strings.HasPrefix(stdout, "{") {
			t.Errorf("%s --json: exit code = %d, stderr = %q, stdout = %q, want an envelope and no stderr", cmd[0], code, stderr, stdout)
		}
	}
}
