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

// TestPrune checks retention: the last keep backups stay, and so does the
// oldest, which holds what was there before the first install. Names that
// are not backups are never pruned.
func TestPrune(t *testing.T) {
	ids := func(from, to int) []string {
		var out []string
		for i := from; i <= to; i++ {
			out = append(out, backup.ID(at.Add(time.Duration(i)*time.Minute), backup.OperationInstall))
		}
		return out
	}
	tests := []struct {
		name  string
		names []string
		want  []string
	}{
		{name: "none", names: nil, want: nil},
		{name: "fewer than keep", names: ids(1, 5), want: nil},
		{name: "keep plus the first", names: ids(1, 11), want: nil},
		{name: "two too many", names: ids(1, 13), want: ids(2, 3)},
		{name: "unsorted with strangers", names: append([]string{"notes", ".tmp"}, append(ids(7, 13), ids(1, 6)...)...), want: ids(2, 3)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := backup.Prune(tt.names, 10); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("Prune() = %v, want %v", got, tt.want)
			}
		})
	}
}
