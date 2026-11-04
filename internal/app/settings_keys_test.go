package app_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/lartweib/dotai/internal/app"
	"github.com/lartweib/dotai/internal/plan"
	"github.com/lartweib/dotai/internal/state"
)

// claudeSettings is Claude Code's settings file in the isolated install.
const claudeSettings = ".dotai/isolated/claude/settings.json"

func installVariant(t *testing.T, d app.ExecuteFS, dir, variant, home string) (app.InstallResult, error) {
	t.Helper()
	f := quiet(t)
	return app.InstallWith(context.Background(), d, f.fetch, f.run, dir, variant, home)
}

// settingsOf decodes a settings file as JSON.
func settingsOf(t *testing.T, name string) map[string]any {
	t.Helper()
	data, err := os.ReadFile(name)
	if err != nil {
		t.Fatal(err)
	}
	var v map[string]any
	if err := json.Unmarshal(data, &v); err != nil {
		t.Fatalf("%s: %v\n%s", name, err, data)
	}
	return v
}

// harnessWrites adds keys to a settings file as a harness does while it
// runs: Claude Code writes the theme on its first launch.
func harnessWrites(t *testing.T, name string, keys map[string]any) {
	t.Helper()
	v := settingsOf(t, name)
	for k, val := range keys {
		v[k] = val
	}
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, name, string(data)+"\n")
}

func ownedFile(t *testing.T, s state.State, target string) (state.File, bool) {
	t.Helper()
	for _, f := range s.Files {
		if f.Target == target {
			return f, true
		}
	}
	return state.File{}, false
}

func keyNames(keys []plan.Key) []string {
	var out []string
	for _, k := range keys {
		out = append(out, k.Name())
	}
	return out
}

// isolatedClaude is want, Claude Code's isolated settings as a test
// expects them, with the claudeMdExcludes an isolated install into home
// owns there (homeExcludes).
func isolatedClaude(home string, want map[string]any) map[string]any {
	want["claudeMdExcludes"] = homeExcludes(home)
	return want
}

// TestInstallKeepsWhatTheHarnessWroteInItsSettings is the case decision
// 0035 starts from: Claude Code adds the theme to its settings on the first
// launch, and a later install of another variant must neither refuse nor
// drop it.
func TestInstallKeepsWhatTheHarnessWroteInItsSettings(t *testing.T) {
	home, _ := installHome(t)
	d := newClockFS(t)
	if _, err := installVariant(t, d, examplePack, "standalone", home); err != nil {
		t.Fatal(err)
	}
	target := at(home, claudeSettings)
	harnessWrites(t, target, map[string]any{"theme": "dark"})

	res, err := installVariant(t, d, examplePack, "tmux", home)
	if err != nil {
		t.Fatalf("Install() error = %v, want the variant installed over the harness's key", err)
	}
	got := settingsOf(t, target)
	if want := isolatedClaude(home, map[string]any{"model": "opus", "theme": "dark", "env": map[string]any{"DOTAI_TERMINAL": "tmux"}}); !reflect.DeepEqual(got, want) {
		t.Errorf("settings = %v, want %v", got, want)
	}
	var op app.InstallOp
	for _, o := range res.Ops {
		if o.Target == target {
			op = o
		}
	}
	if want := []plan.KeyChange{{Key: "env.DOTAI_TERMINAL", Action: plan.Create}, {Key: "model", Action: plan.Replace}}; op.Action != plan.Replace || !reflect.DeepEqual(op.Keys, want) {
		t.Errorf("settings op = %+v, want a replace of %+v", op, want)
	}
	copied, err := os.ReadFile(filepath.Join(res.Backup.Path, "files", filepath.FromSlash(claudeSettings)))
	if err != nil || !strings.Contains(string(copied), `"theme": "dark"`) {
		t.Errorf("backup copy = %s, %v, want the whole file as the harness left it", copied, err)
	}
	s := readJSON(t, res.State, state.Decode)
	f, ok := ownedFile(t, s, target)
	if !ok || !reflect.DeepEqual(keyNames(f.Keys), []string{"model", "env.DOTAI_TERMINAL", "claudeMdExcludes"}) {
		t.Errorf("state record = %+v, want the keys model, env.DOTAI_TERMINAL and claudeMdExcludes", f)
	}
	for _, o := range res.Ops {
		if o.Target != target && o.Keys == nil {
			t.Errorf("op %+v has nil keys, want an empty list", o)
		}
	}
}

// TestInstallRefusesASettingsKeyTheUserChanged checks that a key dotai owns
// is still protected: changed by hand, or by /model in Claude Code, it is
// named and nothing is written.
func TestInstallRefusesASettingsKeyTheUserChanged(t *testing.T) {
	home, _ := installHome(t)
	d := newClockFS(t)
	first, err := installVariant(t, d, examplePack, "standalone", home)
	if err != nil {
		t.Fatal(err)
	}
	target := at(home, claudeSettings)
	harnessWrites(t, target, map[string]any{"theme": "dark", "model": "haiku"})
	stateBefore, _ := os.ReadFile(first.State)
	filesBefore := snapshot(t, home)

	_, err = installVariant(t, d, examplePack, "tmux", home)
	var e *plan.EditedError
	if !errors.As(err, &e) || !reflect.DeepEqual(e.Keys, map[string][]string{target: {"model"}}) {
		t.Fatalf("Install() error = %v, want the key model of %s", err, target)
	}
	if msg := err.Error(); !strings.Contains(msg, "the key model in "+target) || !strings.Contains(msg, "delete the key") {
		t.Errorf("Error() = %q, want it to name the key, the file and how to resolve it", msg)
	}
	if got := snapshot(t, home); !reflect.DeepEqual(got, filesBefore) {
		t.Errorf("home = %v, want %v", got, filesBefore)
	}
	if got, _ := os.ReadFile(first.State); string(got) != string(stateBefore) {
		t.Errorf("state = %s, want it unchanged", got)
	}
}

// TestInstallAfterTheHarnessWroteItsSettingsWritesNothing checks that a key
// the harness added is no reason to write: the settings file is unchanged
// and no backup is taken.
func TestInstallAfterTheHarnessWroteItsSettingsWritesNothing(t *testing.T) {
	home, _ := installHome(t)
	d := newClockFS(t)
	if _, err := installVariant(t, d, examplePack, "", home); err != nil {
		t.Fatal(err)
	}
	target := at(home, claudeSettings)
	harnessWrites(t, target, map[string]any{"theme": "dark"})
	written, _ := os.ReadFile(target)
	res, err := installVariant(t, d, examplePack, "", home)
	if err != nil {
		t.Fatal(err)
	}
	for _, op := range res.Ops {
		if op.Action != plan.Unchanged {
			t.Errorf("op %+v, want unchanged", op)
		}
	}
	if got, _ := os.ReadFile(target); string(got) != string(written) || res.Backup != nil || len(backups(t, home)) != 1 {
		t.Errorf("settings = %s, backup %+v, want the file untouched and no backup", got, res.Backup)
	}
	s := readJSON(t, res.State, state.Decode)
	if f, _ := ownedFile(t, s, target); f.SHA256 != sum(string(written)) || !reflect.DeepEqual(keyNames(f.Keys), []string{"model", "claudeMdExcludes"}) {
		t.Errorf("state record = %+v, want the file as it is and the keys model and claudeMdExcludes", f)
	}
}

// TestInstallReleasesTheSettingsOfADroppedHarness checks a harness the pack
// drops: dotai deletes its keys from the settings file, keeps the file
// with the keys the harness wrote, and no longer owns it.
func TestInstallReleasesTheSettingsOfADroppedHarness(t *testing.T) {
	home, _ := installHome(t)
	dir := copyPack(t)
	d := newClockFS(t)
	if _, err := installVariant(t, d, dir, "", home); err != nil {
		t.Fatal(err)
	}
	agy := at(home, ".dotai/isolated/gemini/antigravity-cli/settings.json")
	harnessWrites(t, agy, map[string]any{"ui": map[string]any{"theme": "dark"}})
	manifest := filepath.Join(dir, "dotai.yaml")
	data, err := os.ReadFile(manifest)
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, manifest, strings.Replace(string(data), "harnesses: [claudecode, antigravity-cli, opencode]", "harnesses: [claudecode, opencode]", 1))
	settings := filepath.Join(dir, "base", "dotai", "settings.yaml")
	data, err = os.ReadFile(settings)
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, settings, strings.Split(string(data), "antigravity-cli:")[0])

	res, err := installVariant(t, d, dir, "", home)
	if err != nil {
		t.Fatal(err)
	}
	if got := settingsOf(t, agy); !reflect.DeepEqual(got, map[string]any{"ui": map[string]any{"theme": "dark"}}) {
		t.Errorf("agy settings = %v, want only the harness's key", got)
	}
	s := readJSON(t, res.State, state.Decode)
	if f, ok := ownedFile(t, s, agy); ok {
		t.Errorf("state still owns %+v", f)
	}
}

// finalStateCrashFS kills an install when it writes its final state, after
// every target is written: the state keeps the journal.
type finalStateCrashFS struct {
	*clockFS
	states int
}

func (f *finalStateCrashFS) WriteIn(root, name string, data []byte, mode os.FileMode) error {
	if name == "state.json" {
		if f.states++; f.states == 2 { // the journal, then the final state
			panic(errCrash)
		}
	}
	return f.clockFS.WriteIn(root, name, data, mode)
}

// TestInstallAndRestoreSettleAKilledSettingsWrite checks an install of
// another variant killed before it wrote the settings file, or after it,
// with the harness writing a key in between: the next install converges
// and a restore undoes it, both keeping the harness's keys.
func TestInstallAndRestoreSettleAKilledSettingsWrite(t *testing.T) {
	for name, kill := range map[string]func(*clockFS) app.ExecuteFS{
		"before the write": func(c *clockFS) app.ExecuteFS { return &crashFS{clockFS: c, crashAt: 1} },
		"after the write":  func(c *clockFS) app.ExecuteFS { return &finalStateCrashFS{clockFS: c} },
	} {
		t.Run(name, func(t *testing.T) {
			for _, then := range []string{"install", "restore"} {
				t.Run(then, func(t *testing.T) {
					home, _ := installHome(t)
					c := newClockFS(t)
					if _, err := installVariant(t, c, examplePack, "standalone", home); err != nil {
						t.Fatal(err)
					}
					target := at(home, claudeSettings)
					harnessWrites(t, target, map[string]any{"theme": "dark"})
					crash(t, kill(c), examplePack, "tmux", home)
					if s := readJSON(t, at(home, ".dotai/state.json"), state.Decode); s.Pending == nil {
						t.Fatal("the killed install left no journal")
					}
					harnessWrites(t, target, map[string]any{"verbose": true})
					want := isolatedClaude(home, map[string]any{"model": "opus", "theme": "dark", "verbose": true, "env": map[string]any{"DOTAI_TERMINAL": "tmux"}})
					if then == "install" {
						if _, err := installVariant(t, c, examplePack, "tmux", home); err != nil {
							t.Fatalf("Install() error = %v", err)
						}
						assertConverged(t, examplePack, "tmux", home)
					} else {
						if _, err := restore(c, home, "", nil); err != nil {
							t.Fatalf("Restore() error = %v", err)
						}
						want = isolatedClaude(home, map[string]any{"model": "sonnet", "theme": "dark", "verbose": true})
					}
					if got := settingsOf(t, target); !reflect.DeepEqual(got, want) {
						t.Errorf("settings = %v, want %v", got, want)
					}
				})
			}
		})
	}
}

// TestRestoreKeepsWhatTheHarnessWroteInItsSettings checks that a restore
// puts back the keys dotai owned before, deletes those it added and keeps
// the keys the harness wrote; undoing every install leaves the file with
// only those.
func TestRestoreKeepsWhatTheHarnessWroteInItsSettings(t *testing.T) {
	home, _ := installHome(t)
	d := newClockFS(t)
	first, err := installVariant(t, d, examplePack, "standalone", home)
	if err != nil {
		t.Fatal(err)
	}
	target := at(home, claudeSettings)
	harnessWrites(t, target, map[string]any{"theme": "dark"})
	if _, err := installVariant(t, d, examplePack, "tmux", home); err != nil {
		t.Fatal(err)
	}
	harnessWrites(t, target, map[string]any{"verbose": true})

	res, err := restore(d, home, "", nil)
	if err != nil {
		t.Fatalf("Restore() error = %v", err)
	}
	if got, want := settingsOf(t, target), isolatedClaude(home, map[string]any{"model": "sonnet", "theme": "dark", "verbose": true}); !reflect.DeepEqual(got, want) {
		t.Errorf("settings = %v, want %v", got, want)
	}
	var op app.RestoreOp
	for _, o := range res.Ops {
		if o.Target == target {
			op = o
		}
	}
	if want := []plan.KeyChange{{Key: "env.DOTAI_TERMINAL", Action: plan.Remove}, {Key: "model", Action: plan.Replace}}; op.Action != plan.Replace || !reflect.DeepEqual(op.Keys, want) {
		t.Errorf("settings op = %+v, want a replace of %+v", op, want)
	}
	s := readJSON(t, res.State, state.Decode)
	if f, _ := ownedFile(t, s, target); !reflect.DeepEqual(keyNames(f.Keys), []string{"model", "claudeMdExcludes"}) {
		t.Errorf("state record = %+v, want the key model of the standalone variant and claudeMdExcludes", f)
	}

	res, err = restore(d, home, first.Backup.ID, nil)
	if err != nil {
		t.Fatalf("Restore(%s) error = %v", first.Backup.ID, err)
	}
	if got, want := settingsOf(t, target), map[string]any{"theme": "dark", "verbose": true}; !res.Uninstalled || !reflect.DeepEqual(got, want) {
		t.Errorf("settings = %v, uninstalled %v; want %v left and nothing installed", got, res.Uninstalled, want)
	}
	if _, err := os.Stat(at(home, ".dotai/isolated/gemini/antigravity-cli/settings.json")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("agy settings = %v, want a file that held only dotai's keys removed", err)
	}
}

// TestRestoreRefusesAChangedSettingsKeyWithoutConsent checks that a key
// dotai owns whose value changed is a conflict that names the key, and
// that with consent the restore puts dotai's value back, after the whole
// file is copied into its backup.
func TestRestoreRefusesAChangedSettingsKeyWithoutConsent(t *testing.T) {
	home, _ := installHome(t)
	d := newClockFS(t)
	if _, err := installVariant(t, d, examplePack, "standalone", home); err != nil {
		t.Fatal(err)
	}
	if _, err := installVariant(t, d, examplePack, "tmux", home); err != nil {
		t.Fatal(err)
	}
	target := at(home, claudeSettings)
	harnessWrites(t, target, map[string]any{"model": "haiku", "theme": "dark"})
	before := snapshot(t, home)

	_, err := restore(d, home, "", nil)
	var ce *app.ConflictError
	if !errors.As(err, &ce) || len(ce.Conflicts) != 1 || ce.Conflicts[0].Target != target || ce.Conflicts[0].Key != "model" ||
		!strings.Contains(err.Error(), "the key model in "+target+" was changed") {
		t.Fatalf("Restore() error = %v, want a conflict on the key model of %s", err, target)
	}
	if got := snapshot(t, home); !reflect.DeepEqual(got, before) {
		t.Errorf("home = %v, want it unchanged", got)
	}

	res, err := restore(d, home, "", func(app.RestoreResult) (bool, error) { return true, nil })
	if err != nil {
		t.Fatalf("Restore() error = %v", err)
	}
	if got, want := settingsOf(t, target), isolatedClaude(home, map[string]any{"model": "sonnet", "theme": "dark"}); !reflect.DeepEqual(got, want) {
		t.Errorf("settings = %v, want %v", got, want)
	}
	copied, err := os.ReadFile(filepath.Join(res.Backup.Path, "files", filepath.FromSlash(claudeSettings)))
	if err != nil || !strings.Contains(string(copied), `"haiku"`) {
		t.Errorf("restore backup copy = %s, %v, want the file with the user's value", copied, err)
	}
	for _, op := range res.Ops {
		if op.Conflict != (op.Target == target) {
			t.Errorf("op %+v, want only %s marked as a conflict", op, target)
		}
	}
}
