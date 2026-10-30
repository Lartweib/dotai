package app_test

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"testing"

	"github.com/lartweib/dotai/internal/app"
	"github.com/lartweib/dotai/internal/backup"
	"github.com/lartweib/dotai/internal/plan"
	"github.com/lartweib/dotai/internal/rulesync"
	"github.com/lartweib/dotai/internal/state"
)

const agySkill = "---\nname: commit-message\ndescription: Writes a commit message.\n---\nWrite it.\n"

// agyRun is rulesync writing agy's skill where it writes it, under
// antigravity-cli/skills in the user scope.
func agyRun(t *testing.T) func(context.Context, rulesync.Result, []string, ...rulesync.Job) error {
	return func(_ context.Context, _ rulesync.Result, _ []string, jobs ...rulesync.Job) error {
		for _, j := range jobs {
			if j.Global {
				writeFile(t, filepath.Join(j.Dir, ".gemini", "antigravity-cli", "skills", "commit-message", "SKILL.md"), agySkill)
			}
		}
		return nil
	}
}

// oldLayoutHome is a home with an install from before agy's skills moved
// to config/skills: its state and its backup record the skill at
// antigravity-cli/skills. layout is what agy did with it since: left it
// there ("not migrated"), moved it ("migrated"), or moved it and left a
// link at the old path ("migrated with a link"). It returns the home, the
// first install's backup ID, the old and the new path of the skill and the
// state as seeded.
func oldLayoutHome(t *testing.T, d app.ExecuteFS, layout string) (home, first, old, moved string, seeded []byte) {
	t.Helper()
	home, _ = installHome(t)
	res, err := app.InstallWith(context.Background(), d, quiet(t).fetch, agyRun(t), examplePack, "", home)
	if err != nil {
		t.Fatal(err)
	}
	old = at(home, ".dotai/isolated/gemini/antigravity-cli/skills/commit-message/SKILL.md")
	moved = at(home, ".dotai/isolated/gemini/config/skills/commit-message/SKILL.md")

	s := readJSON(t, at(home, ".dotai/state.json"), state.Decode)
	for i := range s.Files {
		if s.Files[i].Target == moved {
			s.Files[i].Target = old
		}
	}
	data, err := state.Encode(s)
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, at(home, ".dotai/state.json"), string(data))
	m := manifest(t, home, res.Backup.ID)
	for i := range m.Ops {
		if m.Ops[i].Target == moved {
			m.Ops[i].Target = old
		}
	}
	mdata, err := backup.Encode(m)
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(res.Backup.Path, "manifest.json"), string(mdata))

	switch layout {
	case "not migrated":
		writeFile(t, old, agySkill)
		if err := os.RemoveAll(at(home, ".dotai/isolated/gemini/config/skills")); err != nil {
			t.Fatal(err)
		}
	case "migrated":
	case "migrated with a link":
		target, link := at(home, ".dotai/isolated/gemini/config/skills"), at(home, ".dotai/isolated/gemini/antigravity-cli/skills")
		if err := os.Symlink(target, link); err != nil {
			// A junction, where Windows does not let links be created.
			if runtime.GOOS != "windows" || exec.Command("cmd", "/c", "mklink", "/J", link, target).Run() != nil {
				t.Skipf("cannot create a link: %v", err)
			}
		}
	}
	return home, res.Backup.ID, old, moved, data
}

// TestInstallOverTheOldAgySkillsLayout checks that an install, its dry
// run and the restores of both installs work whatever agy did with the
// skills an older install put at antigravity-cli/skills, and that nothing
// is removed through a link agy left there.
func TestInstallOverTheOldAgySkillsLayout(t *testing.T) {
	for _, layout := range []string{"not migrated", "migrated", "migrated with a link"} {
		t.Run(layout, func(t *testing.T) {
			d := newClockFS(t)
			home, first, old, moved, seeded := oldLayoutHome(t, d, layout)
			link := at(home, ".dotai/isolated/gemini/antigravity-cli/skills")

			p, err := app.PlanInstallWith(context.Background(), app.OSInstallFS{}, quiet(t).fetch, agyRun(t), examplePack, "", home)
			if err != nil || len(p.Diagnostics) != 0 {
				t.Fatalf("PlanInstall() = %+v, %v", p.Diagnostics, err)
			}
			removesOld := slices.ContainsFunc(p.Ops, func(op app.InstallOp) bool { return op.Target == old && op.Action == plan.Remove })
			if want := layout == "not migrated"; removesOld != want || slices.ContainsFunc(p.Ops, func(op app.InstallOp) bool { return op.Target == old && op.Action != plan.Remove }) {
				t.Errorf("dry run ops = %+v, want the old skill removed: %t", p.Ops, want)
			}
			if !slices.ContainsFunc(p.Ops, func(op app.InstallOp) bool { return op.Target == moved }) {
				t.Errorf("dry run ops = %+v, want the skill at %s", p.Ops, moved)
			}

			if _, err := app.InstallWith(context.Background(), d, quiet(t).fetch, agyRun(t), examplePack, "", home); err != nil {
				t.Fatalf("Install() error = %v", err)
			}
			s := readJSON(t, at(home, ".dotai/state.json"), state.Decode)
			targets := []string{}
			for _, f := range s.Files {
				targets = append(targets, f.Target)
			}
			if slices.Contains(targets, old) || !slices.Contains(targets, moved) {
				t.Errorf("state targets = %q, want %s and not %s", targets, moved, old)
			}
			if got, err := os.ReadFile(moved); err != nil || string(got) != agySkill {
				t.Errorf("skill = %q, %v, want it at %s", got, err, moved)
			}
			if layout == "not migrated" {
				if _, err := os.Stat(old); !errors.Is(err, fs.ErrNotExist) {
					t.Errorf("old skill = %v, want it removed", err)
				}
			}
			assertLink := func(when string) {
				if layout != "migrated with a link" {
					return
				}
				if info, err := os.Lstat(link); err != nil || info.Mode()&(fs.ModeSymlink|fs.ModeIrregular) == 0 {
					t.Errorf("%s: the link agy left = %v, %v, want it kept", when, info, err)
				}
			}
			assertLink("after the install")

			if layout != "not migrated" {
				// The install wrote nothing, so a restore undoes the first one,
				// whose backup records the skill at the old path.
				if _, err := restore(d, home, "", nil); err != nil {
					t.Fatalf("Restore() error = %v", err)
				}
				if _, err := os.Stat(at(home, ".dotai/state.json")); !errors.Is(err, fs.ErrNotExist) {
					t.Errorf("state = %v, want everything uninstalled", err)
				}
				assertLink("after the uninstall")
				return
			}
			if _, err := restore(d, home, "", nil); err != nil {
				t.Fatalf("Restore() error = %v", err)
			}
			if got, _ := os.ReadFile(at(home, ".dotai/state.json")); string(got) != string(seeded) {
				t.Errorf("state = %s, want the old layout's %s", got, seeded)
			}
			if got, err := os.ReadFile(old); err != nil || string(got) != agySkill {
				t.Errorf("old skill = %q, %v, want it back", got, err)
			}
			if _, err := os.Stat(moved); !errors.Is(err, fs.ErrNotExist) {
				t.Errorf("new skill = %v, want it removed", err)
			}
			if _, err := restore(d, home, first, nil); err != nil {
				t.Fatalf("Restore(%s) error = %v", first, err)
			}
			if _, err := os.Stat(old); !errors.Is(err, fs.ErrNotExist) {
				t.Errorf("old skill = %v, want it removed with the first install", err)
			}
		})
	}
}
