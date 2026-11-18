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
	"github.com/lartweib/dotai/internal/plan"
	"github.com/lartweib/dotai/internal/state"
)

// recordOf reads the record of project under home.
func recordOf(t *testing.T, home, project string) state.Project {
	t.Helper()
	return readJSON(t, app.RecordPath(home, project), func(data []byte) (state.Project, error) { return state.DecodeProject(data, project) })
}

// writeRecord writes r as the record of its project under home.
func writeRecord(t *testing.T, home string, r state.Project) {
	t.Helper()
	data, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, app.RecordPath(home, r.Project), string(data))
}

// TestATamperedRecordIsRefusedBeforeAnyWrite checks that a record that
// names what an apply of the project never writes, a target outside it, a
// backup outside its backups, a journal written through another directory
// or the keys of another home file, is refused by apply and by restore
// --project before anything is written, the home directory and the project
// left byte for byte.
func TestATamperedRecordIsRefusedBeforeAnyWrite(t *testing.T) {
	for _, tt := range []struct {
		name   string
		tamper func(r *state.Project, project, home string)
	}{
		{"target outside the project", func(r *state.Project, _, home string) {
			r.Files = append(r.Files, state.ProjectFile{Target: at(home, ".ssh/authorized_keys"), ProjectOwned: plan.ProjectOwned{Hash: "x", Created: true}})
		}},
		{"backup outside the backups", func(r *state.Project, project, _ string) {
			r.Pending = &state.ApplyJournal{Backup: "../../../" + filepath.Base(project), Ops: []state.JournalOp{}}
		}},
		{"journal through another directory", func(r *state.Project, project, home string) {
			r.Pending = &state.ApplyJournal{Backup: r.Backups[0], Ops: []state.JournalOp{{Target: at(project, ".claude/rules/web.md"), Root: at(home, ".ssh"), Name: "authorized_keys"}}}
		}},
		{"journal outside the project", func(r *state.Project, _, home string) {
			r.Pending = &state.ApplyJournal{Backup: r.Backups[0], Ops: []state.JournalOp{{Target: at(home, ".ssh/authorized_keys"), Root: at(home, ".ssh"), Name: "authorized_keys", Copy: "files/0"}}}
		}},
		{"copy outside the backup", func(r *state.Project, project, _ string) {
			r.Pending = &state.ApplyJournal{Backup: r.Backups[0], Ops: []state.JournalOp{{Target: at(project, ".claude/rules/web.md"), Root: project, Name: ".claude/rules/web.md", Copy: "../../x"}}}
		}},
		{"keys of another home file", func(r *state.Project, _, home string) {
			r.Files = append(r.Files, state.ProjectFile{Target: at(home, ".claude/settings.json"), ProjectOwned: plan.ProjectOwned{Kind: "mcp",
				Keys: []plan.OwnedKey{{Path: []string{"hooks"}, SHA256: "x", Before: json.RawMessage(`{"Stop":[]}`)}}}})
		}},
		{"another key of the .claude.json", func(r *state.Project, _, home string) {
			for i, f := range r.Files {
				if f.Target == at(home, ".claude.json") {
					r.Files[i].Keys = append(r.Files[i].Keys, plan.OwnedKey{Path: []string{"oauthAccount"}, SHA256: "x", Before: json.RawMessage(`{}`)})
				}
			}
		}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			project, home, g := applyRepo(t)
			writeFile(t, at(home, ".ssh/authorized_keys"), "ssh-ed25519 mine\n")
			writeFile(t, at(home, ".claude/settings.json"), `{"theme":"dark"}`)
			if _, err := applyWith(t, newClockFS(t), g, profileTree, project, home, nil); err != nil {
				t.Fatal(err)
			}
			r := recordOf(t, home, project)
			tt.tamper(&r, project, home)
			writeRecord(t, home, r)
			beforeHome, beforeProject := files(t, home), files(t, project)
			_, err := applyWith(t, newClockFS(t), g, profileTree, project, home, yes)
			var ri *app.RecordInvalidError
			if !errors.As(err, &ri) {
				t.Errorf("Apply() error = %v, want the record refused", err)
			}
			_, err = restoreProject(t, g, project, home, func(app.ProjectRestoreResult) (bool, error) { return true, nil })
			if !errors.As(err, &ri) {
				t.Errorf("RestoreProject() error = %v, want the record refused", err)
			}
			if got := files(t, home); !reflect.DeepEqual(got, beforeHome) {
				t.Errorf("home changed:\n%v\nwant\n%v", got, beforeHome)
			}
			if got := files(t, project); !reflect.DeepEqual(got, beforeProject) {
				t.Errorf("project changed")
			}
		})
	}
}

// TestAMarkerTheRepositoryShipsIsNeverTrusted checks that a project
// marker with a journal, as a crafted repository could hold, writes
// nothing outside the project: the record is only read from the home
// directory, and the marker is rewritten.
func TestAMarkerTheRepositoryShipsIsNeverTrusted(t *testing.T) {
	project, home, g := applyRepo(t)
	writeFile(t, at(home, ".ssh/authorized_keys"), "ssh-ed25519 mine\n")
	crafted, _ := json.Marshal(state.Project{Schema: state.ProjectSchema, Project: project, Pending: &state.ApplyJournal{Backup: "../../x",
		Ops: []state.JournalOp{{Target: at(home, ".ssh/authorized_keys"), Root: at(home, ".ssh"), Name: "authorized_keys", Copy: "../../evil"}}}})
	writeFile(t, at(project, ".dotai/state.json"), string(crafted))
	writeFile(t, at(project, "evil"), "ssh-ed25519 attacker\n")
	res, err := applyWith(t, newClockFS(t), g, profileTree, project, home, nil)
	if err != nil || !res.FirstApply {
		t.Fatalf("Apply() = first %v, %v, want a first apply", res.FirstApply, err)
	}
	if got, _ := os.ReadFile(at(home, ".ssh/authorized_keys")); string(got) != "ssh-ed25519 mine\n" {
		t.Errorf("authorized_keys = %q, want it untouched", got)
	}
	var m state.Marker
	data, _ := os.ReadFile(at(project, ".dotai/state.json"))
	if err := json.Unmarshal(data, &m); err != nil || m.Schema != state.MarkerSchema || m.Pack.Name != "minimal-pack" {
		t.Errorf("marker = %s, want dotai's marker", data)
	}
	writeFile(t, at(project, ".dotai/state.json"), "{}")
	again, err := applyWith(t, newClockFS(t), g, profileTree, project, home, nil)
	if err != nil || !slices.ContainsFunc(again.Warnings, func(w plan.Warning) bool { return w.Code == app.CodeMarkerMismatch }) {
		t.Errorf("Apply() over an edited marker = %+v, %v, want the marker-mismatch warning", again.Warnings, err)
	}
}

// TestANoOpApplyRepairsItsExcludeBlockAndItsEntry checks that an apply
// that changes nothing still puts back its exclude block and its entry in
// the list of projects when they went missing.
func TestANoOpApplyRepairsItsExcludeBlockAndItsEntry(t *testing.T) {
	project, home, g := applyRepo(t)
	if _, err := applyWith(t, newClockFS(t), g, profileTree, project, home, nil); err != nil {
		t.Fatal(err)
	}
	exclude, list := at(project, ".git/info/exclude"), at(home, ".dotai/projects.json")
	wantExclude, wantList := files(t, project)[".git/info/exclude"], files(t, home)[".dotai/projects.json"]
	writeFile(t, exclude, string(plan.WithoutExcludeBlock([]byte(wantExclude), project)))
	if err := os.Remove(list); err != nil {
		t.Fatal(err)
	}
	res, err := applyWith(t, newClockFS(t), g, profileTree, project, home, nil)
	if err != nil || res.FirstApply {
		t.Fatalf("Apply() = first %v, %v", res.FirstApply, err)
	}
	if got, _ := os.ReadFile(exclude); string(got) != wantExclude {
		t.Errorf("exclude = %q, want the block put back %q", got, wantExclude)
	}
	if got, _ := os.ReadFile(list); string(got) != wantList {
		t.Errorf("projects.json = %q, want the entry put back", got)
	}
}

// sharedGit is two projects of one repository, as two worktrees: each is
// its own top, both share the main worktree's key and its exclude file.
type sharedGit struct{ main, exclude string }

func (g sharedGit) Repo(_ context.Context, dir string) (*app.Repo, error) {
	return &app.Repo{Top: dir, Main: g.main, Exclude: g.exclude}, nil
}

func (sharedGit) Tracked(context.Context, string, []string) (map[string]bool, error) {
	return map[string]bool{}, nil
}

func (sharedGit) Files(context.Context, string) ([]string, error) { return nil, nil }

// killAfterFS lets the apply write up to and including the target after,
// then fails every write and removal, as when the apply is killed there:
// its rollback fails too, so its journal stays.
type killAfterFS struct {
	*clockFS
	after  string
	killed bool
}

func (f *killAfterFS) WriteIn(root, name string, data []byte, mode os.FileMode) error {
	if f.killed {
		return errDisk
	}
	err := f.clockFS.WriteIn(root, name, data, mode)
	f.killed = filepath.Join(root, filepath.FromSlash(name)) == f.after
	return err
}

func (f *killAfterFS) RemoveIn(root, name string) error {
	if f.killed {
		return errDisk
	}
	return f.clockFS.RemoveIn(root, name)
}

// TestAKilledApplySettlesAfterAnotherProjectWroteTheSharedFiles checks
// that an apply killed after it wrote the exclude file and the list of
// projects, which another worktree then rewrote with its own block and
// entry, is still rolled back by its next apply, which gives back its own
// part of those files only, and that restoring both leaves the shared
// files as they were.
func TestAKilledApplySettlesAfterAnotherProjectWroteTheSharedFiles(t *testing.T) {
	root, home, _ := projectDirs(t)
	a, b := at(root, "a"), at(root, "b")
	g := sharedGit{main: a, exclude: at(root, "a/.git/info/exclude")}
	writeFile(t, g.exclude, "*.log\n")
	writeFile(t, at(b, "README.md"), "b\n")
	writeFile(t, at(home, ".claude.json"), `{"numStartups":3}`)
	before := files(t, home)
	excludeBefore, _ := os.ReadFile(g.exclude)
	_, err := applyWith(t, &killAfterFS{clockFS: newClockFS(t), after: at(home, ".dotai/projects.json")}, g, profileTree, a, home, nil)
	if !errors.As(err, new(*app.ApplyError)) {
		t.Fatalf("killed Apply() error = %v", err)
	}
	if r := recordOf(t, home, a); r.Pending == nil {
		t.Fatal("the killed apply left no journal")
	}
	if _, err := applyWith(t, newClockFS(t), g, profileTree, b, home, nil); err != nil {
		t.Fatalf("Apply() of the other worktree error = %v", err)
	}
	res, err := applyWith(t, newClockFS(t), g, profileTree, a, home, nil)
	if err != nil || !res.FirstApply {
		t.Fatalf("next Apply() = first %v, %v, want the journal settled and a first apply", res.FirstApply, err)
	}
	for _, p := range []string{a, b} {
		if _, err := restoreProject(t, g, p, home, nil); err != nil {
			t.Fatalf("RestoreProject(%s) error = %v", p, err)
		}
	}
	if got, _ := os.ReadFile(g.exclude); string(got) != string(excludeBefore) {
		t.Errorf("exclude = %q, want %q", got, excludeBefore)
	}
	got := files(t, home)
	for k := range got {
		if strings.HasPrefix(k, ".dotai/") {
			delete(got, k)
		}
	}
	if !reflect.DeepEqual(got, before) {
		t.Errorf("home = %v, want %v", got, before)
	}
	if _, err := os.Stat(at(home, ".dotai/projects.json")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("projects.json = %v, want it gone", err)
	}
}

// readFailFS fails the next read of target once it was written, as a
// verification read that a cancellation stops.
type readOnceFailFS struct {
	*clockFS
	target  string
	written bool
	failed  bool
}

func (f *readOnceFailFS) WriteIn(root, name string, data []byte, mode os.FileMode) error {
	err := f.clockFS.WriteIn(root, name, data, mode)
	if filepath.Join(root, filepath.FromSlash(name)) == f.target {
		f.written = true
	}
	return err
}

func (f *readOnceFailFS) Read(ctx context.Context, name string) ([]byte, plan.Current, error) {
	if name == f.target && f.written && !f.failed {
		f.failed = true
		return nil, plan.Current{}, context.Canceled
	}
	return f.clockFS.Read(ctx, name)
}

// TestAFailedHomeFileWriteIsRolledBack checks that a home file whose
// write succeeded but whose check failed is rolled back with the rest.
func TestAFailedHomeFileWriteIsRolledBack(t *testing.T) {
	project, home, g := applyRepo(t)
	writeFile(t, at(home, ".claude.json"), `{"numStartups":3}`)
	before := files(t, home)
	_, err := applyWith(t, &readOnceFailFS{clockFS: newClockFS(t), target: at(home, ".claude.json")}, g, profileTree, project, home, nil)
	var ae *app.ApplyError
	if !errors.As(err, &ae) || ae.Rollback != nil {
		t.Fatalf("Apply() error = %v, want it rolled back", err)
	}
	if got, _ := os.ReadFile(at(home, ".claude.json")); string(got) != before[".claude.json"] {
		t.Errorf("~/.claude.json = %s, want %s", got, before[".claude.json"])
	}
}

// changeOnHashFS writes a key into target the first time its hash is
// taken, as a harness that writes the file between dotai's read and its
// write.
type changeOnHashFS struct {
	*clockFS
	target  string
	changed bool
}

func (f *changeOnHashFS) Hash(ctx context.Context, name string) (plan.Current, error) {
	if name == f.target && !f.changed {
		f.changed = true
		writeFile(f.t, f.target, `{"numStartups":4}`)
	}
	return f.clockFS.Hash(ctx, name)
}

// TestTheHomeFileIsEditedAgainWhenAHarnessWroteItMeanwhile checks that
// dotai never writes over the home file what it held before a harness
// wrote it, and keeps a copy of it in the backup.
func TestTheHomeFileIsEditedAgainWhenAHarnessWroteItMeanwhile(t *testing.T) {
	project, home, g := applyRepo(t)
	target := at(home, ".claude.json")
	writeFile(t, target, `{"numStartups":3}`)
	res, err := applyWith(t, &changeOnHashFS{clockFS: newClockFS(t), target: target}, g, profileTree, project, home, nil)
	if err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(target)
	if v, _, _ := plan.GetKey(data, []string{"numStartups"}); string(v) != "4" {
		t.Errorf("~/.claude.json = %s, want what the harness wrote kept", data)
	}
	if _, ok, _ := plan.GetKey(data, []string{"projects", filepath.ToSlash(project), "mcpServers", "browser"}); !ok {
		t.Errorf("~/.claude.json = %s, want the server", data)
	}
	if !strings.Contains(files(t, res.Backup.Path)["manifest.json"], filepath.ToSlash(target)) &&
		!strings.Contains(files(t, res.Backup.Path)["manifest.json"], strings.ReplaceAll(target, `\`, `\\`)) {
		t.Errorf("backup manifest = %s, want a copy of ~/.claude.json", files(t, res.Backup.Path)["manifest.json"])
	}
}
