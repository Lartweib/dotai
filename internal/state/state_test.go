package state_test

import (
	"encoding/json"
	"errors"
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
	if len(owned) != 3 || !reflect.DeepEqual(owned[ops[2].Target], plan.Owned{Hash: "h1", Exec: true}) || !reflect.DeepEqual(owned[ops[0].Target], plan.Owned{Harness: "claudecode", Hash: "a"}) {
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
	wipe := s
	wipe.Pending = &state.Pending{Backup: "b", Ops: []state.PendingOp{}, Wipes: []string{"relative/x"}}
	split := s
	split.Pending = &state.Pending{Backup: "b", Ops: []state.PendingOp{}, Splits: []string{".claude.json"}}
	for name, tt := range map[string]struct {
		data string
		want string
	}{
		"not json":         {"{", "state"},
		"another schema":   {`{"schema": "dotai.state/v2"}`, `schema "dotai.state/v2"`},
		"no schema":        {`{}`, `schema ""`},
		"duplicate target": {encode(t, dup), "twice"},
		"relative target":  {encode(t, rel), "not absolute"},
		"relative wipe":    {encode(t, wipe), "not absolute"},
		"relative split":   {encode(t, split), "not absolute"},
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

// TestJournal checks the record an install writes before its first write:
// every operation it is about to carry out, and how the plan then reads
// the targets it was writing.
func TestJournal(t *testing.T) {
	s, ops := sample(t)
	base := state.State{Schema: state.Schema, Harnesses: []string{}, Files: []state.File{{Target: ops[1].Target, Harness: "claudecode", SHA256: "s"}, {Target: ops[2].Target, SHA256: "h0", Exec: true}, {Target: ops[3].Target, Harness: "claudecode", SHA256: "o"}}}
	j := state.Journal(base, s.Backup, ops)
	want := &state.Pending{Backup: s.Backup, Ops: []state.PendingOp{
		{Action: plan.Create, Target: ops[0].Target, Harness: "claudecode", After: &state.Content{SHA256: "a"}},
		{Action: plan.Replace, Target: ops[2].Target, Before: &state.Content{SHA256: "h0"}, After: &state.Content{SHA256: "h1", Exec: true}},
		{Action: plan.Remove, Target: ops[3].Target, Harness: "claudecode", Before: &state.Content{SHA256: "o"}},
	}}
	if !reflect.DeepEqual(j.Pending, want) || !reflect.DeepEqual(j.Files, base.Files) || base.Pending != nil {
		t.Fatalf("Journal() = %+v, want %+v over the base files", j.Pending, want)
	}
	data, err := state.Encode(j)
	if err != nil {
		t.Fatal(err)
	}
	if back, err := state.Decode(data); err != nil || !reflect.DeepEqual(back, j) {
		t.Errorf("Decode(Encode()) = %+v, %v, want %+v", back, err, j)
	}
	if strings.Contains(encode(t, s), "pending") {
		t.Errorf("Encode() of a finished install = %s, want no pending", encode(t, s))
	}

	// A file dotai did not own and was replacing: still the file it found.
	j.Files = j.Files[:1]
	j.Pending.Ops[1].Before = &state.Content{SHA256: "f0"}
	owned := j.Owned()
	wantOwned := map[string]plan.Owned{
		ops[0].Target: {Harness: "claudecode", Hash: "a"},
		ops[1].Target: {Harness: "claudecode", Hash: "s"},
		ops[2].Target: {Hash: "h1", Exec: true, Foreign: []plan.Current{{Hash: "f0"}}},
		ops[3].Target: {Harness: "claudecode", Hash: "o"},
	}
	if !reflect.DeepEqual(owned, wantOwned) {
		t.Errorf("Owned() = %+v, want %+v", owned, wantOwned)
	}
	j.Files = base.Files
	if got := j.Owned()[ops[2].Target]; !reflect.DeepEqual(got, plan.Owned{Hash: "h0", Exec: true, Also: []plan.Current{{Hash: "h1", Exec: true}}}) {
		t.Errorf("Owned() of a replaced owned file = %+v, want its old and new content", got)
	}
}

// TestSettingsKeys checks the record of a settings file, whose keys dotai
// owns (decision 0035): the keys it owns once an install writes it, none
// for a file it releases, the keys the journal records, and how the plan
// reads them.
func TestSettingsKeys(t *testing.T) {
	home := t.TempDir()
	settings := filepath.Join(home, ".dotai", "isolated", "claude", "settings.json")
	released := filepath.Join(home, ".dotai", "isolated", "gemini", "antigravity-cli", "settings.json")
	k0 := []plan.Key{{Path: []string{"model"}, SHA256: "sonnet"}}
	k1 := []plan.Key{{Path: []string{"model"}, SHA256: "opus"}, {Path: []string{"env", "DOTAI_TERMINAL"}, SHA256: "tmux"}}
	ops := []plan.Op{
		{Action: plan.Replace, Harness: "claudecode", Source: ".claude/settings.json", Target: settings, Before: "s0", After: "s1", Settings: true, Keys: k1},
		{Action: plan.Replace, Harness: "antigravity-cli", Target: released, Before: "r0", After: "r1", Settings: true},
	}
	s := state.New(state.Pack{Name: "p"}, "tmux", "isolated", at, []string{"claudecode"}, "b1", ops)
	if want := []state.File{{Target: settings, Harness: "claudecode", SHA256: "s1", Keys: k1}}; !reflect.DeepEqual(s.Files, want) {
		t.Errorf("New() files = %+v, want %+v", s.Files, want)
	}
	if got := s.Owned()[settings]; !reflect.DeepEqual(got, plan.Owned{Harness: "claudecode", Hash: "s1", Keys: k1}) {
		t.Errorf("Owned() = %+v, want the keys", got)
	}
	data, err := state.Encode(s)
	if err != nil || !strings.Contains(string(data), `"keys": [`) || !strings.Contains(string(data), `"path": [`) {
		t.Errorf("Encode() = %s, %v, want the keys with their paths", data, err)
	}
	if back, err := state.Decode(data); err != nil || !reflect.DeepEqual(back, s) {
		t.Errorf("Decode(Encode()) = %+v, %v, want %+v", back, err, s)
	}

	base := state.State{Schema: state.Schema, Harnesses: []string{}, Files: []state.File{
		{Target: released, Harness: "antigravity-cli", SHA256: "r0", Keys: k0},
		{Target: settings, Harness: "claudecode", SHA256: "s0", Keys: k0},
	}}
	j := state.Journal(base, "b2", ops)
	if got := j.Pending.Ops[0]; !reflect.DeepEqual(got.Keys, k1) {
		t.Errorf("Journal() op = %+v, want the keys after", got)
	}
	if got := j.Owned()[settings]; !reflect.DeepEqual(got, plan.Owned{Harness: "claudecode", Hash: "s0", Keys: k0, Also: []plan.Current{{Hash: "s1"}}, AlsoKeys: k1}) {
		t.Errorf("Owned() of a settings file being written = %+v, want the keys before and after", got)
	}
	j.Files = nil
	if got := j.Owned()[settings]; !reflect.DeepEqual(got, plan.Owned{Harness: "claudecode", Hash: "s1", Keys: k1, Foreign: []plan.Current{{Hash: "s0"}}}) {
		t.Errorf("Owned() of a settings file dotai did not own = %+v, want the keys it was writing", got)
	}

	settled := state.Journal(base, "b2", ops).Settled(
		map[string]plan.Current{settings: {Hash: "s9"}},
		map[string][]plan.Key{settings: k1},
	)
	if want := []state.File{{Target: settings, Harness: "claudecode", SHA256: "s9", Keys: k1}}; !reflect.DeepEqual(settled.Files, want) {
		t.Errorf("Settled() files = %+v, want %+v", settled.Files, want)
	}

	bad := s
	bad.Files = []state.File{{Target: settings, SHA256: "x", Keys: []plan.Key{{SHA256: "y"}}}}
	if _, err := state.Decode([]byte(encode(t, bad))); err == nil || !strings.Contains(err.Error(), "key") {
		t.Errorf("Decode() of a key without a path error = %v, want one", err)
	}
	bad = state.Journal(base, "b2", ops)
	bad.Pending.Ops[0].Keys = []plan.Key{{Path: []string{}, SHA256: "y"}}
	if _, err := state.Decode([]byte(encode(t, bad))); err == nil || !strings.Contains(err.Error(), "key") || !strings.Contains(err.Error(), "pending") {
		t.Errorf("Decode() of a pending key without a path error = %v, want one", err)
	}
}

func TestSettled(t *testing.T) {
	s, ops := sample(t)
	j := state.Journal(s, "b2", ops)
	got := j.Settled(map[string]plan.Current{ops[0].Target: {Hash: "x"}, ops[2].Target: {Hash: "h1", Exec: true}}, nil)
	want := []state.File{{Target: ops[0].Target, Harness: "claudecode", SHA256: "x"}, {Target: ops[2].Target, SHA256: "h1", Exec: true}}
	if got.Pending != nil || !reflect.DeepEqual(got.Files, want) || got.Backup != s.Backup {
		t.Errorf("Settled() = %+v, want files %+v and no pending", got, want)
	}
}

// TestLinks checks the record of a link to a shared skill (decision 0036):
// a file of kind link with its destination and no hash, whose content the
// plan reads as plan.LinkHash of the destination, in the files, the
// journal and once the journal is settled.
func TestLinks(t *testing.T) {
	home := t.TempDir()
	target := func(rel string) string { return filepath.Join(home, filepath.FromSlash(rel)) }
	dest, old := target(".agents/skills/a"), target(".agents/skills/b")
	created, removed := target(".dotai/isolated/claude/skills/a"), target(".dotai/isolated/claude/skills/b")
	ops := []plan.Op{
		{Action: plan.Create, Harness: "claudecode", Source: ".claude/skills/a", Target: created, After: plan.LinkHash(dest), Link: dest},
		{Action: plan.Remove, Harness: "claudecode", Target: removed, Before: plan.LinkHash(old), BeforeLink: old},
	}
	s := state.New(state.Pack{Name: "p"}, "shared", "isolated", at, []string{"claudecode"}, "b1", ops)
	s.SharedSkills = true
	if want := []state.File{{Target: created, Harness: "claudecode", Kind: state.KindLink, Link: dest}}; !reflect.DeepEqual(s.Files, want) {
		t.Errorf("New() files = %+v, want %+v", s.Files, want)
	}
	data := encode(t, s)
	for _, key := range []string{`"kind": "link"`, `"link": ` + jsonString(t, dest), `"sharedSkills": true`} {
		if !strings.Contains(data, key) {
			t.Errorf("Encode() = %s, want it to contain %s", data, key)
		}
	}
	if strings.Contains(data, `"sha256"`) {
		t.Errorf("Encode() = %s, want no hash for a link", data)
	}
	if back, err := state.Decode([]byte(data)); err != nil || !reflect.DeepEqual(back, s) {
		t.Errorf("Decode(Encode()) = %+v, %v, want %+v", back, err, s)
	}
	if got := s.Owned()[created]; !reflect.DeepEqual(got, plan.Owned{Harness: "claudecode", Hash: plan.LinkHash(dest)}) {
		t.Errorf("Owned() = %+v, want the link's hash", got)
	}

	base := state.State{Schema: state.Schema, Harnesses: []string{}, Files: []state.File{{Target: removed, Harness: "claudecode", Kind: state.KindLink, Link: old}}}
	j := state.Journal(base, "b2", ops)
	want := []state.PendingOp{
		{Action: plan.Create, Target: created, Harness: "claudecode", After: &state.Content{Link: dest}},
		{Action: plan.Remove, Target: removed, Harness: "claudecode", Before: &state.Content{Link: old}},
	}
	if !reflect.DeepEqual(j.Pending.Ops, want) {
		t.Errorf("Journal() = %+v, want %+v", j.Pending.Ops, want)
	}
	if back, err := state.Decode([]byte(encode(t, j))); err != nil || !reflect.DeepEqual(back, j) {
		t.Errorf("Decode(Encode()) of the journal = %+v, %v, want %+v", back, err, j)
	}
	owned := j.Owned()
	if got := owned[created]; got.Hash != plan.LinkHash(dest) {
		t.Errorf("Owned() of a link being created = %+v, want its hash", got)
	}
	if got := owned[removed]; got.Hash != plan.LinkHash(old) {
		t.Errorf("Owned() of a link being removed = %+v, want its hash", got)
	}
	settled := j.Settled(map[string]plan.Current{created: plan.LinkCurrent(dest)}, nil)
	if want := []state.File{{Target: created, Harness: "claudecode", Kind: state.KindLink, Link: dest}}; !reflect.DeepEqual(settled.Files, want) {
		t.Errorf("Settled() = %+v, want %+v", settled.Files, want)
	}
	if c := (state.Content{Link: dest}).Current(); c != plan.LinkCurrent(dest) {
		t.Errorf("Current() = %+v, want the link", c)
	}
	if c := (state.Content{SHA256: "h", Exec: true}).Current(); c != (plan.Current{Hash: "h", Exec: true}) {
		t.Errorf("Current() = %+v, want the file", c)
	}
}

func TestDecodeRejectsABadLink(t *testing.T) {
	home := t.TempDir()
	target := filepath.Join(home, ".dotai", "isolated", "claude", "skills", "a")
	for name, f := range map[string]state.File{
		"relative destination": {Target: target, Kind: state.KindLink, Link: "a"},
		"no destination":       {Target: target, Kind: state.KindLink},
		"a hash":               {Target: target, Kind: state.KindLink, Link: filepath.Join(home, "a"), SHA256: "h"},
		"a destination only":   {Target: target, Link: filepath.Join(home, "a"), SHA256: "h"},
		"an unknown kind":      {Target: target, Kind: "pipe", SHA256: "h"},
	} {
		t.Run(name, func(t *testing.T) {
			s := state.State{Schema: state.Schema, Files: []state.File{f}}
			if _, err := state.Decode([]byte(encode(t, s))); err == nil {
				t.Errorf("Decode() of %+v = nil error, want one", f)
			}
		})
	}
	bad := state.State{Schema: state.Schema, Files: []state.File{}, Pending: &state.Pending{Ops: []state.PendingOp{
		{Action: plan.Create, Target: target, After: &state.Content{Link: "a"}},
	}}}
	if _, err := state.Decode([]byte(encode(t, bad))); err == nil {
		t.Error("Decode() of a pending link that is not absolute = nil error, want one")
	}
}

func jsonString(t *testing.T, s string) string {
	t.Helper()
	data, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// TestCheckLinks checks that a state whose link points at anything but the
// shared skill of its name, owned or in the journal, is refused.
func TestCheckLinks(t *testing.T) {
	home := t.TempDir()
	target := filepath.Join(home, ".dotai", "isolated", "claude", "skills", "a")
	good, bad := filepath.Join(home, ".agents", "skills", "a"), filepath.Join(home, ".ssh")
	ok := state.State{Files: []state.File{{Target: target, Kind: state.KindLink, Link: good}},
		Pending: &state.Pending{Ops: []state.PendingOp{{Action: plan.Create, Target: target, After: &state.Content{Link: good}}}}}
	if err := ok.CheckLinks(home, false); err != nil {
		t.Errorf("CheckLinks() = %v, want the shared skill accepted", err)
	}
	owned := state.State{Files: []state.File{{Target: target, Kind: state.KindLink, Link: bad}}}
	pending := state.State{Pending: &state.Pending{Ops: []state.PendingOp{{Action: plan.Remove, Target: target, Before: &state.Content{Link: bad}}}}}
	for name, s := range map[string]state.State{"owned": owned, "pending": pending} {
		var e *plan.LinkDestError
		if err := s.CheckLinks(home, false); !errors.As(err, &e) || e.Dest != bad {
			t.Errorf("CheckLinks() of a link %s elsewhere = %v, want it refused", name, err)
		}
	}
}
