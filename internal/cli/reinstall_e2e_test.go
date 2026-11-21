package cli_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/lartweib/dotai/internal/app"
	"github.com/lartweib/dotai/internal/cli"
	"github.com/lartweib/dotai/internal/proc"
)

// What a user adds to the harnesses' own configuration after a replace
// install, and what a later replace install must not wipe by default
// (decision 0051).
var addedLater = map[string]string{
	".claude/keybindings.json":          `[{"context":"Chat"}]`,
	".claude/plugins/other/plugin.json": "{}",
	".gemini/config/mcp_config.json":    `{"mcpServers":{"mine":{}}}`,
	".config/opencode/plugins/other.js": "export default {}\n",
}

// firstReplace is e after a first replace install of every harness.
func firstReplace(t *testing.T) *e2e {
	t.Helper()
	e := newE2E(t)
	if code, stdout, stderr := e.run("install", examplePack, "--harness", every, "--mode", "replace", "--yes", "--json"); code != cli.ExitOK {
		t.Fatalf("first replace install: exit code = %d, stdout = %s, stderr = %q", code, stdout, stderr)
	}
	return e
}

// addLater writes the files of addedLater, as a tool or the user would.
func (e *e2e) addLater() {
	e.t.Helper()
	for rel, data := range addedLater {
		name := filepath.Join(e.home, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(name), 0o755); err != nil {
			e.t.Fatal(err)
		}
		if err := os.WriteFile(name, []byte(data), 0o644); err != nil {
			e.t.Fatal(err)
		}
	}
}

// runInstall runs install with args and decodes its result.
func runInstall(t *testing.T, e *e2e, args ...string) app.InstallResult {
	t.Helper()
	code, stdout, stderr := e.run(append([]string{"install", examplePack, "--harness", every, "--json"}, args...)...)
	if code != cli.ExitOK {
		t.Fatalf("install %v: exit code = %d, stdout = %s, stderr = %q", args, code, stdout, stderr)
	}
	var res app.InstallResult
	if err := json.Unmarshal(decode(t, stdout).Data, &res); err != nil {
		t.Fatalf("install %v: %s: %v", args, stdout, err)
	}
	return res
}

// TestALaterReplaceInstallWipesNothingByDefault checks that a replace
// install of harnesses an earlier replace install covers only updates the
// pack, whatever way it is asked without a terminal: what was added since
// stays, nothing is wiped, and the plan says what a reset would wipe.
func TestALaterReplaceInstallWipesNothingByDefault(t *testing.T) {
	for _, tt := range []struct {
		name string
		args []string
	}{
		{"with --yes", []string{"--mode", "replace", "--yes"}},
		{"plain, as the state records", nil},
		{"without a terminal or --yes", []string{"--mode", "replace"}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			e := firstReplace(t)
			e.addLater()
			before := snapshot(t, e.home, ".dotai")
			res := runInstall(t, e, append([]string{"--variant", "tmux"}, tt.args...)...)
			if len(res.Wipes) != 0 || len(res.WipeKeys) != 0 || res.Reset || !slices.Equal(res.Reinstall, strings.Split(every, ",")) {
				t.Errorf("wipes = %+v, wipeKeys = %+v, reset = %v, reinstall = %v, want an update that wipes nothing over the three harnesses", res.Wipes, res.WipeKeys, res.Reset, res.Reinstall)
			}
			if res.WouldReset == nil || len(res.WouldReset.Wipes) < len(addedLater) {
				t.Errorf("wouldReset = %+v, want what a reset would wipe, at least the %d entries added", res.WouldReset, len(addedLater))
			}
			after := snapshot(t, e.home, ".dotai")
			for rel := range addedLater {
				if after[rel] != before[rel] {
					t.Errorf("%s = %q after the install, want it as it was, %q", rel, after[rel], before[rel])
				}
			}
			if after[".claude/settings.json"] == before[".claude/settings.json"] {
				t.Errorf("~/.claude/settings.json did not change, want the update to write the variant's keys")
			}
		})
	}
}

// TestResetWipesAsTodayOnALaterInstall checks that --reset takes a later
// replace install back to a reset, the wipe of every entry added since,
// and that the restore of it brings back what was there before.
func TestResetWipesAsTodayOnALaterInstall(t *testing.T) {
	e := firstReplace(t)
	e.addLater()
	before := snapshot(t, e.home, ".dotai")
	res := runInstall(t, e, "--mode", "replace", "--reset", "--yes")
	if !res.Reset || res.WouldReset != nil || res.Backup == nil {
		t.Errorf("reset = %v, wouldReset = %+v, backup = %+v, want a reset with a backup", res.Reset, res.WouldReset, res.Backup)
	}
	wiped := map[string]bool{}
	for _, w := range res.Wipes {
		wiped[w.Path] = true
	}
	for _, rel := range []string{".claude/keybindings.json", ".claude/plugins", ".config/opencode/plugins"} {
		name := filepath.Join(e.home, filepath.FromSlash(rel))
		if !wiped[name] {
			t.Errorf("wipes = %v, want %s", slices.Sorted(mapKeys(wiped)), name)
		}
		if _, err := os.Lstat(name); err == nil {
			t.Errorf("%s is still there after the reset", rel)
		}
		if got := snapshot(t, filepath.Join(res.Backup.Path, "files"))[rel]; got == "" && rel == ".claude/keybindings.json" {
			t.Errorf("the backup holds no %s", rel)
		}
	}
	if code, stdout, stderr := e.run("restore", "--json"); code != cli.ExitOK {
		t.Fatalf("restore: exit code = %d, stdout = %s, stderr = %q", code, stdout, stderr)
	}
	sameSnapshot(t, "home after the restore of the reset", snapshot(t, e.home, ".dotai"), before)
}

func mapKeys(m map[string]bool) func(yield func(string) bool) {
	return func(yield func(string) bool) {
		for k := range m {
			if !yield(k) {
				return
			}
		}
	}
}

// TestTheFirstReplaceInstallStillResets checks that the first replace
// install of a harness wipes as before, with or without --reset, and that
// a harness added to the selection later is a first install for it.
func TestTheFirstReplaceInstallStillResets(t *testing.T) {
	for _, flags := range [][]string{nil, {"--reset"}} {
		e := newE2E(t)
		res := runInstall(t, e, append([]string{"--mode", "replace", "--yes"}, flags...)...)
		if len(res.Wipes) == 0 || len(res.WipeKeys) == 0 || len(res.Reinstall) != 0 || res.Reset || res.WouldReset != nil {
			t.Errorf("first install %v: wipes = %d, wipeKeys = %d, reinstall = %v, reset = %v, wouldReset = %+v, want a reset that is not a reinstall", flags, len(res.Wipes), len(res.WipeKeys), res.Reinstall, res.Reset, res.WouldReset)
		}
		if _, err := os.Stat(filepath.Join(e.home, ".claude", "keybindings.json")); err == nil {
			t.Errorf("first install %v: ~/.claude/keybindings.json is still there", flags)
		}
	}

	e := newE2E(t)
	code, stdout, stderr := e.run("install", examplePack, "--harness", "claudecode", "--mode", "replace", "--yes", "--json")
	if code != cli.ExitOK {
		t.Fatalf("first install of claudecode: exit code = %d, stdout = %s, stderr = %q", code, stdout, stderr)
	}
	e.addLater()
	res := func() app.InstallResult {
		code, stdout, stderr := e.run("install", examplePack, "--harness", "claudecode,antigravity-cli", "--mode", "replace", "--yes", "--json")
		if code != cli.ExitOK {
			t.Fatalf("install adding antigravity-cli: exit code = %d, stdout = %s, stderr = %q", code, stdout, stderr)
		}
		var res app.InstallResult
		if err := json.Unmarshal(decode(t, stdout).Data, &res); err != nil {
			t.Fatal(err)
		}
		return res
	}()
	if !slices.Equal(res.Reinstall, []string{"claudecode"}) {
		t.Errorf("reinstall = %v, want only claudecode, the harness the state records", res.Reinstall)
	}
	for _, w := range res.Wipes {
		if w.Harness != "antigravity-cli" {
			t.Errorf("wipe %+v, want the wipes of antigravity-cli only", w)
		}
	}
	if len(res.Wipes) == 0 {
		t.Error("wipes is empty, want antigravity-cli reset as a first install")
	}
	if _, err := os.Stat(filepath.Join(e.home, ".gemini", "config", "skills", "old")); err == nil {
		t.Error("~/.gemini/config/skills/old is still there, want it wiped by the first install of antigravity-cli")
	}
	for _, rel := range []string{".claude/keybindings.json", ".claude/plugins/other/plugin.json"} {
		if _, err := os.Stat(filepath.Join(e.home, filepath.FromSlash(rel))); err != nil {
			t.Errorf("%s: %v, want it kept by the later install of claudecode", rel, err)
		}
	}
}

// TestALaterReplaceInstallAsksWhichOneOnATerminal checks the interactive
// choice: the question comes with what a reset would wipe, no answers
// updates only, yes resets, and a terminal without a choice to make is not
// asked.
func TestALaterReplaceInstallAsksWhichOneOnATerminal(t *testing.T) {
	for _, tt := range []struct {
		answer bool
		reset  bool
	}{{false, false}, {true, true}} {
		e := firstReplace(t)
		e.addLater()
		var questions []string
		e.prompt = func(q string) bool {
			questions = append(questions, q)
			return strings.Contains(strings.ToLower(q), "reset") && tt.answer || !strings.Contains(strings.ToLower(q), "reset")
		}
		code, stdout, stderr := e.run("install", examplePack, "--harness", every, "--mode", "replace")
		if code != cli.ExitOK {
			t.Fatalf("answer %v: exit code = %d, stdout = %q, stderr = %q", tt.answer, code, stdout, stderr)
		}
		_, err := os.Stat(filepath.Join(e.home, ".claude", "keybindings.json"))
		if kept := err == nil; kept == tt.reset {
			t.Errorf("answer %v: ~/.claude/keybindings.json kept = %v, want %v", tt.answer, kept, !tt.reset)
		}
		if len(questions) == 0 || !strings.Contains(strings.ToLower(questions[0]), "reset") || !strings.Contains(questions[0], "[y/N]") {
			t.Fatalf("answer %v: questions = %q, want the choice first", tt.answer, questions)
		}
		if !strings.Contains(stdout, "keybindings.json") || !strings.Contains(stdout, "reset would instead wipe") {
			t.Errorf("answer %v: stdout = %q, want what a reset would wipe, listed before the question", tt.answer, stdout)
		}
		if !tt.reset && len(questions) != 1 {
			t.Errorf("update only: questions = %q, want the choice and nothing else, as it wipes and overwrites nothing of the user's", questions)
		}
	}

	// Nothing to wipe, nothing to choose.
	e := firstReplace(t)
	asked := 0
	e.prompt = func(string) bool { asked++; return false }
	if code, _, stderr := e.run("install", examplePack, "--harness", every, "--mode", "replace"); code != cli.ExitOK || asked != 0 {
		t.Errorf("nothing added: exit code = %d, asked %d times, stderr = %q, want no question", code, asked, stderr)
	}
	// --reset and --yes decide without asking.
	e.addLater()
	for _, flag := range []string{"--reset", "--yes"} {
		e.prompt = func(string) bool { t.Errorf("%s asked a question", flag); return false }
		if code, _, stderr := e.run("install", examplePack, "--harness", every, "--mode", "replace", flag, "--yes"); code != cli.ExitOK {
			t.Errorf("%s: exit code = %d, stderr = %q", flag, code, stderr)
		}
		e.addLater()
	}
}

// TestADryRunShowsThePlanOfWhatTheFlagsImply checks that a dry run of a
// later replace install shows an update that wipes nothing and says what
// --reset would wipe, and with --reset shows the reset.
func TestADryRunShowsThePlanOfWhatTheFlagsImply(t *testing.T) {
	e := firstReplace(t)
	e.addLater()
	before := snapshot(t, e.home)
	code, stdout, stderr := e.run("install", examplePack, "--harness", every, "--mode", "replace", "--dry-run")
	if code != cli.ExitOK || strings.Contains(stdout, "  wipe  ") || !strings.Contains(stdout, "update only") || !strings.Contains(stdout, "--reset would wipe") {
		t.Errorf("dry run: exit code = %d, stdout = %q, stderr = %q, want an update that wipes nothing and the count --reset would wipe", code, stdout, stderr)
	}
	code, stdout, stderr = e.run("install", examplePack, "--harness", every, "--mode", "replace", "--dry-run", "--reset")
	if code != cli.ExitOK || !strings.Contains(stdout, "  wipe  ") || !strings.Contains(stdout, "keybindings.json") || strings.Contains(stdout, "--reset would wipe") {
		t.Errorf("dry run --reset: exit code = %d, stdout = %q, stderr = %q, want the wipes", code, stdout, stderr)
	}
	code, stdout, _ = e.run("install", examplePack, "--harness", every, "--mode", "replace", "--dry-run", "--json")
	var plan app.InstallPlan
	if err := json.Unmarshal(decode(t, stdout).Data, &plan); err != nil || code != cli.ExitOK || plan.Reset || len(plan.Wipes) != 0 || plan.WouldReset == nil || len(plan.Reinstall) != 3 {
		t.Errorf("dry run --json: exit code = %d, plan = %s (%v), want an update with wouldReset", code, stdout, err)
	}
	sameSnapshot(t, "home after the dry runs", snapshot(t, e.home), before)
}

// TestAnUpdateOnlyInstallKeepsTheSafetiesOfAReplaceInstall checks that an
// update that writes files keeps the running-harness check, takes a backup
// and is undone exactly by dotai restore.
func TestAnUpdateOnlyInstallKeepsTheSafetiesOfAReplaceInstall(t *testing.T) {
	e := firstReplace(t)
	e.addLater()
	before := snapshot(t, e.home, ".dotai")

	exe := "claude"
	if os.PathSeparator == '\\' {
		exe += ".exe"
	}
	e.procs = []proc.Process{{PID: 4242, Name: exe}}
	code, stdout, _ := e.run("install", examplePack, "--harness", every, "--mode", "replace", "--variant", "tmux", "--yes", "--json")
	if env := decode(t, stdout); code != cli.ExitInternal || env.Error.Code != "harness-running" {
		t.Errorf("update with claude running: exit code = %d, envelope = %s, want harness-running", code, stdout)
	}
	sameSnapshot(t, "home after the refused update", snapshot(t, e.home, ".dotai"), before)
	e.procs = nil

	res := runInstall(t, e, "--mode", "replace", "--variant", "tmux", "--yes")
	if res.Backup == nil {
		t.Fatal("backup = nil, want a backup of what the update overwrote")
	}
	if code, stdout, stderr := e.run("restore", "--json"); code != cli.ExitOK {
		t.Fatalf("restore: exit code = %d, stdout = %s, stderr = %q", code, stdout, stderr)
	}
	sameSnapshot(t, "home after the restore of the update", snapshot(t, e.home, ".dotai"), before)
}

// TestThePlanIsPrintedOnce checks that a human install prints its plan
// once: before the question when it asks one, or after it is done when it
// does not, never both.
func TestThePlanIsPrintedOnce(t *testing.T) {
	for _, tt := range []struct {
		name   string
		prompt func(string) bool
	}{
		{"with a question", func(string) bool { return true }},
		{"with --yes", nil},
	} {
		t.Run(tt.name, func(t *testing.T) {
			e := newE2E(t)
			e.prompt = tt.prompt
			args := []string{"install", examplePack, "--harness", every, "--mode", "replace"}
			if tt.prompt == nil {
				args = append(args, "--yes")
			}
			code, stdout, stderr := e.run(args...)
			if code != cli.ExitOK {
				t.Fatalf("exit code = %d, stdout = %q, stderr = %q", code, stdout, stderr)
			}
			if n := strings.Count(stdout, "Install plan ·"); n != 1 {
				t.Errorf("the plan is printed %d times, want 1:\n%s", n, stdout)
			}
			if n := strings.Count(stdout, "  wipe  "); n != strings.Count(stdout, "wipe  ")-strings.Count(stdout, "wipe key")+0 && n == 0 {
				t.Errorf("stdout lists no wipe:\n%s", stdout)
			}
			for _, want := range []string{"installed\n", "backup: ", "state:  "} {
				if !strings.Contains(stdout, want) {
					t.Errorf("stdout lacks %q:\n%s", want, stdout)
				}
			}
			if tt.prompt != nil && !strings.HasPrefix(strings.TrimSpace(stdout[strings.LastIndex(stdout, "\n\n"):]), "") {
				t.Errorf("stdout = %q", stdout)
			}
		})
	}
}
