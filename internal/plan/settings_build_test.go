package plan_test

import (
	"errors"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/lartweib/dotai/internal/plan"
)

// settingsInput is the plan of Claude Code's settings file staged as
// staged ("" for not staged), holding disk ("" for missing), and owned by
// keys (nil for not owned).
func settingsInput(t *testing.T, staged, disk string, keys []plan.Key) (plan.Input, string) {
	t.Helper()
	home := t.TempDir()
	target := under(home, ".dotai/isolated/claude/settings.json")
	in := plan.Input{Home: home, Adapters: adapters(t, "claudecode"), Disk: map[string]plan.Current{}, Content: map[string][]byte{}}
	if staged != "" {
		in.Files = []plan.File{{Path: ".claude/settings.json", Hash: valueHash(staged), Data: []byte(staged)}}
	}
	if disk != "" {
		in.Disk[target] = plan.Current{Hash: valueHash(disk)}
		in.Content[target] = []byte(disk)
	}
	if keys != nil {
		in.Owned = map[string]plan.Owned{target: {Harness: "claudecode", Hash: "as written", Keys: keys}}
	}
	return in, target
}

// TestBuildMergesSettingsKeys checks that a settings file is planned key by
// key (decision 0035): dotai sets the keys the pack stages, deletes those it
// owned and the pack dropped, and leaves every other key as the harness
// wrote it.
func TestBuildMergesSettingsKeys(t *testing.T) {
	const staged = `{"model": "opus", "env": {"DOTAI_TERMINAL": "tmux"}}`
	tests := []struct {
		name, staged, disk string
		owned              []plan.Key
		action             plan.Action
		data               string // what the target gets, "" when it is the staged file as it is
		changes            []plan.KeyChange
	}{
		{
			name: "harness key kept", staged: staged, disk: "{\n  \"model\": \"sonnet\",\n  \"theme\": \"dark\"\n}\n",
			owned: []plan.Key{key(`"sonnet"`, "model")}, action: plan.Replace,
			data:    "{\n  \"model\": \"opus\",\n  \"theme\": \"dark\",\n  \"env\": {\n    \"DOTAI_TERMINAL\": \"tmux\"\n  }\n}\n",
			changes: []plan.KeyChange{{Key: "env.DOTAI_TERMINAL", Action: plan.Create}, {Key: "model", Action: plan.Replace}},
		},
		{
			name: "dropped keys deleted", staged: `{"model": "sonnet"}`,
			disk:  "{\n  \"model\": \"opus\",\n  \"theme\": \"dark\",\n  \"env\": {\n    \"DOTAI_TERMINAL\": \"tmux\"\n  }\n}\n",
			owned: []plan.Key{key(`"opus"`, "model"), key(`"tmux"`, "env", "DOTAI_TERMINAL")}, action: plan.Replace,
			data:    "{\n  \"model\": \"sonnet\",\n  \"theme\": \"dark\"\n}\n",
			changes: []plan.KeyChange{{Key: "env.DOTAI_TERMINAL", Action: plan.Remove}, {Key: "model", Action: plan.Replace}},
		},
		{
			name: "an owned key the user deleted is set again", staged: `{"model": "sonnet"}`, disk: `{"theme": "dark"}`,
			owned: []plan.Key{key(`"sonnet"`, "model")}, action: plan.Replace,
			data: `{"theme": "dark", "model": "sonnet"}`, changes: []plan.KeyChange{{Key: "model", Action: plan.Create}},
		},
		{
			name: "a harness key the pack now sets", staged: `{"theme": "light"}`, disk: `{"theme": "dark"}`,
			action: plan.Replace, data: `{"theme": "light"}`, changes: []plan.KeyChange{{Key: "theme", Action: plan.Replace}},
		},
		{
			name: "only harness keys changed", staged: `{"model": "sonnet"}`, disk: `{"model": "sonnet", "theme": "dark"}`,
			owned: []plan.Key{key(`"sonnet"`, "model")}, action: plan.Unchanged,
		},
		{
			name: "missing", staged: staged, action: plan.Create,
			changes: []plan.KeyChange{{Key: "env.DOTAI_TERMINAL", Action: plan.Create}, {Key: "model", Action: plan.Create}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			in, target := settingsInput(t, tt.staged, tt.disk, tt.owned)
			got, err := plan.Build(in)
			if err != nil || len(got.Ops) != 1 {
				t.Fatalf("Build() = %+v, %v, want one op", got.Ops, err)
			}
			op := got.Ops[0]
			after := valueHash(tt.data)
			switch {
			case tt.action == plan.Unchanged:
				after = valueHash(tt.disk)
			case tt.data == "":
				after = valueHash(tt.staged)
			}
			wantKeys, _ := plan.SettingsKeys("claudecode", []byte(tt.staged))
			if op.Action != tt.action || op.Target != target || op.Source != ".claude/settings.json" || !op.Settings || op.After != after ||
				string(op.Data) != tt.data || !reflect.DeepEqual(op.Keys, wantKeys) || !reflect.DeepEqual(op.Changes, tt.changes) {
				t.Errorf("op = %+v\ndata %s\nwant %s %s, keys %+v, changes %+v", op, op.Data, tt.action, tt.data, wantKeys, tt.changes)
			}
			// What dotai owns now: the keys of its record that the file holds.
			var now []plan.Key
			have, _ := plan.SettingsKeys("claudecode", []byte(tt.disk))
			for _, k := range tt.owned {
				if slices.ContainsFunc(have, func(h plan.Key) bool { return reflect.DeepEqual(h, k) }) {
					now = append(now, k)
				}
			}
			if !reflect.DeepEqual(got.Keys[target], now) || now != nil && got.Owned[target] != in.Disk[target] {
				t.Errorf("Keys, Owned = %+v, %+v; want the keys dotai owns now, %+v", got.Keys, got.Owned, now)
			}
		})
	}
}

// TestBuildReleasesASettingsFile checks a settings file that the pack no
// longer stages: dotai deletes its keys, and removes the file only when no
// other key is left.
func TestBuildReleasesASettingsFile(t *testing.T) {
	owned := []plan.Key{key(`"sonnet"`, "model")}
	in, target := settingsInput(t, "", `{"model": "sonnet", "theme": "dark"}`, owned)
	got, err := plan.Build(in)
	want := plan.Op{
		Action: plan.Replace, Scope: plan.ScopeUser, Harness: "claudecode", Target: target, Settings: true,
		Before: valueHash(`{"model": "sonnet", "theme": "dark"}`), Data: []byte(`{"theme": "dark"}`), After: valueHash(`{"theme": "dark"}`),
		Changes: []plan.KeyChange{{Key: "model", Action: plan.Remove}},
	}
	if err != nil || len(got.Ops) != 1 || !reflect.DeepEqual(got.Ops[0], want) {
		t.Errorf("Build() = %+v, %v, want %+v", got.Ops, err, want)
	}
	in, target = settingsInput(t, "", "{\n  // mine\n  \"model\": \"sonnet\"\n}\n", owned)
	got, err = plan.Build(in)
	if err != nil || len(got.Ops) != 1 || got.Ops[0].Action != plan.Remove || got.Ops[0].Target != target || !got.Ops[0].Settings {
		t.Errorf("Build() = %+v, %v, want the remove of a file left without a key", got.Ops, err)
	}
	in, _ = settingsInput(t, "", `{"theme": "dark"}`, owned)
	if got, err := plan.Build(in); err != nil || len(got.Ops) != 0 {
		t.Errorf("Build() = %+v, %v, want nothing for a file whose dotai keys are gone", got.Ops, err)
	}
}

// TestBuildRefusesEditedSettingsKeys checks that a key dotai owns whose
// value changed is an edit naming the file and the key, while the other
// keys the harness wrote are not.
func TestBuildRefusesEditedSettingsKeys(t *testing.T) {
	owned := []plan.Key{key(`"sonnet"`, "model"), key(`"tmux"`, "env", "DOTAI_TERMINAL")}
	in, target := settingsInput(t, `{"model": "sonnet"}`, `{"model": "opus", "theme": "dark", "env": {"DOTAI_TERMINAL": "zellij"}}`, owned)
	_, err := plan.Build(in)
	var e *plan.EditedError
	if !errors.As(err, &e) || len(e.Targets) != 0 || !reflect.DeepEqual(e.Keys, map[string][]string{target: {"env.DOTAI_TERMINAL", "model"}}) {
		t.Fatalf("Build() error = %#v, want the keys model and env.DOTAI_TERMINAL of %s", err, target)
	}
	for _, s := range []string{target, "env.DOTAI_TERMINAL", "model"} {
		if !strings.Contains(err.Error(), s) {
			t.Errorf("Error() = %q, want it to name %s", err, s)
		}
	}
}

// TestBuildReadsAnOlderSettingsRecord checks a settings file that an
// install before decision 0035 owned whole: as written, dotai owns every
// key in it; changed, it is an edited file.
func TestBuildReadsAnOlderSettingsRecord(t *testing.T) {
	const disk = `{"model": "sonnet", "theme": "dark"}`
	in, target := settingsInput(t, `{"model": "opus"}`, disk, nil)
	in.Owned = map[string]plan.Owned{target: {Harness: "claudecode", Hash: valueHash(disk)}}
	got, err := plan.Build(in)
	if err != nil || len(got.Ops) != 1 || string(got.Ops[0].Data) != `{"model": "opus"}` {
		t.Errorf("Build() = %+v, %v, want every key of the file as written owned", got.Ops, err)
	}
	in.Owned[target] = plan.Owned{Harness: "claudecode", Hash: "another"}
	var e *plan.EditedError
	if _, err := plan.Build(in); !errors.As(err, &e) || !reflect.DeepEqual(e.Targets, []string{target}) {
		t.Errorf("Build() error = %v, want the edited file", err)
	}
}

// TestBuildResolvesAnInterruptedSettingsWrite checks the keys of a settings
// file an install that stopped was writing: the keys it was writing are
// dotai's when the file holds what it wrote, or holds those keys after the
// harness wrote another one.
func TestBuildResolvesAnInterruptedSettingsWrite(t *testing.T) {
	for _, disk := range []string{`{"model": "opus"}`, `{"model": "opus", "theme": "dark"}`} {
		in, target := settingsInput(t, `{"model": "opus"}`, disk, []plan.Key{key(`"sonnet"`, "model")})
		o := in.Owned[target]
		o.Also, o.AlsoKeys = []plan.Current{{Hash: valueHash(`{"model": "opus"}`)}}, []plan.Key{key(`"opus"`, "model")}
		in.Owned[target] = o
		got, err := plan.Build(in)
		if err != nil || len(got.Ops) != 1 || got.Ops[0].Action != plan.Unchanged || !reflect.DeepEqual(got.Keys[target], o.AlsoKeys) {
			t.Errorf("Build() of %s = %+v, keys %+v, %v; want the keys the interrupted install wrote", disk, got.Ops, got.Keys, err)
		}
	}
}

func TestBuildRefusesASettingsFileItCannotRead(t *testing.T) {
	in, target := settingsInput(t, `{"model": "opus"}`, `{"model": `, nil)
	if _, err := plan.Build(in); err == nil || !strings.Contains(err.Error(), target) {
		t.Errorf("Build() error = %v, want one naming %s", err, target)
	}
	delete(in.Content, target)
	if _, err := plan.Build(in); err == nil || !strings.Contains(err.Error(), target) {
		t.Errorf("Build() without the content error = %v, want one naming %s", err, target)
	}
	// Nested deeper than encoding/json reads: an error, never a panic.
	deep := `{"theme": ` + strings.Repeat("[", 20000) + strings.Repeat("]", 20000) + `}`
	for _, owned := range [][]plan.Key{nil, {key(`"opus"`, "model")}} {
		in, target = settingsInput(t, `{"model": "opus"}`, deep, owned)
		if _, err := plan.Build(in); err == nil || !strings.Contains(err.Error(), target) || !strings.Contains(err.Error(), "nested") {
			t.Errorf("Build() of a deeply nested file (owned %v) error = %v, want one naming %s", owned != nil, err, target)
		}
	}
	in, _ = settingsInput(t, deep, "", nil)
	if _, err := plan.Build(in); err == nil || !strings.Contains(err.Error(), ".claude/settings.json") {
		t.Errorf("Build() of a deeply nested staged file error = %v, want one naming it", err)
	}
}

// TestEditedErrorWordingForKeys checks the message for edited keys, which
// says how to resolve them.
func TestEditedErrorWordingForKeys(t *testing.T) {
	one := (&plan.EditedError{Keys: map[string][]string{"/h/s.json": {"model"}}}).Error()
	if want := "the key model in /h/s.json was changed after dotai installed it, and dotai does not overwrite it: " +
		"put back the value dotai installed, or delete the key to take the pack's, then install again"; one != want {
		t.Errorf("Error() = %q, want %q", one, want)
	}
	both := (&plan.EditedError{Targets: []string{"/h/a"}, Keys: map[string][]string{"/h/s.json": {"env.A", "model"}}}).Error()
	if !strings.Contains(both, "the installed file /h/a was changed") ||
		!strings.Contains(both, "; the keys env.A, model in /h/s.json were changed after dotai installed them, and dotai does not overwrite them: "+
			"put back the values dotai installed, or delete the keys to take the pack's, then install again") {
		t.Errorf("Error() = %q, want the file and then the keys", both)
	}
}
