package app_test

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/lartweib/dotai/internal/app"
	"github.com/lartweib/dotai/internal/state"
)

// hookFiles installs, in home, the hooks files of each harness the way
// rulesync renders the session-start hook of decision 0045 that runs bin,
// recorded in the install state, and gives them by harness.
func hookFiles(t *testing.T, home, bin string) map[string]string {
	t.Helper()
	quoted := `"` + filepath.ToSlash(bin) + `" onboarding hook --harness `
	command := func(c string) string {
		data, err := json.Marshal(c)
		if err != nil {
			t.Fatal(err)
		}
		return string(data)
	}
	files := map[string]struct{ target, content string }{
		"claudecode": {at(home, ".claude/settings.json"),
			`{"hooks":{"SessionStart":[{"matcher":"startup","hooks":[{"type":"command","command":` + command(quoted+"claudecode") + `}]}]}}`},
		"antigravity-cli": {at(home, ".gemini/config/hooks.json"),
			`{"rulesync":{"SessionStart":[{"command":` + command(bin+" onboarding hook --harness antigravity-cli") + `}]}}`},
		"opencode": {at(home, ".config/opencode/plugins/rulesync-hooks.js"),
			"export const RulesyncHooks = async ({ $ }) => ({\n  event: async ({ event }) => {\n    if (event.type === \"session.created\") {\n      await $`" + quoted + "opencode`;\n    }\n  },\n});\n"},
	}
	s := state.State{Schema: state.Schema, Pack: state.Pack{Name: "minimal-pack", Version: "0.1.0", Path: absPath(t, examplePack)},
		Mode: app.ModeIsolated, Variant: "standalone", Harnesses: []string{"antigravity-cli", "claudecode", "opencode"}}
	out := map[string]string{}
	for _, id := range s.Harnesses {
		f := files[id]
		writeFile(t, f.target, f.content)
		s.Files = append(s.Files, state.File{Target: f.target, Harness: id, SHA256: sum(f.content)})
		out[id] = f.target
	}
	writeState(t, at(home, ".dotai/state.json"), s)
	return out
}

func doctorExe(t *testing.T, home string, exe func() (string, error)) app.DoctorResult {
	t.Helper()
	res, err := app.DoctorExe(context.Background(), newClockFS(t), &fakeGit{}, exe, home, app.DoctorOptions{})
	if err != nil {
		t.Fatalf("Doctor() error = %v", err)
	}
	return res
}

// TestDoctorFindsAStaleSessionStartHook checks that doctor reports each
// harness whose session-start hook runs a dotai binary that is gone, as
// after an upgrade moved it, or that is not the dotai that runs now.
func TestDoctorFindsAStaleSessionStartHook(t *testing.T) {
	home := t.TempDir()
	bin := filepath.Join(t.TempDir(), "bin", "dotai")
	writeFile(t, bin, "binary")
	running := func() (string, error) { return bin, nil }
	t.Run("the dotai that runs", func(t *testing.T) {
		hookFiles(t, home, bin)
		if res := doctorExe(t, home, running); len(res.Findings) != 0 {
			t.Errorf("Doctor() = %+v, want no finding", res.Findings)
		}
	})
	t.Run("a binary that is gone", func(t *testing.T) {
		files := hookFiles(t, home, filepath.Join(t.TempDir(), "old", "dotai"))
		res := doctorExe(t, home, running)
		want := []string{"onboarding-hook-stale " + files["antigravity-cli"], "onboarding-hook-stale " + files["claudecode"], "onboarding-hook-stale " + files["opencode"]}
		if got := codes(res); !reflect.DeepEqual(got, want) {
			t.Fatalf("Doctor() = %q, want %q", got, want)
		}
		for _, f := range res.Findings {
			if f.Fixable || f.Harness == "" || !strings.Contains(f.Message, "dotai install") || !strings.Contains(f.Message, "gone") {
				t.Errorf("finding = %+v, want one per harness, not fixable, that says to run dotai install", f)
			}
		}
	})
	t.Run("another dotai", func(t *testing.T) {
		other := filepath.Join(t.TempDir(), "dotai")
		writeFile(t, other, "binary")
		hookFiles(t, home, other)
		res := doctorExe(t, home, running)
		if len(res.Findings) != 3 || !strings.Contains(res.Findings[0].Message, bin) {
			t.Errorf("Doctor() = %+v, want each hook told it runs another dotai than %s", res.Findings, bin)
		}
	})
	t.Run("the running binary unknown", func(t *testing.T) {
		other := filepath.Join(t.TempDir(), "dotai")
		writeFile(t, other, "binary")
		hookFiles(t, home, other)
		unknown := func() (string, error) { return "", errors.New("unknown") }
		if res := doctorExe(t, home, unknown); len(res.Findings) != 0 {
			t.Errorf("Doctor() = %+v, want an existing binary kept when the one that runs is unknown", res.Findings)
		}
	})
}
