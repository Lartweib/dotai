package backup_test

import (
	"path/filepath"
	"reflect"
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
		{name: "protected", entries: all, protect: []string{all[1].ID}, want: ids(all[2:3])},
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
