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

// TestNewRecordsLinks checks that a link (decision 0036) is recorded with
// its destination and has no copy, even when it is replaced or removed.
func TestNewRecordsLinks(t *testing.T) {
	home := filepath.Join(t.TempDir(), "home")
	target := func(rel string) string { return filepath.Join(home, filepath.FromSlash(rel)) }
	a, b, c := target(".agents/skills/a"), target(".agents/skills/b"), target(".agents/skills/c")
	ops := []plan.Op{
		{Action: plan.Create, Harness: "claudecode", Source: ".claude/skills/a", Target: target(".dotai/isolated/claude/skills/a"), After: plan.LinkHash(a), Link: a},
		{Action: plan.Replace, Harness: "claudecode", Source: ".claude/skills/b", Target: target(".dotai/isolated/claude/skills/b"),
			Before: plan.LinkHash(b), BeforeLink: b, After: plan.LinkHash(b), Link: b},
		{Action: plan.Remove, Harness: "claudecode", Target: target(".dotai/isolated/claude/skills/c"), Before: plan.LinkHash(c), BeforeLink: c},
	}
	m, err := backup.New("20261029T131500.123Z_install", backup.OperationInstall, at, home, backup.Info{}, ops)
	if err != nil {
		t.Fatal(err)
	}
	want := []backup.Op{
		{Action: plan.Create, Target: ops[0].Target, After: &backup.Content{Link: a}},
		{Action: plan.Replace, Target: ops[1].Target, Before: &backup.Content{Link: b}, After: &backup.Content{Link: b}},
		{Action: plan.Remove, Target: ops[2].Target, Before: &backup.Content{Link: c}},
	}
	if !reflect.DeepEqual(m.Ops, want) {
		t.Errorf("New() ops = %+v, want %+v", m.Ops, want)
	}
	m.PriorState = &backup.PriorState{None: true}
	if err := backup.Check(m, m.ID, home); err != nil {
		t.Errorf("Check() = %v, want the links valid", err)
	}
	if c := want[0].After.Current(); c != plan.LinkCurrent(a) {
		t.Errorf("Current() = %+v, want the link", c)
	}
	if c := (backup.Content{SHA256: "h", Exec: true}).Current(); c != (plan.Current{Hash: "h", Exec: true}) {
		t.Errorf("Current() = %+v, want the file", c)
	}
	for name, change := range map[string]func(*backup.Manifest){
		"a link with a copy":            func(m *backup.Manifest) { m.Ops[2].File = "files/.dotai/isolated/claude/skills/c" },
		"a link that is not absolute":   func(m *backup.Manifest) { m.Ops[0].After.Link = "a" },
		"a link with a hash":            func(m *backup.Manifest) { m.Ops[0].After.SHA256 = "h" },
		"a link to another skill":       func(m *backup.Manifest) { m.Ops[0].After.Link = b },
		"a link out of the folder":      func(m *backup.Manifest) { m.Ops[2].Before.Link = filepath.Join(home, ".ssh") },
		"a file without a hash or link": func(m *backup.Manifest) { m.Ops[0].After = &backup.Content{} },
	} {
		t.Run(name, func(t *testing.T) {
			bad, err := backup.Decode(mustEncode(t, m))
			if err != nil {
				t.Fatal(err)
			}
			change(&bad)
			if err := backup.Check(bad, m.ID, home); err == nil {
				t.Errorf("Check() = nil, want an error")
			}
		})
	}
}

func mustEncode(t *testing.T, m backup.Manifest) []byte {
	t.Helper()
	data, err := backup.Encode(m)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

// TestCheckWipesAndSplits checks the entries a replace install moves into
// its backup, and the split files it rewrites by key (decision 0038): a
// valid manifest passes, and each way an edited one can point elsewhere is
// refused.
func TestCheckWipesAndSplits(t *testing.T) {
	home := filepath.Join(t.TempDir(), "home")
	id, restoreID := "20261029T131500.123Z_install", "20261029T131600.000Z_restore"
	agents := filepath.Join(home, ".claude", "agents")
	valid := func() backup.Manifest {
		return backup.Manifest{Schema: backup.Schema, ID: id, Operation: backup.OperationInstall, Home: home,
			PriorState: &backup.PriorState{None: true}, Ops: []backup.Op{
				{Action: plan.Create, Target: filepath.Join(agents, "pack.md"), After: &backup.Content{SHA256: "p"}},
			},
			Wipes: []backup.Wipe{
				{Harness: "claudecode", Path: agents, Kind: backup.KindDir, File: "files/.claude/agents", Items: []backup.Item{
					{Path: "", Kind: backup.KindDir}, {Path: "a.md", Kind: backup.KindFile, SHA256: "a"},
					{Path: "sub", Kind: backup.KindDir}, {Path: "sub/b.md", Kind: backup.KindFile, SHA256: "b", Exec: true},
					{Path: "sub/l", Kind: backup.KindLink, Link: "/elsewhere"},
				}},
				{Harness: "claudecode", Path: filepath.Join(home, "CLAUDE.md"), Kind: backup.KindLink, File: "files/CLAUDE.md",
					Items: []backup.Item{{Kind: backup.KindLink}}},
			},
			Splits: []backup.Split{{Harness: "claudecode", Target: filepath.Join(home, ".claude.json"), File: "files/.claude.json",
				Before: backup.Content{SHA256: "b"}, After: backup.Content{SHA256: "a"}, Keys: [][]string{{"mcpServers"}}}},
		}
	}
	if err := backup.Check(valid(), id, home); err != nil {
		t.Fatalf("Check() = %v, want a valid manifest", err)
	}
	r := valid()
	r.ID, r.Operation, r.Splits = restoreID, backup.OperationRestore, nil
	r.Wipes = r.Wipes[1:]
	r.Wipes[0].File = "moved/" + id + "/CLAUDE.md"
	if err := backup.Check(r, restoreID, home); err != nil {
		t.Fatalf("Check() of a restore = %v, want a valid manifest", err)
	}
	// A restore that puts back a split file that is missing has no copy.
	r.Splits = []backup.Split{{Harness: "claudecode", Target: filepath.Join(home, ".claude.json"), After: backup.Content{SHA256: "a"}, Keys: [][]string{{"mcpServers"}}}}
	if err := backup.Check(r, restoreID, home); err != nil {
		t.Fatalf("Check() of a restore putting back a missing split file = %v, want a valid manifest", err)
	}
	r.Splits[0].Before.SHA256 = "b"
	if err := backup.Check(r, restoreID, home); err == nil {
		t.Error("Check() of a split file with a before and no copy passed")
	}
	r.Splits = nil
	if got, err := backup.MovedFile(home, id, filepath.Join(home, "CLAUDE.md")); err != nil || got != r.Wipes[0].File {
		t.Errorf("MovedFile() = %q, %v, want %q", got, err, r.Wipes[0].File)
	}
	for name, tt := range map[string]struct {
		change func(*backup.Manifest)
		want   string
	}{
		"a wipe outside":         {func(m *backup.Manifest) { m.Wipes[0].Path = filepath.Join(filepath.Dir(home), "x") }, "not under the home directory"},
		"a wipe moved elsewhere": {func(m *backup.Manifest) { m.Wipes[0].File = "files/../../x" }, "moved to"},
		"a wipe inside another":  {func(m *backup.Manifest) { m.Wipes[1].Path = filepath.Join(agents, "sub") }, "overlaps"},
		"a wipe twice":           {func(m *backup.Manifest) { m.Wipes[1].Path = agents }, "overlaps"},
		"a wipe of no kind":      {func(m *backup.Manifest) { m.Wipes[0].Kind = "pipe" }, "kind"},
		"no items":               {func(m *backup.Manifest) { m.Wipes[0].Items = nil }, "items"},
		"an item outside":        {func(m *backup.Manifest) { m.Wipes[0].Items[1].Path = "../x" }, "inside"},
		"an item twice":          {func(m *backup.Manifest) { m.Wipes[0].Items[2] = m.Wipes[0].Items[1] }, "inside"},
		"an item below no dir":   {func(m *backup.Manifest) { m.Wipes[0].Items[3].Path = "a.md/b.md" }, "inside"},
		"an item in a file": {func(m *backup.Manifest) {
			m.Wipes[1].Items = append(m.Wipes[1].Items, backup.Item{Path: "x", Kind: backup.KindDir})
		}, "inside"},
		"a file without a hash": {func(m *backup.Manifest) { m.Wipes[0].Items[1].SHA256 = "" }, "SHA-256"},
		"an unclean wipe path": {func(m *backup.Manifest) {
			sep := string(filepath.Separator)
			m.Wipes[1].Path = home + sep + ".claude" + sep + ".." + sep + ".ssh" + sep + "authorized_keys"
			m.Wipes[1].File = "files/.ssh/authorized_keys"
		}, "clean"},
		"an unclean split path": {func(m *backup.Manifest) {
			sep := string(filepath.Separator)
			m.Splits[0].Target = home + sep + "x" + sep + ".." + sep + ".claude.json"
		}, "clean"},
		"a split outside":        {func(m *backup.Manifest) { m.Splits[0].Target = filepath.Join(filepath.Dir(home), "x") }, "not under the home directory"},
		"a split copy elsewhere": {func(m *backup.Manifest) { m.Splits[0].File = "files/x" }, "copy"},
		"a split without keys":   {func(m *backup.Manifest) { m.Splits[0].Keys = nil }, "keys"},
		"a split without a hash": {func(m *backup.Manifest) { m.Splits[0].After = backup.Content{} }, "SHA-256"},
		"a split that is an op too": {func(m *backup.Manifest) {
			m.Splits[0].Target = m.Ops[0].Target
			m.Splits[0].File = "files/.claude/agents/pack.md"
		}, "twice"},
		"another operation": {func(m *backup.Manifest) { m.Operation = backup.OperationRestore }, "operation"},
	} {
		t.Run(name, func(t *testing.T) {
			m := valid()
			tt.change(&m)
			if err := backup.Check(m, id, home); err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Errorf("Check() = %v, want an error about %s", err, tt.want)
			}
		})
	}
	for name, file := range map[string]string{"in files": "files/CLAUDE.md", "from a restore": "moved/" + restoreID + "/CLAUDE.md", "elsewhere": "moved/" + id + "/x"} {
		t.Run("a restore's wipe moved "+name, func(t *testing.T) {
			m := r
			m.Wipes = []backup.Wipe{r.Wipes[0]}
			m.Wipes[0].File = file
			if err := backup.Check(m, restoreID, home); err == nil || !strings.Contains(err.Error(), "moved to") {
				t.Errorf("Check() = %v, want the entry refused", err)
			}
		})
	}
	m := valid()
	m.ID, m.Operation = restoreID, backup.OperationRestore
	if err := backup.Check(m, restoreID, home); err == nil || !strings.Contains(err.Error(), "moved to") {
		t.Errorf("Check() of a restore with an install's wipes = %v, want them refused", err)
	}
}
