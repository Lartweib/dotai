package app_test

import (
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/lartweib/dotai/internal/app"
	"github.com/lartweib/dotai/internal/harness"
	"github.com/lartweib/dotai/internal/state"
)

// TestInstallRoot checks the directory each target is written through, by
// the layouts an install may write in: a harness's isolated directory or
// the tools directory, and in the replace layout the harness's own
// configuration directory, never anything else.
func TestInstallRoot(t *testing.T) {
	home := t.TempDir()
	isolated, replace := []harness.Mode{harness.Isolated}, []harness.Mode{harness.Replace}
	both := []harness.Mode{harness.Replace, harness.Isolated}
	tests := []struct {
		rel        string
		layouts    []harness.Mode
		root, name string // "" when it is refused
	}{
		{".dotai/isolated/claude/CLAUDE.md", nil, ".dotai/isolated/claude", "CLAUDE.md"},
		{".dotai/isolated/opencode/config/opencode/AGENTS.md", isolated, ".dotai/isolated/opencode", "config/opencode/AGENTS.md"},
		{".dotai/tools/hello.sh", replace, ".dotai/tools", "hello.sh"},
		{".claude/CLAUDE.md", nil, "", ""},
		{".claude/CLAUDE.md", isolated, "", ""},
		{".claude/CLAUDE.md", replace, ".claude", "CLAUDE.md"},
		{".gemini/config/GEMINI.md", replace, ".gemini", "config/GEMINI.md"},
		{".config/opencode/opencode.jsonc", replace, ".config/opencode", "opencode.jsonc"},
		{".dotai/isolated/claude/CLAUDE.md", replace, "", ""},
		{".dotai/isolated/claude/CLAUDE.md", both, ".dotai/isolated/claude", "CLAUDE.md"},
		{".claude.json", replace, "", ""},
		{".claude", replace, "", ""},
		{".config/other/x", replace, "", ""},
		{".ssh/config", both, "", ""},
	}
	for _, tt := range tests {
		root, name, err := app.InstallRoot(home, at(home, tt.rel), tt.layouts...)
		if tt.root == "" {
			if err == nil {
				t.Errorf("InstallRoot(%s, %v) = %s, %s, want it refused", tt.rel, tt.layouts, root, name)
			}
			continue
		}
		if err != nil || root != filepath.Join(home, filepath.FromSlash(tt.root)) || name != tt.name {
			t.Errorf("InstallRoot(%s, %v) = %s, %s, %v, want %s, %s", tt.rel, tt.layouts, root, name, err, tt.root, tt.name)
		}
	}
}

// TestRestoreRefusesAnUnknownMode checks that restore does not undo an
// install of a mode it does not know, and writes nothing.
func TestRestoreRefusesAnUnknownMode(t *testing.T) {
	home, _ := installHome(t)
	if _, err := quiet(t).install(newClockFS(t), examplePack, home); err != nil {
		t.Fatal(err)
	}
	name := at(home, ".dotai/state.json")
	s := readJSON(t, name, state.Decode)
	s.Mode = "merge"
	writeState(t, name, s)
	before := snapshot(t, home)
	if _, err := restore(newClockFS(t), home, "", nil); err == nil || !strings.Contains(err.Error(), "does not know") {
		t.Fatalf("Restore() error = %v, want the unknown mode refused", err)
	}
	if got := snapshot(t, home); !reflect.DeepEqual(got, before) {
		t.Errorf("home = %v, want nothing written", got)
	}
}
