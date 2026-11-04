package app_test

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/lartweib/dotai/internal/app"
)

// examplePack copies the example pack into a directory of its own and
// returns its absolute path.
func packCopy(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "minimal-pack")
	if err := os.CopyFS(dir, os.DirFS(filepath.Join("..", "..", "examples", "minimal-pack"))); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestListPacksOfAHomeWithoutARegistryIsEmptyAndWritesNothing(t *testing.T) {
	home := t.TempDir()
	list, err := app.ListPacks(home)
	if err != nil {
		t.Fatalf("ListPacks() error = %v", err)
	}
	if len(list.Packs) != 0 || list.Path != filepath.Join(home, ".dotai", "packs.json") {
		t.Errorf("list = %+v, want no packs and the path of packs.json", list)
	}
	if _, err := os.Stat(filepath.Join(home, ".dotai")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("ListPacks created ~/.dotai: %v", err)
	}
}

func TestImportPackListsTheValidatedPackOnce(t *testing.T) {
	home, dir := t.TempDir(), packCopy(t)
	list, err := app.ImportPack(home, dir)
	if err != nil {
		t.Fatalf("ImportPack() error = %v", err)
	}
	want := []app.PackEntry{{Path: dir, Name: "minimal-pack", Version: "0.1.0"}}
	if !reflect.DeepEqual(list.Packs, want) {
		t.Errorf("packs = %+v, want %+v", list.Packs, want)
	}
	data, err := os.ReadFile(filepath.Join(home, ".dotai", "packs.json"))
	if err != nil || !strings.Contains(string(data), `"schema": "dotai.packs/v1"`) {
		t.Fatalf("packs.json = %s, %v", data, err)
	}
	again, err := app.ImportPack(home, dir+string(filepath.Separator))
	if err != nil || !reflect.DeepEqual(again.Packs, want) {
		t.Errorf("a second import = %+v, %v, want the same single entry", again.Packs, err)
	}
	if _, err := os.Stat(filepath.Join(home, ".dotai", "lock")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("the import left its lock: %v", err)
	}
}

func TestImportPackRefusesWhatIsNotAValidPack(t *testing.T) {
	home := t.TempDir()
	t.Run("relative", func(t *testing.T) {
		if _, err := app.ImportPack(home, "pack"); !errors.As(err, new(*app.UsageError)) {
			t.Errorf("error = %v, want a usage error", err)
		}
	})
	t.Run("missing", func(t *testing.T) {
		if _, err := app.ImportPack(home, filepath.Join(home, "nothing")); !errors.As(err, new(*app.UsageError)) {
			t.Errorf("error = %v, want a usage error", err)
		}
	})
	t.Run("invalid", func(t *testing.T) {
		dir := packCopy(t)
		if err := os.WriteFile(filepath.Join(dir, "dotai.yaml"), []byte("schema: dotai.pack/v1\nname: x\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		_, err := app.ImportPack(home, dir)
		var invalid *app.PackInvalidError
		if !errors.As(err, &invalid) || invalid.Result.Valid || len(invalid.Result.Diagnostics) == 0 {
			t.Errorf("error = %v, want a PackInvalidError with the diagnostics", err)
		}
	})
	if _, err := os.Stat(filepath.Join(home, ".dotai", "packs.json")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("a refused import wrote packs.json: %v", err)
	}
}

func TestListPacksKeepsAPackThatIsGoneWithAnError(t *testing.T) {
	home, dir := t.TempDir(), packCopy(t)
	if _, err := app.ImportPack(home, dir); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(dir); err != nil {
		t.Fatal(err)
	}
	list, err := app.ListPacks(home)
	if err != nil {
		t.Fatalf("ListPacks() error = %v", err)
	}
	if len(list.Packs) != 1 || list.Packs[0].Path != dir || list.Packs[0].Error == "" || list.Packs[0].Name != "" {
		t.Errorf("packs = %+v, want the gone pack listed with an error", list.Packs)
	}
}

func TestRemovePackForgetsTheEntryAndKeepsTheFiles(t *testing.T) {
	home, dir := t.TempDir(), packCopy(t)
	if _, err := app.ImportPack(home, dir); err != nil {
		t.Fatal(err)
	}
	list, err := app.RemovePack(home, dir)
	if err != nil || len(list.Packs) != 0 {
		t.Fatalf("RemovePack() = %+v, %v, want no packs", list, err)
	}
	if _, err := os.Stat(filepath.Join(dir, "dotai.yaml")); err != nil {
		t.Errorf("RemovePack touched the pack: %v", err)
	}
	if _, err := app.RemovePack(home, filepath.Join(home, "other")); err != nil {
		t.Errorf("removing a path that is not listed = %v, want no error", err)
	}
}

func TestCopyPackCopiesFilesWithoutGitOrImport(t *testing.T) {
	home, from := t.TempDir(), packCopy(t)
	if err := os.MkdirAll(filepath.Join(from, ".git", "objects"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(from, ".git", "HEAD"), []byte("ref\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	to := filepath.Join(filepath.Dir(from), "minimal-pack-copy")
	res, err := app.CopyPack(from, to)
	if err != nil {
		t.Fatalf("CopyPack() error = %v", err)
	}
	if res.From != from || res.To != to || res.Files == 0 {
		t.Errorf("result = %+v", res)
	}
	for _, name := range []string{"dotai.yaml", filepath.Join("base", ".rulesync", "rules", "overview.md"), filepath.Join("mods", "hello", "hooks", "register.js")} {
		want, _ := os.ReadFile(filepath.Join(from, name))
		got, err := os.ReadFile(filepath.Join(to, name))
		if err != nil || string(got) != string(want) {
			t.Errorf("%s in the copy = %q, %v", name, got, err)
		}
	}
	if _, err := os.Stat(filepath.Join(to, ".git")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("the copy holds .git: %v", err)
	}
	if list, _ := app.ListPacks(home); len(list.Packs) != 0 {
		t.Errorf("the copy was imported: %+v", list.Packs)
	}
}

func TestSummarizePackOfAPackWithoutAnOnboardingAgentHasNone(t *testing.T) {
	dir := packCopy(t)
	if err := os.Remove(filepath.Join(dir, "base", ".rulesync", "subagents", "onboarding.md")); err != nil {
		t.Fatal(err)
	}
	sum, err := app.SummarizePack(dir)
	if err != nil || sum.Onboarding != nil {
		t.Errorf("SummarizePack() = %+v, %v, want no onboarding agent", sum.Onboarding, err)
	}
}

func TestCopyPackRefusesATargetThatExistsOrHasNoParent(t *testing.T) {
	from := packCopy(t)
	for name, to := range map[string]string{
		"exists":    from,
		"no parent": filepath.Join(t.TempDir(), "missing", "copy"),
		"relative":  "copy",
	} {
		t.Run(name, func(t *testing.T) {
			_, err := app.CopyPack(from, to)
			if !errors.As(err, new(*app.CopyRefusedError)) && !errors.As(err, new(*app.UsageError)) {
				t.Errorf("error = %v, want a refusal", err)
			}
		})
	}
	if _, err := app.CopyPack(filepath.Join(t.TempDir(), "nothing"), filepath.Join(t.TempDir(), "copy")); !errors.As(err, new(*app.UsageError)) {
		t.Errorf("copying a missing pack = %v, want a usage error", err)
	}
}

func TestCopyPackRefusesALinkAndLeavesNoCopy(t *testing.T) {
	from := packCopy(t)
	if err := os.Symlink(filepath.Join(from, "dotai.yaml"), filepath.Join(from, "base", "link.yaml")); err != nil {
		t.Skipf("no symbolic links here: %v", err)
	}
	to := filepath.Join(t.TempDir(), "copy")
	if _, err := app.CopyPack(from, to); !errors.As(err, new(*app.CopyRefusedError)) {
		t.Errorf("error = %v, want a CopyRefusedError", err)
	}
	if _, err := os.Stat(to); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("a refused copy left %s: %v", to, err)
	}
}

func TestSummarizePackListsEachLayerByType(t *testing.T) {
	sum, err := app.SummarizePack(filepath.Join("..", "..", "examples", "minimal-pack"))
	if err != nil {
		t.Fatalf("SummarizePack() error = %v", err)
	}
	byName := map[string]app.LayerContents{}
	for _, l := range sum.Layers {
		byName[l.Kind+"/"+l.Name] = l.Contents
	}
	base := byName["base/base"]
	if !reflect.DeepEqual(base.Rules, []string{"overview"}) || !reflect.DeepEqual(base.Skills, []string{"commit-message"}) ||
		!reflect.DeepEqual(base.Subagents, []string{"onboarding", "reviewer"}) || !base.Permissions || base.Hooks ||
		!reflect.DeepEqual(base.Tools, []string{"hello.ps1", "hello.sh"}) || len(base.Settings) == 0 {
		t.Errorf("base = %+v", base)
	}
	if tmux := byName["variant/tmux"]; !tmux.Permissions || !reflect.DeepEqual(tmux.Subagents, []string{"reviewer"}) {
		t.Errorf("tmux = %+v", tmux)
	}
	if web := byName["profile/web"]; !reflect.DeepEqual(web.Rules, []string{"web"}) || !reflect.DeepEqual(web.Mods, []string{"hello"}) {
		t.Errorf("web = %+v", web)
	}
	if !reflect.DeepEqual(sum.Mods, []string{"hello"}) {
		t.Errorf("mods = %v", sum.Mods)
	}
	want, err := os.ReadFile(filepath.Join("..", "..", "examples", "minimal-pack", "base", ".rulesync", "subagents", "onboarding.md"))
	if err != nil {
		t.Fatal(err)
	}
	if o := sum.Onboarding; o == nil || o.Layer != "base" || o.Path != "base/.rulesync/subagents/onboarding.md" || o.Content != string(want) {
		t.Errorf("onboarding = %+v, want the base's onboarding subagent", o)
	}
}
