package app_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/lartweib/dotai/internal/app"
	"github.com/lartweib/dotai/internal/pack"
	"github.com/lartweib/dotai/internal/plan"
	"github.com/lartweib/dotai/internal/rulesync"
	"github.com/lartweib/dotai/internal/state"
)

// Other programs register their own hooks in the files dotai writes hooks
// to: a status tracker appends entries to the events of Claude Code's
// settings.json and adds a sibling key to agy's hooks.json. dotai owns its
// own entries there, not those files' hook lists (decision 0050).

// packClaudeSettings and packAgyHooks are the files rulesync renders for a
// pack with a SessionStart and a Stop hook.
const (
	packClaudeSettings = `{
  "hooks": {
    "SessionStart": [
      {"matcher": "startup", "hooks": [{"type": "command", "command": "echo pack-start"}]}
    ],
    "Stop": [
      {"hooks": [{"type": "command", "command": "echo pack-stop"}]}
    ]
  }
}
`
	packAgyHooks = `{
  "rulesync": {
    "SessionStart": [{"command": "echo pack-start"}],
    "Stop": [{"command": "echo pack-stop"}]
  }
}
`
	isolatedAgyHooks = ".dotai/isolated/gemini/config/hooks.json"
)

// hooksRulesync is a fake rulesync that renders the hooks files of the
// user scope, besides each harness's rules file.
func hooksRulesync(t *testing.T) func(context.Context, rulesync.Result, []string, ...rulesync.Job) error {
	rules := replaceRun(t)
	return func(ctx context.Context, bin rulesync.Result, targets []string, jobs ...rulesync.Job) error {
		if err := rules(ctx, bin, targets, jobs...); err != nil {
			return err
		}
		for _, j := range jobs {
			if !j.Global {
				continue
			}
			if slices.Contains(targets, "claudecode") {
				writeFile(t, filepath.Join(j.Dir, ".claude", "settings.json"), packClaudeSettings)
			}
			if slices.Contains(targets, "antigravity-cli") {
				writeFile(t, filepath.Join(j.Dir, ".gemini", "config", "hooks.json"), packAgyHooks)
			}
		}
		return nil
	}
}

func installHooks(t *testing.T, d app.ExecuteFS, home, variant string) (app.InstallResult, error) {
	t.Helper()
	f := quiet(t)
	return app.InstallWith(context.Background(), d, f.fetch, hooksRulesync(t), examplePack, variant, home)
}

// orcaCommand is how the status tracker registers itself, with a path of
// its own.
func orcaCommand(harness string) string {
	return "/home/user/.tracker/agent-hooks/tracker-hook " + harness
}

// trackerRegisters does what the tracker does each time it starts: it
// appends its own matcher group to many events of Claude Code's settings,
// PreToolUse with the matcher "*", and adds its own key beside dotai's in
// agy's hooks file.
func trackerRegisters(t *testing.T, claude, agy string) {
	t.Helper()
	settings := settingsOf(t, claude)
	hooks, _ := settings["hooks"].(map[string]any)
	if hooks == nil {
		hooks = map[string]any{}
	}
	for _, event := range []string{"SessionStart", "PreToolUse", "PostToolUse", "Stop", "UserPromptSubmit", "SubagentStart"} {
		group := map[string]any{"hooks": []any{map[string]any{"type": "command", "command": orcaCommand("claude"), "timeout": 10}}}
		if event == "PreToolUse" {
			group["matcher"] = "*"
		}
		list, _ := hooks[event].([]any)
		hooks[event] = append(list, group)
	}
	settings["hooks"] = hooks
	writeJSON(t, claude, settings)

	file := settingsOf(t, agy)
	file["tracker-status"] = map[string]any{"SessionStart": []any{map[string]any{"command": orcaCommand("agy")}}}
	writeJSON(t, agy, file)
}

func writeJSON(t *testing.T, name string, v any) {
	t.Helper()
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, name, string(data)+"\n")
}

// hookCommands are the commands of the hooks of Claude Code's settings at
// name, by event, in order.
func hookCommands(t *testing.T, name string) map[string][]string {
	t.Helper()
	var doc struct {
		Hooks map[string][]struct {
			Hooks []struct{ Command string } `json:"hooks"`
		} `json:"hooks"`
	}
	data, err := os.ReadFile(name)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatalf("%s: %v\n%s", name, err, data)
	}
	out := map[string][]string{}
	for event, groups := range doc.Hooks {
		for _, g := range groups {
			for _, h := range g.Hooks {
				out[event] = append(out[event], h.Command)
			}
		}
	}
	return out
}

// wantHooks is hookCommands for the pack's two hooks, and the tracker's
// after them in each of its events.
func wantHooks(withTracker bool) map[string][]string {
	pack := map[string][]string{"SessionStart": {"echo pack-start"}, "Stop": {"echo pack-stop"}}
	if !withTracker {
		return pack
	}
	for _, event := range []string{"SessionStart", "PreToolUse", "PostToolUse", "Stop", "UserPromptSubmit", "SubagentStart"} {
		pack[event] = append(pack[event], orcaCommand("claude"))
	}
	return pack
}

func agyKeys(t *testing.T, name string) []string {
	t.Helper()
	var out []string
	for k := range settingsOf(t, name) {
		out = append(out, k)
	}
	slices.Sort(out)
	return out
}

// TestDoctorIgnoresHooksAnotherProgramAdds is the bug: after an install,
// a program that registers its own hooks made doctor report the whole
// hooks key of settings.json changed and agy's hooks.json changed, and
// every later install stopped on them.
func TestDoctorIgnoresHooksAnotherProgramAdds(t *testing.T) {
	home, _ := installHome(t)
	if _, err := installHooks(t, newClockFS(t), home, ""); err != nil {
		t.Fatal(err)
	}
	claude, agy := at(home, claudeSettings), at(home, isolatedAgyHooks)
	if got := hookCommands(t, claude); !reflect.DeepEqual(got, wantHooks(false)) {
		t.Fatalf("hooks after the install = %v, want the pack's", got)
	}
	if res := runDoctor(t, &fakeGit{}, home, false); len(res.Findings) != 0 {
		t.Fatalf("Doctor() after the install = %q, want nothing", codes(res))
	}
	trackerRegisters(t, claude, agy)
	if res := runDoctor(t, &fakeGit{}, home, false); len(res.Findings) != 0 {
		t.Errorf("Doctor() after another program registered its hooks = %q, want nothing", codes(res))
	}
}

// TestDoctorStillReportsDotaisOwnHooksChanged checks that the entries
// dotai wrote stay protected: one that is edited, one that is removed and
// agy's rulesync key edited are found, next to the foreign ones.
func TestDoctorStillReportsDotaisOwnHooksChanged(t *testing.T) {
	home, _ := installHome(t)
	if _, err := installHooks(t, newClockFS(t), home, ""); err != nil {
		t.Fatal(err)
	}
	claude, agy := at(home, claudeSettings), at(home, isolatedAgyHooks)
	trackerRegisters(t, claude, agy)

	settings := settingsOf(t, claude)
	hooks := settings["hooks"].(map[string]any)
	start := hooks["SessionStart"].([]any)
	start[0].(map[string]any)["hooks"].([]any)[0].(map[string]any)["timeout"] = 99.0 // dotai's entry edited
	delete(hooks, "Stop")                                                            // dotai's entry removed, the tracker's with it
	writeJSON(t, claude, settings)
	file := settingsOf(t, agy)
	file["rulesync"] = map[string]any{"SessionStart": []any{}}
	writeJSON(t, agy, file)

	res := runDoctor(t, &fakeGit{}, home, false)
	var got []string
	for _, f := range res.Findings {
		got = append(got, f.Code+" "+filepath.Base(f.Path)+" "+strings.Join(f.Key, "|"))
	}
	slices.Sort(got)
	want := []string{
		`key-changed hooks.json rulesync`,
		`key-changed settings.json hooks|SessionStart|startup|echo pack-start`,
		`key-missing settings.json hooks|Stop||echo pack-stop`,
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Doctor() findings = %q, want %q", got, want)
	}
}

// TestInstallKeepsHooksAnotherProgramAdds is the second half of the bug:
// an install over what the tracker wrote stopped on both files. It now
// goes through, and the tracker's entries stay beside dotai's.
func TestInstallKeepsHooksAnotherProgramAdds(t *testing.T) {
	home, _ := installHome(t)
	d := newClockFS(t)
	if _, err := installHooks(t, d, home, "standalone"); err != nil {
		t.Fatal(err)
	}
	claude, agy := at(home, claudeSettings), at(home, isolatedAgyHooks)
	trackerRegisters(t, claude, agy)

	// Another variant changes the keys of settings.json around the hooks.
	res, err := installHooks(t, d, home, "tmux")
	if err != nil {
		t.Fatalf("Install() error = %v, want it to go through over the tracker's entries", err)
	}
	if got := hookCommands(t, claude); !reflect.DeepEqual(got, wantHooks(true)) {
		t.Errorf("hooks = %v, want the pack's and the tracker's", got)
	}
	if got := agyKeys(t, agy); !reflect.DeepEqual(got, []string{"rulesync", "tracker-status"}) {
		t.Errorf("agy hooks.json keys = %v, want dotai's and the tracker's", got)
	}
	if op, _ := opAt(res.Ops, agy); op.Action != plan.Unchanged {
		t.Errorf("agy hooks.json op = %+v, want it unchanged: nothing of dotai's changed", op)
	}
	st := readJSON(t, res.State, state.Decode)
	f, ok := ownedFile(t, st, claude)
	if !ok || slices.ContainsFunc(f.Keys, func(k plan.Key) bool { return slices.Equal(k.Path, []string{"hooks"}) }) {
		t.Errorf("record of settings.json = %+v, want the hook entries owned one by one, not the hooks key", f)
	}
	if f, ok := ownedFile(t, st, agy); !ok || !reflect.DeepEqual(keyNames(f.Keys), []string{"rulesync"}) {
		t.Errorf("record of agy hooks.json = %+v, want the key rulesync only", f)
	}
	if r := runDoctor(t, &fakeGit{}, home, false); len(r.Findings) != 0 {
		t.Errorf("Doctor() = %q, want nothing", codes(r))
	}

	// A second install over them changes nothing.
	again, err := installHooks(t, d, home, "tmux")
	if err != nil {
		t.Fatal(err)
	}
	for _, op := range again.Ops {
		if op.Action != plan.Unchanged {
			t.Errorf("op %+v, want unchanged", op)
		}
	}
}

// TestInstallPutsBackAHookEntryAndRefusesAnEditedOne checks dotai's own
// entries on install: one that is gone is written again, one that was
// edited stops the install, naming the entry.
func TestInstallPutsBackAHookEntryAndRefusesAnEditedOne(t *testing.T) {
	home, _ := installHome(t)
	d := newClockFS(t)
	if _, err := installHooks(t, d, home, "standalone"); err != nil {
		t.Fatal(err)
	}
	claude, agy := at(home, claudeSettings), at(home, isolatedAgyHooks)
	trackerRegisters(t, claude, agy)

	settings := settingsOf(t, claude)
	hooks := settings["hooks"].(map[string]any)
	hooks["Stop"] = hooks["Stop"].([]any)[1:] // dotai's Stop entry removed, the tracker's stays
	writeJSON(t, claude, settings)
	if _, err := installHooks(t, d, home, "standalone"); err != nil {
		t.Fatalf("Install() error = %v, want the removed entry put back", err)
	}
	if got := hookCommands(t, claude)["Stop"]; !slices.Contains(got, "echo pack-stop") || !slices.Contains(got, orcaCommand("claude")) {
		t.Errorf("Stop hooks = %v, want dotai's entry back next to the tracker's", got)
	}

	settings = settingsOf(t, claude)
	settings["hooks"].(map[string]any)["SessionStart"].([]any)[0].(map[string]any)["hooks"].([]any)[0].(map[string]any)["timeout"] = 99.0
	writeJSON(t, claude, settings)
	before, _ := os.ReadFile(claude)
	_, err := installHooks(t, d, home, "standalone")
	var e *plan.EditedError
	if !errors.As(err, &e) || len(e.Keys[claude]) != 1 || !strings.Contains(e.Keys[claude][0], "echo pack-start") {
		t.Fatalf("Install() error = %v, want the edited SessionStart entry named", err)
	}
	if after, _ := os.ReadFile(claude); string(after) != string(before) {
		t.Errorf("settings.json changed by a refused install:\n%s", after)
	}
}

// TestRestoreKeepsHooksAnotherProgramAdds undoes an install after the
// tracker registered: dotai's entries go and the tracker's stay, in both
// files, and a changed entry of dotai's is a conflict as any changed key.
func TestRestoreKeepsHooksAnotherProgramAdds(t *testing.T) {
	home, _ := installHome(t)
	d := newClockFS(t)
	if _, err := installHooks(t, d, home, "standalone"); err != nil {
		t.Fatal(err)
	}
	claude, agy := at(home, claudeSettings), at(home, isolatedAgyHooks)
	trackerRegisters(t, claude, agy)

	res, err := restore(d, home, "", nil)
	if err != nil || len(res.Conflicts) != 0 {
		t.Fatalf("Restore() = %+v, %v, want no conflicts", res, err)
	}
	want := map[string][]string{}
	for event := range wantHooks(true) {
		want[event] = []string{orcaCommand("claude")}
	}
	if got := hookCommands(t, claude); !reflect.DeepEqual(got, want) {
		t.Errorf("hooks after the restore = %v, want the tracker's only", got)
	}
	if got := agyKeys(t, agy); !reflect.DeepEqual(got, []string{"tracker-status"}) {
		t.Errorf("agy hooks.json keys after the restore = %v, want the tracker's only", got)
	}
}

func TestRestoreAsksAboutAnEditedHookEntryOfDotai(t *testing.T) {
	home, _ := installHome(t)
	d := newClockFS(t)
	if _, err := installHooks(t, d, home, "standalone"); err != nil {
		t.Fatal(err)
	}
	claude, agy := at(home, claudeSettings), at(home, isolatedAgyHooks)
	trackerRegisters(t, claude, agy)
	settings := settingsOf(t, claude)
	settings["hooks"].(map[string]any)["SessionStart"].([]any)[0].(map[string]any)["hooks"].([]any)[0].(map[string]any)["timeout"] = 99.0
	writeJSON(t, claude, settings)

	asked := false
	_, err := restore(d, home, "", func(r app.RestoreResult) (bool, error) {
		asked = len(r.Conflicts) == 1 && strings.Contains(r.Conflicts[0].Key, "echo pack-start")
		return false, nil
	})
	if err == nil || !asked {
		t.Errorf("Restore() error = %v, asked = %v, want the edited entry to ask and a refusal to stop", err, asked)
	}
}

// TestReplaceCoexistsWithHooksAnotherProgramAdds is the replace mode: what
// the user had before is wiped and backed up as ever, and what another
// program adds after it is kept by the next install.
func TestReplaceCoexistsWithHooksAnotherProgramAdds(t *testing.T) {
	home, h := realHome(t)
	claude, agy := at(home, ".claude/settings.json"), at(home, ".gemini/config/hooks.json")
	writeFile(t, claude, `{"model":"opus","hooks":{"Stop":[{"hooks":[{"type":"command","command":"echo user-stop"}]}]}}`)
	writeFile(t, agy, `{"user-hooks":{"Stop":[{"command":"echo user-stop"}]}}`)
	d := newClockFS(t)
	f := quiet(t)
	opts := app.InstallOptions{Harnesses: pack.Harnesses}
	first, err := app.InstallReplace(context.Background(), d, h, f.fetch, hooksRulesync(t), examplePack, home, opts)
	if err != nil {
		t.Fatalf("Install() error = %v", err)
	}
	if got := hookCommands(t, claude); !reflect.DeepEqual(got, wantHooks(false)) {
		t.Errorf("hooks after the replace = %v, want the pack's only: what was there is wiped", got)
	}
	if got := agyKeys(t, agy); !reflect.DeepEqual(got, []string{"rulesync"}) {
		t.Errorf("agy hooks.json keys after the replace = %v, want dotai's only", got)
	}
	saved, err := os.ReadFile(filepath.Join(first.Backup.Path, "files", ".claude", "settings.json"))
	if err != nil || !strings.Contains(string(saved), "user-stop") {
		t.Errorf("backup of settings.json = %s, %v, want the user's hooks kept in it", saved, err)
	}

	trackerRegisters(t, claude, agy)
	if r := runDoctor(t, &fakeGit{}, home, false); slices.ContainsFunc(r.Findings, func(f app.Finding) bool { return f.Path == claude || f.Path == agy }) {
		t.Errorf("Doctor() = %q, want nothing about the hooks files", codes(r))
	}
	if _, err := app.InstallAgainReplace(context.Background(), d, h, f.fetch, hooksRulesync(t), examplePack, home, opts); err != nil {
		t.Fatalf("second Install() error = %v, want it to go through", err)
	}
	if got := hookCommands(t, claude); !reflect.DeepEqual(got, wantHooks(true)) {
		t.Errorf("hooks after the second install = %v, want the pack's and the tracker's", got)
	}
	if got := agyKeys(t, agy); !reflect.DeepEqual(got, []string{"rulesync", "tracker-status"}) {
		t.Errorf("agy hooks.json keys after the second install = %v", got)
	}
}

// asBeforeDecision0050 is the install state of home as an install before
// decision 0050 recorded it: Claude Code's settings own the hooks key whole
// and agy's hooks file is owned whole, without keys.
func asBeforeDecision0050(t *testing.T, home string) {
	t.Helper()
	name := at(home, ".dotai/state.json")
	st := readJSON(t, name, state.Decode)
	st.Files = append([]state.File{}, st.Files...)
	for i, f := range st.Files {
		switch f.Target {
		case at(home, claudeSettings):
			data, err := os.ReadFile(f.Target)
			if err != nil {
				t.Fatal(err)
			}
			held, err := plan.HeldKeys("claudecode", data, []plan.Key{{Path: []string{"hooks"}}})
			if err != nil {
				t.Fatal(err)
			}
			keys := []plan.Key{}
			for _, k := range f.Keys {
				if k.Path[0] != "hooks" {
					keys = append(keys, k)
				}
			}
			st.Files[i].Keys = append(keys, held[len(held)-1])
		case at(home, isolatedAgyHooks):
			st.Files[i].Keys = nil
		}
	}
	writeState(t, name, st)
}

// TestInstallOverARecordFromBeforeDecision0050 checks an install that
// recorded the hooks of Claude Code's settings as one key and agy's hooks
// file whole, over what another program added since: it goes through, the
// entries the pack stages are set and recorded one by one, and the other
// program's stay.
func TestInstallOverARecordFromBeforeDecision0050(t *testing.T) {
	home, _ := installHome(t)
	d := newClockFS(t)
	if _, err := installHooks(t, d, home, "standalone"); err != nil {
		t.Fatal(err)
	}
	claude, agy := at(home, claudeSettings), at(home, isolatedAgyHooks)
	asBeforeDecision0050(t, home)
	if res := runDoctor(t, &fakeGit{}, home, false); slices.ContainsFunc(res.Findings, func(f app.Finding) bool { return f.Path == claude }) {
		t.Errorf("Doctor() = %q, want nothing about settings.json: the legacy key cannot tell the entries apart", codes(res))
	}
	trackerRegisters(t, claude, agy)
	// The record cannot tell which key of agy's file changed: a hand edit of
	// dotai's own key is rewritten, with a warning.
	edited := settingsOf(t, agy)
	edited["rulesync"] = map[string]any{"Stop": []any{}}
	writeJSON(t, agy, edited)

	res, err := installHooks(t, d, home, "standalone")
	if err != nil {
		t.Fatalf("Install() error = %v, want it to go through over the tracker's entries", err)
	}
	if got := agyKeys(t, agy); !reflect.DeepEqual(got, []string{"rulesync", "tracker-status"}) {
		t.Errorf("agy hooks.json keys after the install = %v, want dotai's and the tracker's", got)
	}
	if got := settingsOf(t, agy)["rulesync"].(map[string]any); len(got) != 2 {
		t.Errorf("rulesync after the install = %v, want the pack's two events: dotai rewrites its own key", got)
	}
	var warned bool
	for _, w := range res.InstallPlan.Warnings {
		warned = warned || w.Code == plan.CodeHooksFileRebased && w.Path == agy && w.Harness == "antigravity-cli" && strings.Contains(w.Message, "backup")
	}
	if !warned {
		t.Errorf("warnings = %+v, want hooks-file-rebased for %s", res.InstallPlan.Warnings, agy)
	}
	if got := hookCommands(t, claude); !reflect.DeepEqual(got, wantHooks(true)) {
		t.Errorf("hooks = %v, want the pack's and the tracker's", got)
	}
	st := readJSON(t, res.State, state.Decode)
	f, _ := ownedFile(t, st, claude)
	if slices.ContainsFunc(f.Keys, func(k plan.Key) bool { return plan.LegacyKey("claudecode", k) }) || !slices.ContainsFunc(f.Keys, func(k plan.Key) bool { return len(k.Path) == 4 }) {
		t.Errorf("record of settings.json = %+v, want the hook entries one by one", f)
	}
	if f, _ := ownedFile(t, st, agy); !reflect.DeepEqual(keyNames(f.Keys), []string{"rulesync"}) {
		t.Errorf("record of agy hooks.json = %+v, want the key rulesync", f)
	}
	if r := runDoctor(t, &fakeGit{}, home, false); len(r.Findings) != 0 {
		t.Errorf("Doctor() = %q, want nothing", codes(r))
	}
}

// TestRestoreOverARecordFromBeforeDecision0050 checks that such a record,
// which cannot tell dotai's entries from another program's, still asks
// before a restore overwrites what changed since, as it always did.
func TestRestoreOverARecordFromBeforeDecision0050(t *testing.T) {
	home, _ := installHome(t)
	d := newClockFS(t)
	if _, err := installHooks(t, d, home, "standalone"); err != nil {
		t.Fatal(err)
	}
	claude, agy := at(home, claudeSettings), at(home, isolatedAgyHooks)
	asBeforeDecision0050(t, home)
	trackerRegisters(t, claude, agy)
	before, _ := os.ReadFile(claude)

	var conflicts []app.RestoreConflict
	_, err := restore(d, home, "", func(r app.RestoreResult) (bool, error) {
		conflicts = r.Conflicts
		return false, nil
	})
	var got []string
	for _, c := range conflicts {
		got = append(got, filepath.Base(c.Target)+" "+c.Key)
	}
	slices.Sort(got)
	if err == nil || !reflect.DeepEqual(got, []string{"hooks.json ", "settings.json hooks"}) {
		t.Fatalf("Restore() error = %v, conflicts = %q, want the hooks key of settings.json and the whole hooks.json to ask", err, got)
	}
	if after, _ := os.ReadFile(claude); string(after) != string(before) {
		t.Errorf("settings.json changed by a refused restore:\n%s", after)
	}
}

// TestRestoreOfAChainThatStartedBeforeDecision0050IsExact follows two
// installs, the first recorded as before decision 0050 and the second after
// it, through two restores: the second install keeps the record of the
// first as the state it started from, so undoing both gives each file back
// as it was before the first, or removes it when dotai created it.
func TestRestoreOfAChainThatStartedBeforeDecision0050IsExact(t *testing.T) {
	const (
		userClaude = "{\n  \"theme\": \"dark\",\n  \"hooks\": {\n    \"Stop\": [\n      {\n        \"hooks\": [\n          {\n            \"type\": \"command\",\n            \"command\": \"echo user-stop\"\n          }\n        ]\n      }\n    ]\n  }\n}\n"
		userAgy    = "{\n  \"user-hooks\": {\n    \"Stop\": [\n      {\n        \"command\": \"echo user-stop\"\n      }\n    ]\n  }\n}\n"
	)
	for _, existing := range []bool{true, false} {
		name := "a file dotai created"
		if existing {
			name = "a file the user had"
		}
		t.Run(name, func(t *testing.T) {
			home, _ := installHome(t)
			claude, agy := at(home, claudeSettings), at(home, isolatedAgyHooks)
			if existing {
				writeFile(t, claude, userClaude)
				writeFile(t, agy, userAgy)
			}
			d := newClockFS(t)
			if _, err := installHooks(t, d, home, "standalone"); err != nil {
				t.Fatal(err)
			}
			asBeforeDecision0050(t, home)
			if _, err := installHooks(t, d, home, "tmux"); err != nil {
				t.Fatalf("second Install() error = %v", err)
			}
			for range 2 {
				if res, err := restore(d, home, "", nil); err != nil || len(res.Conflicts) != 0 {
					t.Fatalf("Restore() = %+v, %v, want no conflicts", res, err)
				}
			}
			for target, want := range map[string]string{claude: userClaude, agy: userAgy} {
				got, err := os.ReadFile(target)
				switch {
				case !existing && !errors.Is(err, os.ErrNotExist):
					t.Errorf("%s after both restores = %s, %v, want it removed: dotai created it", target, got, err)
				case existing && string(got) != want:
					t.Errorf("%s after both restores = %s, want it as it was before the first install:\n%s", target, got, want)
				}
			}
		})
	}
}

// TestReplaceRestoreAsksAboutHooksAnotherProgramAdded checks what a restore
// of a replace install does with them: the hook files that the replace
// wiped are put back whole, so what another program added since is a
// conflict, which is asked about and, once confirmed, gone with the rest.
func TestReplaceRestoreAsksAboutHooksAnotherProgramAdded(t *testing.T) {
	home, h := realHome(t)
	claude, agy := at(home, ".claude/settings.json"), at(home, ".gemini/config/hooks.json")
	const userClaude = `{"model":"opus","hooks":{"Stop":[{"hooks":[{"type":"command","command":"echo user-stop"}]}]}}`
	const userAgy = `{"user-hooks":{"Stop":[{"command":"echo user-stop"}]}}`
	writeFile(t, claude, userClaude)
	writeFile(t, agy, userAgy)
	d := newClockFS(t)
	f := quiet(t)
	if _, err := app.InstallReplace(context.Background(), d, h, f.fetch, hooksRulesync(t), examplePack, home, app.InstallOptions{Harnesses: pack.Harnesses}); err != nil {
		t.Fatal(err)
	}
	trackerRegisters(t, claude, agy)
	foreign, _ := os.ReadFile(claude)

	var asked []string
	_, err := restore(d, home, "", func(r app.RestoreResult) (bool, error) {
		for _, c := range r.Conflicts {
			asked = append(asked, filepath.Base(c.Target))
		}
		return false, nil
	})
	slices.Sort(asked)
	if err == nil || !slices.Contains(asked, "settings.json") || !slices.Contains(asked, "hooks.json") {
		t.Fatalf("Restore() error = %v, asked about %q, want both hook files to ask", err, asked)
	}
	if after, _ := os.ReadFile(claude); string(after) != string(foreign) {
		t.Errorf("settings.json changed by a refused restore:\n%s", after)
	}
	if _, err := restore(d, home, "", func(app.RestoreResult) (bool, error) { return true, nil }); err != nil {
		t.Fatalf("confirmed Restore() error = %v", err)
	}
	for target, want := range map[string]string{claude: userClaude, agy: userAgy} {
		if got, _ := os.ReadFile(target); string(got) != want {
			t.Errorf("%s after the confirmed restore = %s, want the file as it was before the replace", target, got)
		}
	}
}

// addGroup adds a matcher group to an event of Claude Code's settings at
// name, first or last in the event.
func addGroup(t *testing.T, name, event string, first bool, group map[string]any) {
	t.Helper()
	settings := settingsOf(t, name)
	hooks := settings["hooks"].(map[string]any)
	list, _ := hooks[event].([]any)
	if first {
		hooks[event] = append([]any{group}, list...)
	} else {
		hooks[event] = append(list, group)
	}
	writeJSON(t, name, settings)
}

func trackerGroup(command string) map[string]any {
	return map[string]any{"hooks": []any{map[string]any{"type": "command", "command": command, "timeout": 10}}}
}

// TestHooksAnotherProgramPutsBeforeDotaisGroups checks that the position of
// a foreign group does not matter: one placed before dotai's group in its
// event is no drift, survives an install, and stays when the installs are
// undone.
func TestHooksAnotherProgramPutsBeforeDotaisGroups(t *testing.T) {
	home, _ := installHome(t)
	d := newClockFS(t)
	if _, err := installHooks(t, d, home, "standalone"); err != nil {
		t.Fatal(err)
	}
	claude := at(home, claudeSettings)
	for _, event := range []string{"SessionStart", "Stop"} {
		addGroup(t, claude, event, true, trackerGroup(orcaCommand("claude")))
	}
	if res := runDoctor(t, &fakeGit{}, home, false); len(res.Findings) != 0 {
		t.Fatalf("Doctor() = %q, want nothing", codes(res))
	}
	if _, err := installHooks(t, d, home, "tmux"); err != nil {
		t.Fatalf("Install() error = %v", err)
	}
	want := map[string][]string{"SessionStart": {orcaCommand("claude"), "echo pack-start"}, "Stop": {orcaCommand("claude"), "echo pack-stop"}}
	if got := hookCommands(t, claude); !reflect.DeepEqual(got, want) {
		t.Errorf("hooks after the install = %v, want %v", got, want)
	}
	if res := runDoctor(t, &fakeGit{}, home, false); len(res.Findings) != 0 {
		t.Errorf("Doctor() after the install = %q, want nothing", codes(res))
	}
	for range 2 {
		if res, err := restore(d, home, "", nil); err != nil || len(res.Conflicts) != 0 {
			t.Fatalf("Restore() = %+v, %v", res, err)
		}
	}
	want = map[string][]string{"SessionStart": {orcaCommand("claude")}, "Stop": {orcaCommand("claude")}}
	if got := hookCommands(t, claude); !reflect.DeepEqual(got, want) {
		t.Errorf("hooks after the restores = %v, want the tracker's only", got)
	}
}

// TestForeignGroupWithTheSameMatcherAndCommand checks the identity of a
// group (decision 0050): the first group of an event with a matcher and a
// first command is dotai's. A foreign one after it, which gets #2, is not
// reported, survives an install and stays after a restore; one before it
// takes the identity, so dotai's group is the repeat and the entry reads as
// changed.
func TestForeignGroupWithTheSameMatcherAndCommand(t *testing.T) {
	same := map[string]any{"matcher": "startup", "hooks": []any{map[string]any{"type": "command", "command": "echo pack-start", "timeout": 7}}}
	t.Run("after dotai's", func(t *testing.T) {
		home, _ := installHome(t)
		d := newClockFS(t)
		if _, err := installHooks(t, d, home, "standalone"); err != nil {
			t.Fatal(err)
		}
		claude := at(home, claudeSettings)
		addGroup(t, claude, "SessionStart", false, same)
		if res := runDoctor(t, &fakeGit{}, home, false); len(res.Findings) != 0 {
			t.Fatalf("Doctor() = %q, want nothing", codes(res))
		}
		if _, err := installHooks(t, d, home, "tmux"); err != nil {
			t.Fatalf("Install() error = %v", err)
		}
		if got := hookCommands(t, claude)["SessionStart"]; !reflect.DeepEqual(got, []string{"echo pack-start", "echo pack-start"}) {
			t.Errorf("SessionStart commands = %v, want both groups", got)
		}
		for range 2 {
			if res, err := restore(d, home, "", nil); err != nil || len(res.Conflicts) != 0 {
				t.Fatalf("Restore() = %+v, %v", res, err)
			}
		}
		var groups struct {
			Hooks map[string][]struct {
				Hooks []struct{ Timeout int } `json:"hooks"`
			} `json:"hooks"`
		}
		data, _ := os.ReadFile(claude)
		if err := json.Unmarshal(data, &groups); err != nil || len(groups.Hooks["SessionStart"]) != 1 || groups.Hooks["SessionStart"][0].Hooks[0].Timeout != 7 {
			t.Errorf("settings after the restores = %s, %v, want the foreign group only", data, err)
		}
	})
	t.Run("before dotai's", func(t *testing.T) {
		home, _ := installHome(t)
		if _, err := installHooks(t, newClockFS(t), home, "standalone"); err != nil {
			t.Fatal(err)
		}
		claude := at(home, claudeSettings)
		addGroup(t, claude, "SessionStart", true, same)
		res := runDoctor(t, &fakeGit{}, home, false)
		if want := []string{"key-changed " + claude + " hooks.SessionStart.startup.echo pack-start"}; !reflect.DeepEqual(codes(res), want) {
			t.Errorf("Doctor() = %q, want %q", codes(res), want)
		}
	})
}
