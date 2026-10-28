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
	return cli.Env{Stdout: stdout, Stderr: stderr, Version: fixedVersion, Validate: app.Validate, Stage: app.Stage}
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
		{name: "validate broken text", args: []string{"validate", brokenPack}, wantCode: 3, goldenOut: "validate-broken.txt.golden"},
		{name: "validate broken json", args: []string{"validate", brokenPack, "--json"}, wantCode: 3, goldenOut: "validate-broken.json.golden"},
		{name: "validate two paths", args: []string{"validate", "a", "b"}, wantCode: 2, wantStderr: true},
		{name: "validate example with a profile", args: []string{"validate", examplePack, "--variant", "tmux", "--profile", "web"}, wantCode: 0, goldenOut: "validate-example.txt.golden"},
		{name: "validate merge rules text", args: []string{"validate", mergeBrokenPack}, wantCode: 3, goldenOut: "validate-merge-broken.txt.golden"},
		{name: "validate merge rules compact", args: []string{"validate", mergeBrokenPack, "--format=compact"}, wantCode: 3, goldenOut: "validate-merge-broken.compact.golden"},
		{name: "validate json ignores the format", args: []string{"validate", mergeBrokenPack, "--json", "--format=compact"}, wantCode: 3, goldenOut: "validate-merge-broken.json.golden"},
		{name: "validate unknown format", args: []string{"validate", examplePack, "--format=fancy"}, wantCode: 2, wantStderr: true},
		{name: "validate unknown format json", args: []string{"validate", examplePack, "--json", "--format=fancy"}, wantCode: 2, goldenOut: "validate-unknown-format.json.golden"},
		{name: "validate merge rules json", args: []string{"validate", mergeBrokenPack, "--json"}, wantCode: 3, goldenOut: "validate-merge-broken.json.golden"},
		{name: "validate unknown profile", args: []string{"validate", examplePack, "--profile", "web,nope"}, wantCode: 3, goldenOut: "validate-unknown-profile.txt.golden"},
		{name: "stage without out", args: []string{"stage", examplePack, "--json"}, wantCode: 2, goldenOut: "stage-no-out.json.golden"},
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
			var gotValidate, gotStage app.Selection
			env := cli.Env{
				Stdout: &bytes.Buffer{}, Stderr: &bytes.Buffer{},
				Validate: func(_ string, sel app.Selection) (app.ValidateResult, error) {
					gotValidate = sel
					return app.ValidateResult{Valid: true, Pack: &app.PackSummary{}}, nil
				},
				Stage: func(_, _ string, sel app.Selection, _ bool) (app.StageResult, error) {
					gotStage = sel
					return app.StageResult{}, nil
				},
			}
			if code := cli.Run(append([]string{"validate"}, tt.args...), env); code != cli.ExitOK || !reflect.DeepEqual(gotValidate, tt.want) {
				t.Errorf("validate: exit code = %d, selection = %#v, want %#v", code, gotValidate, tt.want)
			}
			if code := cli.Run(append([]string{"stage", "--out", "x"}, tt.args...), env); code != cli.ExitOK || !reflect.DeepEqual(gotStage, tt.want) {
				t.Errorf("stage: exit code = %d, selection = %#v, want %#v", code, gotStage, tt.want)
			}
		})
	}
}

func TestRunStage(t *testing.T) {
	out := t.TempDir()
	var stdout, stderr bytes.Buffer
	code := cli.Run([]string{"stage", examplePack, "--out", out, "--variant", "tmux", "--profile", "web", "--json"}, testEnv(&stdout, &stderr))

	if code != cli.ExitOK || stderr.Len() != 0 {
		t.Errorf("exit code = %d, stderr = %q, want 0 and empty", code, stderr.String())
	}
	assertGolden(t, filepath.Join("testdata", "stage-example.json.golden"), stdout.Bytes())
	if _, err := os.Stat(filepath.Join(out, "user", ".rulesync", "subagents", "reviewer.md")); err != nil {
		t.Errorf("staged file missing: %v", err)
	}

	stdout.Reset()
	code = cli.Run([]string{"stage", examplePack, "--out", out}, testEnv(&stdout, &stderr))
	if code != cli.ExitInternal || !strings.Contains(stderr.String(), "use --force") {
		t.Errorf("second run: exit code = %d, stderr = %q, want a refusal", code, stderr.String())
	}

	stdout.Reset()
	stderr.Reset()
	code = cli.Run([]string{"stage", examplePack, "--out", out, "--force"}, testEnv(&stdout, &stderr))
	if code != cli.ExitOK || !strings.Contains(stdout.String(), "user/.rulesync/rules/overview.md  base") {
		t.Errorf("forced run: exit code = %d, stdout = %q", code, stdout.String())
	}
}

func TestRunStageOutInsidePack(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := cli.Run([]string{"stage", examplePack, "--out", filepath.Join(examplePack, "staging"), "--json"}, testEnv(&stdout, &stderr))

	if code != cli.ExitUsage {
		t.Errorf("exit code = %d, want %d", code, cli.ExitUsage)
	}
	if e := decodeOne(t, stdout.Bytes()); e.OK || e.Error == nil || e.Error.Code != output.CodeUsage || !strings.Contains(e.Error.Message, "inside the pack") {
		t.Errorf("envelope = %+v, want a usage error", e)
	}
}

func TestRunStageInvalidPack(t *testing.T) {
	out := filepath.Join(t.TempDir(), "staging")
	var stdout, stderr bytes.Buffer
	code := cli.Run([]string{"stage", mergeBrokenPack, "--out", out, "--json"}, testEnv(&stdout, &stderr))

	if code != cli.ExitInvalid {
		t.Errorf("exit code = %d, want %d", code, cli.ExitInvalid)
	}
	if e := decodeOne(t, stdout.Bytes()); e.OK || e.Error == nil || e.Error.Code != output.CodeInvalid {
		t.Errorf("envelope = %+v, want invalid", e)
	}
	if _, err := os.Stat(out); !os.IsNotExist(err) {
		t.Errorf("out exists after an invalid stage: %v", err)
	}
}

func TestRunStageReportHeader(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := cli.Run([]string{"stage", mergeBrokenPack, "--out", filepath.Join(t.TempDir(), "s")}, testEnv(&stdout, &stderr))

	if code != cli.ExitInvalid || !strings.HasPrefix(stdout.String(), "✗ merge-broken-pack 0.1.0 · ") {
		t.Errorf("exit code = %d, stdout = %q, want %d and the pack in the header", code, stdout.String(), cli.ExitInvalid)
	}
}

// TestRunTextHasNoColor runs with a real file as stdout, which is not a
// terminal, so the report must stay plain even without NO_COLOR.
func TestRunTextHasNoColor(t *testing.T) {
	t.Setenv("NO_COLOR", "")
	f, err := os.Create(filepath.Join(t.TempDir(), "out.txt"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	env := testEnv(nil, &bytes.Buffer{})
	env.Stdout = f
	if code := cli.Run([]string{"validate", mergeBrokenPack}, env); code != cli.ExitInvalid {
		t.Fatalf("exit code = %d, want %d", code, cli.ExitInvalid)
	}
	out, err := os.ReadFile(f.Name())
	if err != nil {
		t.Fatal(err)
	}
	if len(out) == 0 || bytes.Contains(out, []byte("\x1b[")) {
		t.Errorf("stdout = %q, want a report without ANSI escape codes", out)
	}
}

func TestEnableANSIOnAFileIsANoOp(t *testing.T) {
	f, err := os.Create(filepath.Join(t.TempDir(), "out.txt"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	cli.EnableANSI(f)()
}

func TestRunStageCompactDiagnostics(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := cli.Run([]string{"stage", mergeBrokenPack, "--out", filepath.Join(t.TempDir(), "s"), "--format", "compact"}, testEnv(&stdout, &stderr))

	if code != cli.ExitInvalid || stderr.Len() != 0 {
		t.Errorf("exit code = %d, stderr = %q, want %d and empty", code, stderr.String(), cli.ExitInvalid)
	}
	lines := strings.Split(strings.TrimSpace(stdout.String()), "\n")
	if len(lines) < 2 || lines[0] != "base/.rulesync/rules/second.md: root-rule-duplicate: second root rule (first: base/.rulesync/rules/overview.md)" {
		t.Errorf("stdout = %q, want one compact line per diagnostic", stdout.String())
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
