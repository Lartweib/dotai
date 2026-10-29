package backup_test

import (
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/lartweib/dotai/internal/backup"
	"github.com/lartweib/dotai/internal/plan"
)

var at = time.Date(2026, 10, 29, 10, 15, 0, 123456789, time.FixedZone("ART", -3*3600))

func TestID(t *testing.T) {
	if got, want := backup.ID(at, backup.OperationInstall), "20261029T131500.123Z_install"; got != want {
		t.Errorf("ID() = %q, want %q", got, want)
	}
	for _, id := range []string{"20261029T131500.123Z_install", "20261029T131500.000Z_restore"} {
		if _, ok := backup.ParseID(id); !ok {
			t.Errorf("ParseID(%q) failed", id)
		}
	}
	for _, id := range []string{"", "20261029T131500Z_install", "20261029T131500.123Z_", "20261029T131500.123Z_Install", "x20261029T131500.123Z_install", "20261329T131500.123Z_install", "notes"} {
		if _, ok := backup.ParseID(id); ok {
			t.Errorf("ParseID(%q) accepted it", id)
		}
	}
	if got, _ := backup.ParseID("20261029T131500.123Z_install"); !got.Equal(at.Truncate(time.Millisecond)) {
		t.Errorf("ParseID() = %v, want %v", got, at.Truncate(time.Millisecond))
	}
}

// TestNew checks that the manifest records every written target and only
// those, with a copy for each target that existed.
func TestNew(t *testing.T) {
	home := filepath.Join(t.TempDir(), "home")
	target := func(rel string) string { return filepath.Join(home, filepath.FromSlash(rel)) }
	ops := []plan.Op{
		{Action: plan.Create, Harness: "claudecode", Source: ".claude/CLAUDE.md", Target: target(".dotai/isolated/claude/CLAUDE.md"), After: "a1"},
		{Action: plan.Unchanged, Harness: "claudecode", Source: ".claude/settings.json", Target: target(".dotai/isolated/claude/settings.json"), Before: "s", After: "s"},
		{Action: plan.Replace, Source: ".dotai/tools/hello.sh", Target: target(".dotai/tools/hello.sh"), Before: "h0", After: "h1", Exec: true},
		{Action: plan.Remove, Harness: "claudecode", Target: target(".dotai/isolated/claude/agents/old.md"), Before: "o", BeforeExec: true},
	}
	info := backup.Info{Mode: "isolated", Pack: backup.Pack{Name: "p", Version: "1.0.0", Path: "/packs/p"}, Variant: "v", Harnesses: []string{"claudecode"}}
	m, err := backup.New("20261029T131500.123Z_install", backup.OperationInstall, at, home, info, ops)
	if err != nil {
		t.Fatal(err)
	}
	want := backup.Manifest{
		Schema: backup.Schema, ID: "20261029T131500.123Z_install", Operation: "install", CreatedAt: at.UTC(), Home: home, Info: info,
		Ops: []backup.Op{
			{Action: plan.Create, Target: target(".dotai/isolated/claude/CLAUDE.md"), After: &backup.Content{SHA256: "a1"}},
			{Action: plan.Replace, Target: target(".dotai/tools/hello.sh"), File: "files/.dotai/tools/hello.sh", Before: &backup.Content{SHA256: "h0"}, After: &backup.Content{SHA256: "h1", Exec: true}},
			{Action: plan.Remove, Target: target(".dotai/isolated/claude/agents/old.md"), File: "files/.dotai/isolated/claude/agents/old.md", Before: &backup.Content{SHA256: "o", Exec: true}},
		},
	}
	if !reflect.DeepEqual(m, want) {
		t.Errorf("New() = %+v, want %+v", m, want)
	}
	data, err := backup.Encode(m)
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range []string{`"schema": "dotai.backup/v1"`, `"createdAt": "2026-10-29T13:15:00.123456789Z"`, `"file": "files/.dotai/tools/hello.sh"`} {
		if !strings.Contains(string(data), s) {
			t.Errorf("Encode() = %s, want it to contain %s", data, s)
		}
	}
	if strings.Count(string(data), `"before"`) != 2 || strings.Count(string(data), `"after"`) != 2 || !strings.HasSuffix(string(data), "}\n") {
		t.Errorf("Encode() = %s, want before on the replace and remove, after on the create and replace, and a final newline", data)
	}
	back, err := backup.Decode(data)
	if err != nil || !reflect.DeepEqual(back, m) {
		t.Errorf("Decode(Encode()) = %+v, %v, want %+v", back, err, m)
	}
}

func TestNewRefusesATargetOutsideTheHome(t *testing.T) {
	home := filepath.Join(t.TempDir(), "home")
	ops := []plan.Op{{Action: plan.Create, Target: filepath.Join(filepath.Dir(home), "x"), After: "a"}}
	if _, err := backup.New("id", backup.OperationInstall, at, home, backup.Info{}, ops); err == nil || !strings.Contains(err.Error(), "not under the home directory") {
		t.Errorf("New() error = %v, want a target outside the home directory", err)
	}
}

func TestDecodeRefusesAnotherSchema(t *testing.T) {
	for _, data := range []string{`{"schema": "dotai.backup/v2"}`, `{}`, `not json`} {
		if _, err := backup.Decode([]byte(data)); err == nil {
			t.Errorf("Decode(%s) error = nil, want one", data)
		}
	}
}

// entries are complete backups taken a minute apart from minute from to
// minute to.
func entries(from, to int) []backup.Entry {
	var out []backup.Entry
	for i := from; i <= to; i++ {
		t := at.Add(time.Duration(i) * time.Minute)
		out = append(out, backup.Entry{ID: backup.ID(t, backup.OperationInstall), CreatedAt: t, Complete: true})
	}
	return out
}

func ids(es []backup.Entry) []string {
	var out []string
	for _, e := range es {
		out = append(out, e.ID)
	}
	return out
}

// TestPrune checks retention: the last keep complete backups stay, and so
// does the oldest, which holds what was there before the first install,
// ordered by the time each manifest records. Incomplete backups go, except
// a protected one, and protected backups always stay.
func TestPrune(t *testing.T) {
	all := entries(1, 13)
	incomplete := backup.Entry{ID: backup.ID(at, backup.OperationInstall)}
	tests := []struct {
		name    string
		entries []backup.Entry
		protect []string
		want    []string
	}{
		{name: "none"},
		{name: "fewer than keep", entries: entries(1, 5)},
		{name: "keep plus the first", entries: entries(1, 11)},
		{name: "two too many", entries: all, want: ids(all[1:3])},
		{name: "unsorted", entries: append(entries(7, 13), entries(1, 6)...), want: ids(all[1:3])},
		{name: "protected", entries: all, protect: []string{all[1].ID}, want: ids(all[2:4])},
		{name: "incomplete", entries: append(entries(1, 3), incomplete), want: []string{incomplete.ID}},
		{name: "incomplete and protected", entries: append(entries(1, 3), incomplete), protect: []string{incomplete.ID}},
		{name: "incomplete does not count", entries: append(entries(1, 11), incomplete), want: []string{incomplete.ID}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := backup.Prune(tt.entries, 10, tt.protect...); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("Prune() = %v, want %v", got, tt.want)
			}
		})
	}
}

// TestPruneSparesTheHistory checks that the backups the install state
// leads to are pruned last: restored installs and restores go first, oldest
// first, and of the history only what is beyond its newest keep backups.
func TestPruneSparesTheHistory(t *testing.T) {
	es := entries(1, 12) // eight installs, then four restores of the last four
	for i := range es {
		es[i].Chain = i < 4
		if i >= 8 {
			es[i].ID = strings.Replace(es[i].ID, "_install", "_restore", 1)
		}
	}
	if got := backup.Prune(es, 10); !reflect.DeepEqual(got, []string{es[4].ID}) {
		t.Errorf("Prune() = %v, want only the oldest restored install %s", got, es[4].ID)
	}
	if got := backup.Prune(es, 2); !reflect.DeepEqual(got, sorted(append(ids(es[4:]), es[1].ID))) {
		t.Errorf("Prune(keep 2) = %v, want every spent backup and the history beyond its newest two, %v", got, sorted(append(ids(es[4:]), es[1].ID)))
	}
	chain := entries(1, 13)
	for i := range chain {
		chain[i].Chain = true
	}
	if got := backup.Prune(chain, 10); !reflect.DeepEqual(got, ids(chain[1:3])) {
		t.Errorf("Prune() of a long history = %v, want %v", got, ids(chain[1:3]))
	}
}

func sorted(s []string) []string {
	slices.Sort(s)
	return s
}

// TestPruneWithAClockThatWentBack checks that the order is the recorded
// time, and that the backup just taken is never pruned even when the clock
// put it before every other one.
func TestPruneWithAClockThatWentBack(t *testing.T) {
	all := entries(1, 12)
	back := backup.Entry{ID: backup.ID(at, backup.OperationInstall), CreatedAt: at, Complete: true} // the newest, taken after the clock went back
	got := backup.Prune(append(all, back), 10, back.ID)
	// back is now the oldest, so it is the protected first one, and the
	// one recorded as taken first is pruned with the next ones.
	if want := ids(all[:2]); !reflect.DeepEqual(got, want) {
		t.Errorf("Prune() = %v, want %v", got, want)
	}
	// The same time on two backups orders them by id.
	same := []backup.Entry{{ID: "20261029T131500.000Z_install", CreatedAt: at, Complete: true}, {ID: "20261029T131500.001Z_install", CreatedAt: at, Complete: true}}
	if got := backup.Prune(same, 0); !reflect.DeepEqual(got, []string{"20261029T131500.001Z_install"}) {
		t.Errorf("Prune() = %v, want the second id pruned", got)
	}
}

func TestOperation(t *testing.T) {
	for id, want := range map[string]string{"20261029T131500.123Z_install": "install", "20261029T131500.123Z_restore": "restore", "notes": ""} {
		if got, ok := backup.Operation(id); got != want || ok != (want != "") {
			t.Errorf("Operation(%q) = %q, %v, want %q", id, got, ok, want)
		}
	}
}

// TestManifestRecordsThePriorStateAndTheRestore checks that the state
// before the operation and the restore that undid it round-trip, and that
// a manifest without them, as an older dotai wrote, still decodes.
func TestManifestRecordsThePriorStateAndTheRestore(t *testing.T) {
	m, err := backup.New("20261029T131500.123Z_install", backup.OperationInstall, at, "/h", backup.Info{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	old, err := backup.Encode(m)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(old), "priorState") || strings.Contains(string(old), "restored") {
		t.Errorf("Encode() = %s, want neither field when unset", old)
	}
	if back, err := backup.Decode(old); err != nil || back.PriorState != nil || back.Restored != nil {
		t.Errorf("Decode() = %+v, %v, want no prior state and no restore", back, err)
	}
	for _, prior := range []backup.PriorState{{None: true}, {File: backup.StateName, SHA256: "5e"}} {
		m.PriorState = &prior
		m.Restored = &backup.Restored{At: at.UTC(), Backup: "20261029T131600.000Z_restore"}
		data, err := backup.Encode(m)
		if err != nil {
			t.Fatal(err)
		}
		if back, err := backup.Decode(data); err != nil || !reflect.DeepEqual(back, m) {
			t.Errorf("Decode(Encode()) = %+v, %v, want %+v", back, err, m)
		}
	}
}

// TestCheck checks that a manifest is refused unless every op is one an
// install writes: the action's contents, the copy at the path of its
// target, a target under the home directory and named once, and the id and
// home directory it is read for.
func TestCheck(t *testing.T) {
	home := filepath.Join(t.TempDir(), "home")
	target := filepath.Join(home, ".dotai", "tools", "a.sh")
	id := "20261029T131500.123Z_install"
	valid := func() backup.Manifest {
		return backup.Manifest{Schema: backup.Schema, ID: id, Operation: backup.OperationInstall, Home: home, PriorState: &backup.PriorState{None: true}, Ops: []backup.Op{
			{Action: plan.Create, Target: filepath.Join(home, ".dotai", "tools", "b.sh"), After: &backup.Content{SHA256: "b"}},
			{Action: plan.Replace, Target: target, File: "files/.dotai/tools/a.sh", Before: &backup.Content{SHA256: "a0"}, After: &backup.Content{SHA256: "a1"}},
		}}
	}
	if err := backup.Check(valid(), id, home); err != nil {
		t.Fatalf("Check() = %v, want a valid manifest", err)
	}
	for name, tt := range map[string]struct {
		change func(*backup.Manifest)
		want   string
	}{
		"another id":               {func(m *backup.Manifest) { m.ID = "20261029T131500.124Z_install" }, "id"},
		"another home":             {func(m *backup.Manifest) { m.Home = filepath.Dir(home) }, "home directory"},
		"another operation":        {func(m *backup.Manifest) { m.Operation = "restore" }, "operation"},
		"an unknown action":        {func(m *backup.Manifest) { m.Ops[0].Action = "unchanged" }, "action"},
		"a create with a copy":     {func(m *backup.Manifest) { m.Ops[0].File = "files/x" }, "create"},
		"a replace without before": {func(m *backup.Manifest) { m.Ops[1].Before = nil }, "replace"},
		"a remove with after":      {func(m *backup.Manifest) { m.Ops[1].Action = plan.Remove }, "remove"},
		"a copy elsewhere":         {func(m *backup.Manifest) { m.Ops[1].File = "files/../../../.ssh/id_rsa" }, "copy"},
		"a target outside":         {func(m *backup.Manifest) { m.Ops[0].Target = filepath.Join(filepath.Dir(home), "x") }, "not under the home directory"},
		"a relative target":        {func(m *backup.Manifest) { m.Ops[0].Target = "x" }, "not under the home directory"},
		"a target twice":           {func(m *backup.Manifest) { m.Ops[0].Target = target }, "twice"},
		"a prior state elsewhere":  {func(m *backup.Manifest) { m.PriorState = &backup.PriorState{File: "../state.json", SHA256: "s"} }, "state"},
		"a prior state and none":   {func(m *backup.Manifest) { m.PriorState = &backup.PriorState{None: true, File: backup.StateName} }, "state"},
	} {
		t.Run(name, func(t *testing.T) {
			m := valid()
			tt.change(&m)
			if err := backup.Check(m, id, home); err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Errorf("Check() = %v, want an error about %s", err, tt.want)
			}
		})
	}
}
