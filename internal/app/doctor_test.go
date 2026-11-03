package app_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/lartweib/dotai/internal/app"
	"github.com/lartweib/dotai/internal/plan"
	"github.com/lartweib/dotai/internal/state"
)

func runDoctor(t *testing.T, g app.GitPort, home string, fix bool) app.DoctorResult {
	t.Helper()
	res, err := app.DoctorWith(context.Background(), newClockFS(t), g, home, app.DoctorOptions{Fix: fix})
	if err != nil {
		t.Fatalf("Doctor() error = %v", err)
	}
	return res
}

// codes is each finding of res as code, path and key, in order.
func codes(res app.DoctorResult) []string {
	out := []string{}
	for _, f := range res.Findings {
		s := f.Code + " " + f.Path
		if len(f.Key) > 0 {
			s += " " + strings.Join(f.Key, ".")
		}
		out = append(out, s)
	}
	return out
}

func TestDoctorInAFreshHomeFindsNothingAndWritesNothing(t *testing.T) {
	home, _ := installHome(t)
	res := runDoctor(t, &fakeGit{}, home, true)
	if res.State != "" || len(res.Findings) != 0 || len(res.Projects) != 0 || len(res.Backups) != 0 || res.Problems() != 0 {
		t.Errorf("Doctor() = %+v, want nothing", res)
	}
	if _, err := os.Lstat(at(home, ".dotai")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("~/.dotai = %v, want nothing created", err)
	}
}

func TestDoctorReportsInstalledFilesEditedByHandOrRemoved(t *testing.T) {
	home, _ := installHome(t)
	if _, err := (&fakeRulesync{t: t}).install(newClockFS(t), examplePack, home); err != nil {
		t.Fatal(err)
	}
	if res := runDoctor(t, &fakeGit{}, home, false); len(res.Findings) != 0 || res.State != at(home, ".dotai/state.json") {
		t.Fatalf("Doctor() after the install = %+v, want the state checked and nothing found", res)
	}
	edited, gone, settings := at(home, ".dotai/isolated/claude/CLAUDE.md"), at(home, ".dotai/tools/hello.sh"), at(home, ".dotai/isolated/claude/settings.json")
	writeFile(t, edited, "# mine\n")
	if err := os.Remove(gone); err != nil {
		t.Fatal(err)
	}
	writeFile(t, settings, "{\"model\": \"haiku\", \"theme\": \"dark\"}\n")
	before := snapshot(t, home)
	res := runDoctor(t, &fakeGit{}, home, false)
	want := []string{"file-changed " + edited, "key-changed " + settings + " model", "file-missing " + gone}
	slices.Sort(want)
	got := codes(res)
	slices.Sort(got)
	if !reflect.DeepEqual(got, want) || res.Problems() != 3 {
		t.Errorf("Doctor() = %q, want %q", got, want)
	}
	for _, f := range res.Findings {
		if f.Harness == "" && f.Path != gone || f.Fixable || f.Message == "" {
			t.Errorf("finding %+v, want a harness, a message and nothing to fix", f)
		}
	}
	if got := snapshot(t, home); !reflect.DeepEqual(got, before) {
		t.Error("Doctor() wrote something")
	}
	writeFile(t, settings, "{\"theme\": \"dark\"}\n")
	if got := codes(runDoctor(t, &fakeGit{}, home, false)); !slices.Contains(got, "key-missing "+settings+" model") {
		t.Errorf("Doctor() = %q, want the removed key", got)
	}
}

func TestDoctorReportsWhatChangedInAnAppliedProject(t *testing.T) {
	project, home, g := applyRepo(t)
	if _, err := applyWith(t, newClockFS(t), g, profileTree, project, home, nil); err != nil {
		t.Fatal(err)
	}
	res := runDoctor(t, g, home, false)
	if len(res.Findings) != 0 || !reflect.DeepEqual(res.Projects, []string{project}) {
		t.Fatalf("Doctor() after the apply = %+v, want the project checked and nothing found", res)
	}
	rule, removed, local := at(project, ".claude/rules/web.md"), at(project, ".agents/rules/web.md"), at(project, ".claude/settings.local.json")
	marker, exclude, claudeJSON := at(project, ".dotai/state.json"), at(project, ".git/info/exclude"), at(home, ".claude.json")
	writeFile(t, rule, "# edited by hand\n")
	if err := os.Remove(removed); err != nil {
		t.Fatal(err)
	}
	writeFile(t, local, `{"model":"haiku"}`)
	writeFile(t, marker, "{}\n")
	writeFile(t, exclude, "# the user's own\n*.log\n")
	data, err := os.ReadFile(claudeJSON)
	if err != nil {
		t.Fatal(err)
	}
	server := []string{"projects", filepath.ToSlash(project), "mcpServers", "browser"}
	out, err := plan.ApplyEdits(data, []plan.KeyEdit{{Path: server}})
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, claudeJSON, string(out))
	beforeProject, beforeHome := files(t, project), files(t, home)

	res = runDoctor(t, g, home, false)
	want := []string{
		"file-changed " + rule, "file-missing " + removed, "key-changed " + local + " model",
		"key-missing " + claudeJSON + " " + strings.Join(server, "."), "marker-mismatch " + marker, "exclude-missing " + exclude,
	}
	slices.Sort(want)
	got := codes(res)
	slices.Sort(got)
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Doctor() = %q\nwant %q", got, want)
	}
	for _, f := range res.Findings {
		if f.Project != project || f.Fixable != (f.Code == app.CodeExcludeMissing) || f.Fixed {
			t.Errorf("finding %+v, want the project, and only the exclude block fixable", f)
		}
	}
	if !reflect.DeepEqual(files(t, project), beforeProject) || !reflect.DeepEqual(files(t, home), beforeHome) {
		t.Error("Doctor() without --fix wrote something")
	}

	fixed := runDoctor(t, g, home, true)
	if fixed.Problems() != len(want)-1 || len(fixed.Backups) != 1 {
		t.Errorf("Doctor(--fix) = %+v, want the exclude block fixed with a backup", fixed)
	}
	if i := slices.IndexFunc(fixed.Findings, func(f app.Finding) bool { return f.Code == app.CodeExcludeMissing }); i < 0 || !fixed.Findings[i].Fixed {
		t.Errorf("Doctor(--fix) findings = %+v, want the exclude block fixed", fixed.Findings)
	}
	if got, _ := os.ReadFile(exclude); !strings.HasPrefix(string(got), "# the user's own\n*.log\n# >>> dotai ") || !strings.Contains(string(got), "\n/.dotai/\n") {
		t.Errorf("exclude = %q, want the block written back after the user's lines", got)
	}
	r := readJSON(t, app.RecordPath(home, project), func(data []byte) (state.Project, error) { return state.DecodeProject(data, project) })
	if r.Pending != nil || len(r.Backups) != 2 || r.Exclude.File != exclude || r.Exclude.Pending {
		t.Errorf("record = %+v, want the fix's backup, no journal and the exclude file", r)
	}
	if got := codes(runDoctor(t, g, home, false)); slices.ContainsFunc(got, func(s string) bool { return strings.HasPrefix(s, "exclude-") }) {
		t.Errorf("Doctor() after --fix = %q, want the exclude block fine", got)
	}
}

func TestDoctorFixCompletesPendingExcludeEntriesOnceInARepository(t *testing.T) {
	project, home, _ := projectDirs(t)
	g := &fakeGit{outside: true}
	if _, err := applyWith(t, newClockFS(t), g, profileTree, project, home, nil); err != nil {
		t.Fatal(err)
	}
	if res := runDoctor(t, g, home, true); len(res.Findings) != 0 {
		t.Errorf("Doctor() outside a repository = %q, want the pending entries left to wait", codes(res))
	}
	g.outside = false
	if err := os.MkdirAll(at(project, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	exclude := at(project, ".git/info/exclude")
	res := runDoctor(t, g, home, false)
	if got := codes(res); !reflect.DeepEqual(got, []string{"exclude-pending " + exclude}) || !res.Findings[0].Fixable || res.Problems() != 1 {
		t.Fatalf("Doctor() in a repository = %q, want the pending entries, fixable", got)
	}
	if _, err := os.Lstat(exclude); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("exclude = %v, want nothing written without --fix", err)
	}
	fixed := runDoctor(t, g, home, true)
	if fixed.Problems() != 0 || !fixed.Findings[0].Fixed || len(fixed.Backups) != 1 {
		t.Errorf("Doctor(--fix) = %+v, want the entries written", fixed)
	}
	got, err := os.ReadFile(exclude)
	if err != nil || !strings.HasPrefix(string(got), "# >>> dotai "+project) || !strings.Contains(string(got), "\n/.claude/rules/web.md\n") {
		t.Errorf("exclude = %q, %v, want dotai's block", got, err)
	}
	r := readJSON(t, app.RecordPath(home, project), func(data []byte) (state.Project, error) { return state.DecodeProject(data, project) })
	if r.Exclude.Pending || r.Exclude.File != exclude || !r.Exclude.Created {
		t.Errorf("record exclude = %+v, want the file it created, no longer pending", r.Exclude)
	}
	if res := runDoctor(t, g, home, false); len(res.Findings) != 0 {
		t.Errorf("Doctor() after --fix = %q, want nothing", codes(res))
	}
	if _, err := restoreProject(t, g, project, home, nil); err != nil {
		t.Fatalf("RestoreProject() after --fix error = %v", err)
	}
	if _, err := os.Lstat(exclude); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("exclude after the restore = %v, want the file dotai created gone", err)
	}
}

func TestDoctorReportsOrphansAndInvalidRecords(t *testing.T) {
	project, home, g := applyRepo(t)
	if _, err := applyWith(t, newClockFS(t), g, profileTree, project, home, nil); err != nil {
		t.Fatal(err)
	}
	record := app.RecordPath(home, project)
	data, err := os.ReadFile(record)
	if err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(filepath.Dir(project), "elsewhere.md")
	writeFile(t, record, strings.Replace(string(data), `"files": [`, `"files": [{"target": `+strconv.Quote(outside)+`, "harness": "claudecode", "kind": "rules", "sha256": "00", "exec": false, "created": true},`, 1))
	if got := codes(runDoctor(t, g, home, false)); !reflect.DeepEqual(got, []string{"record-invalid " + record}) {
		t.Errorf("Doctor() with a record that owns a file outside the project = %q, want record-invalid", got)
	}
	writeFile(t, record, string(data))
	if err := os.RemoveAll(project); err != nil {
		t.Fatal(err)
	}
	got := codes(runDoctor(t, g, home, false))
	want := []string{"orphan-entry " + at(home, ".dotai/projects.json"), "orphan-record " + record}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Doctor() with the project gone = %q, want %q", got, want)
	}
}

func TestDoctorSkipsAnInterruptedApply(t *testing.T) {
	project, home, g := applyRepo(t)
	if _, err := applyWith(t, newClockFS(t), g, profileTree, project, home, nil); err != nil {
		t.Fatal(err)
	}
	record := app.RecordPath(home, project)
	r := readJSON(t, record, func(data []byte) (state.Project, error) { return state.DecodeProject(data, project) })
	r.Pending = &state.ApplyJournal{Backup: r.Backups[0], Ops: []state.JournalOp{}}
	data, err := state.EncodeProject(r)
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, record, string(data))
	writeFile(t, at(project, ".claude/rules/web.md"), "# edited\n")
	if got := codes(runDoctor(t, g, home, false)); !reflect.DeepEqual(got, []string{"apply-interrupted " + record}) {
		t.Errorf("Doctor() = %q, want only the interrupted apply", got)
	}
}
