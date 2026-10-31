package app_test

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/lartweib/dotai/internal/app"
	"github.com/lartweib/dotai/internal/backup"
	"github.com/lartweib/dotai/internal/plan"
	"github.com/lartweib/dotai/internal/rulesync"
	"github.com/lartweib/dotai/internal/state"
)

// packSkill is the pack's own skill, which rulesync renders for every
// harness.
const packSkill = "commit-message"

// skillsRun is rulesync writing the root rule and the pack's skills named
// skills for each harness, in the user scope.
func skillsRun(t *testing.T, skills ...string) func(context.Context, rulesync.Result, []string, ...rulesync.Job) error {
	return func(_ context.Context, _ rulesync.Result, _ []string, jobs ...rulesync.Job) error {
		for _, j := range jobs {
			if !j.Global {
				continue
			}
			writeFile(t, filepath.Join(j.Dir, ".claude", "CLAUDE.md"), "# generated\n")
			for _, s := range skills {
				for _, dir := range []string{".claude/skills", ".gemini/antigravity-cli/skills", ".config/opencode/skills"} {
					writeFile(t, filepath.Join(j.Dir, filepath.FromSlash(dir), s, "SKILL.md"), "---\nname: "+s+"\n---\nThe pack's.\n")
				}
			}
		}
		return nil
	}
}

// sharedHome is a home whose shared skills folder holds a skill (alpha), a
// link to a skill kept elsewhere (beta), a file and a directory that are
// not skills, a hidden directory, and a skill named as the pack's.
func sharedHome(t *testing.T) string {
	t.Helper()
	home, _ := installHome(t)
	skills := at(home, ".agents/skills")
	writeFile(t, filepath.Join(skills, "alpha", "SKILL.md"), "alpha\n")
	elsewhere := filepath.Join(t.TempDir(), "beta")
	writeFile(t, filepath.Join(elsewhere, "SKILL.md"), "beta\n")
	makeLink(t, elsewhere, filepath.Join(skills, "beta"))
	writeFile(t, filepath.Join(skills, "notes.md"), "not a skill\n")
	if err := os.MkdirAll(filepath.Join(skills, "empty"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(skills, ".cache", "SKILL.md"), "hidden\n")
	writeFile(t, filepath.Join(skills, packSkill, "SKILL.md"), "another tool's\n")
	return home
}

// listing is every path under dir with what it is, without following a
// link: what a test compares to check that dotai left a directory alone.
func listing(t *testing.T, dir string) []string {
	t.Helper()
	var out []string
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(dir, p)
		entry := filepath.ToSlash(rel) + " " + info.Mode().Type().String()
		if info.Mode().IsRegular() {
			data, err := os.ReadFile(p)
			if err != nil {
				return err
			}
			entry += " " + sum(string(data))
		}
		out = append(out, entry)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func installShared(t *testing.T, d app.ExecuteFS, home, variant string, skills ...string) (app.InstallResult, error) {
	t.Helper()
	return app.InstallWith(context.Background(), d, quiet(t).fetch, skillsRun(t, skills...), examplePack, variant, home)
}

// linkTargets are the isolated paths of the links to the shared skills
// names, for Claude Code and agy.
func linkTargets(home string, names ...string) []string {
	var out []string
	for _, n := range names {
		out = append(out, at(home, ".dotai/isolated/claude/skills/"+n), at(home, ".dotai/isolated/gemini/config/skills/"+n))
	}
	return out
}

// assertLinks checks that each shared skill in want, and no other, is
// linked into Claude Code and agy, each link to its entry in the shared
// folder.
func assertLinks(t *testing.T, home string, want ...string) {
	t.Helper()
	for _, dir := range []string{".dotai/isolated/claude/skills", ".dotai/isolated/gemini/config/skills"} {
		entries, err := os.ReadDir(at(home, dir))
		if err != nil && !errors.Is(err, fs.ErrNotExist) {
			t.Fatal(err)
		}
		var got []string
		for _, e := range entries {
			name := filepath.Join(at(home, dir), e.Name())
			cur, err := (app.OSInstallFS{}).Link(name)
			if err != nil {
				continue // the pack's own skill
			}
			if dest := at(home, ".agents/skills/"+e.Name()); cur != plan.LinkCurrent(dest) {
				t.Errorf("%s points at %s, want %s", name, cur.Link, dest)
			}
			got = append(got, e.Name())
		}
		if !slices.Equal(got, want) {
			t.Errorf("links in %s = %v, want %v", dir, got, want)
		}
	}
}

// TestInstallLinksTheSharedSkills checks an install of a variant that
// shares skills (decision 0036): each skill of ~/.agents/skills is linked
// into Claude Code and agy, but not what is not a skill, a hidden entry or
// a skill the pack has too, which wins with a warning; the links are
// owned and backed up without a copy, and the shared folder is untouched.
func TestInstallLinksTheSharedSkills(t *testing.T) {
	home := sharedHome(t)
	before := listing(t, at(home, ".agents"))
	var warned []string
	res, err := app.InstallWarnWith(context.Background(), newClockFS(t), quiet(t).fetch, skillsRun(t, packSkill), examplePack, "shared", home,
		func(w string) { warned = append(warned, w) })
	if err != nil || len(res.Diagnostics) != 0 {
		t.Fatalf("Install() = %+v, %v", res.Diagnostics, err)
	}
	assertLinks(t, home, "alpha", "beta")
	for _, h := range []string{"claudecode", "antigravity-cli"} {
		want := "the shared skill " + packSkill + " is not linked into " + h + ": the pack has a skill of that name, which wins"
		if !slices.Contains(warned, want) || !slices.Contains(res.Warnings, want) {
			t.Errorf("warnings = %q, %q, want %q", warned, res.Warnings, want)
		}
	}
	if data, err := os.ReadFile(at(home, ".dotai/isolated/claude/skills/"+packSkill+"/SKILL.md")); err != nil || !strings.Contains(string(data), "The pack's.") {
		t.Errorf("the pack's skill = %q, %v, want it installed", data, err)
	}
	if data, err := os.ReadFile(at(home, ".dotai/isolated/claude/skills/beta/SKILL.md")); err != nil || string(data) != "beta\n" {
		t.Errorf("the skill through its link = %q, %v, want beta's", data, err)
	}
	if _, err := os.Lstat(at(home, ".dotai/isolated/opencode/config/opencode/skills/alpha")); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("OpenCode got a link: %v, want none, since it reads the shared folder itself", err)
	}
	if mode, err := os.Lstat(at(home, ".dotai/isolated/claude/skills/alpha")); err != nil ||
		runtime.GOOS == "windows" && mode.Mode()&fs.ModeIrregular == 0 || runtime.GOOS != "windows" && mode.Mode()&fs.ModeSymlink == 0 {
		t.Errorf("the link = %v, %v, want a junction on Windows and a symbolic link elsewhere", mode, err)
	}

	sp := res.SharedSkills
	wantSkipped := []app.SkippedSkill{
		{Name: packSkill, Harness: "claudecode", Reason: app.ReasonPackSkill},
		{Name: packSkill, Harness: "antigravity-cli", Reason: app.ReasonPackSkill},
		{Name: "empty", Reason: app.ReasonNotASkill},
		{Name: "notes.md", Reason: app.ReasonNotASkill},
	}
	if sp == nil || sp.Dir != at(home, ".agents/skills") || !sp.Found || !slices.Equal(sp.Skills, []string{"alpha", "beta", packSkill}) || !reflect.DeepEqual(sp.Skipped, wantSkipped) {
		t.Errorf("shared skills = %+v, want alpha, beta and %s, skipping %+v", sp, packSkill, wantSkipped)
	}
	for _, h := range res.Harnesses {
		if !slices.ContainsFunc(h.Leaks, func(l string) bool { return strings.Contains(l, "shares") }) {
			t.Errorf("%s leaks = %q, want the shared skills named", h.ID, h.Leaks)
		}
	}

	s := readJSON(t, at(home, ".dotai/state.json"), state.Decode)
	if !s.SharedSkills {
		t.Error("state does not record that the variant shares skills")
	}
	for _, target := range linkTargets(home, "alpha", "beta") {
		f, ok := ownedFile(t, s, target)
		if !ok || f.Kind != state.KindLink || f.Link != at(home, ".agents/skills/"+filepath.Base(target)) || f.SHA256 != "" {
			t.Errorf("state record of %s = %+v, %t, want a link to its shared skill", target, f, ok)
		}
	}
	m := manifest(t, home, res.Backup.ID)
	for _, op := range m.Ops {
		if slices.Contains(linkTargets(home, "alpha", "beta"), op.Target) && (op.Action != plan.Create || op.File != "" || op.After == nil || op.After.Link == "") {
			t.Errorf("backup op = %+v, want a create of a link, without a copy", op)
		}
	}
	if after := listing(t, at(home, ".agents")); !slices.Equal(after, before) {
		t.Errorf("~/.agents = %v, want it as it was: %v", after, before)
	}
}

// TestInstallWithoutTheSharedFolderSharesNothing checks that a variant
// that shares skills installs without links, and without an error, when
// there is no shared skills folder.
func TestInstallWithoutTheSharedFolderSharesNothing(t *testing.T) {
	home, _ := installHome(t)
	res, err := installShared(t, newClockFS(t), home, "shared")
	if err != nil {
		t.Fatal(err)
	}
	if sp := res.SharedSkills; sp == nil || sp.Found || len(sp.Skills) != 0 || len(sp.Skipped) != 0 {
		t.Errorf("shared skills = %+v, want a missing folder", sp)
	}
	assertLinks(t, home)
	if _, err := os.Lstat(at(home, ".agents")); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("~/.agents = %v, want it still missing", err)
	}
	if res, err := installShared(t, newClockFS(t), home, "standalone"); err != nil || res.SharedSkills != nil {
		t.Errorf("a closed variant: shared skills = %+v, %v, want none", res.SharedSkills, err)
	}
}

// TestInstallFollowsTheSharedFolder checks the dry run and the reinstall
// after the shared folder changed: unchanged links stay, a new skill is
// linked and a removed one is unlinked; a skill added after an install
// shows up only with the next one.
func TestInstallFollowsTheSharedFolder(t *testing.T) {
	home := sharedHome(t)
	d := newClockFS(t)
	if _, err := installShared(t, d, home, "shared"); err != nil {
		t.Fatal(err)
	}
	p, err := app.PlanInstallWith(context.Background(), app.OSInstallFS{}, quiet(t).fetch, skillsRun(t), examplePack, "shared", home)
	if err != nil {
		t.Fatal(err)
	}
	for _, op := range p.Ops {
		if op.Action != plan.Unchanged {
			t.Errorf("dry run op = %+v, want every op unchanged", op)
		}
	}
	if n := countLinks(p.Ops); n != 6 {
		t.Errorf("dry run has %d links, want 6 (alpha, beta and %s for two harnesses)", n, packSkill)
	}

	writeFile(t, at(home, ".agents/skills/gamma/SKILL.md"), "gamma\n")
	assertLinks(t, home, "alpha", "beta", packSkill)
	if err := os.RemoveAll(at(home, ".agents/skills/alpha")); err != nil {
		t.Fatal(err)
	}
	res, err := installShared(t, d, home, "shared")
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]plan.Action{}
	for _, op := range res.Ops {
		if op.Action != plan.Unchanged {
			got[op.Target] = op.Action
		}
	}
	want := map[string]plan.Action{}
	for _, target := range linkTargets(home, "gamma") {
		want[target] = plan.Create
	}
	for _, target := range linkTargets(home, "alpha") {
		want[target] = plan.Remove
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("reinstall ops = %v, want %v", got, want)
	}
	assertLinks(t, home, "beta", packSkill, "gamma")
}

func countLinks(ops []app.InstallOp) int {
	n := 0
	for _, op := range ops {
		if op.Link != "" || op.BeforeLink != "" {
			n++
		}
	}
	return n
}

// TestSwitchingVariantsAddsAndRemovesTheLinks checks that a closed variant
// removes every link and stops sharing, and that the shared variant puts
// them back; restoring each install undoes it, and the oldest backup
// undoes everything, links included, with the shared folder untouched.
func TestSwitchingVariantsAddsAndRemovesTheLinks(t *testing.T) {
	home := sharedHome(t)
	before := listing(t, at(home, ".agents"))
	d := newClockFS(t)
	first, err := installShared(t, d, home, "shared")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := installShared(t, d, home, "standalone"); err != nil {
		t.Fatal(err)
	}
	assertLinks(t, home)
	if s := readJSON(t, at(home, ".dotai/state.json"), state.Decode); s.SharedSkills || slices.ContainsFunc(s.Files, func(f state.File) bool { return f.Kind == state.KindLink }) {
		t.Errorf("state after the closed variant = %+v, want no shared skills and no link", s)
	}
	if _, err := installShared(t, d, home, "shared"); err != nil {
		t.Fatal(err)
	}
	assertLinks(t, home, "alpha", "beta", packSkill)

	// Undo the last install, then the one before it.
	if _, err := app.RestoreWith(context.Background(), d, home, "", nil); err != nil {
		t.Fatalf("Restore() error = %v", err)
	}
	assertLinks(t, home)
	res, err := app.RestoreWith(context.Background(), d, home, "", nil)
	if err != nil {
		t.Fatalf("Restore() error = %v", err)
	}
	assertLinks(t, home, "alpha", "beta", packSkill)
	if !slices.ContainsFunc(res.Ops, func(op app.RestoreOp) bool { return op.Action == plan.Create && op.Link != "" && op.From != "" }) {
		t.Errorf("restore ops = %+v, want links put back from the backup's manifest", res.Ops)
	}
	if s := readJSON(t, at(home, ".dotai/state.json"), state.Decode); !s.SharedSkills {
		t.Error("state after restoring the shared install does not share skills")
	}

	if _, err := app.RestoreWith(context.Background(), d, home, first.Backup.ID, nil); err != nil {
		t.Fatalf("Restore(%s) error = %v", first.Backup.ID, err)
	}
	if got := slices.DeleteFunc(installed(t, home), func(f string) bool { return strings.HasPrefix(f, ".agents/") }); len(got) != 0 {
		t.Errorf("home after restoring the first install = %v, want nothing installed", got)
	}
	if _, err := os.Lstat(at(home, ".dotai/isolated")); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("the isolated root = %v, want it removed", err)
	}
	if after := listing(t, at(home, ".agents")); !slices.Equal(after, before) {
		t.Errorf("~/.agents = %v, want it as it was: %v", after, before)
	}
}

// TestInstallRefusesALinkChangedByHand checks that a link dotai owns that
// points elsewhere is an edit: install refuses it, and so does restore,
// even with consent, since its backup would record a link restore must
// never make; once it is removed by hand, restore works.
func TestInstallRefusesALinkChangedByHand(t *testing.T) {
	home := sharedHome(t)
	d := newClockFS(t)
	if _, err := installShared(t, d, home, "shared"); err != nil {
		t.Fatal(err)
	}
	link, elsewhere := at(home, ".dotai/isolated/claude/skills/alpha"), t.TempDir()
	if err := os.Remove(link); err != nil {
		t.Fatal(err)
	}
	makeLink(t, elsewhere, link)
	_, err := installShared(t, d, home, "standalone")
	var e *plan.EditedError
	if !errors.As(err, &e) || !slices.Equal(e.Targets, []string{link}) {
		t.Fatalf("Install() error = %v, want the changed link refused", err)
	}
	_, err = app.RestoreWith(context.Background(), d, home, "", func(app.RestoreResult) (bool, error) { return true, nil })
	if err == nil || !strings.Contains(err.Error(), "which restore does not overwrite") {
		t.Fatalf("Restore() with consent error = %v, want the changed link refused", err)
	}
	if cur, err := (app.OSInstallFS{}).Link(link); err != nil || cur.Link != elsewhere {
		t.Errorf("the changed link = %+v, %v, want it left as it is", cur, err)
	}
	if err := os.Remove(link); err != nil {
		t.Fatal(err)
	}
	if _, err := app.RestoreWith(context.Background(), d, home, "", nil); err != nil {
		t.Fatalf("Restore() once the link is gone error = %v", err)
	}
	assertLinks(t, home)
	if entries, err := os.ReadDir(elsewhere); err != nil || len(entries) != 0 {
		t.Errorf("what the changed link pointed at = %v, %v, want it untouched", entries, err)
	}
}

// TestRestoreRefusesATamperedLink checks that a backup or a state edited to
// make a link to anything but its shared skill is refused before anything
// is written.
func TestRestoreRefusesATamperedLink(t *testing.T) {
	for _, what := range []string{"manifest", "state"} {
		t.Run(what, func(t *testing.T) {
			home := sharedHome(t)
			d := newClockFS(t)
			if _, err := installShared(t, d, home, "shared"); err != nil {
				t.Fatal(err)
			}
			second, err := installShared(t, d, home, "standalone")
			if err != nil {
				t.Fatal(err)
			}
			evil := at(home, ".ssh")
			link := at(home, ".dotai/isolated/claude/skills/alpha")
			if what == "manifest" {
				m := manifest(t, home, second.Backup.ID)
				for i := range m.Ops {
					if m.Ops[i].Target == link {
						m.Ops[i].Before.Link = evil
					}
				}
				data, err := backup.Encode(m)
				if err != nil {
					t.Fatal(err)
				}
				writeFile(t, filepath.Join(second.Backup.Path, "manifest.json"), string(data))
			} else {
				if _, err := app.RestoreWith(context.Background(), d, home, "", nil); err != nil {
					t.Fatal(err)
				}
				s := readJSON(t, at(home, ".dotai/state.json"), state.Decode)
				for i := range s.Files {
					if s.Files[i].Target == link {
						s.Files[i].Link = evil
					}
				}
				data, err := state.Encode(s)
				if err != nil {
					t.Fatal(err)
				}
				writeFile(t, at(home, ".dotai/state.json"), string(data))
				_, err = installShared(t, d, home, "shared")
				var e *plan.LinkDestError
				if !errors.As(err, &e) || e.Dest != evil {
					t.Errorf("Install() over the tampered state error = %v, want it refused", err)
				}
			}
			before := listing(t, at(home, ".dotai/isolated"))
			_, err = app.RestoreWith(context.Background(), d, home, "", func(app.RestoreResult) (bool, error) { return true, nil })
			var e *plan.LinkDestError
			if !errors.As(err, &e) || e.Dest != evil {
				t.Fatalf("Restore() error = %v, want the tampered link refused", err)
			}
			if after := listing(t, at(home, ".dotai/isolated")); !slices.Equal(after, before) {
				t.Errorf("isolated = %v, want nothing written: %v", after, before)
			}
			if _, err := os.Lstat(evil); !errors.Is(err, fs.ErrNotExist) {
				t.Errorf("%s = %v, want no link made there", evil, err)
			}
		})
	}
}

// TestInstallRefusesARealDirectoryWhereALinkGoes checks that a directory
// or a file that dotai did not make, where a link goes, is not replaced.
func TestInstallRefusesARealDirectoryWhereALinkGoes(t *testing.T) {
	for _, kind := range []string{"directory", "file"} {
		t.Run(kind, func(t *testing.T) {
			home := sharedHome(t)
			target := at(home, ".dotai/isolated/claude/skills/alpha")
			if kind == "directory" {
				writeFile(t, filepath.Join(target, "SKILL.md"), "mine\n")
			} else {
				writeFile(t, target, "mine\n")
			}
			_, err := installShared(t, newClockFS(t), home, "shared")
			var nr *app.NotRegularError
			var ld *app.LinkDirError
			switch {
			case kind == "file" && (!errors.As(err, &nr) || nr.Path != target || !nr.Link || !strings.Contains(err.Error(), "where the install needs a link")):
				t.Fatalf("Install() error = %v, want the file refused", err)
			case kind == "directory" && (!errors.As(err, &ld) || ld.Dir != target || ld.File != filepath.Join(target, "SKILL.md")):
				t.Fatalf("Install() error = %v, want the directory refused, naming its file", err)
			}
			if _, err := os.Lstat(at(home, ".dotai/state.json")); !errors.Is(err, fs.ErrNotExist) {
				t.Errorf("state = %v, want nothing written", err)
			}
		})
	}
}

// TestASharedSkillAndThePacksTradePlaces checks an install where the pack
// adds a skill that was linked from the shared folder, which replaces the
// link with the pack's directory, and one where the pack drops it again,
// which replaces the directory with the link; each restore undoes it.
func TestASharedSkillAndThePacksTradePlaces(t *testing.T) {
	home := sharedHome(t)
	d := newClockFS(t)
	if _, err := installShared(t, d, home, "shared"); err != nil {
		t.Fatal(err)
	}
	assertLinks(t, home, "alpha", "beta", packSkill)
	skill := at(home, ".dotai/isolated/claude/skills/alpha/SKILL.md")

	if _, err := installShared(t, d, home, "shared", "alpha"); err != nil {
		t.Fatalf("Install() with the pack's alpha error = %v", err)
	}
	assertLinks(t, home, "beta", packSkill)
	if data, err := os.ReadFile(skill); err != nil || !strings.Contains(string(data), "The pack's.") {
		t.Errorf("alpha = %q, %v, want the pack's", data, err)
	}
	if data, err := os.ReadFile(at(home, ".agents/skills/alpha/SKILL.md")); err != nil || string(data) != "alpha\n" {
		t.Errorf("the shared alpha = %q, %v, want it untouched", data, err)
	}

	if _, err := installShared(t, d, home, "shared"); err != nil {
		t.Fatalf("Install() without the pack's alpha error = %v", err)
	}
	assertLinks(t, home, "alpha", "beta", packSkill)

	if _, err := app.RestoreWith(context.Background(), d, home, "", nil); err != nil {
		t.Fatalf("Restore() of the install that dropped alpha error = %v", err)
	}
	assertLinks(t, home, "beta", packSkill)
	if data, err := os.ReadFile(skill); err != nil || !strings.Contains(string(data), "The pack's.") {
		t.Errorf("alpha after the restore = %q, %v, want the pack's back", data, err)
	}
	if _, err := app.RestoreWith(context.Background(), d, home, "", nil); err != nil {
		t.Fatalf("Restore() of the install that added alpha error = %v", err)
	}
	assertLinks(t, home, "alpha", "beta", packSkill)
}

// TestInstallAndRestoreSettleAKilledInstallOfLinks checks that the journal
// of an install killed after it made some links lets the next install own
// them, and the next restore remove them.
func TestInstallAndRestoreSettleAKilledInstallOfLinks(t *testing.T) {
	for _, then := range []string{"install", "restore"} {
		t.Run(then, func(t *testing.T) {
			home := sharedHome(t)
			// The second write is agy's settings, after Claude Code's links.
			crash(t, &crashFS{clockFS: newClockFS(t), crashAt: 2}, examplePack, "shared", home)
			s := readJSON(t, at(home, ".dotai/state.json"), state.Decode)
			if s.Pending == nil || !slices.ContainsFunc(s.Pending.Ops, func(op state.PendingOp) bool { return op.After != nil && op.After.Link != "" }) {
				t.Fatalf("state after the crash = %+v, want a journal with links", s)
			}
			if _, err := (app.OSInstallFS{}).Link(at(home, ".dotai/isolated/claude/skills/alpha")); err != nil {
				t.Fatalf("the crash left no link: %v", err)
			}
			if then == "install" {
				if _, err := app.InstallWith(context.Background(), newClockFS(t), quiet(t).fetch, quiet(t).run, examplePack, "shared", home); err != nil {
					t.Fatalf("Install() after the crash error = %v", err)
				}
				assertConverged(t, examplePack, "shared", home)
				assertLinks(t, home, "alpha", "beta", packSkill)
				return
			}
			if _, err := app.RestoreWith(context.Background(), newClockFS(t), home, "", nil); err != nil {
				t.Fatalf("Restore() after the crash error = %v", err)
			}
			assertLinks(t, home)
			if _, err := os.Lstat(at(home, ".dotai/state.json")); !errors.Is(err, fs.ErrNotExist) {
				t.Errorf("state = %v, want it removed", err)
			}
		})
	}
}

// failAgySettingsFS fails the write of agy's settings, which comes after
// Claude Code's links.
type failAgySettingsFS struct{ *clockFS }

func (f failAgySettingsFS) WriteIn(root, name string, data []byte, mode os.FileMode) error {
	if name == "antigravity-cli/settings.json" {
		return errors.New("disk full")
	}
	return f.clockFS.WriteIn(root, name, data, mode)
}

// TestInstallRollsBackTheLinksItMade checks that a failed install removes
// the links it made, and leaves what they pointed at.
func TestInstallRollsBackTheLinksItMade(t *testing.T) {
	home := sharedHome(t)
	before := listing(t, at(home, ".agents"))
	_, err := installShared(t, failAgySettingsFS{newClockFS(t)}, home, "shared")
	var ie *app.InstallError
	if !errors.As(err, &ie) || ie.Rollback != nil {
		t.Fatalf("Install() error = %v, want an install rolled back", err)
	}
	assertLinks(t, home)
	if after := listing(t, at(home, ".agents")); !slices.Equal(after, before) {
		t.Errorf("~/.agents = %v, want it as it was: %v", after, before)
	}
}

// TestMakeLinkChecksTheDestinationAgain checks the last check before a link
// is made, which an install, a restore and a rollback all go through.
func TestMakeLinkChecksTheDestinationAgain(t *testing.T) {
	home, _ := installHome(t)
	root := at(home, ".dotai/isolated/claude")
	if err := os.MkdirAll(filepath.Join(root, "skills"), 0o755); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(root, "skills", "alpha")
	var e *plan.LinkDestError
	if err := app.MakeLinkWith(app.OSInstallFS{}, home, root, "skills/alpha", target, at(home, ".ssh")); !errors.As(err, &e) {
		t.Fatalf("MakeLinkWith() error = %v, want the destination refused", err)
	}
	if _, err := os.Lstat(target); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("the link = %v, want none made", err)
	}
	if err := app.MakeLinkWith(app.OSInstallFS{}, home, root, "skills/alpha", target, at(home, ".agents/skills/alpha")); err != nil {
		t.Errorf("MakeLinkWith() of the shared skill error = %v", err)
	}
}

// tradeCrashFS kills an install, as crashFS does, right before it writes
// the file at, or makes the link at, the absolute path at.
type tradeCrashFS struct {
	*clockFS
	at string
}

func (f *tradeCrashFS) WriteIn(root, name string, data []byte, mode os.FileMode) error {
	if filepath.Join(root, filepath.FromSlash(name)) == f.at {
		panic(errCrash)
	}
	return f.clockFS.WriteIn(root, name, data, mode)
}

func (f *tradeCrashFS) LinkIn(root, name, dest string) error {
	if filepath.Join(root, filepath.FromSlash(name)) == f.at {
		panic(errCrash)
	}
	return f.clockFS.LinkIn(root, name, dest)
}

// TestAKilledTradeOfALinkAndThePacksSkillSettles checks an install killed
// while a shared skill and the pack's skill of its name trade places, in
// both directions: the next install finishes it, and the next restore
// undoes it.
func TestAKilledTradeOfALinkAndThePacksSkillSettles(t *testing.T) {
	claudeSkill := ".dotai/isolated/claude/skills/alpha/SKILL.md"
	agySkill := ".dotai/isolated/gemini/config/skills/alpha/SKILL.md"
	tests := []struct {
		name string
		// The pack's skills before and in the killed install.
		before, during []string
		crashAt        string // relative to the home directory
	}{
		// The link to alpha gives way to the pack's alpha.
		{"link removed before the pack files", nil, []string{"alpha"}, claudeSkill},
		{"link removed after the pack files", nil, []string{"alpha"}, agySkill},
		// The pack drops alpha, and the link takes its place.
		{"pack files removed before the link", []string{"alpha"}, nil, ".dotai/isolated/claude/skills/alpha"},
	}
	for _, tt := range tests {
		for _, then := range []string{"install", "restore"} {
			t.Run(tt.name+"/"+then, func(t *testing.T) {
				home := sharedHome(t)
				d := newClockFS(t)
				if _, err := installShared(t, d, home, "shared", tt.before...); err != nil {
					t.Fatal(err)
				}
				func() {
					defer func() {
						if r := recover(); r != errCrash {
							t.Fatalf("recovered %v, want the crash", r)
						}
					}()
					_, _ = app.InstallWith(context.Background(), &tradeCrashFS{clockFS: newClockFS(t), at: at(home, tt.crashAt)},
						quiet(t).fetch, skillsRun(t, tt.during...), examplePack, "shared", home)
				}()
				if s := readJSON(t, at(home, ".dotai/state.json"), state.Decode); s.Pending == nil {
					t.Fatal("the crash left no journal")
				}
				if then == "install" {
					if _, err := installShared(t, newClockFS(t), home, "shared", tt.during...); err != nil {
						t.Fatalf("Install() after the crash error = %v", err)
					}
					p, err := app.PlanInstallWith(context.Background(), app.OSInstallFS{}, quiet(t).fetch, skillsRun(t, tt.during...), examplePack, "shared", home)
					if err != nil {
						t.Fatal(err)
					}
					for _, op := range p.Ops {
						if op.Action != plan.Unchanged {
							t.Errorf("op %+v after the install, want every target in place", op)
						}
					}
					assertTrade(t, home, tt.during)
					return
				}
				if _, err := app.RestoreWith(context.Background(), newClockFS(t), home, "", nil); err != nil {
					t.Fatalf("Restore() after the crash error = %v", err)
				}
				assertTrade(t, home, tt.before)
			})
		}
	}
}

// assertTrade checks that alpha is the pack's skill for Claude Code and agy
// when skills has it, and the link to the shared alpha otherwise.
func assertTrade(t *testing.T, home string, skills []string) {
	t.Helper()
	if slices.Contains(skills, "alpha") {
		assertLinks(t, home, "beta", packSkill)
		for _, rel := range []string{".dotai/isolated/claude/skills/alpha/SKILL.md", ".dotai/isolated/gemini/config/skills/alpha/SKILL.md"} {
			if data, err := os.ReadFile(at(home, rel)); err != nil || !strings.Contains(string(data), "The pack's.") {
				t.Errorf("%s = %q, %v, want the pack's", rel, data, err)
			}
		}
		return
	}
	assertLinks(t, home, "alpha", "beta", packSkill)
}

// TestInstallRefusesToTradeASkillDirectoryHoldingAForeignFile checks that
// the pack's skill directory that a link to the shared skill would replace
// is refused at plan time, naming the file, when it holds a file dotai did
// not install, such as a cache a harness wrote there, and that nothing is
// written; once the file is gone, the trade goes ahead.
func TestInstallRefusesToTradeASkillDirectoryHoldingAForeignFile(t *testing.T) {
	home := sharedHome(t)
	d := newClockFS(t)
	if _, err := installShared(t, d, home, "shared", "alpha"); err != nil {
		t.Fatal(err)
	}
	cache := at(home, ".dotai/isolated/claude/skills/alpha/.cache/index")
	writeFile(t, cache, "harness cache\n")
	before := listing(t, at(home, ".dotai"))
	for name, try := range map[string]func() error{
		"dry run": func() error {
			_, err := app.PlanInstallWith(context.Background(), app.OSInstallFS{}, quiet(t).fetch, skillsRun(t), examplePack, "shared", home)
			return err
		},
		"install": func() error { _, err := installShared(t, d, home, "shared"); return err },
	} {
		var e *app.LinkDirError
		if err := try(); !errors.As(err, &e) || e.File != cache || !strings.Contains(err.Error(), cache) {
			t.Errorf("%s error = %v, want the trade refused, naming %s", name, err, cache)
		}
	}
	if after := listing(t, at(home, ".dotai")); !slices.Equal(after, before) {
		t.Errorf(".dotai = %v, want nothing written: %v", after, before)
	}
	if err := os.RemoveAll(filepath.Dir(cache)); err != nil {
		t.Fatal(err)
	}
	if _, err := installShared(t, d, home, "shared"); err != nil {
		t.Fatalf("Install() once the cache is gone error = %v", err)
	}
	assertLinks(t, home, "alpha", "beta", packSkill)
}

// TestASharedSkillCollidesWithThePacksInAnotherCase checks that on an OS
// whose file systems ignore case, a pack skill Alpha and the shared alpha
// collide: the pack wins with a warning, as for the same name. Elsewhere
// they are two skills.
func TestASharedSkillCollidesWithThePacksInAnotherCase(t *testing.T) {
	home := sharedHome(t)
	res, err := installShared(t, newClockFS(t), home, "shared", "Alpha")
	if err != nil {
		t.Fatalf("Install() error = %v", err)
	}
	warned := slices.ContainsFunc(res.Warnings, func(w string) bool { return strings.Contains(w, "the shared skill alpha is not linked into claudecode") })
	if fold := runtime.GOOS == "windows" || runtime.GOOS == "darwin"; warned != fold {
		t.Errorf("warnings = %q, want the collision warned about: %t", res.Warnings, fold)
	}
	if runtime.GOOS == "windows" || runtime.GOOS == "darwin" {
		assertLinks(t, home, "beta", packSkill)
	} else {
		assertLinks(t, home, "alpha", "beta", packSkill)
	}
	if data, err := os.ReadFile(at(home, ".dotai/isolated/claude/skills/Alpha/SKILL.md")); err != nil || !strings.Contains(string(data), "The pack's.") {
		t.Errorf("the pack's Alpha = %q, %v, want it installed", data, err)
	}
}
