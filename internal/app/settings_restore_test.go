package app_test

import (
	"context"
	"errors"
	"os"
	"reflect"
	"slices"
	"strings"
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

// orderFS is the real file system port that logs each write, so that a
// test can tell what came before the first one.
type orderFS struct {
	*clockFS
	log *[]string
}

func (f orderFS) WriteIn(root, name string, data []byte, mode os.FileMode) error {
	*f.log = append(*f.log, "write "+name)
	return f.clockFS.WriteIn(root, name, data, mode)
}

func (f orderFS) WriteSynced(dir, name string, data []byte, perm os.FileMode) error {
	*f.log = append(*f.log, "backup "+name)
	return f.clockFS.WriteSynced(dir, name, data, perm)
}

// TestCloseWarningsComeBeforeTheFirstWrite checks that install and restore
// give the warning to close a harness as soon as the plan is known, before
// they back up or write anything, and keep it in their result.
func TestCloseWarningsComeBeforeTheFirstWrite(t *testing.T) {
	home, _ := installHome(t)
	c := newClockFS(t)
	if _, err := installVariant(t, c, examplePack, "standalone", home); err != nil {
		t.Fatal(err)
	}
	var log []string
	warn := func(text string) { log = append(log, "warn "+text) }
	f := quiet(t)
	res, err := app.InstallWarnWith(context.Background(), orderFS{c, &log}, f.fetch, f.run, examplePack, "tmux", home, warn)
	if err != nil {
		t.Fatal(err)
	}
	if len(log) < 2 || !strings.HasPrefix(log[0], "warn close claudecode before installing") || strings.Count(strings.Join(log, "\n"), "warn ") != 1 ||
		!slices.Contains(res.Warnings, strings.TrimPrefix(log[0], "warn ")) {
		t.Errorf("install log = %q, warnings %q; want the warning once, first, and in the result", log, res.Warnings)
	}
	log = nil
	rres, err := app.RestoreWarnWith(context.Background(), orderFS{c, &log}, home, "", warn, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(log) < 2 || !strings.HasPrefix(log[0], "warn close claudecode") || !strings.Contains(log[0], "before restoring") ||
		!slices.Contains(rres.Warnings, strings.TrimPrefix(log[0], "warn ")) {
		t.Errorf("restore log = %q, warnings %q; want the warning first and in the result", log, rres.Warnings)
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
