package app_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"

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

func TestAddLocalPackListsTheValidatedPackOnce(t *testing.T) {
	home, dir := t.TempDir(), packCopy(t)
	list, err := app.AddLocalPack(home, dir)
	if err != nil {
		t.Fatalf("AddLocalPack() error = %v", err)
	}
	want := []app.PackEntry{{ID: "local/minimal-pack", Path: dir, Name: "minimal-pack", Version: "0.1.0"}}
	if !reflect.DeepEqual(list.Packs, want) {
		t.Errorf("packs = %+v, want %+v", list.Packs, want)
	}
	data, err := os.ReadFile(filepath.Join(home, ".dotai", "packs.json"))
	if err != nil || !strings.Contains(string(data), `"schema": "dotai.packs/v2"`) {
		t.Fatalf("packs.json = %s, %v", data, err)
	}
	again, err := app.AddLocalPack(home, dir+string(filepath.Separator))
	if err != nil || !reflect.DeepEqual(again.Packs, want) {
		t.Errorf("a second import = %+v, %v, want the same single entry", again.Packs, err)
	}
	if _, err := os.Stat(filepath.Join(home, ".dotai", "lock")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("the import left its lock: %v", err)
	}
}

func TestAddLocalPackRefusesWhatIsNotAValidPack(t *testing.T) {
	home := t.TempDir()
	t.Run("relative", func(t *testing.T) {
		if _, err := app.AddLocalPack(home, "pack"); !errors.As(err, new(*app.UsageError)) {
			t.Errorf("error = %v, want a usage error", err)
		}
	})
	t.Run("missing", func(t *testing.T) {
		if _, err := app.AddLocalPack(home, filepath.Join(home, "nothing")); !errors.As(err, new(*app.UsageError)) {
			t.Errorf("error = %v, want a usage error", err)
		}
	})
	t.Run("invalid", func(t *testing.T) {
		dir := packCopy(t)
		if err := os.WriteFile(filepath.Join(dir, "dotai.yaml"), []byte("schema: dotai.pack/v1\nname: x\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		_, err := app.AddLocalPack(home, dir)
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
	if _, err := app.AddLocalPack(home, dir); err != nil {
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
	if _, err := app.AddLocalPack(home, dir); err != nil {
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
	res, err := app.CopyPack(context.Background(), from, to)
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

// TestSummarizePackShowsDotaiOnboarding checks the onboarding of the pack
// detail (decision 0045): dotai's profiling skill as an install generates
// it, or the custom item that replaces it, with its file.
func TestSummarizePackShowsDotaiOnboarding(t *testing.T) {
	dir := packCopy(t)
	sum, err := app.SummarizePack(dir)
	if err != nil {
		t.Fatal(err)
	}
	res, err := app.Validate(dir, app.Selection{})
	if err != nil {
		t.Fatal(err)
	}
	o := sum.Onboarding
	if o.Mode != "dotai" || o.Kind != "skill" || o.Name != "dotai-profiles" || o.Layer != "" || o.Path != "" || o.Content != string(app.ProfilesSkill(*res.Pack)) {
		t.Errorf("onboarding = %+v, want dotai's profiling skill", o)
	}
	writeFile(t, filepath.Join(dir, "variants", "tmux", ".rulesync", "skills", "setup", "SKILL.md"), "---\nname: setup\n---\nSet up.\n")
	m, err := os.ReadFile(filepath.Join(dir, "dotai.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(dir, "dotai.yaml"), string(m)+"onboarding:\n  custom: variants/tmux/.rulesync/skills/setup\n")
	if sum, err = app.SummarizePack(dir); err != nil {
		t.Fatal(err)
	}
	want := app.OnboardingOutline{Mode: "custom", Kind: "skill", Name: "setup", Layer: "tmux", Path: "variants/tmux/.rulesync/skills/setup/SKILL.md", Content: "---\nname: setup\n---\nSet up.\n"}
	if sum.Onboarding != want {
		t.Errorf("onboarding = %+v, want %+v", sum.Onboarding, want)
	}
}

func TestCopyPackRefusesATargetThatExistsOrHasNoParent(t *testing.T) {
	from := packCopy(t)
	for name, to := range map[string]string{
		"exists":    from,
		"no parent": filepath.Join(t.TempDir(), "missing", "copy"),
		"relative":  "copy",
		"inside":    filepath.Join(from, "base", "copy"),
	} {
		t.Run(name, func(t *testing.T) {
			_, err := app.CopyPack(context.Background(), from, to)
			if !errors.As(err, new(*app.CopyRefusedError)) && !errors.As(err, new(*app.UsageError)) {
				t.Errorf("error = %v, want a refusal", err)
			}
		})
	}
	if _, err := app.CopyPack(context.Background(), filepath.Join(t.TempDir(), "nothing"), filepath.Join(t.TempDir(), "copy")); !errors.As(err, new(*app.UsageError)) {
		t.Errorf("copying a missing pack = %v, want a usage error", err)
	}
}

func TestCopyPackRefusesALinkAndLeavesNoCopy(t *testing.T) {
	from := packCopy(t)
	if err := os.Symlink(filepath.Join(from, "dotai.yaml"), filepath.Join(from, "base", "link.yaml")); err != nil {
		t.Skipf("no symbolic links here: %v", err)
	}
	to := filepath.Join(t.TempDir(), "copy")
	if _, err := app.CopyPack(context.Background(), from, to); !errors.As(err, new(*app.CopyRefusedError)) {
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
		!reflect.DeepEqual(base.Subagents, []string{"reviewer"}) || !base.Permissions || base.Hooks ||
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
}

// siblings lists the entries of dir, to find what a copy left there.
func siblings(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	return names
}

func TestCopyPackRefusesWhatIsNotAPack(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "notes.md"), []byte("# notes\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := app.CopyPack(context.Background(), dir, filepath.Join(t.TempDir(), "copy")); !errors.As(err, new(*app.UsageError)) {
		t.Errorf("copying a directory without a manifest = %v, want a usage error", err)
	}
}

func TestCopyPackThatFailsPartwayLeavesNothing(t *testing.T) {
	from := packCopy(t)
	parent := filepath.Dir(from)
	to := filepath.Join(parent, "copy")
	stop := errors.New("stopped")
	_, err := app.CopyPackWith(context.Background(), from, to, func(n int) error {
		if n == 3 {
			return stop
		}
		return nil
	}, nil)
	if !errors.Is(err, stop) {
		t.Fatalf("error = %v, want the error of the third file", err)
	}
	if got := siblings(t, parent); !reflect.DeepEqual(got, []string{"minimal-pack"}) {
		t.Errorf("after a failed copy the parent holds %v, want the pack alone", got)
	}
}

func TestCopyPackStopsWhenItsContextEnds(t *testing.T) {
	from := packCopy(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	to := filepath.Join(filepath.Dir(from), "copy")
	if _, err := app.CopyPack(ctx, from, to); !errors.Is(err, context.Canceled) {
		t.Errorf("error = %v, want context.Canceled", err)
	}
	if got := siblings(t, filepath.Dir(from)); !reflect.DeepEqual(got, []string{"minimal-pack"}) {
		t.Errorf("a cancelled copy left %v", got)
	}
}

func TestCopyPackRefusesATargetCreatedWhileItCopies(t *testing.T) {
	from := packCopy(t)
	parent := filepath.Dir(from)
	to := filepath.Join(parent, "copy")
	_, err := app.CopyPackWith(context.Background(), from, to, nil, func() {
		if err := os.Mkdir(to, 0o755); err != nil {
			t.Fatal(err)
		}
	})
	if !errors.As(err, new(*app.CopyRefusedError)) {
		t.Errorf("error = %v, want a CopyRefusedError", err)
	}
	if got := siblings(t, parent); !reflect.DeepEqual(got, []string{"copy", "minimal-pack"}) {
		t.Errorf("the parent holds %v, want the pack and the other program's directory", got)
	}
	if got := siblings(t, to); len(got) != 0 {
		t.Errorf("the copy wrote into the other program's directory: %v", got)
	}
}

func TestCopyPackSkipsAGitFile(t *testing.T) {
	from := packCopy(t)
	if err := os.WriteFile(filepath.Join(from, ".git"), []byte("gitdir: /elsewhere\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	to := filepath.Join(filepath.Dir(from), "copy")
	if _, err := app.CopyPack(context.Background(), from, to); err != nil {
		t.Fatalf("CopyPack() error = %v", err)
	}
	if _, err := os.Lstat(filepath.Join(to, ".git")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("the copy holds the .git file: %v", err)
	}
}

func TestCopyPackKeepsTheModesOfDirectories(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows has no directory permissions of this kind")
	}
	from := packCopy(t)
	if err := os.Chmod(filepath.Join(from, "base"), 0o700); err != nil {
		t.Fatal(err)
	}
	to := filepath.Join(filepath.Dir(from), "copy")
	if _, err := app.CopyPack(context.Background(), from, to); err != nil {
		t.Fatal(err)
	}
	if info, err := os.Stat(filepath.Join(to, "base")); err != nil || info.Mode().Perm() != 0o700 {
		t.Errorf("base in the copy = %v, %v, want 0700", info.Mode().Perm(), err)
	}
}

func TestCopyPackRefusesAFileLargerThanAPackHolds(t *testing.T) {
	from := packCopy(t)
	if err := os.WriteFile(filepath.Join(from, "base", "big.bin"), make([]byte, 1<<20+1), 0o644); err != nil {
		t.Fatal(err)
	}
	to := filepath.Join(filepath.Dir(from), "copy")
	if _, err := app.CopyPack(context.Background(), from, to); !errors.As(err, new(*app.CopyRefusedError)) {
		t.Errorf("error = %v, want a CopyRefusedError", err)
	}
	if _, err := os.Stat(to); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("a refused copy left %s", to)
	}
}

func TestSummarizePackSuggestsAFreeSiblingForACopy(t *testing.T) {
	dir := packCopy(t)
	sum, err := app.SummarizePack(dir)
	if err != nil || sum.CopyTarget != filepath.Join(filepath.Dir(dir), "minimal-pack-copy") {
		t.Fatalf("copy target = %q, %v", sum.CopyTarget, err)
	}
	if err := os.Mkdir(sum.CopyTarget, 0o755); err != nil {
		t.Fatal(err)
	}
	if again, _ := app.SummarizePack(dir); again.CopyTarget != filepath.Join(filepath.Dir(dir), "minimal-pack-copy-2") {
		t.Errorf("copy target with the first taken = %q", again.CopyTarget)
	}
}

// writeRegistry writes data as the pack registry of home.
func writeRegistry(t *testing.T, home, data string) string {
	t.Helper()
	name := filepath.Join(home, ".dotai", "packs.json")
	if err := os.MkdirAll(filepath.Dir(name), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(name, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
	return name
}

func TestACorruptRegistryIsRefusedWithItsPath(t *testing.T) {
	for name, data := range map[string]string{
		"not JSON":     "{",
		"other schema": `{"schema":"dotai.packs/v9","packs":[]}`,
	} {
		t.Run(name, func(t *testing.T) {
			home, dir := t.TempDir(), packCopy(t)
			file := writeRegistry(t, home, data)
			for op, call := range map[string]func() error{
				"list":   func() error { _, err := app.ListPacks(home); return err },
				"import": func() error { _, err := app.AddLocalPack(home, dir); return err },
				"remove": func() error { _, err := app.RemovePack(home, dir); return err },
			} {
				var bad *app.RegistryInvalidError
				if err := call(); !errors.As(err, &bad) || bad.Path != file {
					t.Errorf("%s = %v, want a RegistryInvalidError for %s", op, err, file)
				}
			}
			if got, _ := os.ReadFile(file); string(got) != data {
				t.Errorf("packs.json = %q, want it left as it was", got)
			}
		})
	}
}

func TestARelativeEntryIsListedWithAnErrorAndCanBeRemoved(t *testing.T) {
	home, dir := t.TempDir(), packCopy(t)
	writeRegistry(t, home, `{"schema":"dotai.packs/v1","packs":[{"path":"relative/pack"},{"path":`+strconvQuote(dir)+`}]}`)
	list, err := app.ListPacks(home)
	if err != nil || len(list.Packs) != 2 || list.Packs[0].Error == "" || list.Packs[1].Name != "minimal-pack" {
		t.Fatalf("ListPacks() = %+v, %v, want the relative entry with an error and the pack", list.Packs, err)
	}
	removed, err := app.RemovePack(home, "relative/pack")
	if err != nil || len(removed.Packs) != 1 || removed.Packs[0].Path != dir {
		t.Errorf("RemovePack(relative) = %+v, %v, want the pack alone", removed.Packs, err)
	}
}

func TestListPacksDoesNotWaitLongForAPackThatDoesNotAnswer(t *testing.T) {
	home, dir := t.TempDir(), packCopy(t)
	if _, err := app.AddLocalPack(home, dir); err != nil {
		t.Fatal(err)
	}
	release := make(chan struct{})
	defer close(release)
	restore := app.SetPackIdentify(func(string) (string, string, error) {
		<-release
		return "late", "1", nil
	}, 50*time.Millisecond)
	defer restore()
	start := time.Now()
	list, err := app.ListPacks(home)
	if err != nil || len(list.Packs) != 1 || list.Packs[0].Error == "" {
		t.Errorf("ListPacks() = %+v, %v, want the pack listed with an error", list.Packs, err)
	}
	if d := time.Since(start); d > 2*time.Second {
		t.Errorf("ListPacks() took %s", d)
	}
}

func strconvQuote(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}
