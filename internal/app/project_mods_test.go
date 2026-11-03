package app_test

import (
	"context"
	"errors"
	"os"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/lartweib/dotai/internal/app"
	"github.com/lartweib/dotai/internal/plan"
)

// The files of the example pack's mod hello, as a project apply installs
// them for Claude Code.
var helloFiles = []string{".claude/skills/hello/.claude-plugin/plugin.json", ".claude/skills/hello/hooks/hooks.json", ".claude/skills/hello/hooks/register.js"}

func TestPlanProjectInstallsTheModsOfTheProfiles(t *testing.T) {
	project, home, _ := projectDirs(t)
	g := &fakeGit{}
	p, err := planProject(t, g, examplePack, project, home)
	if err != nil || len(p.Diagnostics) != 0 {
		t.Fatalf("PlanProject() = %+v, %v", p, err)
	}
	var mods []string
	for _, op := range p.Ops {
		if op.Kind == "mods" {
			mods = append(mods, string(op.Action)+" "+op.Harness+" "+op.Source+" "+op.Target)
		}
	}
	want := []string{
		"create claudecode mods/hello/.claude-plugin/plugin.json " + at(project, helloFiles[0]),
		"create claudecode mods/hello/hooks/hooks.json " + at(project, helloFiles[1]),
		"create claudecode mods/hello/hooks/register.js " + at(project, helloFiles[2]),
	}
	if !reflect.DeepEqual(mods, want) {
		t.Errorf("mod ops =\n%q\nwant\n%q", mods, want)
	}
	if !slices.Contains(g.asked, ".claude/skills/hello/") {
		t.Errorf("git was asked about %q, want the mod's directory as a whole", g.asked)
	}
	notice := "the mod hello loads in Claude Code 2.1.287 or later, in a terminal, only in a session started at " + project + ", and only after you accept the workspace trust dialog there"
	if !slices.Contains(p.Notices, notice) {
		t.Errorf("notices = %q, want %q", p.Notices, notice)
	}
	var slots []string
	for _, w := range p.Warnings {
		if w.Code == plan.CodeNoLocalSlot && w.Item == "mods" {
			slots = append(slots, w.Harness)
		}
	}
	if !reflect.DeepEqual(slots, []string{"antigravity-cli", "opencode"}) {
		t.Errorf("no-local-slot for mods = %v, want agy and OpenCode", slots)
	}
}

func TestPlanProjectSkipsAModWhoseDirectoryGitTracks(t *testing.T) {
	project, home, _ := projectDirs(t)
	p, err := planProject(t, &fakeGit{tracked: []string{".claude/skills/hello/"}}, examplePack, project, home)
	if err != nil || len(p.Diagnostics) != 0 {
		t.Fatalf("PlanProject() = %+v, %v", p, err)
	}
	if slices.ContainsFunc(p.Ops, func(op plan.ProjectOp) bool { return op.Kind == "mods" }) {
		t.Errorf("ops = %+v, want no mod", p.Ops)
	}
	want := plan.Skipped{Harness: "claudecode", Kind: "mods", Item: "mod hello", Target: at(project, ".claude/skills/hello"), Reason: plan.SkipTracked}
	if !slices.Contains(p.Skipped, want) {
		t.Errorf("skipped = %+v, want %+v", p.Skipped, want)
	}
	if slices.ContainsFunc(p.Notices, func(n string) bool { return strings.Contains(n, "mod hello") }) {
		t.Errorf("notices = %q, want none for a skipped mod", p.Notices)
	}
}

func TestApplyInstallsAModAndRestoreRemovesIt(t *testing.T) {
	project, home, g := applyRepo(t)
	before := files(t, project)
	if _, err := applyWith(t, newClockFS(t), g, profileTree, project, home, nil); err != nil {
		t.Fatalf("Apply() error = %v", err)
	}
	got := files(t, project)
	if m := got[helloFiles[0]]; m != "{\n  \"name\": \"hello\",\n  \"version\": \"0.1.0\",\n  \"description\": \"A minimal example pack for the dotai engine.\",\n  \"author\": {\n    \"name\": \"dotai examples\"\n  }\n}\n" {
		t.Errorf("plugin.json = %q, want the mod's name and the pack's version, description and author", m)
	}
	src, err := os.ReadFile(at(examplePack, "mods/hello/hooks/register.js"))
	if err != nil || got[helloFiles[2]] != string(src) {
		t.Errorf("register.js = %q, want the pack's, %v", got[helloFiles[2]], err)
	}
	if !strings.Contains(got[".git/info/exclude"], "\n/.claude/skills/hello/\n") {
		t.Errorf("exclude = %q, want the mod's directory", got[".git/info/exclude"])
	}
	if _, err := restoreProject(t, g, project, home, nil); err != nil {
		t.Fatalf("RestoreProject() error = %v", err)
	}
	if got := files(t, project); !reflect.DeepEqual(got, before) {
		t.Errorf("project = %v\nwant %v", got, before)
	}
	if _, err := os.Lstat(at(project, ".claude/skills/hello")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf(".claude/skills/hello = %v, want it gone", err)
	}
}

func TestApplyNeedsConsentOnlyToOverwriteAFileInAFolderOfTheModsName(t *testing.T) {
	project, home, g := applyRepo(t)
	writeFile(t, at(project, ".claude/skills/hello/notes.md"), "mine\n")
	writeFile(t, at(project, helloFiles[1]), `{"modules": ["./other.js"]}`)
	_, err := applyWith(t, newClockFS(t), g, profileTree, project, home, nil)
	if c, ok := errors.AsType[*app.ApplyConsentError](err); !ok || c.Files != 1 {
		t.Fatalf("Apply() error = %v, want it to need consent for the one file it overwrites", err)
	}
	if _, err := os.Lstat(at(project, helloFiles[2])); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("%s = %v, want nothing written without consent", helloFiles[2], err)
	}
	if _, err := applyWith(t, newClockFS(t), g, profileTree, project, home, yes); err != nil {
		t.Fatalf("Apply() with consent error = %v", err)
	}
	if _, err := restoreProject(t, g, project, home, nil); err != nil {
		t.Fatalf("RestoreProject() error = %v", err)
	}
	got := files(t, project)
	if got[".claude/skills/hello/notes.md"] != "mine\n" || got[helloFiles[1]] != `{"modules": ["./other.js"]}` || got[helloFiles[2]] != "" {
		t.Errorf("project after the restore = %v, want the user's folder as it was", got)
	}
}

func TestApplyAgainAfterARestoreKeepsWhatSomeoneAddedToTheMod(t *testing.T) {
	project, home, g := applyRepo(t)
	if _, err := applyWith(t, newClockFS(t), g, profileTree, project, home, nil); err != nil {
		t.Fatal(err)
	}
	writeFile(t, at(project, ".claude/skills/hello/notes.md"), "mine\n")
	if _, err := restoreProject(t, g, project, home, nil); err != nil {
		t.Fatalf("RestoreProject() error = %v", err)
	}
	if _, err := applyWith(t, newClockFS(t), g, profileTree, project, home, nil); err != nil {
		t.Fatalf("Apply() after the restore, without consent, error = %v, want it to create the mod's files", err)
	}
	got := files(t, project)
	if got[".claude/skills/hello/notes.md"] != "mine\n" {
		t.Errorf("notes.md = %q, want it kept", got[".claude/skills/hello/notes.md"])
	}
	for _, rel := range helloFiles {
		if _, ok := got[rel]; !ok {
			t.Errorf("%s is missing, want the mod installed again", rel)
		}
	}
}

func TestApplyThatDropsAModRemovesIt(t *testing.T) {
	project, home, g := applyRepo(t)
	if _, err := applyWith(t, newClockFS(t), g, profileTree, project, home, nil); err != nil {
		t.Fatal(err)
	}
	pack := copyPack(t)
	editFile(t, at(pack, "dotai.yaml"), "    mods: [hello]\n", "")
	if _, err := applyFrom(t, pack, g, project, home); err != nil {
		t.Fatalf("Apply() without the mod error = %v", err)
	}
	if _, err := os.Lstat(at(project, ".claude/skills/hello")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf(".claude/skills/hello = %v, want it gone", err)
	}
	if ex := files(t, project)[".git/info/exclude"]; strings.Contains(ex, "hello") {
		t.Errorf("exclude = %q, want the mod's entry gone", ex)
	}
}

// editFile replaces old with new in the file name.
func editFile(t *testing.T, name, old, new string) {
	t.Helper()
	data, err := os.ReadFile(name)
	if err != nil || !strings.Contains(string(data), old) {
		t.Fatalf("%s does not hold %q: %v", name, old, err)
	}
	writeFile(t, name, strings.Replace(string(data), old, new, 1))
}

// applyFrom applies the profile web of the pack in dir to project.
func applyFrom(t *testing.T, dir string, g app.GitPort, project, home string) (app.ApplyResult, error) {
	t.Helper()
	f := &treeRulesync{t: t, tree: profileTree}
	return app.ApplyWith(context.Background(), newClockFS(t), g, f.fetch, f.run, dir, project, home, app.ApplyOptions{Profiles: []string{"web"}}, nil)
}

func TestStageCopiesAModByteForByte(t *testing.T) {
	dir := copyPack(t)
	const js = "// runs ${DOTAI_TOOLS}/x.sh and ${DOTAI_PROJECT_TOOLS}/y.sh\r\nexport function register(on) {}\r\n"
	writeFile(t, at(dir, "mods/hello/hooks/register.js"), js)
	out := at(t.TempDir(), "staging")
	fr := quiet(t)
	fr.tools = app.ToolsIn("home", "project")
	res, err := fr.stage(dir, out, app.Selection{Profiles: []string{"web"}}, false)
	if err != nil || len(res.Diagnostics) != 0 {
		t.Fatalf("Stage() = %+v, %v", res.Diagnostics, err)
	}
	if got, err := os.ReadFile(at(out, "project/mods/hello/hooks/register.js")); err != nil || string(got) != js {
		t.Errorf("staged register.js = %q, %v, want the pack's bytes, path variables included", got, err)
	}
}
