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

// wantParity previews the restore of id in home, then carries it out with
// consent, and checks that the preview planned what the restore did.
func wantParity(t *testing.T, home, id string, wantUninstalled bool) {
	t.Helper()
	preview, err := app.PreviewRestore(context.Background(), home, id)
	if err != nil {
		t.Fatalf("PreviewRestore(%q) error = %v", id, err)
	}
	res, err := restore(newClockFS(t), home, id, func(app.RestoreResult) (bool, error) { return true, nil })
	if err != nil {
		t.Fatalf("Restore(%q) error = %v", id, err)
	}
	if !reflect.DeepEqual(preview.Ops, res.Ops) || !reflect.DeepEqual(preview.Conflicts, res.Conflicts) ||
		!reflect.DeepEqual(preview.Undone, res.Undone) || !reflect.DeepEqual(preview.Wipes, res.Wipes) || !reflect.DeepEqual(preview.WipeKeys, res.WipeKeys) {
		t.Errorf("PreviewRestore(%q) = %+v\nwant what the restore did %+v", id, preview, res)
	}
	if preview.Uninstalled != res.Uninstalled || res.Uninstalled != wantUninstalled {
		t.Errorf("uninstalled: preview %v, restore %v, want %v", preview.Uninstalled, res.Uninstalled, wantUninstalled)
	}
}

// TestPreviewRestorePlansWhatRestoreDoes checks, after a chain of
// installs, that the preview of a restore plans what the restore does:
// the restore of the last install while an install is left, of an id in
// the middle of the chain with a file changed since, and back to the
// first install, which uninstalls.
func TestPreviewRestorePlansWhatRestoreDoes(t *testing.T) {
	for _, tt := range []struct {
		name            string
		variants        []string
		pick            int // the install whose backup to restore, or -1 for the last
		edit            bool
		wantUninstalled bool
	}{
		{"when an install is left", []string{"standalone", "tmux"}, -1, false, false},
		{"for an id mid chain", []string{"standalone", "tmux", "standalone"}, 1, true, false},
		{"back to the first install", []string{"standalone", "tmux"}, 0, false, true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			home, _ := installHome(t)
			d := newClockFS(t)
			var ids []string
			for _, variant := range tt.variants {
				res, err := app.InstallWith(context.Background(), d, quiet(t).fetch, quiet(t).run, examplePack, variant, home)
				if err != nil {
					t.Fatal(err)
				}
				if res.Backup == nil {
					t.Fatalf("the %s install took no backup", variant)
				}
				ids = append(ids, res.Backup.ID)
			}
			if tt.edit {
				writeFile(t, at(home, ".dotai/tools/hello.sh"), "echo mine\n")
			}
			id := ""
			if tt.pick >= 0 {
				id = ids[tt.pick]
			}
			wantParity(t, home, id, tt.wantUninstalled)
		})
	}
}
