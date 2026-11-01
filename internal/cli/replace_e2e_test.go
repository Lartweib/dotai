package cli_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/lartweib/dotai/internal/app"
	"github.com/lartweib/dotai/internal/cli"
	"github.com/lartweib/dotai/internal/proc"
	"github.com/lartweib/dotai/internal/rulesync"
)

// seededConfig is a real-looking user-level configuration of all three
// harnesses: logins, history and caches they keep, the user's own rules,
// agents, skills, settings and MCP servers they lose to a replace install,
// the files of Gemini CLI and of other tools, and the generic paths under
// the home directory.
var seededConfig = map[string]string{
	".claude/.credentials.json":               `{"claudeAiOauth":{"accessToken":"fake"}}`,
	".claude/history.jsonl":                   `{"display":"hi"}` + "\n",
	".claude/projects/p/session.jsonl":        "{}\n",
	".claude/settings.json":                   `{"model":"opus","theme":"dark","permissions":{"allow":["Bash"]}}`,
	".claude/CLAUDE.md":                       "# my rules\n",
	".claude/agents/mine.md":                  "---\nname: mine\n---\nmine\n",
	".claude/skills/mine/SKILL.md":            "---\nname: mine\ndescription: Mine.\n---\n",
	".claude/commands/review.md":              "review\n",
	".claude/plugins/known.json":              "{}",
	".claude/keybindings.json":                "[]",
	".claude.json":                            `{"numStartups":3,"mcpServers":{"mine":{"command":"x"}},"projects":{"/work":{"allowedTools":["Bash"],"hasTrustDialogAccepted":true}}}`,
	"CLAUDE.md":                               "# home rules\n",
	".gemini/settings.json":                   `{"theme":"x"}`,
	".gemini/oauth_creds.json":                "{}",
	".gemini/GEMINI.md":                       "# gemini rules\n",
	".gemini/config/.migrated":                "",
	".gemini/config/mcp_config.json":          `{"mcpServers":{}}`,
	".gemini/config/skills/old/SKILL.md":      "old\n",
	".gemini/antigravity-cli/settings.json":   `{"x":1}`,
	".gemini/antigravity-cli/conversations/c": "x",
	".gemini/antigravity-cli/installation_id": "id",
	".config/opencode/opencode.json":          `{"$schema":"https://opencode.ai/config.json"}`,
	".config/opencode/AGENTS.md":              "# oc rules\n",
	".config/opencode/agents/a.md":            "a\n",
	".local/share/opencode/opencode.db":       "db",
	"AGENTS.md":                               "# home agents\n",
	".agents/skills/shared-one/SKILL.md":      "---\nname: shared-one\ndescription: Shared.\n---\n",
	"Documents/notes.txt":                     "mine\n",
}

// e2e is a temporary home seeded with seededConfig, the HOME and
// USERPROFILE of this test process, with fake claude, agy and opencode
// commands alone on PATH and no relocation variable set, and the command
// line wired to the real use cases, rulesync faked and the processes
// listed by procs.
type e2e struct {
	t     *testing.T
	home  string
	procs []proc.Process
	// prompt, when set, answers the question on a terminal.
	prompt func(question string) bool
}

func newE2E(t *testing.T) *e2e {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("DOTAI_CACHE_DIR", t.TempDir())
	for _, kv := range os.Environ() {
		name, _, _ := strings.Cut(kv, "=")
		upper := strings.ToUpper(name)
		if upper == "CLAUDE_CONFIG_DIR" || strings.HasPrefix(upper, "XDG_") || strings.HasPrefix(upper, "OPENCODE_") {
			t.Setenv(name, "")
			os.Unsetenv(name)
		}
	}
	bin := t.TempDir()
	for _, name := range []string{"claude", "agy", "opencode"} {
		file, data := filepath.Join(bin, name), "#!/bin/sh\nexit 0\n"
		if runtime.GOOS == "windows" {
			file, data = file+".exe", "" // found by name, never run
		}
		if err := os.WriteFile(file, []byte(data), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", bin)
	for rel, data := range seededConfig {
		name := filepath.Join(home, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(name), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(name, []byte(data), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return &e2e{t: t, home: home}
}

// run runs dotai with args and gives its exit code, stdout and stderr.
func (e *e2e) run(args ...string) (int, string, string) {
	e.t.Helper()
	ports := app.Ports{
		FetchRulesync: func(_ context.Context, _, version string, _ app.Progress) (rulesync.Result, error) {
			return rulesync.Result{Version: version}, nil
		},
		RunRulesync: fakeRulesyncRun,
		Processes:   func() ([]proc.Process, error) { return e.procs, nil },
	}
	var stdout, stderr bytes.Buffer
	env := testEnv(&stdout, &stderr)
	env.PlanInstall, env.Install, env.Restore, env.PlanRun = ports.PlanInstall, ports.Install, ports.Restore, app.PlanRun
	if e.prompt != nil {
		env.Prompt = func(_ context.Context, q string) (bool, error) { return e.prompt(q), nil }
	}
	code := cli.Run(args, env)
	return code, stdout.String(), stderr.String()
}

// fakeRulesyncRun writes each harness's rules file in the user scope, as
// rulesync does for the targets it is run for.
func fakeRulesyncRun(_ context.Context, _ rulesync.Result, targets []string, jobs ...rulesync.Job) error {
	files := map[string]string{"claudecode": ".claude/CLAUDE.md", "antigravity-cli": ".gemini/GEMINI.md", "opencode": ".config/opencode/AGENTS.md"}
	for _, j := range jobs {
		for _, id := range targets {
			if !j.Global {
				continue
			}
			name := filepath.Join(j.Dir, filepath.FromSlash(files[id]))
			if err := os.MkdirAll(filepath.Dir(name), 0o755); err != nil {
				return err
			}
			if err := os.WriteFile(name, []byte("# pack rules\n"), 0o644); err != nil {
				return err
			}
		}
	}
	return nil
}

// snapshot is every entry under dir by its slash path, a directory as
// "dir" and a file as the SHA-256 of its content, leaving out the paths in
// skip.
func snapshot(t *testing.T, dir string, skip ...string) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(dir, p)
		rel = filepath.ToSlash(rel)
		for _, s := range skip {
			if rel == s {
				return filepath.SkipDir
			}
		}
		if d.IsDir() {
			out[rel+"/"] = "dir"
			return nil
		}
		data, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		h := sha256.Sum256(data)
		out[rel] = hex.EncodeToString(h[:])
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// sameSnapshot reports each path where got differs from want.
func sameSnapshot(t *testing.T, what string, got, want map[string]string) {
	t.Helper()
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s: %s = %q, want %q", what, k, got[k], v)
		}
	}
	for k, v := range got {
		if _, ok := want[k]; !ok {
			t.Errorf("%s: %s = %q, want nothing there", what, k, v)
		}
	}
}

// failure is the envelope of a command that failed.
type failure struct {
	OK    bool            `json:"ok"`
	Data  json.RawMessage `json:"data"`
	Error struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

func decode(t *testing.T, stdout string) failure {
	t.Helper()
	var e failure
	if err := json.Unmarshal([]byte(stdout), &e); err != nil {
		t.Fatalf("stdout = %q: %v", stdout, err)
	}
	return e
}

// TestReplaceInstallAndRestore installs the example pack in the replace
// mode over the seeded configuration of all three harnesses: without
// --yes it shows the plan and is refused, writing nothing, in text and in
// JSON; with --yes it installs, the wiped entries end up in the backup,
// and dotai restore brings the whole seeded home back byte for byte.
func TestReplaceInstallAndRestore(t *testing.T) {
	e := newE2E(t)
	seeded := snapshot(t, e.home)

	code, stdout, stderr := e.run("install", examplePack, "--mode", "replace")
	if code != cli.ExitInternal || !strings.Contains(stderr, "--yes") || !strings.Contains(stdout, "wipe  ") || !strings.Contains(stdout, "refused, nothing was written") {
		t.Errorf("without --yes: exit code = %d, stdout = %q, stderr = %q, want the plan and a refusal that names --yes", code, stdout, stderr)
	}
	sameSnapshot(t, "home after the install without --yes", snapshot(t, e.home), seeded)

	code, stdout, _ = e.run("install", examplePack, "--mode", "replace", "--json")
	env := decode(t, stdout)
	var plan app.InstallPlan
	if err := json.Unmarshal(env.Data, &plan); err != nil || code != cli.ExitInternal || env.OK || env.Error.Code != "consent-required" ||
		plan.Mode != app.ModeReplace || len(plan.Wipes) == 0 {
		t.Errorf("--json without --yes: exit code = %d, envelope = %s, want consent-required with the plan", code, stdout)
	}
	sameSnapshot(t, "home after the install --json without --yes", snapshot(t, e.home), seeded)

	code, stdout, stderr = e.run("install", examplePack, "--mode", "replace", "--yes", "--json")
	if code != cli.ExitOK {
		t.Fatalf("with --yes: exit code = %d, stdout = %s, stderr = %q", code, stdout, stderr)
	}
	var res app.InstallResult
	if err := json.Unmarshal(decode(t, stdout).Data, &res); err != nil || res.Backup == nil {
		t.Fatalf("with --yes: data = %s (%v), want a backup", stdout, err)
	}
	backup := snapshot(t, filepath.Join(res.Backup.Path, "files"))
	for _, w := range res.Wipes {
		rel, _ := filepath.Rel(e.home, w.Path)
		rel = filepath.ToSlash(rel)
		found := false
		for k, v := range seeded {
			if k == rel || strings.HasPrefix(k, rel+"/") {
				found = true
				if backup[k] != v {
					t.Errorf("the backup of %s = %q, want the seeded %q", k, backup[k], v)
				}
			}
		}
		if !found {
			t.Errorf("wipe %s is not one of the seeded entries", rel)
		}
	}
	for _, rel := range []string{".claude/CLAUDE.md", ".gemini/config/GEMINI.md", ".config/opencode/AGENTS.md"} {
		if got, _ := os.ReadFile(filepath.Join(e.home, filepath.FromSlash(rel))); string(got) != "# pack rules\n" {
			t.Errorf("%s = %q, want the pack's rules", rel, got)
		}
	}
	for _, rel := range []string{".claude/.credentials.json", ".claude/history.jsonl", ".gemini/oauth_creds.json", ".gemini/settings.json", "Documents/notes.txt"} {
		if got := snapshot(t, e.home)[rel]; got != seeded[rel] {
			t.Errorf("%s = %q, want it kept as seeded", rel, got)
		}
	}

	code, stdout, stderr = e.run("install", examplePack, "--mode", "isolated")
	if code != cli.ExitUsage || !strings.Contains(stderr, "run dotai restore") {
		t.Errorf("--mode isolated over the replace install: exit code = %d, stderr = %q, want a usage error pointing to restore", code, stderr)
	}

	code, stdout, stderr = e.run("restore", "--json")
	if code != cli.ExitOK {
		t.Fatalf("restore: exit code = %d, stdout = %s, stderr = %q", code, stdout, stderr)
	}
	sameSnapshot(t, "home after the restore", snapshot(t, e.home, ".dotai"), seeded)
}

// TestReplaceInstallAsksOnATerminal checks that on a terminal the plan is
// shown and the user asked once: anything but yes cancels without
// writing, and yes installs.
func TestReplaceInstallAsksOnATerminal(t *testing.T) {
	e := newE2E(t)
	seeded := snapshot(t, e.home)
	asked := 0
	e.prompt = func(string) bool { asked++; return false }
	code, stdout, stderr := e.run("install", examplePack, "--mode", "replace")
	if code != cli.ExitInternal || asked != 1 || !strings.Contains(stdout, "nothing was written yet") || !strings.Contains(stderr, "the install was cancelled: nothing was written") {
		t.Errorf("answered no: exit code = %d, asked %d times, stdout = %q, stderr = %q, want the plan, one question and a cancel", code, asked, stdout, stderr)
	}
	sameSnapshot(t, "home after the answer no", snapshot(t, e.home), seeded)
	e.prompt = func(string) bool { asked++; return true }
	if code, _, stderr := e.run("install", examplePack, "--mode", "replace"); code != cli.ExitOK || asked != 2 {
		t.Errorf("answered yes: exit code = %d, asked %d times, stderr = %q, want it installed", code, asked, stderr)
	}
	// Each replace install wipes again, with consent: a file added since
	// in a wiped area is asked about.
	if err := os.WriteFile(filepath.Join(e.home, ".claude", "added-later.md"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	e.prompt = func(string) bool { asked++; return true }
	if code, _, stderr := e.run("install", examplePack); code != cli.ExitOK || asked != 3 {
		t.Errorf("again with a file added: exit code = %d, asked %d times, stderr = %q, want one more question", code, asked, stderr)
	}
	e.prompt = func(string) bool { asked++; return false }
	if code, _, stderr := e.run("install", examplePack); code != cli.ExitOK || asked != 3 {
		t.Errorf("again with nothing of the user's to wipe: exit code = %d, asked %d times, stderr = %q, want no question", code, asked, stderr)
	}
}

// TestReplaceRefusalBlocksEvenWithYes checks that a refusal of the plan
// stops the install before anything is written, --yes or not.
func TestReplaceRefusalBlocksEvenWithYes(t *testing.T) {
	for _, tt := range []struct {
		name  string
		setup func(e *e2e)
		code  string
	}{
		{"unclassified entry", func(e *e2e) {
			if err := os.WriteFile(filepath.Join(e.home, ".claude", "oauth-backup.json"), []byte("{}"), 0o600); err != nil {
				e.t.Fatal(err)
			}
		}, "unclassified-entry"},
		{"relocation variable", func(e *e2e) { e.t.Setenv("CLAUDE_CONFIG_DIR", filepath.Join(e.home, "elsewhere")) }, "relocation-variable"},
		{"split file", func(e *e2e) {
			if err := os.WriteFile(filepath.Join(e.home, ".claude.json"), []byte("not json"), 0o600); err != nil {
				e.t.Fatal(err)
			}
		}, "split-file-invalid"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			e := newE2E(t)
			tt.setup(e)
			seeded := snapshot(t, e.home)
			code, stdout, stderr := e.run("install", examplePack, "--mode", "replace", "--yes", "--json")
			env := decode(t, stdout)
			var plan app.InstallPlan
			if err := json.Unmarshal(env.Data, &plan); err != nil || code != cli.ExitInternal || env.Error.Code != "replace-refused" ||
				len(plan.Refusals) == 0 || plan.Refusals[0].Code != tt.code {
				t.Errorf("exit code = %d, envelope = %s, stderr = %q, want replace-refused with the %s refusal", code, stdout, stderr, tt.code)
			}
			code, stdout, stderr = e.run("install", examplePack, "--mode", "replace", "--yes")
			if code != cli.ExitInternal || !strings.Contains(stdout, "refused") || !strings.Contains(stderr, "nothing was written") {
				t.Errorf("text: exit code = %d, stdout = %q, stderr = %q, want the plan with its refusals and the error", code, stdout, stderr)
			}
			sameSnapshot(t, "home", snapshot(t, e.home), seeded)
		})
	}
}

// TestReplaceRefusesWhileAHarnessRuns checks the running-harness check of
// the install and of its restore: a process named as a harness's command
// refuses them, even with --yes, dotai's own process does not count, and
// --ignore-running skips the check.
func TestReplaceRefusesWhileAHarnessRuns(t *testing.T) {
	exe := func(name string) string {
		if runtime.GOOS == "windows" {
			return name + ".exe"
		}
		return name
	}
	e := newE2E(t)
	seeded := snapshot(t, e.home)
	e.procs = []proc.Process{{PID: os.Getpid(), Name: exe("claude")}, {PID: 4242, Name: exe("opencode-service-4242-ab")}, {PID: 7, Name: "init"}}
	code, stdout, _ := e.run("install", examplePack, "--mode", "replace", "--yes", "--json")
	env := decode(t, stdout)
	var data struct {
		Running []app.RunningHarness `json:"running"`
	}
	if err := json.Unmarshal(env.Data, &data); err != nil || code != cli.ExitInternal || env.Error.Code != "harness-running" ||
		len(data.Running) != 1 || data.Running[0].Harness != "opencode" || data.Running[0].PID != 4242 {
		t.Errorf("exit code = %d, envelope = %s, want harness-running naming OpenCode's service only", code, stdout)
	}
	sameSnapshot(t, "home after the refused install", snapshot(t, e.home), seeded)
	if code, _, stderr := e.run("install", examplePack, "--mode", "replace", "--yes", "--ignore-running"); code != cli.ExitOK {
		t.Fatalf("--ignore-running: exit code = %d, stderr = %q, want it installed", code, stderr)
	}
	code, _, stderr := e.run("restore")
	if code != cli.ExitInternal || !strings.Contains(stderr, "opencode (") || !strings.Contains(stderr, "--ignore-running") {
		t.Errorf("restore: exit code = %d, stderr = %q, want it refused while OpenCode runs", code, stderr)
	}
	if code, _, stderr := e.run("restore", "--ignore-running"); code != cli.ExitOK {
		t.Fatalf("restore --ignore-running: exit code = %d, stderr = %q", code, stderr)
	}
	sameSnapshot(t, "home after the restore", snapshot(t, e.home, ".dotai"), seeded)
}
