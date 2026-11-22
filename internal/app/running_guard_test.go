package app

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/lartweib/dotai/internal/plan"
)

// TestLiveFilesFailClosed checks decision 0053: a write of a harness whose
// target cannot be related to the home directory counts as a write of a live
// file, and a write that names no harness is attributed from its target.
func TestLiveFilesFailClosed(t *testing.T) {
	home := filepath.Join(t.TempDir(), "home")
	other := filepath.Join(t.TempDir(), "elsewhere", "note.md")
	for _, tt := range []struct {
		name   string
		target string
		want   bool
	}{
		{"outside the home directory", other, true},
		{"a relative target, which has no path from the home directory", filepath.Join("claude", "x.md"), true},
		{"a file below the home directory", filepath.Join(home, ".claude", "agents", "worker.md"), false},
		{"a live file", filepath.Join(home, ".claude", "settings.json"), true},
	} {
		got := liveFiles(host{goos: "linux"}, home, []string{"claudecode"}, nil, []writeTo{{"claudecode", tt.target}})
		if (len(got) == 1) != tt.want {
			t.Errorf("%s: liveFiles = %+v, want live = %v", tt.name, got, tt.want)
		}
	}

	ops := []plan.Op{
		{Action: plan.Replace, Target: filepath.Join(home, ".claude", "settings.json")},
		{Action: plan.Unchanged, Target: filepath.Join(home, ".claude.json")},
		{Action: plan.Replace, Target: filepath.Join(home, ".dotai", "tools", "hello.sh")},
		{Action: plan.Replace, Harness: "opencode", Target: filepath.Join(home, ".config", "opencode", "agents", "a.md")},
	}
	want := []writeTo{{"claudecode", ops[0].Target}, {"opencode", ops[3].Target}}
	got := installWrites(home, ops)
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Errorf("installWrites = %+v, want %+v: a write with no harness is attributed from its target, a tool write has none, and an unchanged op does not write", got, want)
	}
}

// TestLiveFilesCompareWithoutCaseWhereTheFileSystemFolds checks that a
// target that differs from a live file only in case counts as one on the
// OSes that fold case, Windows and macOS, and not on Linux.
func TestLiveFilesCompareWithoutCaseWhereTheFileSystemFolds(t *testing.T) {
	home := filepath.Join(t.TempDir(), "Home")
	target := filepath.Join(home, ".Claude", "Settings.JSON")
	for goos, want := range map[string]bool{"windows": true, "darwin": true, "linux": false} {
		got := liveFiles(host{goos: goos}, home, []string{"claudecode"}, nil, []writeTo{{"claudecode", target}})
		if (len(got) == 1) != want {
			t.Errorf("%s: liveFiles(%s) = %+v, want live = %v", goos, target, got, want)
		}
	}
}

// TestAMixedPlanRefusesTheWipeAndNamesTheFile checks the refusal of an
// install that wipes one running harness and only writes a live file of
// another: both are refused, the message says the wipe and the file, and
// the files are those of the second only.
func TestAMixedPlanRefusesTheWipeAndNamesTheFile(t *testing.T) {
	home := filepath.Join(t.TempDir(), "home")
	config := filepath.Join(home, ".config", "opencode", "opencode.jsonc")
	c := runCheck{found: []RunningHarness{{Harness: "claudecode", PID: 5, Name: "claude"}, {Harness: "opencode", PID: 7, Name: "opencode"}}}
	refused, _, _ := guard(host{goos: "linux"}, home, "install", []string{"claudecode", "opencode"}, []string{"claudecode"},
		[]writeTo{{"opencode", config}, {"claudecode", filepath.Join(home, ".claude", "settings.json")}}, c)
	if refused == nil || len(refused.Harnesses) != 2 || len(refused.Files) != 1 || refused.Files[0] != (RunningFile{Harness: "opencode", Path: config}) {
		t.Fatalf("guard = %+v, want both harnesses refused and only OpenCode's file named", refused)
	}
	for _, want := range []string{"claudecode (claude 5), opencode (opencode 7) are running", "would rewrite their configuration under them", "and write " + config + ", a file its harness rewrites while it runs"} {
		if !strings.Contains(refused.Error(), want) {
			t.Errorf("Error() = %q, want it to say %q", refused.Error(), want)
		}
	}
}

// TestRunningErrorCountsHarnessesNotProcesses checks that a refusal for
// one harness with two processes reads in the singular, and one for two
// harnesses in the plural.
func TestRunningErrorCountsHarnessesNotProcesses(t *testing.T) {
	file := RunningFile{Harness: "claudecode", Path: "/h/.claude/settings.json"}
	one := &RunningError{Operation: "install", Harnesses: []string{"claudecode"}, Files: []RunningFile{file},
		Running: []RunningHarness{{Harness: "claudecode", PID: 1, Name: "claude"}, {Harness: "claudecode", PID: 2, Name: "claude"}}}
	if got := one.Error(); !strings.Contains(got, "a file it rewrites while it runs") {
		t.Errorf("one harness, two processes: %q, want the singular", got)
	}
	two := &RunningError{Operation: "install", Harnesses: []string{"claudecode", "opencode"},
		Files:   []RunningFile{file, {Harness: "opencode", Path: "/h/.config/opencode/opencode.jsonc"}},
		Running: []RunningHarness{{Harness: "claudecode", PID: 1, Name: "claude"}, {Harness: "opencode", PID: 2, Name: "opencode"}}}
	if got := two.Error(); !strings.Contains(got, "files they rewrite while they run") {
		t.Errorf("two harnesses: %q, want the plural", got)
	}
}
