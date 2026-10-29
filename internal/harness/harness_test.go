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
			if len(a.Isolation.Leaks) == 0 {
				t.Error("a partial isolation names what leaks")
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
	harness.All()[0].Isolation.Leaks[0] = "changed"
	if harness.All()[0].Isolation.Leaks[0] == "changed" {
		t.Error("All() shares its data with the caller")
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
