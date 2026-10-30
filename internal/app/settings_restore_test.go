package app_test

import (
	"errors"
	"reflect"
	"testing"

	"github.com/lartweib/dotai/internal/app"
)

// TestInstallWarnsToCloseAHarnessWhoseSettingsItWrites checks the warning of
// an install that rewrites or removes a settings file the harness may be
// writing while it runs; creating a missing one needs none.
func TestInstallWarnsToCloseAHarnessWhoseSettingsItWrites(t *testing.T) {
	home, _ := installHome(t)
	d := newClockFS(t)
	first, err := installVariant(t, d, examplePack, "standalone", home)
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Warnings) != 0 {
		t.Errorf("first install warnings = %q, want none", first.Warnings)
	}
	res, err := installVariant(t, d, examplePack, "tmux", home)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"close claudecode before installing: a harness that is running may rewrite its settings file while dotai writes its keys there"}
	if !reflect.DeepEqual(res.Warnings, want) {
		t.Errorf("warnings = %q, want %q", res.Warnings, want)
	}
}

// TestRestorePutsBackAKeyThePackDropped checks the undo of an install that
// deleted a key the pack no longer stages: the key comes back from the
// backup copy, unless it was written again since with another value, which
// is a conflict on that key.
func TestRestorePutsBackAKeyThePackDropped(t *testing.T) {
	for _, readded := range []bool{false, true} {
		home, _ := installHome(t)
		d := newClockFS(t)
		if _, err := installVariant(t, d, examplePack, "tmux", home); err != nil {
			t.Fatal(err)
		}
		if _, err := installVariant(t, d, examplePack, "standalone", home); err != nil {
			t.Fatal(err)
		}
		target := at(home, claudeSettings)
		if got := settingsOf(t, target); !reflect.DeepEqual(got, map[string]any{"model": "sonnet"}) {
			t.Fatalf("settings after the standalone install = %v, want the env dropped", got)
		}
		if readded {
			harnessWrites(t, target, map[string]any{"env": map[string]any{"DOTAI_TERMINAL": "zellij"}})
			_, err := restore(d, home, "", nil)
			var ce *app.ConflictError
			if !errors.As(err, &ce) || len(ce.Conflicts) != 1 || ce.Conflicts[0].Key != "env.DOTAI_TERMINAL" || ce.Conflicts[0].Expected != "" {
				t.Fatalf("Restore() error = %v, want a conflict on the key env.DOTAI_TERMINAL written again", err)
			}
			if got := settingsOf(t, target)["env"]; !reflect.DeepEqual(got, map[string]any{"DOTAI_TERMINAL": "zellij"}) {
				t.Errorf("env = %v, want it untouched", got)
			}
		}
		if _, err := restore(d, home, "", func(app.RestoreResult) (bool, error) { return true, nil }); err != nil {
			t.Fatalf("Restore() error = %v", err)
		}
		want := map[string]any{"model": "opus", "env": map[string]any{"DOTAI_TERMINAL": "tmux"}}
		if got := settingsOf(t, target); !reflect.DeepEqual(got, want) {
			t.Errorf("settings (written again %v) = %v, want %v", readded, got, want)
		}
	}
}
