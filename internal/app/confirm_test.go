package app_test

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/lartweib/dotai/internal/app"
)

// errChanged is what a Confirm answers when the plan is not the one the
// user saw.
var errChanged = errors.New("the plan changed")

// everyCommand finds every harness's command.
func everyCommand(name string) (string, error) {
	return filepath.Join(string(filepath.Separator)+"bin", name), nil
}

// noDotai fails t when home holds ~/.dotai.
func noDotai(t *testing.T, home string) {
	t.Helper()
	if _, err := os.Lstat(at(home, ".dotai")); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("~/.dotai = %v, want nothing written", err)
	}
}

func TestInstallAsksConfirmWithItsPlanBeforeItWrites(t *testing.T) {
	home, _ := installHome(t)
	f := &fakeRulesync{t: t}
	h := app.NewHost(everyCommand, nil, "")
	ctx := context.Background()
	want, err := app.PlanInstallHost(ctx, app.OSInstallFS{}, h, f.fetch, f.run, examplePack, home, app.InstallOptions{})
	if err != nil {
		t.Fatal(err)
	}
	var got []app.InstallPlan
	opts := app.InstallOptions{Confirm: func(p app.InstallPlan) error {
		got = append(got, p)
		return errChanged
	}}
	if _, err := app.InstallHost(ctx, newClockFS(t), h, f.fetch, f.run, examplePack, home, opts); !errors.Is(err, errChanged) {
		t.Fatalf("Install() error = %v, want the error of Confirm", err)
	}
	if len(got) != 1 || !reflect.DeepEqual(got[0], want) {
		t.Errorf("Confirm got %+v, want once the plan of PlanInstall %+v", got, want)
	}
	noDotai(t, home)
	opts.Confirm = func(app.InstallPlan) error { return nil }
	if res, err := app.InstallHost(ctx, newClockFS(t), h, f.fetch, f.run, examplePack, home, opts); err != nil || res.Backup == nil {
		t.Errorf("Install() with a Confirm that agrees = %+v, %v, want the install", res, err)
	}
}

func TestApplyAsksConfirmWithItsPlanBeforeItWrites(t *testing.T) {
	project, home, g := applyRepo(t)
	f := &treeRulesync{t: t, tree: profileTree}
	ctx := context.Background()
	opts := app.ApplyOptions{Profiles: []string{"web"}}
	want, err := app.PlanProjectWith(ctx, app.OSInstallFS{}, g, f.fetch, f.run, examplePack, project, home, opts)
	if err != nil {
		t.Fatal(err)
	}
	before := files(t, project)
	var got []app.ProjectPlan
	opts.Confirm = func(p app.ProjectPlan) error {
		got = append(got, p)
		return errChanged
	}
	if _, err := app.ApplyWith(ctx, newClockFS(t), g, f.fetch, f.run, examplePack, project, home, opts, yes); !errors.Is(err, errChanged) {
		t.Fatalf("Apply() error = %v, want the error of Confirm", err)
	}
	if len(got) != 1 || !reflect.DeepEqual(got[0], want) {
		t.Errorf("Confirm got %+v, want once the plan of PlanProject %+v", got, want)
	}
	if after := files(t, project); !reflect.DeepEqual(after, before) {
		t.Errorf("project = %v, want it as before %v", after, before)
	}
	noDotai(t, home)
}

func TestRestoreAsksConfirmWithItsPreviewBeforeItWrites(t *testing.T) {
	home, _ := installHome(t)
	if _, err := quiet(t).install(newClockFS(t), examplePack, home); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	want, err := app.PreviewRestore(ctx, home, "")
	if err != nil {
		t.Fatal(err)
	}
	before := files(t, home)
	var got []app.RestoreResult
	opts := app.RestoreOptions{Confirm: func(r app.RestoreResult) error {
		got = append(got, r)
		return errChanged
	}}
	if _, err := app.RestoreOptsWith(ctx, newClockFS(t), home, "", opts, nil); !errors.Is(err, errChanged) {
		t.Fatalf("Restore() error = %v, want the error of Confirm", err)
	}
	if len(got) != 1 || !reflect.DeepEqual(got[0], want) {
		t.Errorf("Confirm got %+v, want once the preview %+v", got, want)
	}
	if after := files(t, home); !reflect.DeepEqual(after, before) {
		t.Errorf("home = %v, want it as before %v", after, before)
	}
}

func TestRestoreProjectAsksConfirmWithItsPreviewBeforeItWrites(t *testing.T) {
	project, home, g := applyRepo(t)
	if _, err := applyWith(t, newClockFS(t), g, profileTree, project, home, nil); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	want, err := app.PreviewProjectRestoreWith(ctx, newClockFS(t), g, project, home)
	if err != nil {
		t.Fatal(err)
	}
	beforeProject, beforeHome := files(t, project), files(t, home)
	var got []app.ProjectRestoreResult
	opts := app.ProjectRestoreOptions{Confirm: func(r app.ProjectRestoreResult) error {
		got = append(got, r)
		return errChanged
	}}
	if _, err := app.RestoreProjectOptsWith(ctx, newClockFS(t), g, project, home, opts, nil); !errors.Is(err, errChanged) {
		t.Fatalf("RestoreProject() error = %v, want the error of Confirm", err)
	}
	if len(got) != 1 || !reflect.DeepEqual(got[0], want) {
		t.Errorf("Confirm got %+v, want once the preview %+v", got, want)
	}
	if !reflect.DeepEqual(files(t, project), beforeProject) || !reflect.DeepEqual(files(t, home), beforeHome) {
		t.Errorf("the project or the home changed, want both as before")
	}
}

func TestDoctorFixAsksConfirmWithWhatItFoundBeforeItFixes(t *testing.T) {
	project, home, g := applyRepo(t)
	if _, err := applyWith(t, newClockFS(t), g, profileTree, project, home, nil); err != nil {
		t.Fatal(err)
	}
	writeFile(t, at(project, ".git/info/exclude"), "# the user's own\n*.log\n")
	ctx := context.Background()
	want := runDoctor(t, g, home, false)
	if want.Problems() == 0 {
		t.Fatalf("Doctor() = %+v, want the missing exclude block", want)
	}
	beforeProject, beforeHome := files(t, project), files(t, home)
	var got []app.DoctorResult
	opts := app.DoctorOptions{Fix: true, Confirm: func(r app.DoctorResult) error {
		got = append(got, r)
		return errChanged
	}}
	if _, err := app.DoctorWith(ctx, newClockFS(t), g, home, opts); !errors.Is(err, errChanged) {
		t.Fatalf("Doctor(--fix) error = %v, want the error of Confirm", err)
	}
	if len(got) != 1 || !reflect.DeepEqual(got[0], want) {
		t.Errorf("Confirm got %+v, want once what doctor found %+v", got, want)
	}
	if !reflect.DeepEqual(files(t, project), beforeProject) || !reflect.DeepEqual(files(t, home), beforeHome) {
		t.Errorf("the project or the home changed, want both as before")
	}
	opts.Confirm = func(app.DoctorResult) error { t.Error("Confirm asked without --fix"); return nil }
	opts.Fix = false
	if _, err := app.DoctorWith(ctx, newClockFS(t), g, home, opts); err != nil {
		t.Errorf("Doctor() = %v", err)
	}
}

func TestApplyCompletesTheRecoveryOfAnInterruptedApplyBeforeConfirm(t *testing.T) {
	project, home, g := applyRepo(t)
	before := files(t, project)
	if _, err := applyWith(t, &failRecordFS{clockFS: newClockFS(t), project: project}, g, profileTree, project, home, nil); err == nil {
		t.Fatal("Apply() through a failing record succeeded, want an interrupted apply")
	}
	f := &treeRulesync{t: t, tree: profileTree}
	opts := app.ApplyOptions{Profiles: []string{"web"}, Confirm: func(app.ProjectPlan) error { return errChanged }}
	if _, err := app.ApplyWith(context.Background(), newClockFS(t), g, f.fetch, f.run, examplePack, project, home, opts, yes); !errors.Is(err, errChanged) {
		t.Fatalf("Apply() error = %v, want the error of Confirm", err)
	}
	// The interrupted apply is rolled back, and nothing of the new plan is written.
	if after := files(t, project); !reflect.DeepEqual(after, before) {
		t.Errorf("project = %v, want it as before the interrupted apply %v", after, before)
	}
	if _, err := planProject(t, g, examplePack, project, home); err != nil {
		t.Errorf("dry run after the recovery = %v, want no journal left", err)
	}
}
