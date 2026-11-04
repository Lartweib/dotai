package app_test

import (
	"context"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/lartweib/dotai/internal/app"
)

func TestListBackupsOfAFreshHomeIsEmpty(t *testing.T) {
	home := t.TempDir()
	l, err := app.ListBackups(home)
	if err != nil {
		t.Fatalf("ListBackups() error = %v", err)
	}
	if l.Home == nil || len(l.Home) != 0 || l.Projects == nil || len(l.Projects) != 0 {
		t.Errorf("ListBackups() = %#v, want empty lists", l)
	}
}

func TestListBackupsGivesEachHomeBackupNewestFirst(t *testing.T) {
	home, _ := installHome(t)
	d := newClockFS(t)
	first, err := app.InstallWith(context.Background(), d, quiet(t).fetch, quiet(t).run, examplePack, "standalone", home)
	if err != nil {
		t.Fatal(err)
	}
	second, err := app.InstallWith(context.Background(), d, quiet(t).fetch, quiet(t).run, examplePack, "tmux", home)
	if err != nil {
		t.Fatal(err)
	}
	before := files(t, home)
	l, err := app.ListBackups(home)
	if err != nil {
		t.Fatalf("ListBackups() error = %v", err)
	}
	if got := files(t, home); !reflect.DeepEqual(got, before) {
		t.Error("ListBackups() wrote into the home directory")
	}
	if len(l.Home) != 2 || l.Home[0].ID != second.Backup.ID || l.Home[1].ID != first.Backup.ID {
		t.Fatalf("ListBackups() home = %+v, want %s then %s", l.Home, second.Backup.ID, first.Backup.ID)
	}
	newest := l.Home[0]
	if !newest.Valid || newest.Error != "" || newest.Operation != "install" || newest.Path != second.Backup.Path || newest.CreatedAt.IsZero() {
		t.Errorf("newest = %+v, want a valid install backup at %s", newest, second.Backup.Path)
	}
	if !newest.Head || l.Home[1].Head {
		t.Errorf("head = %v, %v, want only the backup the install state names", newest.Head, l.Home[1].Head)
	}
	if newest.Pack == nil || newest.Pack.Name != "minimal-pack" || newest.Variant != "tmux" || newest.Mode != app.ModeIsolated || len(newest.Harnesses) == 0 {
		t.Errorf("newest = %+v, want what the install installed", newest)
	}
	if len(newest.Targets) == 0 || !slices.IsSorted(newest.Targets) {
		t.Errorf("newest targets = %v, want the files the variant switch wrote, sorted", newest.Targets)
	}
	if len(l.Home[1].Targets) == 0 || !slices.IsSorted(l.Home[1].Targets) {
		t.Errorf("first targets = %v, want the files it created, sorted", l.Home[1].Targets)
	}
}

func TestListBackupsReportsACorruptBackupAndGoesOn(t *testing.T) {
	home, _ := installHome(t)
	res, err := quiet(t).install(newClockFS(t), examplePack, home)
	if err != nil {
		t.Fatal(err)
	}
	broken := "20261028T090000.000Z_install"
	writeFile(t, at(home, ".dotai/backups/"+broken+"/manifest.json"), "{not json")
	writeFile(t, at(home, ".dotai/backups/not-a-backup/manifest.json"), "{}")
	incomplete := "20261028T080000.000Z_install"
	writeFile(t, at(home, ".dotai/backups/"+incomplete+"/files/x"), "x")
	l, err := app.ListBackups(home)
	if err != nil {
		t.Fatalf("ListBackups() error = %v, want the corrupt backup reported in the list", err)
	}
	if len(l.Home) != 3 || l.Home[0].ID != res.Backup.ID || !l.Home[0].Valid {
		t.Fatalf("ListBackups() home = %+v, want the good backup first, then the two bad ones", l.Home)
	}
	for _, b := range l.Home[1:] {
		if b.Valid || b.Error == "" || b.Operation != "install" || b.CreatedAt.IsZero() || b.Targets == nil {
			t.Errorf("backup %+v, want it invalid with why, and its operation and time from its id", b)
		}
	}
	if !strings.Contains(l.Home[2].Error, "manifest") {
		t.Errorf("incomplete backup error = %q, want it to name the missing manifest", l.Home[2].Error)
	}
}

func TestListBackupsGivesTheBackupsOfEachProject(t *testing.T) {
	project, home, g := applyRepo(t)
	writeFile(t, at(project, ".claude/settings.local.json"), `{"theme":"dark"}`)
	if _, err := applyWith(t, newClockFS(t), g, profileTree, project, home, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := restoreProject(t, g, project, home, nil); err != nil {
		t.Fatal(err)
	}
	l, err := app.ListBackups(home)
	if err != nil {
		t.Fatalf("ListBackups() error = %v", err)
	}
	if len(l.Projects) != 1 || l.Projects[0].Project != project || len(l.Projects[0].Backups) != 2 {
		t.Fatalf("ListBackups() projects = %+v, want the apply and the restore of %s", l.Projects, project)
	}
	restored, applied := l.Projects[0].Backups[0], l.Projects[0].Backups[1]
	if restored.Operation != "restore" || applied.Operation != "apply" || !restored.Valid || !applied.Valid {
		t.Errorf("project backups = %+v, want the restore then the apply, both valid", l.Projects[0].Backups)
	}
	if !slices.Contains(applied.Targets, at(project, ".claude/settings.local.json")) {
		t.Errorf("apply backup targets = %v, want the file it overwrote", applied.Targets)
	}
}
