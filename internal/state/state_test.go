package state_test

import (
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/lartweib/dotai/internal/plan"
	"github.com/lartweib/dotai/internal/state"
)

var at = time.Date(2026, 10, 29, 10, 15, 0, 0, time.FixedZone("ART", -3*3600))

func sample(t *testing.T) (state.State, []plan.Op) {
	t.Helper()
	home := t.TempDir()
	target := func(rel string) string { return filepath.Join(home, filepath.FromSlash(rel)) }
	ops := []plan.Op{
		{Action: plan.Create, Harness: "claudecode", Source: ".claude/CLAUDE.md", Target: target(".dotai/isolated/claude/CLAUDE.md"), After: "a"},
		{Action: plan.Unchanged, Harness: "claudecode", Source: ".claude/settings.json", Target: target(".dotai/isolated/claude/settings.json"), Before: "s", After: "s"},
		{Action: plan.Replace, Source: ".dotai/tools/hello.sh", Target: target(".dotai/tools/hello.sh"), Before: "h0", After: "h1", Exec: true},
		{Action: plan.Remove, Harness: "claudecode", Target: target(".dotai/isolated/claude/agents/old.md"), Before: "o"},
	}
	pack := state.Pack{Name: "p", Version: "1.0.0", Path: filepath.Join(home, "pack")}
	return state.New(pack, "v", "isolated", at, []string{"claudecode"}, "20261029T131500.000Z_install", ops), ops
}

// TestNew checks that the ownership record holds every file the install
// leaves in place, written or unchanged, and not what it removed.
func TestNew(t *testing.T) {
	s, ops := sample(t)
	want := []state.File{
		{Target: ops[0].Target, Harness: "claudecode", SHA256: "a"},
		{Target: ops[1].Target, Harness: "claudecode", SHA256: "s"},
		{Target: ops[2].Target, SHA256: "h1", Exec: true},
	}
	if s.Schema != state.Schema || s.Mode != "isolated" || s.Variant != "v" || !s.InstalledAt.Equal(at) || s.InstalledAt.Location() != time.UTC ||
		s.Backup != "20261029T131500.000Z_install" || !reflect.DeepEqual(s.Harnesses, []string{"claudecode"}) || !reflect.DeepEqual(s.Files, want) {
		t.Errorf("New() = %+v, want files %+v", s, want)
	}
	owned := s.Owned()
	if len(owned) != 3 || owned[ops[2].Target] != (plan.Owned{Hash: "h1", Exec: true}) || owned[ops[0].Target] != (plan.Owned{Harness: "claudecode", Hash: "a"}) {
		t.Errorf("Owned() = %+v, want the three files by target", owned)
	}
}

func TestEncodeDecode(t *testing.T) {
	s, _ := sample(t)
	data, err := state.Encode(s)
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{`"schema": "dotai.state/v1"`, `"installedAt": "2026-10-29T13:15:00Z"`, `"sha256": "h1"`, `"exec": true`, `"backup": "20261029T131500.000Z_install"`} {
		if !strings.Contains(string(data), key) {
			t.Errorf("Encode() = %s, want it to contain %s", data, key)
		}
	}
	back, err := state.Decode(data)
	if err != nil || !reflect.DeepEqual(back, s) {
		t.Errorf("Decode(Encode()) = %+v, %v, want %+v", back, err, s)
	}
}

func TestDecodeRejectsABadState(t *testing.T) {
	s, _ := sample(t)
	dup := s
	dup.Files = append(dup.Files, s.Files[0])
	rel := s
	rel.Files = []state.File{{Target: "relative/x", SHA256: "x"}}
	for name, tt := range map[string]struct {
		data string
		want string
	}{
		"not json":         {"{", "state"},
		"another schema":   {`{"schema": "dotai.state/v2"}`, `schema "dotai.state/v2"`},
		"no schema":        {`{}`, `schema ""`},
		"duplicate target": {encode(t, dup), "twice"},
		"relative target":  {encode(t, rel), "not absolute"},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := state.Decode([]byte(tt.data)); err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Errorf("Decode() error = %v, want it to contain %q", err, tt.want)
			}
		})
	}
}

func encode(t *testing.T, s state.State) string {
	t.Helper()
	data, err := state.Encode(s)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestSameInstall(t *testing.T) {
	s, _ := sample(t)
	later := s
	later.InstalledAt, later.Backup = at.Add(time.Hour), ""
	if !s.SameInstall(later) {
		t.Error("SameInstall() = false for the same install at another time and with another backup")
	}
	other := s
	other.Files = append([]state.File{}, s.Files...)
	other.Files[0].SHA256 = "b"
	if s.SameInstall(other) {
		t.Error("SameInstall() = true for another file hash")
	}
	version := s
	version.Pack.Version = "1.0.1"
	if s.SameInstall(version) {
		t.Error("SameInstall() = true for another pack version")
	}
}
