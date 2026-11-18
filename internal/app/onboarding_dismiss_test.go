package app_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/lartweib/dotai/internal/app"
	"github.com/lartweib/dotai/internal/state"
)

// dismissRepo is a project in a git repository, with a home where the
// example pack is installed.
func dismissRepo(t *testing.T) (project, home string, g *fakeGit) {
	t.Helper()
	project, home, g = applyRepo(t)
	gitDir(t, at(project, ".git")) // where the hook finds the repository
	writeState(t, at(home, ".dotai/state.json"), state.State{Schema: state.Schema, Pack: state.Pack{Name: "minimal-pack", Version: "0.1.0", Path: absPath(t, examplePack)},
		Mode: app.ModeIsolated, Variant: "standalone", Harnesses: []string{"claudecode"}, Files: []state.File{}})
	g.files = []string{"README.md"}
	writeFile(t, at(project, "README.md"), "# site\n")
	return project, home, g
}

func dismiss(t *testing.T, g app.GitPort, project, home string) (app.DismissResult, error) {
	t.Helper()
	return app.DismissOnboardingWith(context.Background(), newClockFS(t), g, project, home)
}

func TestDismissInANeverAppliedRepository(t *testing.T) {
	project, home, g := dismissRepo(t)
	before := files(t, project)
	res, err := dismiss(t, g, project, home)
	if err != nil {
		t.Fatalf("DismissOnboarding() error = %v", err)
	}
	if res.Project != project || res.Onboarding != at(project, ".dotai/onboarding.json") || res.Backup == nil || res.Unchanged {
		t.Errorf("DismissOnboarding() = %+v, want the record written with a backup", res)
	}
	o, err := state.DecodeOnboarding([]byte(files(t, project)[".dotai/onboarding.json"]))
	if err != nil || o.Source != state.OnboardingDismissed || o.Pack.ID != "local/minimal-pack" || len(o.Hinted) != 0 {
		t.Errorf("onboarding record = %+v, %v, want a dismissal of the example pack", o, err)
	}
	if ex := files(t, project)[".git/info/exclude"]; !strings.Contains(ex, "/.dotai/") {
		t.Errorf("exclude = %q, want .dotai/ in dotai's block", ex)
	}
	r, err := state.DecodeProject([]byte(readFileText(t, res.Record)), project)
	if err != nil || !r.DismissOnly || len(r.Profiles) != 0 || len(r.Files) != 0 || len(r.Backups) != 1 {
		t.Errorf("record = %+v, %v, want a dismissal with no profile and one backup", r, err)
	}
	if _, err := os.Lstat(at(project, ".dotai/state.json")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("a dismissal wrote the project marker: %v", err)
	}
	if line, err := app.OnboardingHookWith(context.Background(), g, project, home); err != nil || line != "" {
		t.Errorf("hook = %q, %v after the dismissal, want silence", line, err)
	}
	again, err := dismiss(t, g, project, home)
	if err != nil || !again.Unchanged || again.Backup != nil {
		t.Errorf("DismissOnboarding() again = %+v, %v, want nothing to write", again, err)
	}
	doc, err := app.DoctorWith(context.Background(), newClockFS(t), g, home, app.DoctorOptions{})
	if err != nil || doc.Problems() != 0 {
		t.Errorf("Doctor() = %+v, %v, want no finding after a dismissal", doc.Findings, err)
	}
	if _, err := restoreProject(t, g, project, home, nil); err != nil {
		t.Fatalf("RestoreProject() error = %v", err)
	}
	if got := files(t, project); !reflect.DeepEqual(got, before) {
		t.Errorf("project after restore = %v\nwant %v", got, before)
	}
}

func TestDismissAfterAnApplyKeepsTheApply(t *testing.T) {
	project, home, g := dismissRepo(t)
	if _, err := applyWith(t, newClockFS(t), g, profileTree, project, home, nil); err != nil {
		t.Fatal(err)
	}
	applied := files(t, project)
	g.files = []string{"README.md", "package.json"} // the files now suggest web
	if line, _ := app.OnboardingHookWith(context.Background(), g, project, home); !strings.Contains(line, "no longer fit") {
		t.Fatalf("hook = %q, want a mismatch before the dismissal", line)
	}
	res, err := dismiss(t, g, project, home)
	if err != nil || res.Backup == nil {
		t.Fatalf("DismissOnboarding() = %+v, %v", res, err)
	}
	after := files(t, project)
	for name, data := range applied {
		if name != ".dotai/onboarding.json" && after[name] != data {
			t.Errorf("%s changed with the dismissal", name)
		}
	}
	r, err := state.DecodeProject([]byte(readFileText(t, res.Record)), project)
	if err != nil || r.DismissOnly || !reflect.DeepEqual(r.Profiles, []string{"web"}) || len(r.Files) == 0 || len(r.Backups) != 2 {
		t.Errorf("record = %+v, %v, want the apply's, with the dismissal's backup", r, err)
	}
	if line, err := app.OnboardingHookWith(context.Background(), g, project, home); err != nil || line != "" {
		t.Errorf("hook = %q, %v after the dismissal, want silence", line, err)
	}
	if doc, err := app.DoctorWith(context.Background(), newClockFS(t), g, home, app.DoctorOptions{}); err != nil || doc.Problems() != 0 {
		t.Errorf("Doctor() = %+v, %v, want no finding", doc.Findings, err)
	}
}

func TestDismissRefusals(t *testing.T) {
	t.Run("outside a git repository", func(t *testing.T) {
		project, home, g := dismissRepo(t)
		g.outside = true
		var u *app.UsageError
		if _, err := dismiss(t, g, project, home); !errors.As(err, &u) {
			t.Errorf("DismissOnboarding() error = %v, want a usage error", err)
		}
	})
	t.Run("in the home directory", func(t *testing.T) {
		_, home, g := dismissRepo(t)
		var r *app.ProjectRefusedError
		if _, err := dismiss(t, g, home, home); !errors.As(err, &r) {
			t.Errorf("DismissOnboarding() error = %v, want the project refused", err)
		}
	})
	t.Run("without an installed pack", func(t *testing.T) {
		project, home, g := dismissRepo(t)
		if err := os.Remove(at(home, ".dotai/state.json")); err != nil {
			t.Fatal(err)
		}
		var u *app.UsageError
		if _, err := dismiss(t, g, project, home); !errors.As(err, &u) {
			t.Errorf("DismissOnboarding() error = %v, want a usage error", err)
		}
	})
	t.Run("a pack that replaces the onboarding", func(t *testing.T) {
		project, home, g := dismissRepo(t)
		packDir := copyPack(t)
		m, err := os.ReadFile(filepath.Join(packDir, "dotai.yaml"))
		if err != nil {
			t.Fatal(err)
		}
		writeFile(t, filepath.Join(packDir, "dotai.yaml"), string(m)+"onboarding:\n  custom: base/.rulesync/subagents/reviewer.md\n")
		writeState(t, at(home, ".dotai/state.json"), state.State{Schema: state.Schema, Pack: state.Pack{Name: "minimal-pack", Version: "0.1.0", Path: packDir},
			Mode: app.ModeIsolated, Variant: "standalone", Harnesses: []string{"claudecode"}, Files: []state.File{}})
		var u *app.UsageError
		if _, err := dismiss(t, g, project, home); !errors.As(err, &u) || !strings.Contains(err.Error(), "replaces dotai's onboarding") {
			t.Errorf("DismissOnboarding() error = %v, want a usage error for the custom onboarding", err)
		}
		if _, err := os.Lstat(at(project, ".dotai")); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("a refused dismissal wrote .dotai/: %v", err)
		}
	})
	t.Run("a tracked onboarding record", func(t *testing.T) {
		project, home, g := dismissRepo(t)
		g.tracked = []string{".dotai/onboarding.json"}
		_, err := dismiss(t, g, project, home)
		if err == nil || !strings.Contains(err.Error(), "git rm --cached") {
			t.Errorf("DismissOnboarding() error = %v, want the tracked record refused", err)
		}
		if _, err := os.Lstat(at(project, ".dotai")); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("a refused dismissal wrote .dotai/: %v", err)
		}
	})
}

// TestDismissCarriesTheApplyRecordOver pins what a dismissal keeps of the
// apply's project record: everything but the onboarding record it adds
// to the files, its backup and its exclude entry, which the apply had.
func TestDismissCarriesTheApplyRecordOver(t *testing.T) {
	project, home, g := dismissRepo(t)
	if _, err := applyWith(t, newClockFS(t), g, profileTree, project, home, nil); err != nil {
		t.Fatal(err)
	}
	record := app.RecordPath(home, project)
	decode := func(data []byte) (state.Project, error) { return state.DecodeProject(data, project) }
	before := readJSON(t, record, decode)
	projects := readFileText(t, at(home, ".dotai/projects.json"))
	g.files = []string{"README.md", "package.json"}
	if _, err := dismiss(t, g, project, home); err != nil {
		t.Fatal(err)
	}
	after := readJSON(t, record, decode)
	if after.ID != before.ID || after.Pack != before.Pack || after.Variant != before.Variant || after.DismissOnly ||
		!reflect.DeepEqual(after.Profiles, before.Profiles) || !reflect.DeepEqual(after.Harnesses, before.Harnesses) {
		t.Errorf("record = %+v, want the apply's %+v", after, before)
	}
	if !reflect.DeepEqual(after.Exclude, before.Exclude) {
		t.Errorf("exclude = %+v, want the apply's %+v", after.Exclude, before.Exclude)
	}
	kept := func(r state.Project) map[string]state.ProjectFile {
		out := map[string]state.ProjectFile{}
		for _, f := range r.Files {
			if f.Target != at(project, ".dotai/onboarding.json") {
				out[f.Target] = f
			}
		}
		return out
	}
	if !reflect.DeepEqual(kept(after), kept(before)) {
		t.Errorf("files = %+v, want the apply's %+v", after.Files, before.Files)
	}
	if len(after.Backups) != len(before.Backups)+1 {
		t.Errorf("backups = %v, want the apply's and the dismissal's", after.Backups)
	}
	if got := readFileText(t, at(home, ".dotai/projects.json")); got != projects {
		t.Errorf("projects.json = %s, want the apply's %s", got, projects)
	}
}

func TestDoctorChecksTheOnboardingRecord(t *testing.T) {
	project, home, g := dismissRepo(t)
	if _, err := applyWith(t, newClockFS(t), g, profileTree, project, home, nil); err != nil {
		t.Fatal(err)
	}
	for name, data := range map[string]*string{"missing": nil, "not dotai's": ptr(`{"schema":"x"}`)} {
		t.Run(name, func(t *testing.T) {
			if data == nil {
				if err := os.Remove(at(project, ".dotai/onboarding.json")); err != nil {
					t.Fatal(err)
				}
			} else {
				writeFile(t, at(project, ".dotai/onboarding.json"), *data)
			}
			doc, err := app.DoctorWith(context.Background(), newClockFS(t), g, home, app.DoctorOptions{})
			if err != nil || len(doc.Findings) != 1 || doc.Findings[0].Code != app.CodeOnboardingRecordInvalid || doc.Findings[0].Fixable {
				t.Fatalf("Doctor() = %+v, %v, want the onboarding record reported", doc.Findings, err)
			}
			// --fix cannot take a fingerprint, so the message names what can.
			if msg := doc.Findings[0].Message; !strings.Contains(msg, "dotai apply") || !strings.Contains(msg, "dotai onboarding dismiss") || !strings.Contains(msg, "--fix") {
				t.Errorf("message = %q, want it to name dotai apply and dotai onboarding dismiss, not --fix", msg)
			}
		})
	}
}

func ptr(s string) *string { return &s }

func readFileText(t *testing.T, name string) string {
	t.Helper()
	data, err := os.ReadFile(name)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func absPath(t *testing.T, p string) string {
	t.Helper()
	abs, err := filepath.Abs(p)
	if err != nil {
		t.Fatal(err)
	}
	return abs
}
