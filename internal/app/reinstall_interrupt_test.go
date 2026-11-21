package app_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"reflect"
	"slices"
	"testing"

	"github.com/lartweib/dotai/internal/app"
	"github.com/lartweib/dotai/internal/pack"
	"github.com/lartweib/dotai/internal/state"
)

// killReplace runs a first replace install of harnesses through d until d
// kills it.
func killReplace(t *testing.T, d app.ExecuteFS, h app.Host, home string, harnesses []string) {
	t.Helper()
	defer func() {
		if r := recover(); r != errCrash {
			t.Fatalf("recovered %v, want the crash", r)
		}
	}()
	_, _ = app.InstallReplace(context.Background(), d, h, quiet(t).fetch, replaceRun(t), examplePack, home, app.InstallOptions{Harnesses: harnesses})
}

// TestAnInterruptedResetIsNotALaterInstall checks that a replace install
// killed while it reset leaves a state that the next plain install reads as
// a first install again: it plans the wipes the kill did not do, and does
// not take the unfinished reset for a settled one to update.
func TestAnInterruptedResetIsNotALaterInstall(t *testing.T) {
	home, h, _ := replaceHome(t)
	killReplace(t, &stepFS{clockFS: newClockFS(t), at: 3, kill: true}, h, home, pack.Harnesses)
	if s := readJSON(t, at(home, ".dotai/state.json"), state.Decode); s.Pending == nil || len(s.Pending.Wipes) == 0 {
		t.Fatalf("state after the kill = %+v, want the journal of the reset", s)
	}
	p, err := app.PlanInstallHost(context.Background(), app.OSInstallFS{}, h, quiet(t).fetch, replaceRun(t), examplePack, home, app.InstallOptions{Harnesses: pack.Harnesses})
	if err != nil || len(p.Reinstall) != 0 || p.Reset || len(p.Wipes) == 0 {
		t.Fatalf("plan after the kill = reinstall %v, reset %v, wipes %d, %v; want a first install that still wipes", p.Reinstall, p.Reset, len(p.Wipes), err)
	}
	res, err := installUpdate(t, newClockFS(t), h, home)
	if err != nil || len(res.Reinstall) != 0 || len(res.Wipes) == 0 {
		t.Fatalf("install after the kill = reinstall %v, wipes %d, %v; want the reset finished", res.Reinstall, len(res.Wipes), err)
	}
}

// TestAnInterruptedResetOfOneHarnessLeavesTheOthersLater checks that the
// journal of a killed install that was resetting a harness added to the
// selection only makes that harness a first install: the one that already
// had a settled replace install is still updated only, so what was added
// there since is not wiped.
func TestAnInterruptedResetOfOneHarnessLeavesTheOthersLater(t *testing.T) {
	home, h, _ := replaceHome(t)
	both := []string{"claudecode", "antigravity-cli"}
	if _, err := app.InstallReplace(context.Background(), newClockFS(t), h, quiet(t).fetch, replaceRun(t), examplePack, home, app.InstallOptions{Harnesses: []string{"claudecode"}}); err != nil {
		t.Fatal(err)
	}
	writeFile(t, at(home, ".claude/keybindings.json"), "[]\n")
	killReplace(t, &stepFS{clockFS: newClockFS(t), at: 2, kill: true}, h, home, both)
	if s := readJSON(t, at(home, ".dotai/state.json"), state.Decode); s.Pending == nil || len(s.Pending.Wipes) == 0 {
		t.Fatalf("state after the kill = %+v, want the journal of the reset of antigravity-cli", s)
	}
	res, err := app.InstallAgainReplace(context.Background(), newClockFS(t), h, quiet(t).fetch, replaceRun(t), examplePack, home, app.InstallOptions{Harnesses: both})
	if err != nil || !reflect.DeepEqual(res.Reinstall, []string{"claudecode"}) {
		t.Fatalf("install after the kill = reinstall %v, %v; want claudecode alone", res.Reinstall, err)
	}
	for _, w := range res.Wipes {
		if w.Harness != "antigravity-cli" {
			t.Errorf("wipe %+v, want the wipes of antigravity-cli only", w)
		}
	}
	if _, err := os.Stat(at(home, ".claude/keybindings.json")); err != nil {
		t.Errorf("~/.claude/keybindings.json: %v, want it kept by the update of claudecode", err)
	}
}

// TestAKilledResetThatOnlyHadKeysToWipeIsNotSettled checks a first install
// whose only reset work is the keys of ~/.claude.json, killed before it
// wrote them: its journal must still say so, so that the next plain install
// wipes the keys instead of updating over a reset that never happened.
func TestAKilledResetThatOnlyHadKeysToWipeIsNotSettled(t *testing.T) {
	home, _ := installHome(t)
	h := app.NewHost(found(at(home, "bin"), "claude", "agy", "opencode"), nil, "")
	writeFile(t, at(home, ".claude.json"), `{"numStartups":3,"mcpServers":{"mine":{"command":"x"}}}`)
	s := threeServers
	opts := app.InstallOptions{Harnesses: []string{"claudecode"}}
	func() {
		defer func() {
			if r := recover(); r != errCrash {
				t.Fatalf("recovered %v, want the crash", r)
			}
		}()
		_, _ = app.InstallReplace(context.Background(), &stepFS{clockFS: newClockFS(t), at: 1, kill: true}, h, quiet(t).fetch, mcpRulesync(t, &s), examplePack, home, opts)
	}()
	st := readJSON(t, at(home, ".dotai/state.json"), state.Decode)
	if st.Pending == nil || !slices.Contains(st.Pending.Splits, at(home, ".claude.json")) {
		t.Fatalf("state after the kill = %+v, want the journal to name ~/.claude.json as a file it rewrites", st.Pending)
	}
	res, err := app.InstallAgainReplace(context.Background(), newClockFS(t), h, quiet(t).fetch, mcpRulesync(t, &s), examplePack, home, opts)
	if err != nil || len(res.Reinstall) != 0 || len(res.WipeKeys) == 0 {
		t.Fatalf("install after the kill = reinstall %v, wipeKeys %d, %v; want the keys wiped", res.Reinstall, len(res.WipeKeys), err)
	}
	if got := servers(t, at(home, ".claude.json")); !reflect.DeepEqual(got, []string{"codegraph", "engram", "otea"}) {
		t.Errorf("servers = %v, want the pack's and not the user's", got)
	}
}

// TestAnUpdateOnlyInstallAsksConsentForAnUnownedKeyOfTheHomeConfig checks
// that a later replace install that would overwrite a key of ~/.claude.json
// that dotai does not own, such as an MCP server the user has with the name
// of one the pack now adds, needs consent as a file it overwrites does, and
// that, with it, the key is backed up and its restore puts it back.
func TestAnUpdateOnlyInstallAsksConsentForAnUnownedKeyOfTheHomeConfig(t *testing.T) {
	home, h := realHome(t)
	target := at(home, ".claude.json")
	two := twoServers
	if _, err := replaceMCP(t, newClockFS(t), h, home, &two, false); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	var top map[string]any
	if err := json.Unmarshal(data, &top); err != nil {
		t.Fatal(err)
	}
	top["mcpServers"].(map[string]any)["codegraph"] = map[string]any{"command": "mine"}
	data, _ = json.Marshal(top)
	writeFile(t, target, string(data))
	before := homeEntries(t, home)

	three := threeServers
	opts := app.InstallOptions{Mode: app.ModeReplace, Harnesses: pack.Harnesses}
	asked := 0
	_, err = app.InstallConsent(context.Background(), newClockFS(t), h, quiet(t).fetch, mcpRulesync(t, &three), examplePack, home, opts, func(app.InstallPlan) (bool, error) {
		asked++
		return false, nil
	})
	var ce *app.ConsentError
	if !errors.As(err, &ce) || asked != 1 || ce.Keys != 1 || len(ce.Plan.Wipes) != 0 {
		t.Fatalf("update over a user's server of the same name: %v, asked %d times, want a *ConsentError for 1 key", err, asked)
	}
	diff(t, "home after the refusal", homeEntries(t, home), before)

	res, err := app.InstallConsent(context.Background(), newClockFS(t), h, quiet(t).fetch, mcpRulesync(t, &three), examplePack, home, opts, func(app.InstallPlan) (bool, error) { return true, nil })
	if err != nil || res.Backup == nil {
		t.Fatalf("update with consent = %+v, %v, want it installed with a backup", res.Backup, err)
	}
	if _, err := restore(newClockFS(t), home, "", nil); err != nil {
		t.Fatalf("Restore() error = %v", err)
	}
	diff(t, "home after the restore", homeEntries(t, home), before)
}
