package cli_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/lartweib/dotai/internal/app"
	"github.com/lartweib/dotai/internal/cli"
	"github.com/lartweib/dotai/internal/cli/output"
)

func testEnv(stdout, stderr *bytes.Buffer) cli.Env {
	return cli.Env{Stdout: stdout, Stderr: stderr, Version: fixedVersion, Validate: app.Validate}
}

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
		{name: "validate example text", args: []string{"validate", examplePack}, wantCode: 0, goldenOut: "validate-example.txt.golden"},
		{name: "validate example json", args: []string{"validate", examplePack, "--json"}, wantCode: 0, goldenOut: "validate-example.json.golden"},
		{name: "validate broken text", args: []string{"validate", brokenPack}, wantCode: 3, goldenOut: "validate-broken.txt.golden", wantStderr: true},
		{name: "validate broken json", args: []string{"validate", brokenPack, "--json"}, wantCode: 3, goldenOut: "validate-broken.json.golden"},
		{name: "validate two paths", args: []string{"validate", "a", "b"}, wantCode: 2, wantStderr: true},
		{name: "validate example with a profile", args: []string{"validate", examplePack, "--variant", "tmux", "--profile", "web"}, wantCode: 0, goldenOut: "validate-example.txt.golden"},
		{name: "validate merge rules text", args: []string{"validate", mergeBrokenPack}, wantCode: 3, goldenOut: "validate-merge-broken.txt.golden", wantStderr: true},
		{name: "validate merge rules json", args: []string{"validate", mergeBrokenPack, "--json"}, wantCode: 3, goldenOut: "validate-merge-broken.json.golden"},
		{name: "validate unknown profile", args: []string{"validate", examplePack, "--profile", "web,nope"}, wantCode: 3, goldenOut: "validate-unknown-profile.txt.golden", wantStderr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			code := cli.Run(tt.args, testEnv(&stdout, &stderr))

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

var (
	examplePack     = filepath.Join("..", "..", "examples", "minimal-pack")
	brokenPack      = filepath.Join("testdata", "broken-pack")
	mergeBrokenPack = filepath.Join("testdata", "merge-broken-pack")
)

func TestRunValidateDefaultsToCurrentDirectory(t *testing.T) {
	var got string
	env := cli.Env{
		Stdout: &bytes.Buffer{}, Stderr: &bytes.Buffer{},
		Validate: func(dir string, _ app.Selection) (app.ValidateResult, error) {
			got = dir
			return app.ValidateResult{Valid: true, Pack: &app.PackSummary{}}, nil
		},
	}
	if code := cli.Run([]string{"validate"}, env); code != cli.ExitOK || got != "." {
		t.Errorf("exit code = %d, dir = %q, want 0 and \".\"", code, got)
	}
}

func TestRunSelectionFlags(t *testing.T) {
	tests := []struct {
		args []string
		want app.Selection
	}{
		{args: nil, want: app.Selection{}},
		{args: []string{"--variant", "tmux"}, want: app.Selection{Variant: "tmux"}},
		{args: []string{"--profile", "web,docs", "--profile", "api"}, want: app.Selection{Profiles: []string{"web", "docs", "api"}}},
		{args: []string{"--profile="}, want: app.Selection{Profiles: []string{}}},
	}
	for _, tt := range tests {
		t.Run(strings.Join(tt.args, " "), func(t *testing.T) {
			var gotValidate app.Selection
			env := cli.Env{
				Stdout: &bytes.Buffer{}, Stderr: &bytes.Buffer{},
				Validate: func(_ string, sel app.Selection) (app.ValidateResult, error) {
					gotValidate = sel
					return app.ValidateResult{Valid: true, Pack: &app.PackSummary{}}, nil
				},
			}
			if code := cli.Run(append([]string{"validate"}, tt.args...), env); code != cli.ExitOK || !reflect.DeepEqual(gotValidate, tt.want) {
				t.Errorf("validate: exit code = %d, selection = %#v, want %#v", code, gotValidate, tt.want)
			}
		})
	}
}

func TestRunValidateUnreadableManifest(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "dotai.yaml"), 0o755); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	code := cli.Run([]string{"validate", dir, "--json"}, testEnv(&stdout, &stderr))

	if code != cli.ExitInternal {
		t.Errorf("exit code = %d, want %d", code, cli.ExitInternal)
	}
	if e := decodeOne(t, stdout.Bytes()); e.OK || e.Error == nil || e.Error.Code != output.CodeInternal {
		t.Errorf("envelope = %+v, want internal error", e)
	}
}

func TestRunValidateMissingDirectory(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := cli.Run([]string{"validate", filepath.Join(t.TempDir(), "missing"), "--json"}, testEnv(&stdout, &stderr))

	if code != cli.ExitInternal {
		t.Errorf("exit code = %d, want %d", code, cli.ExitInternal)
	}
	if e := decodeOne(t, stdout.Bytes()); e.OK || e.Command != "validate" || e.Error == nil || e.Error.Code != output.CodeInternal {
		t.Errorf("envelope = %+v, want internal error", e)
	}
}

func TestRunUsageErrorWhenJSONValueIsInvalid(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := cli.Run([]string{"version", "--json=garbage"}, cli.Env{Stdout: &stdout, Stderr: &stderr, Version: fixedVersion})

	if code != cli.ExitUsage {
		t.Errorf("exit code = %d, want %d", code, cli.ExitUsage)
	}
	e := decodeOne(t, stdout.Bytes())
	if e.OK || e.Error == nil || e.Error.Code != output.CodeUsage {
		t.Errorf("envelope = %+v, want usage error", e)
	}
	if stderr.Len() != 0 {
		t.Errorf("stderr = %q, want empty", stderr.String())
	}
}

func TestRunHelpInJSONMode(t *testing.T) {
	tests := []struct {
		args        []string
		wantCommand string
	}{
		{[]string{"--json"}, "dotai"},
		{[]string{"--json", "--help"}, "dotai"},
		{[]string{"version", "--help", "--json"}, "version"},
		{[]string{"help", "--json"}, "dotai"},
		{[]string{"help", "version", "--json"}, "version"},
	}
	for _, tt := range tests {
		t.Run(strings.Join(tt.args, " "), func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			code := cli.Run(tt.args, cli.Env{Stdout: &stdout, Stderr: &stderr, Version: fixedVersion})

			if code != cli.ExitOK {
				t.Errorf("exit code = %d, want 0", code)
			}
			var e struct {
				Schema  string `json:"schema"`
				Command string `json:"command"`
				OK      bool   `json:"ok"`
				Data    struct {
					Help string `json:"help"`
				} `json:"data"`
			}
			dec := json.NewDecoder(&stdout)
			if err := dec.Decode(&e); err != nil {
				t.Fatalf("stdout is not one envelope: %v", err)
			}
			if dec.More() {
				t.Errorf("stdout has more than one JSON document")
			}
			if e.Schema != output.Schema || e.Command != tt.wantCommand || !e.OK {
				t.Errorf("envelope = %+v, want ok for %q", e, tt.wantCommand)
			}
			if !strings.Contains(e.Data.Help, "Usage:") {
				t.Errorf("data.help = %q, want help text", e.Data.Help)
			}
			if stderr.Len() != 0 {
				t.Errorf("stderr = %q, want empty", stderr.String())
			}
		})
	}
}

// failingWriter fails the first `fails` writes and records the rest.
type failingWriter struct {
	fails int
	bytes.Buffer
}

func (w *failingWriter) Write(p []byte) (int, error) {
	if w.fails > 0 {
		w.fails--
		return 0, errors.New("disk full")
	}
	return w.Buffer.Write(p)
}

func TestRunInternalError(t *testing.T) {
	t.Run("text", func(t *testing.T) {
		stdout := &failingWriter{fails: 1}
		var stderr bytes.Buffer
		code := cli.Run([]string{"version"}, cli.Env{Stdout: stdout, Stderr: &stderr, Version: fixedVersion})

		if code != cli.ExitInternal {
			t.Errorf("exit code = %d, want %d", code, cli.ExitInternal)
		}
		if stdout.Len() != 0 {
			t.Errorf("stdout = %q, want empty", stdout.String())
		}
		if got := stderr.String(); !strings.Contains(got, "disk full") || strings.Contains(got, "--help") {
			t.Errorf("stderr = %q, want the error without usage hint", got)
		}
	})
	t.Run("json", func(t *testing.T) {
		stdout := &failingWriter{fails: 1}
		var stderr bytes.Buffer
		code := cli.Run([]string{"version", "--json"}, cli.Env{Stdout: stdout, Stderr: &stderr, Version: fixedVersion})

		if code != cli.ExitInternal {
			t.Errorf("exit code = %d, want %d", code, cli.ExitInternal)
		}
		e := decodeOne(t, stdout.Bytes())
		if e.OK || e.Command != "version" || e.Error == nil || e.Error.Code != output.CodeInternal ||
			!strings.Contains(e.Error.Message, "disk full") {
			t.Errorf("envelope = %+v, want internal error", e)
		}
		if stderr.Len() != 0 {
			t.Errorf("stderr = %q, want empty", stderr.String())
		}
	})
}

type envelope struct {
	OK      bool          `json:"ok"`
	Command string        `json:"command"`
	Error   *output.Error `json:"error"`
}

func decodeOne(t *testing.T, b []byte) envelope {
	t.Helper()
	var e envelope
	dec := json.NewDecoder(bytes.NewReader(b))
	if err := dec.Decode(&e); err != nil {
		t.Fatalf("stdout %q is not an envelope: %v", b, err)
	}
	if dec.More() {
		t.Errorf("stdout has more than one JSON document")
	}
	return e
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
