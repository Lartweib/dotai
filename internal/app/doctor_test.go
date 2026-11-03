package app_test

import (
	"context"
	"errors"
	"maps"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/lartweib/dotai/internal/app"
	"github.com/lartweib/dotai/internal/backup"
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

// fixExclude applies the profile to project, takes dotai's block out of its
// exclude file and runs doctor --fix, which writes it back.
func fixExclude(t *testing.T, g app.GitPort, project, home string) app.DoctorResult {
	t.Helper()
	if _, err := applyWith(t, newClockFS(t), g, profileTree, project, home, nil); err != nil {
		t.Fatal(err)
	}
	writeFile(t, at(project, ".git/info/exclude"), "# the user's own\n*.log\n")
	fixed := runDoctor(t, g, home, true)
	if fixed.Problems() != 0 || len(fixed.Backups) != 1 {
		t.Fatalf("Doctor(--fix) = %+v, want the exclude block fixed with a backup", fixed)
	}
	return fixed
}

func TestDoctorFixNamesItsBackupAfterTheFix(t *testing.T) {
	project, home, g := applyRepo(t)
	ref := fixExclude(t, g, project, home).Backups[0]
	if !strings.HasSuffix(ref.ID, "_"+backup.OperationFix) || filepath.Base(ref.Path) != ref.ID {
		t.Errorf("backup = %+v, want an id and a directory that end in _fix", ref)
	}
	data, err := os.ReadFile(filepath.Join(ref.Path, backup.ManifestName))
	if err != nil {
		t.Fatal(err)
	}
	m, err := backup.DecodeProject(data, ref.ID, project)
	if err != nil || m.Operation != backup.OperationFix {
		t.Errorf("manifest = %+v, %v, want the operation fix", m, err)
	}
	r := readJSON(t, app.RecordPath(home, project), func(data []byte) (state.Project, error) { return state.DecodeProject(data, project) })
	if len(r.Backups) != 2 || r.Backups[1] != ref.ID || !strings.HasSuffix(r.Backups[0], "_"+backup.OperationApply) {
		t.Errorf("record backups = %q, want the apply's then the fix's %s", r.Backups, ref.ID)
	}
	if res := runDoctor(t, g, home, false); len(res.Findings) != 0 {
		t.Errorf("Doctor() after --fix = %q, want the record with its fix backup valid", codes(res))
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

// twoProjects are two projects applied in one home, each at the top of its
// own repository, in the order doctor checks them.
func twoProjects(t *testing.T) (first, second, home string, g *fakeGit) {
	t.Helper()
	first, home, _ = projectDirs(t)
	second = t.TempDir()
	g = &fakeGit{}
	for _, p := range []string{first, second} {
		writeFile(t, at(p, ".git/info/exclude"), "*.log\n")
		if _, err := applyWith(t, newClockFS(t), g, profileTree, p, home, nil); err != nil {
			t.Fatal(err)
		}
	}
	if filepath.Base(filepath.Dir(app.RecordPath(home, first))) > filepath.Base(filepath.Dir(app.RecordPath(home, second))) {
		first, second = second, first
	}
	return first, second, home, g
}

func TestDoctorFixesTheExcludeBlocksOfSeveralProjects(t *testing.T) {
	first, second, home, g := twoProjects(t)
	for _, p := range []string{first, second} {
		writeFile(t, at(p, ".git/info/exclude"), "*.log\n")
	}
	res := runDoctor(t, g, home, true)
	if res.Problems() != 0 || len(res.Backups) != 2 || len(res.Findings) != 2 {
		t.Fatalf("Doctor(--fix) = %+v, want both blocks fixed, each with a backup", res)
	}
	for _, p := range []string{first, second} {
		if got := files(t, p)[".git/info/exclude"]; !strings.Contains(got, "# >>> dotai "+p) {
			t.Errorf("exclude of %s = %q, want its block", p, got)
		}
	}
}

func TestDoctorFixThatFailsKeepsWhatItFoundAndFixed(t *testing.T) {
	first, second, home, g := twoProjects(t)
	for _, p := range []string{first, second} {
		writeFile(t, at(p, ".git/info/exclude"), "*.log\n")
	}
	failing := at(second, ".git/info/exclude")
	d := &failAtFS{clockFS: newClockFS(t), fail: failing}
	res, err := app.DoctorWith(context.Background(), d, g, home, app.DoctorOptions{Fix: true})
	var ae *app.ApplyError
	if !errors.As(err, &ae) || ae.Operation != "fix" || ae.Rollback != nil || !strings.Contains(err.Error(), "the fix stopped") {
		t.Fatalf("Doctor(--fix) error = %v, want the fix stopped and rolled back", err)
	}
	if len(res.Findings) != 2 || !res.Findings[0].Fixed || res.Findings[0].Project != first || res.Findings[1].Fixed || len(res.Backups) != 1 {
		t.Errorf("Doctor(--fix) = %+v, want the first block fixed with its backup and the second one found", res)
	}
	if got, _ := os.ReadFile(failing); string(got) != "*.log\n" {
		t.Errorf("exclude of the second project = %q, want it as it was", got)
	}
	r := readJSON(t, app.RecordPath(home, second), func(data []byte) (state.Project, error) { return state.DecodeProject(data, second) })
	if r.Pending != nil || len(r.Backups) != 1 {
		t.Errorf("record of the second project = %+v, want it as before the fix", r)
	}
}

func TestDoctorReportsAProjectGitFailsOnAndChecksTheRest(t *testing.T) {
	first, second, home, g := twoProjects(t)
	g.fail = map[string]error{second: errors.New("git rev-parse: signal: killed")}
	edited := at(first, ".claude/rules/web.md")
	writeFile(t, edited, "# edited\n")
	res := runDoctor(t, g, home, false)
	want := []string{"check-failed " + second, "file-changed " + edited}
	got := codes(res)
	slices.Sort(got)
	slices.Sort(want)
	if !reflect.DeepEqual(got, want) || !reflect.DeepEqual(res.Projects, []string{first}) {
		t.Errorf("Doctor() = %q, projects %q, want %q and the first project checked", got, res.Projects, want)
	}
	i := slices.IndexFunc(res.Findings, func(f app.Finding) bool { return f.Code == app.CodeCheckFailed })
	if f := res.Findings[i]; f.Project != second || !strings.Contains(f.Message, "killed") {
		t.Errorf("finding = %+v, want the project and git's error", f)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := app.DoctorWith(ctx, newClockFS(t), g, home, app.DoctorOptions{}); !errors.Is(err, context.Canceled) {
		t.Errorf("Doctor() cancelled error = %v, want it to stop", err)
	}
}

func TestDoctorSkipsAnInterruptedInstall(t *testing.T) {
	home, _ := installHome(t)
	if _, err := (&fakeRulesync{t: t}).install(newClockFS(t), examplePack, home); err != nil {
		t.Fatal(err)
	}
	name := at(home, ".dotai/state.json")
	s := readJSON(t, name, state.Decode)
	s.Pending = &state.Pending{Backup: s.Backup, Ops: []state.PendingOp{}}
	data, err := state.Encode(s)
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, name, string(data))
	writeFile(t, at(home, ".dotai/isolated/claude/CLAUDE.md"), "# mine\n")
	if got := codes(runDoctor(t, &fakeGit{}, home, false)); !reflect.DeepEqual(got, []string{"install-interrupted " + name}) {
		t.Errorf("Doctor() = %q, want only the interrupted install", got)
	}
}

func TestDoctorReportsAProjectNowInsideAnotherWorktree(t *testing.T) {
	project, home, g := applyRepo(t)
	if _, err := applyWith(t, newClockFS(t), g, profileTree, project, home, nil); err != nil {
		t.Fatal(err)
	}
	outer := filepath.Dir(project)
	g.repos = map[string]*app.Repo{project: {Top: outer, Main: outer, Exclude: filepath.Join(outer, ".git", "info", "exclude")}}
	if got := codes(runDoctor(t, g, home, true)); !reflect.DeepEqual(got, []string{"project-moved " + app.RecordPath(home, project)}) {
		t.Errorf("Doctor() = %q, want the project moved", got)
	}
}

func TestDoctorDoesNotFixAnExcludeFileThatIsNotAFile(t *testing.T) {
	project, home, g := applyRepo(t)
	if _, err := applyWith(t, newClockFS(t), g, profileTree, project, home, nil); err != nil {
		t.Fatal(err)
	}
	exclude := at(project, ".git/info/exclude")
	if err := os.Remove(exclude); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(exclude, 0o755); err != nil {
		t.Fatal(err)
	}
	res := runDoctor(t, g, home, true)
	if got := codes(res); !reflect.DeepEqual(got, []string{"exclude-missing " + exclude}) || res.Findings[0].Fixable || res.Findings[0].Fixed || len(res.Backups) != 0 {
		t.Errorf("Doctor(--fix) = %+v, want the exclude file reported and left alone", res)
	}
	if info, err := os.Lstat(exclude); err != nil || !info.IsDir() {
		t.Errorf("exclude = %v, %v, want the directory left", info, err)
	}
}

func TestDoctorRefusesARecordWhoseExcludeEntriesBreakTheBlock(t *testing.T) {
	project, home, g := applyRepo(t)
	if _, err := applyWith(t, newClockFS(t), g, profileTree, project, home, nil); err != nil {
		t.Fatal(err)
	}
	record := app.RecordPath(home, project)
	saved := mustRead(t, record)
	for _, bad := range []string{"/x\n*", "# <<< dotai " + project, "/x\r"} {
		r := readJSON(t, record, func(data []byte) (state.Project, error) { return state.DecodeProject(data, project) })
		r.Exclude.Entries = append(r.Exclude.Entries, bad)
		data, err := state.EncodeProject(r)
		if err != nil {
			t.Fatal(err)
		}
		writeFile(t, record, string(data))
		if got := codes(runDoctor(t, g, home, true)); !reflect.DeepEqual(got, []string{"record-invalid " + record}) {
			t.Errorf("Doctor() with the entry %q = %q, want record-invalid", bad, got)
		}
		if _, err := restoreProject(t, g, project, home, nil); !errors.As(err, new(*app.RecordInvalidError)) {
			t.Errorf("RestoreProject() with the entry %q error = %v, want record-invalid", bad, err)
		}
		writeFile(t, record, string(saved))
	}
}

// TestDoctorAcceptsAKeyAnotherWorktreeRewrote checks that the worktrees of
// a repository, which own the MCP servers of their shared projects entry
// jointly, do not report a value the other one wrote as changed, while a
// value no record wrote still is.
func TestDoctorAcceptsAKeyAnotherWorktreeRewrote(t *testing.T) {
	main, home, _ := projectDirs(t)
	linked := t.TempDir()
	exclude := filepath.Join(main, ".git", "info", "exclude")
	writeFile(t, exclude, "*.log\n")
	g := &fakeGit{repos: map[string]*app.Repo{
		main:   {Top: main, Main: main, Exclude: exclude},
		linked: {Top: linked, Main: main, Exclude: exclude},
	}}
	if _, err := applyWith(t, newClockFS(t), g, profileTree, main, home, nil); err != nil {
		t.Fatal(err)
	}
	other := maps.Clone(profileTree)
	other[".mcp.json"] = `{"mcpServers":{"browser":{"command":"uvx"}}}`
	if _, err := applyWith(t, newClockFS(t), g, other, linked, home, yes); err != nil {
		t.Fatal(err)
	}
	claudeJSON := at(home, ".claude.json")
	server := []string{"projects", filepath.ToSlash(main), "mcpServers", "browser"}
	command := append(slices.Clone(server), "command")
	if v, _, _ := plan.GetKey(mustRead(t, claudeJSON), command); string(v) != `"uvx"` {
		t.Fatalf("server command = %s, want the linked worktree's value", v)
	}
	if got := codes(runDoctor(t, g, home, false)); len(got) != 0 {
		t.Errorf("Doctor() = %q, want nothing: dotai wrote the value", got)
	}
	out, err := plan.ApplyEdits(mustRead(t, claudeJSON), []plan.KeyEdit{{Path: command, Value: []byte(`"edited"`)}})
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, claudeJSON, string(out))
	want := "key-changed " + claudeJSON + " " + strings.Join(server, ".")
	if got := codes(runDoctor(t, g, home, false)); !reflect.DeepEqual(got, []string{want, want}) {
		t.Errorf("Doctor() after a hand edit = %q, want the key changed for both worktrees", got)
	}
}

func mustRead(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(name)
	if err != nil {
		t.Fatal(err)
	}
	return data
}
