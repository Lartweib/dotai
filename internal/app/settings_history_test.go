package app_test

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/lartweib/dotai/internal/app"
	"github.com/lartweib/dotai/internal/backup"
	"github.com/lartweib/dotai/internal/state"
)

// editPack replaces old with new in the file rel of the pack in dir.
func editPack(t *testing.T, dir, rel, old, new string) {
	t.Helper()
	name := filepath.Join(dir, filepath.FromSlash(rel))
	data, err := os.ReadFile(name)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), old) {
		t.Fatalf("%s does not hold %q", rel, old)
	}
	writeFile(t, name, strings.Replace(string(data), old, new, 1))
}

// baseSettings is the settings of the example pack's base layer.
const baseSettings = "base/dotai/settings.yaml"

// withoutKeys is s as an install before decision 0035 recorded it: no
// settings file has keys, in the record or in the journal.
func withoutKeys(s state.State) state.State {
	s.Files = append([]state.File{}, s.Files...)
	for i := range s.Files {
		s.Files[i].Keys = nil
	}
	if s.Pending != nil {
		p := *s.Pending
		p.Ops = append([]state.PendingOp{}, p.Ops...)
		for i := range p.Ops {
			p.Ops[i].Keys = nil
		}
		s.Pending = &p
	}
	return s
}

func writeState(t *testing.T, name string, s state.State) {
	t.Helper()
	data, err := state.Encode(s)
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, name, string(data))
}

// stripStateKeys rewrites the install state of home as an older install
// wrote it.
func stripStateKeys(t *testing.T, home string) {
	t.Helper()
	name := at(home, ".dotai/state.json")
	writeState(t, name, withoutKeys(readJSON(t, name, state.Decode)))
}

// stripPriorKeys rewrites the state the backup id keeps as an older install
// wrote it, and its hash in the manifest.
func stripPriorKeys(t *testing.T, home, id string) {
	t.Helper()
	dir := at(home, ".dotai/backups/"+id)
	name := filepath.Join(dir, "state.json")
	writeState(t, name, withoutKeys(readJSON(t, name, state.Decode)))
	data, err := os.ReadFile(name)
	if err != nil {
		t.Fatal(err)
	}
	m := manifest(t, home, id)
	m.PriorState.SHA256 = sum(string(data))
	encoded, err := backup.Encode(m)
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(dir, "manifest.json"), string(encoded))
}

var consent = func(app.RestoreResult) (bool, error) { return true, nil }

// TestRestoreAnOlderWholeFileRecord checks a settings file that the state
// records whole, as an install before decision 0035 did: as written, the
// restore deletes it like any file it created; changed by the harness, it
// is a conflict on the whole file.
func TestRestoreAnOlderWholeFileRecord(t *testing.T) {
	for _, changed := range []bool{false, true} {
		home, _ := installHome(t)
		d := newClockFS(t)
		if _, err := installVariant(t, d, examplePack, "standalone", home); err != nil {
			t.Fatal(err)
		}
		stripStateKeys(t, home)
		target := at(home, claudeSettings)
		if changed {
			harnessWrites(t, target, map[string]any{"theme": "dark"})
			_, err := restore(d, home, "", nil)
			var ce *app.ConflictError
			if !errors.As(err, &ce) || len(ce.Conflicts) != 1 || ce.Conflicts[0].Target != target || ce.Conflicts[0].Key != "" {
				t.Fatalf("Restore() error = %v, want a conflict on the whole of %s", err, target)
			}
		}
		res, err := restore(d, home, "", consent)
		if err != nil || !res.Uninstalled {
			t.Fatalf("Restore() = %+v, %v, want everything undone", res, err)
		}
		if _, err := os.Stat(target); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("settings (changed %v) = %v, want the file the install created deleted", changed, err)
		}
	}
}

// TestRestoreAnOlderJournal checks a journal without keys: one an older
// install left is undone whole, and one that released a settings file the
// record owns by key puts that file's keys back.
func TestRestoreAnOlderJournal(t *testing.T) {
	t.Run("whole", func(t *testing.T) {
		home, _ := installHome(t)
		c := newClockFS(t)
		crash(t, &finalStateCrashFS{clockFS: c}, examplePack, "standalone", home)
		stripStateKeys(t, home)
		res, err := restore(c, home, "", nil)
		if err != nil || !res.Uninstalled {
			t.Fatalf("Restore() = %+v, %v, want the killed install undone", res, err)
		}
		if _, err := os.Stat(at(home, claudeSettings)); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("settings = %v, want it deleted", err)
		}
	})
	t.Run("released", func(t *testing.T) {
		home, _ := installHome(t)
		dir := copyPack(t)
		c := newClockFS(t)
		if _, err := installVariant(t, c, dir, "standalone", home); err != nil {
			t.Fatal(err)
		}
		target := at(home, claudeSettings)
		harnessWrites(t, target, map[string]any{"theme": "dark"})
		editPack(t, dir, baseSettings, "claudecode:\n  model: sonnet\n", "")
		editPack(t, dir, "variants/tmux/dotai/settings.yaml", "dotai:\n  overrides: [claudecode.model]\n", "") // nothing left to override
		crash(t, &finalStateCrashFS{clockFS: c}, dir, "standalone", home)
		if got := settingsOf(t, target); !reflect.DeepEqual(got, isolatedClaude(home, map[string]any{"theme": "dark"})) {
			t.Fatalf("settings after the killed install = %v, want dotai's key deleted", got)
		}
		if _, err := restore(c, home, "", nil); err != nil {
			t.Fatalf("Restore() error = %v", err)
		}
		if got, want := settingsOf(t, target), isolatedClaude(home, map[string]any{"model": "sonnet", "theme": "dark"}); !reflect.DeepEqual(got, want) {
			t.Errorf("settings = %v, want %v", got, want)
		}
	})
}

// TestRestoreOverAnOlderPriorState checks an install whose backup keeps a
// state from before decision 0035: dotai owned every key of the file then,
// so each comes back from the copy, with the keys the harness wrote.
func TestRestoreOverAnOlderPriorState(t *testing.T) {
	home, _ := installHome(t)
	d := newClockFS(t)
	if _, err := installVariant(t, d, examplePack, "standalone", home); err != nil {
		t.Fatal(err)
	}
	target := at(home, claudeSettings)
	harnessWrites(t, target, map[string]any{"theme": "dark"})
	second, err := installVariant(t, d, examplePack, "tmux", home)
	if err != nil {
		t.Fatal(err)
	}
	stripPriorKeys(t, home, second.Backup.ID)
	if _, err := restore(d, home, "", nil); err != nil {
		t.Fatalf("Restore() error = %v", err)
	}
	if got, want := settingsOf(t, target), isolatedClaude(home, map[string]any{"model": "sonnet", "theme": "dark"}); !reflect.DeepEqual(got, want) {
		t.Errorf("settings = %v, want %v", got, want)
	}
}

// TestRestoreAChainOfThreeInstalls checks a restore that undoes several
// installs of a settings file at once, keeping the harness's key.
func TestRestoreAChainOfThreeInstalls(t *testing.T) {
	setup := func(t *testing.T) (home string, ids []string) {
		home, _ = installHome(t)
		dir := copyPack(t)
		d := newClockFS(t)
		for i, variant := range []string{"standalone", "tmux", "standalone"} {
			if i == 1 {
				harnessWrites(t, at(home, claudeSettings), map[string]any{"theme": "dark"})
			}
			if i == 2 {
				editPack(t, dir, baseSettings, "  model: sonnet\n", "  model: sonnet\n  verbose: true\n")
			}
			res, err := installVariant(t, d, dir, variant, home)
			if err != nil {
				t.Fatal(err)
			}
			ids = append(ids, res.Backup.ID)
		}
		want := isolatedClaude(home, map[string]any{"model": "sonnet", "verbose": true, "theme": "dark"})
		if got := settingsOf(t, at(home, claudeSettings)); !reflect.DeepEqual(got, want) {
			t.Fatalf("settings after three installs = %v, want %v", got, want)
		}
		return home, ids
	}
	// Undoing all three installs takes dotai's claudeMdExcludes too.
	for i, want := range []map[string]any{{"theme": "dark"}, {"model": "sonnet", "theme": "dark"}} {
		home, ids := setup(t)
		if i > 0 {
			want = isolatedClaude(home, want)
		}
		res, err := restore(newClockFS(t), home, ids[i], nil)
		if err != nil || len(res.Undone) != 3-i {
			t.Fatalf("Restore(%s) = %+v, %v, want %d installs undone", ids[i], res.Undone, err, 3-i)
		}
		if got := settingsOf(t, at(home, claudeSettings)); !reflect.DeepEqual(got, want) {
			t.Errorf("settings after restoring %d installs = %v, want %v", 3-i, got, want)
		}
	}
}

// TestRestoreGivesBackTheHarnessValueThePackOverwrote checks a key the
// harness wrote that the pack then set: the restore puts the harness's
// value back.
func TestRestoreGivesBackTheHarnessValueThePackOverwrote(t *testing.T) {
	home, _ := installHome(t)
	dir := copyPack(t)
	d := newClockFS(t)
	if _, err := installVariant(t, d, dir, "standalone", home); err != nil {
		t.Fatal(err)
	}
	target := at(home, claudeSettings)
	harnessWrites(t, target, map[string]any{"theme": "dark"})
	editPack(t, dir, baseSettings, "  model: sonnet\n", "  model: sonnet\n  theme: light\n")
	if _, err := installVariant(t, d, dir, "standalone", home); err != nil {
		t.Fatal(err)
	}
	if got := settingsOf(t, target)["theme"]; got != "light" {
		t.Fatalf("theme = %v, want the pack's", got)
	}
	if _, err := restore(d, home, "", nil); err != nil {
		t.Fatalf("Restore() error = %v", err)
	}
	if got, want := settingsOf(t, target), isolatedClaude(home, map[string]any{"model": "sonnet", "theme": "dark"}); !reflect.DeepEqual(got, want) {
		t.Errorf("settings = %v, want %v", got, want)
	}
}

// TestRestorePutsBackASettingsFileAnInstallRemoved checks an install that
// deleted every key of a settings file, and so the file: the restore puts
// it back.
func TestRestorePutsBackASettingsFileAnInstallRemoved(t *testing.T) {
	home, _ := installHome(t)
	dir := copyPack(t)
	d := newClockFS(t)
	if _, err := installVariant(t, d, dir, "standalone", home); err != nil {
		t.Fatal(err)
	}
	agy := at(home, ".dotai/isolated/gemini/antigravity-cli/settings.json")
	written, err := os.ReadFile(agy)
	if err != nil {
		t.Fatal(err)
	}
	editPack(t, dir, baseSettings, "antigravity-cli:\n", "unused:\n")
	data, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(baseSettings)))
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(dir, filepath.FromSlash(baseSettings)), strings.Split(string(data), "unused:")[0])
	if _, err := installVariant(t, d, dir, "standalone", home); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(agy); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("agy settings = %v, want the file the install emptied removed", err)
	}
	if _, err := restore(d, home, "", nil); err != nil {
		t.Fatalf("Restore() error = %v", err)
	}
	if got, err := os.ReadFile(agy); err != nil || string(got) != string(written) {
		t.Errorf("agy settings = %s, %v, want %s", got, err, written)
	}
}
