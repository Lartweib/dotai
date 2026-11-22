package app_test

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"reflect"
	"slices"
	"testing"

	"github.com/lartweib/dotai/internal/app"
	"github.com/lartweib/dotai/internal/pack"
)

// putSince is what a user or another tool puts in the harnesses' own
// configuration after a replace install.
func putSince(t *testing.T, home string) {
	t.Helper()
	writeFile(t, at(home, ".claude/agents/new.md"), "new\n")
	writeFile(t, at(home, "CLAUDE.md"), "# written since\n")
	writeFile(t, at(home, ".claude/new-tool/x"), "x\n")
}

// TestReplaceAgainUpdatesOnlyByDefault checks decision 0051: a replace
// install over a settled one wipes nothing, says what a reset would wipe,
// and leaves what was put since where it is.
func TestReplaceAgainUpdatesOnlyByDefault(t *testing.T) {
	home, h, _ := replaceHome(t)
	first, err := installReplace(t, newClockFS(t), h, home)
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Wipes) == 0 || len(first.Reinstall) != 0 || first.WouldReset != nil {
		t.Fatalf("first install = wipes %d, reinstall %v, wouldReset %+v, want a reset that is not a reinstall", len(first.Wipes), first.Reinstall, first.WouldReset)
	}
	putSince(t, home)
	between := homeEntries(t, home)
	second, err := installUpdate(t, newClockFS(t), h, home)
	if err != nil {
		t.Fatalf("Install() again error = %v", err)
	}
	if len(second.Wipes) != 0 || len(second.WipeKeys) != 0 || second.Reset || !reflect.DeepEqual(second.Reinstall, pack.Harnesses) {
		t.Errorf("second install = wipes %v, keys %v, reset %v, reinstall %v, want an update only of every harness", second.Wipes, second.WipeKeys, second.Reset, second.Reinstall)
	}
	if got := rels(home, wipePaths(second.WouldReset.Wipes)); !reflect.DeepEqual(got, []string{".claude/agents", ".claude/new-tool", "CLAUDE.md"}) {
		t.Errorf("wouldReset wipes = %q, want what was put since", got)
	}
	diff(t, "home after the update", homeEntries(t, home), between)
	if app.PinnedBackup(t, home) != first.Backup.ID {
		t.Errorf("pinned backup = %q, want the first install's %s", app.PinnedBackup(t, home), first.Backup.ID)
	}
}

// TestChooseResetDecidesALaterInstall checks the hook an interactive
// caller uses: it is asked, with what a reset would wipe, only for a later
// replace install that has something to wipe; an answer of true resets as
// Reset does, false updates only, and an error stops the install before it
// writes.
func TestChooseResetDecidesALaterInstall(t *testing.T) {
	home, h, _ := replaceHome(t)
	f := quiet(t)
	run := func(opts app.InstallOptions) (app.InstallResult, error) {
		opts.Harnesses = pack.Harnesses
		return app.InstallAgainReplace(context.Background(), newClockFS(t), h, f.fetch, replaceRun(t), examplePack, home, opts)
	}
	asks := 0
	never := func(app.ResetChoice) (bool, error) { asks++; return true, nil }
	if _, err := run(app.InstallOptions{Mode: app.ModeReplace, ChooseReset: never}); err != nil || asks != 0 {
		t.Fatalf("first install: error = %v, asked %d times, want no question: it is always a reset", err, asks)
	}
	if res, err := run(app.InstallOptions{ChooseReset: never}); err != nil || asks != 0 || res.Reset {
		t.Fatalf("later install with nothing to wipe: error = %v, asked %d times, reset %v, want no question", err, asks, res.Reset)
	}
	putSince(t, home)
	between := homeEntries(t, home)

	var choice app.ResetChoice
	res, err := run(app.InstallOptions{ChooseReset: func(c app.ResetChoice) (bool, error) { choice = c; return false, nil }})
	if err != nil || res.Reset || len(res.Wipes) != 0 || !reflect.DeepEqual(choice.Harnesses, pack.Harnesses) || len(choice.Preview.Wipes) != 3 {
		t.Fatalf("answer false: error = %v, reset %v, wipes %d, choice = %+v, want an update only, asked with the three entries", err, res.Reset, len(res.Wipes), choice)
	}
	diff(t, "home after the answer false", homeEntries(t, home), between)

	boom := errors.New("no terminal")
	if _, err := run(app.InstallOptions{ChooseReset: func(app.ResetChoice) (bool, error) { return false, boom }}); !errors.Is(err, boom) {
		t.Errorf("the hook failed: error = %v, want it", err)
	}
	asked := 0
	if res, err := run(app.InstallOptions{Reset: true, ChooseReset: func(app.ResetChoice) (bool, error) { asked++; return false, nil }}); err != nil || asked != 0 || !res.Reset || len(res.Wipes) != 3 {
		t.Fatalf("Reset: error = %v, asked %d times, reset %v, wipes %d, want a reset without a question", err, asked, res.Reset, len(res.Wipes))
	}
	if _, err := restore(newClockFS(t), home, "", nil); err != nil {
		t.Fatalf("Restore() of the reset error = %v", err)
	}
	diff(t, "home after the restore of the reset", homeEntries(t, home), between)

	res, err = run(app.InstallOptions{ChooseReset: never})
	if err != nil || asks != 1 || !res.Reset || len(res.Wipes) != 3 {
		t.Fatalf("answer true: error = %v, asked %d times, reset %v, wipes %d, want a reset", err, asks, res.Reset, len(res.Wipes))
	}
}

// TestAnUpdateOnlyInstallStillRefusesARelocation checks that a harness that
// is only updated is refused when a variable moves its configuration, as
// a reset is: the pack would be written where the harness does not read.
func TestAnUpdateOnlyInstallStillRefusesARelocation(t *testing.T) {
	home, h, _ := replaceHome(t)
	if _, err := installReplace(t, newClockFS(t), h, home); err != nil {
		t.Fatal(err)
	}
	relocated := app.NewHost(found(at(home, "bin"), "claude", "agy", "opencode"), []string{"CLAUDE_CONFIG_DIR=" + filepath.Join(home, "elsewhere")}, "")
	p, err := app.PlanInstallHost(context.Background(), app.OSInstallFS{}, relocated, quiet(t).fetch, replaceRun(t), examplePack, home, app.InstallOptions{Harnesses: pack.Harnesses})
	if err != nil {
		t.Fatal(err)
	}
	if p.Reset || !p.Blocked || !slices.ContainsFunc(p.Refusals, func(r app.Refusal) bool { return r.Code == app.RefuseRelocation && r.Harness == "claudecode" }) {
		t.Errorf("update with CLAUDE_CONFIG_DIR set: reset %v, blocked %v, refusals %+v, want the relocation refusal", p.Reset, p.Blocked, p.Refusals)
	}
}

// TestWouldResetListsMarshalAsEmptyArrays checks the plan JSON contract: a
// later replace install that a reset would wipe no keys for still sends
// wipeKeys and refusals as [], never null, because the web UI reads their length.
func TestWouldResetListsMarshalAsEmptyArrays(t *testing.T) {
	home, h, _ := replaceHome(t)
	if _, err := installReplace(t, newClockFS(t), h, home); err != nil {
		t.Fatal(err)
	}
	second, err := installUpdate(t, newClockFS(t), h, home)
	if err != nil {
		t.Fatal(err)
	}
	if second.WouldReset == nil {
		t.Fatal("wouldReset = nil, want the preview of a later replace install")
	}
	raw, err := json.Marshal(second.WouldReset)
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]json.RawMessage
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"wipes", "wipeKeys", "refusals"} {
		if string(got[k]) == "null" || got[k] == nil {
			t.Errorf("wouldReset.%s = %s, want an array", k, got[k])
		}
	}
}
