package app_test

import (
	"context"
	"errors"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/lartweib/dotai/internal/app"
)

func restoreProject(t *testing.T, g app.GitPort, project, home string, overwrite func(app.ProjectRestoreResult) (bool, error)) (app.ProjectRestoreResult, error) {
	t.Helper()
	return app.RestoreProjectWith(context.Background(), newClockFS(t), g, project, home, overwrite)
}

// homeFiles is files of home without dotai's own directory.
func homeFiles(t *testing.T, home string) map[string]string {
	t.Helper()
	out := files(t, home)
	for k := range out {
		if strings.HasPrefix(k, ".dotai/") {
			delete(out, k)
		}
	}
	return out
}

func TestRestoreProjectLeavesTheProjectAsBeforeTheFirstApply(t *testing.T) {
	project, home, g := applyRepo(t)
	writeFile(t, at(project, ".claude/settings.local.json"), "{\n  // mine\n  \"model\": \"opus\",\n  \"permissions\": {\"allow\": [\"Bash(ls)\"]}\n}\n")
	writeFile(t, at(project, "README.md"), "# site\n")
	writeFile(t, at(home, ".claude.json"), `{"numStartups":3,"projects":{"/other":{"mcpServers":{"x":{}}}}}`)
	before, homeBefore := files(t, project), homeFiles(t, home)
	if _, err := applyWith(t, newClockFS(t), g, profileTree, project, home, nil); err != nil {
		t.Fatal(err)
	}
	writeFile(t, at(project, ".dotai/packs/minimal-pack/onboarded"), "yes\n") // the pack's own
	smaller := map[string]string{".claude/rules/web.md": "# web\n", ".claude/settings.json": `{"model":"sonnet","env":{"A":"1"}}`, ".mcp.json": profileTree[".mcp.json"]}
	if _, err := applyWith(t, newClockFS(t), g, smaller, project, home, nil); err != nil {
		t.Fatal(err)
	}
	res, err := restoreProject(t, g, project, home, nil)
	if err != nil {
		t.Fatalf("RestoreProject() error = %v", err)
	}
	if len(res.Conflicts) != 0 || res.Backup == nil || !res.Exclude.Removed {
		t.Errorf("RestoreProject() = %+v, want no conflicts, a backup and the exclude block removed", res)
	}
	if got := files(t, project); !reflect.DeepEqual(got, before) {
		t.Errorf("project = %v\nwant %v", got, before)
	}
	if got := homeFiles(t, home); !reflect.DeepEqual(got, homeBefore) {
		t.Errorf("home = %v\nwant %v", got, homeBefore)
	}
	for _, rel := range []string{".dotai", ".claude/rules", ".agents"} {
		if _, err := os.Lstat(at(project, rel)); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("%s = %v, want it gone", rel, err)
		}
	}
	if _, err := os.Lstat(at(home, ".dotai/projects.json")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("projects.json = %v, want it gone", err)
	}
	if got := files(t, res.Backup.Path); !strings.Contains(strings.Join(mapValues(got), "\n"), "yes\n") {
		t.Errorf("restore backup = %v, want the pack's own file kept there", got)
	}
	again, err := applyWith(t, newClockFS(t), g, profileTree, project, home, nil)
	if err != nil || !again.FirstApply {
		t.Errorf("Apply() after the restore = first %v, %v, want a first apply", again.FirstApply, err)
	}
}

func TestRestoreProjectAfterADoctorFixLeavesTheProjectAsBefore(t *testing.T) {
	project, home, g := applyRepo(t)
	writeFile(t, at(project, "README.md"), "# site\n")
	before, homeBefore := files(t, project), homeFiles(t, home)
	fixExclude(t, g, project, home)
	res, err := restoreProject(t, g, project, home, nil)
	if err != nil {
		t.Fatalf("RestoreProject() after doctor --fix error = %v", err)
	}
	if len(res.Conflicts) != 0 || res.Backup == nil || !res.Exclude.Removed {
		t.Errorf("RestoreProject() = %+v, want no conflicts, a backup and the exclude block removed", res)
	}
	if got := files(t, project); !reflect.DeepEqual(got, before) {
		t.Errorf("project = %v\nwant %v", got, before)
	}
	if got := homeFiles(t, home); !reflect.DeepEqual(got, homeBefore) {
		t.Errorf("home = %v\nwant %v", got, homeBefore)
	}
}

func mapValues(m map[string]string) []string {
	var out []string
	for _, v := range m {
		out = append(out, v)
	}
	return out
}

func TestRestoreProjectRemovesTheProjectTools(t *testing.T) {
	project, home, g := applyRepo(t)
	before := files(t, project)
	withTool := map[string]string{".claude/rules/web.md": "# web\n", ".dotai/tools/check.sh": "echo ok\n"}
	if _, err := applyWith(t, newClockFS(t), g, withTool, project, home, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(at(project, ".dotai/tools/check.sh")); err != nil {
		t.Fatalf("the tool was not installed: %v", err)
	}
	if _, err := restoreProject(t, g, project, home, nil); err != nil {
		t.Fatalf("RestoreProject() error = %v", err)
	}
	if got := files(t, project); !reflect.DeepEqual(got, before) {
		t.Errorf("project = %v\nwant %v", got, before)
	}
}

func TestRestoreProjectPutsBackAFileTheApplyOverwrote(t *testing.T) {
	project, home, g := applyRepo(t)
	writeFile(t, at(project, ".claude/rules/web.md"), "# the user's\n")
	before := files(t, project)
	if _, err := applyWith(t, newClockFS(t), g, profileTree, project, home, yes); err != nil {
		t.Fatal(err)
	}
	if _, err := restoreProject(t, g, project, home, nil); err != nil {
		t.Fatal(err)
	}
	if got := files(t, project); !reflect.DeepEqual(got, before) {
		t.Errorf("project = %v\nwant %v", got, before)
	}
}

func TestRestoreProjectNeedsConsentForWhatChangedSince(t *testing.T) {
	project, home, g := applyRepo(t)
	if _, err := applyWith(t, newClockFS(t), g, profileTree, project, home, nil); err != nil {
		t.Fatal(err)
	}
	writeFile(t, at(project, ".claude/rules/web.md"), "# edited since\n")
	writeFile(t, at(project, ".claude/settings.local.json"), `{"model":"haiku","theme":"dark"}`)
	after := files(t, project)
	_, err := restoreProject(t, g, project, home, nil)
	var ce *app.ConflictError
	if !errors.As(err, &ce) || len(ce.Conflicts) != 2 {
		t.Fatalf("RestoreProject() error = %v, want 2 conflicts", err)
	}
	if got := files(t, project); !reflect.DeepEqual(got, after) {
		t.Error("a refused restore wrote something")
	}
	res, err := restoreProject(t, g, project, home, func(app.ProjectRestoreResult) (bool, error) { return true, nil })
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(at(project, ".claude/rules/web.md")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("web.md = %v, want it removed with consent", err)
	}
	if got, _ := os.ReadFile(at(project, ".claude/settings.local.json")); string(got) != `{"theme":"dark"}` {
		t.Errorf("settings.local.json = %s, want the foreign key kept and dotai's removed", got)
	}
	if !strings.Contains(strings.Join(mapValues(files(t, res.Backup.Path)), "\n"), "# edited since\n") {
		t.Error("the restore backup lacks the file it removed")
	}
}

func TestRestoreProjectWithoutARecord(t *testing.T) {
	project, home, g := applyRepo(t)
	if _, err := restoreProject(t, g, project, home, nil); err == nil || !strings.Contains(err.Error(), "nothing to restore") {
		t.Errorf("RestoreProject() error = %v, want nothing to restore", err)
	}
	if _, err := os.Lstat(at(home, ".dotai")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("~/.dotai = %v, want nothing written", err)
	}
}
