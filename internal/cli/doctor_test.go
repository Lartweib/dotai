package cli_test

import (
	"bytes"
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lartweib/dotai/internal/app"
	"github.com/lartweib/dotai/internal/cli"
)

// doctorFound is what doctor finds with fixed paths: a file edited by
// hand, an MCP server gone from Claude Code's .claude.json, an orphan
// entry and an exclude block that --fix wrote.
var doctorFound = app.DoctorResult{
	State:    "/home/u/.dotai/state.json",
	Projects: []string{"/work/site"},
	Findings: []app.Finding{
		{Code: app.CodeFileChanged, Harness: "claudecode", Path: "/home/u/.dotai/isolated/claude/CLAUDE.md", Key: []string{},
			Message: "/home/u/.dotai/isolated/claude/CLAUDE.md was changed since dotai installed it: an install stops on it until you move it away, and a restore asks before overwriting it"},
		{Code: app.CodeKeyMissing, Harness: "claudecode", Project: "/work/site", Path: "/home/u/.claude.json", Key: []string{"projects", "/work/site", "mcpServers", "browser"},
			Message: "the key projects[\"/work/site\"].mcpServers.browser that dotai set in /home/u/.claude.json is gone: apply again in /work/site to put it back"},
		{Code: app.CodeOrphanEntry, Project: "/work/old", Path: "/home/u/.dotai/projects.json", Key: []string{},
			Message: "/home/u/.dotai/projects.json lists the project /work/old, but its directory is gone: the keys it owns in /home/u/.claude.json stay there until you remove them and its entry by hand"},
		{Code: app.CodeExcludeMissing, Project: "/work/site", Path: "/work/site/.git/info/exclude", Key: []string{}, Fixable: true, Fixed: true,
			Message: "wrote the 4 exclude entries of /work/site to /work/site/.git/info/exclude"},
	},
	Backups: []app.BackupRef{{ID: "20261103T101500.000Z_apply", Path: "/home/u/.dotai/projects/0123456789abcdef/backups/20261103T101500.000Z_apply"}},
}

func TestRunDoctor(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	for _, tt := range []struct {
		name, golden string
		args         []string
	}{
		{"text", "doctor.txt.golden", []string{"doctor", "--fix"}},
		{"json", "doctor.json.golden", []string{"doctor", "--fix", "--json"}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			env := testEnv(&stdout, &stderr)
			env.Doctor = func(_ context.Context, gotHome string, opts app.DoctorOptions) (app.DoctorResult, error) {
				if gotHome != home || !opts.Fix {
					t.Errorf("Doctor(%q, %+v), want the home and --fix", gotHome, opts)
				}
				return doctorFound, nil
			}
			if code := cli.Run(tt.args, env); code != cli.ExitInvalid || stderr.Len() != 0 {
				t.Errorf("exit code = %d, stderr = %q, want %d and nothing on stderr", code, stderr.String(), cli.ExitInvalid)
			}
			assertGolden(t, filepath.Join("testdata", tt.golden), stdout.Bytes())
		})
	}
}

func TestRunDoctorWithNothingFound(t *testing.T) {
	var stdout bytes.Buffer
	env := testEnv(&stdout, &bytes.Buffer{})
	env.Doctor = func(context.Context, string, app.DoctorOptions) (app.DoctorResult, error) {
		return app.DoctorResult{State: "/home/u/.dotai/state.json", Projects: []string{}, Findings: []app.Finding{}, Backups: []app.BackupRef{}}, nil
	}
	if code := cli.Run([]string{"doctor", "--json"}, env); code != cli.ExitOK {
		t.Fatalf("exit code = %d, want 0", code)
	}
	if e := decodeOne(t, stdout.Bytes()); !e.OK || e.Command != "doctor" {
		t.Errorf("envelope = %+v, want a success", e)
	}
	stdout.Reset()
	if code := cli.Run([]string{"doctor"}, env); code != cli.ExitOK || !strings.HasPrefix(stdout.String(), "Doctor · nothing changed outside dotai\n") {
		t.Errorf("exit code = %d, stdout = %q", code, stdout.String())
	}
	if code := cli.Run([]string{"doctor", "extra"}, env); code != cli.ExitUsage {
		t.Errorf("exit code = %d, want a usage error for an argument", code)
	}
}

// TestRunDoctorReal runs the real doctor in a home with nothing installed.
func TestRunDoctorReal(t *testing.T) {
	home := restoreHome(t)
	var stdout bytes.Buffer
	env := testEnv(&stdout, &bytes.Buffer{})
	env.Doctor = app.Doctor
	if code := cli.Run([]string{"doctor", "--fix"}, env); code != cli.ExitOK || !strings.Contains(stdout.String(), "state:    none, nothing installed\n") {
		t.Errorf("exit code = %d, stdout = %q", code, stdout.String())
	}
	if got := filepath.Join(home, ".dotai"); dirExists(got) {
		t.Errorf("%s exists, want nothing created", got)
	}
}
