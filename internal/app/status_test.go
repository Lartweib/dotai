package app_test

import (
	"errors"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/lartweib/dotai/internal/app"
)

func TestStatusOfAFreshHomeIsEmptyAndWritesNothing(t *testing.T) {
	home := t.TempDir()
	st, err := app.ReadStatus(home)
	if err != nil {
		t.Fatalf("ReadStatus() error = %v", err)
	}
	if st.State != nil || st.StateError != "" || st.StatePath != at(home, ".dotai/state.json") {
		t.Errorf("ReadStatus() state = %+v, %q, %q, want none at the state path", st.State, st.StateError, st.StatePath)
	}
	if st.Projects == nil || len(st.Projects) != 0 || st.ProjectsError != "" {
		t.Errorf("ReadStatus() projects = %#v, %q, want an empty list", st.Projects, st.ProjectsError)
	}
	if _, err := os.Lstat(at(home, ".dotai")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("~/.dotai = %v, want nothing written", err)
	}
}

func TestStatusReadsTheInstallStateAndEachProjectRecord(t *testing.T) {
	project, home, g := applyRepo(t)
	if _, err := quiet(t).install(newClockFS(t), examplePack, home); err != nil {
		t.Fatal(err)
	}
	if _, err := applyWith(t, newClockFS(t), g, profileTree, project, home, nil); err != nil {
		t.Fatal(err)
	}
	before := files(t, home)
	st, err := app.ReadStatus(home)
	if err != nil {
		t.Fatalf("ReadStatus() error = %v", err)
	}
	if st.State == nil || st.State.Pack.Name != "minimal-pack" || st.StateError != "" {
		t.Errorf("ReadStatus() state = %+v, %q, want the install of minimal-pack", st.State, st.StateError)
	}
	if len(st.Projects) != 1 {
		t.Fatalf("ReadStatus() projects = %+v, want the applied project", st.Projects)
	}
	p := st.Projects[0]
	if p.Project != project || !p.Listed || p.Record == nil || p.Record.Project != project || p.Error != "" || p.RecordPath != app.RecordPath(home, project) {
		t.Errorf("project = %+v, want the listed record of %s", p, project)
	}
	if !reflect.DeepEqual(p.Record.Profiles, []string{"web"}) {
		t.Errorf("record profiles = %v, want [web]", p.Record.Profiles)
	}
	if got := files(t, home); !reflect.DeepEqual(got, before) {
		t.Error("ReadStatus() wrote into the home directory")
	}
}

func TestStatusReportsWhatCannotBeReadAndGoesOn(t *testing.T) {
	project, home, g := applyRepo(t)
	if _, err := applyWith(t, newClockFS(t), g, profileTree, project, home, nil); err != nil {
		t.Fatal(err)
	}
	writeFile(t, at(home, ".dotai/state.json"), "{not json")
	writeFile(t, at(home, ".dotai/projects/0123456789abcdef/record.json"), `{"schema":"nope"}`)
	st, err := app.ReadStatus(home)
	if err != nil {
		t.Fatalf("ReadStatus() error = %v, want the problems reported in the status", err)
	}
	if st.State != nil || !strings.Contains(st.StateError, "state.json") {
		t.Errorf("ReadStatus() state = %+v, %q, want why the state cannot be read", st.State, st.StateError)
	}
	if len(st.Projects) != 2 {
		t.Fatalf("ReadStatus() projects = %+v, want the good record and the bad one", st.Projects)
	}
	var good, bad int
	for _, p := range st.Projects {
		switch {
		case p.Record != nil && p.Error == "" && p.Project == project:
			good++
		case p.Record == nil && p.Error != "" && strings.HasSuffix(p.RecordPath, "record.json"):
			bad++
		}
	}
	if good != 1 || bad != 1 {
		t.Errorf("ReadStatus() projects = %+v, want one readable record and one reported as invalid", st.Projects)
	}
}

func TestStatusListsAProjectOfTheListWithoutARecord(t *testing.T) {
	home := t.TempDir()
	gone := at(t.TempDir(), "gone")
	writeFile(t, at(home, ".dotai/projects.json"), `{"schema":"dotai.projects/v1","projects":[{"project":"`+jsonPath(gone)+`","file":"`+jsonPath(at(home, ".claude.json"))+`","keys":[]}]}`)
	st, err := app.ReadStatus(home)
	if err != nil {
		t.Fatalf("ReadStatus() error = %v", err)
	}
	if st.ProjectsError != "" || len(st.Projects) != 1 || st.Projects[0].Project != gone || !st.Projects[0].Listed || st.Projects[0].Record != nil || st.Projects[0].Error == "" {
		t.Errorf("ReadStatus() projects = %+v, %q, want the listed project reported without a record", st.Projects, st.ProjectsError)
	}
}

// jsonPath is p escaped for a JSON string.
func jsonPath(p string) string { return strings.ReplaceAll(p, `\`, `\\`) }
