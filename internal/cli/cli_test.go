package cli_test

import (
	"bytes"
	"flag"
	"os"
	"path/filepath"
	"testing"

	"github.com/lartweib/dotai/internal/app"
	"github.com/lartweib/dotai/internal/cli"
)

var update = flag.Bool("update", false, "rewrite golden files")

func fixedVersion() app.VersionInfo {
	return app.VersionInfo{
		Version:   "1.2.3",
		Commit:    "deadbeef",
		BuildDate: "2026-10-27T10:00:00Z",
		GoVersion: "go1.26.5",
		OS:        "linux",
		Arch:      "amd64",
	}
}

func TestRun(t *testing.T) {
	tests := []struct {
		name       string
		args       []string
		wantCode   int
		goldenOut  string // golden for stdout; empty means stdout must be empty
		wantStderr bool
	}{
		{name: "version text", args: []string{"version"}, wantCode: 0, goldenOut: "version.txt.golden"},
		{name: "version json", args: []string{"version", "--json"}, wantCode: 0, goldenOut: "version.json.golden"},
		{name: "json flag before command", args: []string{"--json", "version"}, wantCode: 0, goldenOut: "version.json.golden"},
		{name: "unknown command json", args: []string{"nope", "--json"}, wantCode: 2, goldenOut: "unknown-command.json.golden"},
		{name: "unknown command text", args: []string{"nope"}, wantCode: 2, wantStderr: true},
		{name: "unexpected argument json", args: []string{"version", "extra", "--json"}, wantCode: 2, goldenOut: "unexpected-arg.json.golden"},
		{name: "unknown flag json", args: []string{"version", "--bogus", "--json"}, wantCode: 2, goldenOut: "unknown-flag.json.golden"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			code := cli.Run(tt.args, cli.Env{Stdout: &stdout, Stderr: &stderr, Version: fixedVersion})

			if code != tt.wantCode {
				t.Errorf("exit code = %d, want %d", code, tt.wantCode)
			}
			if tt.goldenOut != "" {
				assertGolden(t, filepath.Join("testdata", tt.goldenOut), stdout.Bytes())
			} else if stdout.Len() != 0 {
				t.Errorf("stdout = %q, want empty", stdout.String())
			}
			if gotStderr := stderr.Len() != 0; gotStderr != tt.wantStderr {
				t.Errorf("stderr = %q, want non-empty: %v", stderr.String(), tt.wantStderr)
			}
		})
	}
}

func assertGolden(t *testing.T, path string, got []byte) {
	t.Helper()
	if *update {
		if err := os.WriteFile(path, got, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Errorf("output mismatch for %s\ngot:\n%s\nwant:\n%s", path, got, want)
	}
}
