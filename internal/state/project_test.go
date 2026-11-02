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

func projectRecord(t *testing.T) (state.Project, string) {
	t.Helper()
	project := t.TempDir()
	return state.Project{
		Schema: state.ProjectSchema, Pack: state.Pack{Name: "p", Version: "1", Path: project}, Variant: "v", Profiles: []string{"web"},
		Harnesses: []string{"claudecode"}, Project: project, MCPKey: filepath.ToSlash(project), FirstApply: time.Unix(0, 0).UTC(), AppliedAt: time.Unix(1, 0).UTC(),
		Backups: []string{"20261102T101500.000Z_apply"}, Dirs: []string{".claude", ".claude/rules"},
		Files: []state.ProjectFile{
			{Target: filepath.Join(project, ".claude", "rules", "web.md"), ProjectOwned: plan.ProjectOwned{Harness: "claudecode", Kind: "rules", Hash: "h", Created: true, Exclude: "/.claude/rules/web.md"}},
			{Target: filepath.Join(project, ".claude", "settings.local.json"), ProjectOwned: plan.ProjectOwned{Harness: "claudecode", Kind: "settings",
				Keys: []plan.OwnedKey{{Path: []string{"model"}, SHA256: "s", Before: []byte(`"opus"`)}}, Parents: [][]string{{"hooks"}}}},
		},
		Exclude: state.ProjectExclude{File: filepath.Join(project, ".git", "info", "exclude"), Entries: []string{"/.dotai/", "/.claude/rules/web.md"}},
	}, project
}

func TestProjectRecordRoundTrips(t *testing.T) {
	p, project := projectRecord(t)
	data, err := state.EncodeProject(p)
	if err != nil {
		t.Fatal(err)
	}
	got, err := state.DecodeProject(data, project)
	if err != nil || !reflect.DeepEqual(got, p) {
		t.Errorf("DecodeProject(EncodeProject(p)) = %+v, %v, want %+v", got, err, p)
	}
	if owned := got.Owned(); len(owned) != 2 || owned[p.Files[1].Target].Keys[0].Name() != "model" {
		t.Errorf("Owned() = %+v", owned)
	}
}

func TestDecodeProjectRejectsWhatAnApplyNeverWrites(t *testing.T) {
	for _, rel := range []string{"CLAUDE.md", "agents.md", ".mcp.json", ".claude/settings.json", "opencode.jsonc", ".dotai/packs/p/marker", ".dotai/packs"} {
		p, project := projectRecord(t)
		p.Files[0].Target = filepath.Join(project, filepath.FromSlash(rel))
		data, _ := state.EncodeProject(p)
		if _, err := state.DecodeProject(data, project); err == nil || !strings.Contains(err.Error(), "never writes") {
			t.Errorf("DecodeProject() with %s owned: error = %v, want it refused", rel, err)
		}
	}
}

func TestDecodeProjectRejectsAnotherProject(t *testing.T) {
	p, _ := projectRecord(t)
	data, _ := state.EncodeProject(p)
	if _, err := state.DecodeProject(data, t.TempDir()); err == nil || !strings.Contains(err.Error(), "moved or copied") {
		t.Errorf("DecodeProject() of a moved project: error = %v, want it refused", err)
	}
	p.Dirs = []string{"../out"}
	data, _ = state.EncodeProject(p)
	if _, err := state.DecodeProject(data, p.Project); err == nil {
		t.Error("DecodeProject() with a directory outside the project: want an error")
	}
}

func TestProjectsListsTheKeysOfEachProject(t *testing.T) {
	file := filepath.Join(t.TempDir(), ".claude.json")
	a, b := filepath.Join(t.TempDir(), "a"), filepath.Join(t.TempDir(), "b")
	l := state.Projects{}.With(state.ProjectEntry{Project: b, File: file, Keys: [][]string{{"projects", "b", "mcpServers", "y"}}}).
		With(state.ProjectEntry{Project: a, File: file, Keys: [][]string{{"projects", "a", "mcpServers", "x"}}})
	if len(l.Projects) != 2 || l.Projects[0].Project != a {
		t.Errorf("With() = %+v, want both projects in order", l)
	}
	if got := l.OwnedKeys(file); len(got) != 2 {
		t.Errorf("OwnedKeys() = %q, want both keys", got)
	}
	l = l.With(state.ProjectEntry{Project: a, File: file})
	data, err := state.EncodeProjects(l)
	if err != nil {
		t.Fatal(err)
	}
	got, err := state.DecodeProjects(data)
	if err != nil || len(got.Projects) != 1 || got.Projects[0].Project != b {
		t.Errorf("DecodeProjects() = %+v, %v, want b alone", got, err)
	}
}

// TestProjectsHandsTheObjectsItCreatedOverToTheProjectsLeft checks that a
// project whose entry goes leaves the objects and the file its apply
// created to the projects that still have keys under them.
func TestProjectsHandsTheObjectsItCreatedOverToTheProjectsLeft(t *testing.T) {
	file := filepath.Join(t.TempDir(), ".claude.json")
	a, b, c := filepath.Join(t.TempDir(), "a"), filepath.Join(t.TempDir(), "b"), filepath.Join(t.TempDir(), "c")
	server := []string{"projects", "/repo", "mcpServers", "x"}
	l := state.Projects{}.
		With(state.ProjectEntry{Project: a, File: file, Keys: [][]string{server}, Parents: [][]string{{"projects", "/repo"}, {"projects", "/repo", "mcpServers"}}, Created: true}).
		With(state.ProjectEntry{Project: b, File: file, Keys: [][]string{server}}).
		With(state.ProjectEntry{Project: c, File: file, Keys: [][]string{{"projects", "/other", "mcpServers", "y"}}})
	l = l.With(state.ProjectEntry{Project: a, File: file})
	if e := l.Entry(b); e == nil || len(e.Parents) != 2 || !e.Created {
		t.Errorf("entry of b = %+v, want the parents and the file handed over", e)
	}
	if e := l.Entry(c); e == nil || len(e.Parents) != 0 || e.Created {
		t.Errorf("entry of c = %+v, want nothing handed over to another project's entry", e)
	}
}
