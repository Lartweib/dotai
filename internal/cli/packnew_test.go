package cli_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lartweib/dotai/internal/app"
	"github.com/lartweib/dotai/internal/cli"
	"github.com/lartweib/dotai/internal/envelope"
)

// newEnv runs the real use cases of dotai pack new.
func newEnv(stdout, stderr *bytes.Buffer) cli.Env {
	env := testEnv(stdout, stderr)
	env.PlanPackNew, env.NewPack, env.CopyPack = app.PlanPackNew, app.NewPack, app.CopyPack
	return env
}

// placeholder replaces dir in out, as written in text and in JSON, with
// /work/<base>, so that a golden file does not hold a temporary path.
func placeholder(out []byte, dir string) []byte {
	quoted, _ := json.Marshal(dir)
	want := "/work/" + filepath.Base(dir)
	out = bytes.ReplaceAll(out, quoted[1:len(quoted)-1], []byte(want))
	return bytes.ReplaceAll(out, []byte(dir), []byte(want))
}

func TestRunPackNewDryRunShowsTheFilesAndTheSkill(t *testing.T) {
	packHome(t)
	dir := filepath.Join(t.TempDir(), "my-pack")
	answers := filepath.Join("testdata", "pack-new-answers.yaml")
	for _, tt := range []struct {
		args   []string
		golden string
	}{
		{[]string{"pack", "new", dir, "--answers", answers, "--dry-run", "--json"}, "pack-new-dry-run.json.golden"},
		{[]string{"pack", "new", dir, "--answers", answers, "--dry-run"}, "pack-new-dry-run.txt.golden"},
	} {
		var stdout, stderr bytes.Buffer
		if code := cli.Run(tt.args, newEnv(&stdout, &stderr)); code != cli.ExitOK {
			t.Fatalf("%v: exit = %d; stderr %s", tt.args, code, &stderr)
		}
		assertGolden(t, filepath.Join("testdata", tt.golden), placeholder(stdout.Bytes(), dir))
	}
	if _, err := os.Stat(dir); err == nil {
		t.Error("the dry run wrote the pack")
	}
}

func TestRunPackNewFromFlagsWritesAndAddsThePack(t *testing.T) {
	home := packHome(t)
	dir := filepath.Join(t.TempDir(), "my-pack")
	var stdout, stderr bytes.Buffer
	args := []string{"pack", "new", dir, "--name", "my-pack", "--harness", "claudecode,opencode",
		"--profile", "web:Web apps, with a JavaScript toolchain.", "--hint", "web:package.json", "--mod", "web:panel", "--json"}
	if code := cli.Run(args, newEnv(&stdout, &stderr)); code != cli.ExitOK {
		t.Fatalf("exit = %d; stdout %s; stderr %s", code, &stdout, &stderr)
	}
	e := envelopeOf(t, &stdout)
	data := e["data"].(map[string]any)
	added := data["added"].(map[string]any)
	if e["command"] != "pack new" || added["outcome"] != "added" || added["pack"].(map[string]any)["id"] != "local/my-pack" {
		t.Errorf("envelope = %v, want pack new with the pack added", e)
	}
	if !strings.Contains(data["skill"].(string), "Web apps, with a JavaScript toolchain.") {
		t.Errorf("skill = %v, want the profile's description", data["skill"])
	}
	if res, err := app.Validate(dir, app.Selection{}); err != nil || !res.Valid {
		t.Errorf("the pack is not valid: %v %+v", err, res.Diagnostics)
	}
	if _, err := os.Stat(filepath.Join(home, ".dotai", "packs.json")); err != nil {
		t.Errorf("the registry was not written: %v", err)
	}
}

// TestRunPackNewHarnessFlags checks the harnesses of pack new's flags
// (decision 0047): without --harness, the pack is compatible with every
// supported harness, and --require lists the ones it requires.
func TestRunPackNewHarnessFlags(t *testing.T) {
	packHome(t)
	dir := filepath.Join(t.TempDir(), "my-pack")
	var stdout, stderr bytes.Buffer
	args := []string{"pack", "new", dir, "--name", "my-pack", "--require", "claudecode", "--dry-run", "--json"}
	if code := cli.Run(args, newEnv(&stdout, &stderr)); code != cli.ExitOK {
		t.Fatalf("exit = %d; stdout %s; stderr %s", code, &stdout, &stderr)
	}
	data := envelopeOf(t, &stdout)["data"].(map[string]any)
	if got := fmt.Sprint(data["harnesses"], data["requires"]); got != "[claudecode antigravity-cli] [claudecode]" {
		t.Errorf("harnesses and requires = %s, want every supported harness and claudecode required", got)
	}
}

func TestRunPackNewRefusals(t *testing.T) {
	packHome(t)
	full := t.TempDir()
	if err := os.WriteFile(filepath.Join(full, "notes.txt"), []byte("mine\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	answers := filepath.Join("testdata", "pack-new-answers.yaml")
	bad := filepath.Join(t.TempDir(), "bad.yaml")
	if err := os.WriteFile(bad, []byte("name: x\nharness: [opencode]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	fresh := filepath.Join(t.TempDir(), "p")
	for _, tt := range []struct {
		name string
		args []string
		code int
		want string
	}{
		{"answers and flags", []string{"pack", "new", fresh, "--answers", answers, "--name", "x"}, cli.ExitUsage, envelope.CodeUsage},
		{"no directory", []string{"pack", "new", "--answers", answers}, cli.ExitUsage, envelope.CodeUsage},
		{"a profile without a description", []string{"pack", "new", fresh, "--name", "x", "--harness", "opencode", "--profile", "web"}, cli.ExitUsage, envelope.CodeUsage},
		{"a hint of no profile", []string{"pack", "new", fresh, "--name", "x", "--harness", "opencode", "--hint", "web:a"}, cli.ExitUsage, envelope.CodeUsage},
		{"an unknown answer", []string{"pack", "new", fresh, "--answers", bad}, cli.ExitUsage, envelope.CodeUsage},
		{"a directory that is not empty", []string{"pack", "new", full, "--answers", answers}, cli.ExitInternal, envelope.CodeNewRefused},
		{"a pack with problems", []string{"pack", "new", fresh, "--name", "x", "--harness", "claudecode", "--require", "opencode"}, cli.ExitInvalid, envelope.CodeInvalid},
		{"answers and --require", []string{"pack", "new", fresh, "--answers", answers, "--require", "opencode"}, cli.ExitUsage, envelope.CodeUsage},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			code := cli.Run(append(tt.args, "--json"), newEnv(&stdout, &stderr))
			e := envelopeOf(t, &stdout)
			if code != tt.code || e["error"].(map[string]any)["code"] != tt.want {
				t.Errorf("exit = %d, envelope %v; want %d and %s", code, e, tt.code, tt.want)
			}
		})
	}
	if _, err := os.Stat(fresh); err == nil {
		t.Error("a refused pack new wrote its directory")
	}
}
