package harness_test

import (
	"path"
	"reflect"
	"strings"
	"testing"

	"github.com/lartweib/dotai/internal/harness"
	"github.com/lartweib/dotai/internal/pack"
)

func TestLookup(t *testing.T) {
	tests := []struct {
		ids     []string
		want    []string // ids of the adapters returned
		wantErr string
	}{
		{ids: pack.Harnesses, want: pack.Harnesses},
		{ids: []string{"opencode", "claudecode"}, want: []string{"opencode", "claudecode"}},
		{ids: nil, want: nil},
		{ids: []string{"claudecode", "codex", "cursor"}, wantErr: `no adapter for harness "codex", "cursor"`},
	}
	for _, tt := range tests {
		t.Run(strings.Join(tt.ids, ","), func(t *testing.T) {
			got, err := harness.Lookup(tt.ids)
			if tt.wantErr != "" {
				if err == nil || err.Error() != tt.wantErr {
					t.Fatalf("Lookup() error = %v, want %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("Lookup() error = %v", err)
			}
			var ids []string
			for _, a := range got {
				ids = append(ids, a.ID)
			}
			if !reflect.DeepEqual(ids, tt.want) {
				t.Errorf("Lookup() = %v, want %v", ids, tt.want)
			}
		})
	}
}

// TestAdapters checks the data of every adapter against decisions 0018 and
// 0031.
func TestAdapters(t *testing.T) {
	want := map[string]struct {
		dir, staging, install string
		level                 harness.Level
		pointers              []harness.Pointer
	}{
		"claudecode":      {"claude", ".claude", "", harness.Partial, []harness.Pointer{{Mechanism: harness.Env, Name: "CLAUDE_CONFIG_DIR"}}},
		"antigravity-cli": {"gemini", ".gemini", "", harness.Partial, []harness.Pointer{{Mechanism: harness.Flag, Name: "--gemini_dir"}}},
		"opencode": {"opencode", ".config/opencode", "config/opencode", harness.Partial, []harness.Pointer{
			{Mechanism: harness.Env, Name: "XDG_CONFIG_HOME", Path: "config"},
			{Mechanism: harness.Env, Name: "XDG_DATA_HOME", Path: "data"},
			{Mechanism: harness.Env, Name: "XDG_STATE_HOME", Path: "state"},
			{Mechanism: harness.Env, Name: "XDG_CACHE_HOME", Path: "cache"},
		}},
	}
	all := harness.All()
	if len(all) != len(want) {
		t.Fatalf("All() has %d adapters, want %d", len(all), len(want))
	}
	for _, a := range all {
		t.Run(a.ID, func(t *testing.T) {
			w, ok := want[a.ID]
			if !ok {
				t.Fatalf("unexpected adapter %q", a.ID)
			}
			got := struct {
				dir, staging, install string
				level                 harness.Level
				pointers              []harness.Pointer
			}{a.Dir, a.Staging, a.Install, a.Isolation.Level, a.Isolation.Pointers}
			if !reflect.DeepEqual(got, w) {
				t.Errorf("adapter = %+v, want %+v", got, w)
			}
			if a.Target != a.ID {
				t.Errorf("Target = %q, want the harness id %q", a.Target, a.ID)
			}
			for _, goos := range []string{"windows", "linux", "darwin"} {
				if len(a.Isolation.LeaksOn(goos)) == 0 {
					t.Errorf("a partial isolation names what leaks on %s", goos)
				}
			}
			for _, p := range []string{a.Dir, a.Staging} {
				if !path.IsAbs("/"+p) || path.Clean(p) != p {
					t.Errorf("%q is not a clean relative slash path", p)
				}
			}
		})
	}
}

// TestAllIsACopy checks that a caller cannot change the adapters.
func TestAllIsACopy(t *testing.T) {
	a := harness.All()[0]
	a.Isolation.Leaks[0].Text = "changed"
	a.Isolation.Relogin["windows"] = harness.ReloginNo
	if b := harness.All()[0]; b.Isolation.Leaks[0].Text == "changed" || b.Isolation.Relogin["windows"] == harness.ReloginNo {
		t.Error("All() shares its data with the caller")
	}
}

// TestMoves checks the staged directories that install elsewhere in a
// harness's isolated directory: agy moves antigravity-cli/skills to
// config/skills on its first run, and cannot link it back on Windows.
func TestMoves(t *testing.T) {
	want := map[string][]harness.Move{"antigravity-cli": {{From: "antigravity-cli/skills", To: "config/skills"}}}
	for _, a := range harness.All() {
		if got := a.Moves; !reflect.DeepEqual(got, want[a.ID]) {
			t.Errorf("%s: Moves = %+v, want %+v", a.ID, got, want[a.ID])
		}
	}
}

// TestCommands checks the executable dotai run launches for each harness:
// only Claude Code so far, agy and OpenCode come with roadmap PR 9.
func TestCommands(t *testing.T) {
	want := map[string]string{"claudecode": "claude", "antigravity-cli": "", "opencode": ""}
	for _, a := range harness.All() {
		if got := a.Command; got != want[a.ID] {
			t.Errorf("%s: Command = %q, want %q", a.ID, got, want[a.ID])
		}
	}
}

// TestSettings checks where each harness keeps its settings file, whose
// keys dotai owns rather than the whole file (decision 0035): in the
// staging tree and in its isolated install, relative to the home
// directory.
func TestSettings(t *testing.T) {
	want := map[string][2]string{
		"claudecode":      {".claude/settings.json", ".dotai/isolated/claude/settings.json"},
		"antigravity-cli": {".gemini/antigravity-cli/settings.json", ".dotai/isolated/gemini/antigravity-cli/settings.json"},
		"opencode":        {".config/opencode/opencode.jsonc", ".dotai/isolated/opencode/config/opencode/opencode.jsonc"},
	}
	for _, a := range harness.All() {
		if got := [2]string{a.SettingsSource(), a.SettingsTarget()}; got != want[a.ID] {
			t.Errorf("%s: settings = %q, want %q", a.ID, got, want[a.ID])
		}
	}
}

// TestIsolationPerOS checks the login and the leaks of each harness by OS:
// decision 0018 verified Claude Code's new login on Windows only, where
// its credentials live in the configuration directory.
func TestIsolationPerOS(t *testing.T) {
	claudeMD := "~/.claude/CLAUDE.md still loads when the working directory is under the home directory"
	credentials := "credentials live in the configuration directory, so you must log in again"
	tests := []struct {
		id, goos string
		relogin  harness.Relogin
		leaks    []string
	}{
		{"claudecode", "windows", harness.ReloginYes, []string{claudeMD, credentials}},
		{"claudecode", "linux", harness.ReloginUnknown, []string{claudeMD}},
		{"claudecode", "darwin", harness.ReloginUnknown, []string{claudeMD}},
		{"antigravity-cli", "windows", harness.ReloginNo, []string{"authentication is shared through the OS keyring"}},
		{"antigravity-cli", "linux", harness.ReloginNo, []string{"authentication is shared through the OS keyring"}},
		{"opencode", "darwin", harness.ReloginYes, []string{"~/.agents/skills still loads", "it needs its own service port while your own OpenCode service runs", "provider credentials do not carry over"}},
	}
	for _, tt := range tests {
		t.Run(tt.id+"/"+tt.goos, func(t *testing.T) {
			a, err := harness.Lookup([]string{tt.id})
			if err != nil {
				t.Fatal(err)
			}
			if got := a[0].Isolation.ReloginOn(tt.goos); got != tt.relogin {
				t.Errorf("ReloginOn(%s) = %q, want %q", tt.goos, got, tt.relogin)
			}
			if got := a[0].Isolation.LeaksOn(tt.goos); !reflect.DeepEqual(got, tt.leaks) {
				t.Errorf("LeaksOn(%s) = %q, want %q", tt.goos, got, tt.leaks)
			}
		})
	}
}

func TestIsolationWithoutDataIsUnknown(t *testing.T) {
	var i harness.Isolation
	if got := i.ReloginOn("linux"); got != harness.ReloginUnknown {
		t.Errorf("ReloginOn() = %q, want unknown", got)
	}
	if got := i.LeaksOn("linux"); got == nil || len(got) != 0 {
		t.Errorf("LeaksOn() = %#v, want an empty list", got)
	}
}
