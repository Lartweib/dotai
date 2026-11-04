package app_test

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/lartweib/dotai/internal/app"
)

func TestPreviewRestoreGivesThePlanAndWritesNothing(t *testing.T) {
	home, _ := installHome(t)
	if _, err := quiet(t).install(newClockFS(t), examplePack, home); err != nil {
		t.Fatal(err)
	}
	before := files(t, home)
	preview, err := app.PreviewRestore(context.Background(), home, "")
	if err != nil {
		t.Fatalf("PreviewRestore() error = %v", err)
	}
	if got := files(t, home); !reflect.DeepEqual(got, before) {
		t.Fatal("PreviewRestore() wrote into the home directory")
	}
	if preview.Backup != nil || len(preview.Undone) != 1 || len(preview.Conflicts) != 0 {
		t.Errorf("PreviewRestore() = %+v, want the install undone, no backup and no conflict", preview)
	}
	res, err := restore(newClockFS(t), home, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(preview.Ops, res.Ops) || !reflect.DeepEqual(preview.Undone, res.Undone) || preview.Uninstalled != res.Uninstalled {
		t.Errorf("PreviewRestore() ops = %+v\nwant what the restore did %+v", preview.Ops, res.Ops)
	}
}

func TestPreviewRestoreListsTheConflictsWithoutRefusing(t *testing.T) {
	home, _ := installHome(t)
	if _, err := quiet(t).install(newClockFS(t), examplePack, home); err != nil {
		t.Fatal(err)
	}
	edited := at(home, ".dotai/tools/hello.sh")
	writeFile(t, edited, "echo mine\n")
	before := files(t, home)
	preview, err := app.PreviewRestore(context.Background(), home, "")
	if err != nil {
		t.Fatalf("PreviewRestore() error = %v, want the conflicts in the preview", err)
	}
	if len(preview.Conflicts) != 1 || preview.Conflicts[0].Target != edited {
		t.Errorf("PreviewRestore() conflicts = %+v, want %s", preview.Conflicts, edited)
	}
	if got := files(t, home); !reflect.DeepEqual(got, before) {
		t.Error("PreviewRestore() wrote into the home directory")
	}
}

func TestPreviewRestoreRefusesWhatRestoreRefuses(t *testing.T) {
	home, _ := installHome(t)
	if _, err := app.PreviewRestore(context.Background(), home, ""); err == nil || !strings.Contains(err.Error(), "nothing to restore") {
		t.Errorf("PreviewRestore() error = %v, want nothing to restore", err)
	}
	if _, err := app.PreviewRestore(context.Background(), home, "nope"); err == nil {
		t.Error("PreviewRestore() of a name that is not a backup id succeeded")
	} else if _, ok := err.(*app.UsageError); !ok {
		t.Errorf("PreviewRestore() error = %T %v, want a usage error", err, err)
	}
}

func TestPreviewProjectRestoreGivesThePlanAndWritesNothing(t *testing.T) {
	project, home, g := applyRepo(t)
	if _, err := applyWith(t, newClockFS(t), g, profileTree, project, home, nil); err != nil {
		t.Fatal(err)
	}
	writeFile(t, at(project, ".claude/rules/web.md"), "# edited since\n")
	projectBefore, homeBefore := files(t, project), files(t, home)
	preview, err := app.PreviewProjectRestoreWith(context.Background(), app.OSInstallFS{}, g, project, home)
	if err != nil {
		t.Fatalf("PreviewProjectRestore() error = %v", err)
	}
	if preview.Project != project || preview.Backup != nil || len(preview.Ops) == 0 || len(preview.Conflicts) != 1 || !preview.Exclude.Removed {
		t.Errorf("PreviewProjectRestore() = %+v, want the plan with one conflict and no backup", preview)
	}
	if !reflect.DeepEqual(files(t, project), projectBefore) || !reflect.DeepEqual(files(t, home), homeBefore) {
		t.Error("PreviewProjectRestore() wrote something")
	}
	res, err := restoreProject(t, g, project, home, func(app.ProjectRestoreResult) (bool, error) { return true, nil })
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(preview.Ops, res.Ops) || !reflect.DeepEqual(preview.Conflicts, res.Conflicts) {
		t.Errorf("PreviewProjectRestore() = %+v\nwant what the restore did %+v", preview.Ops, res.Ops)
	}
}

func TestPreviewProjectRestoreWithoutARecord(t *testing.T) {
	project, home, g := applyRepo(t)
	if _, err := app.PreviewProjectRestoreWith(context.Background(), app.OSInstallFS{}, g, project, home); err == nil || !strings.Contains(err.Error(), "nothing to restore") {
		t.Errorf("PreviewProjectRestore() error = %v, want nothing to restore", err)
	}
}
