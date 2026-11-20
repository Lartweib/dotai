package app_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/lartweib/dotai/internal/app"
)

// clonePath is where a git pack of id is cloned in home.
func clonePath(home, id string) string {
	return filepath.Join(append([]string{home, ".dotai", "packs"}, strings.Split(id, "/")...)...)
}

// leftovers lists what the directory of the clones of owner holds besides
// the clones: the temporary siblings a fetch or a swap leaves.
func packLeftovers(t *testing.T, home, owner string) []string {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(home, ".dotai", "packs", "github.com", owner))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".") {
			out = append(out, e.Name())
		}
	}
	return out
}

func TestAddGitPackClonesValidatesAndRegisters(t *testing.T) {
	gitHome(t)
	home := t.TempDir()
	r := newRemote(t, "https://github.com/acme/pack")
	runGit(t, r.dir, "tag", "v1")
	res, err := app.AddGitPack(context.Background(), home, "https://github.com/Acme/Pack.git", app.PackGitOptions{Ref: "v1"})
	if err != nil {
		t.Fatalf("AddGitPack() error = %v", err)
	}
	want := app.PackSource{URL: "https://github.com/Acme/Pack.git", Ref: "v1", FullRef: "refs/tags/v1", Commit: r.head(t)}
	if res.Outcome != app.PackAdded || res.Pack.ID != "github.com/acme/pack" || res.Pack.Source == nil || *res.Pack.Source != want {
		t.Fatalf("result = %+v, want github.com/acme/pack added at v1", res)
	}
	if res.Pack.Path != clonePath(home, "github.com/acme/pack") || res.Pack.Name != "minimal-pack" {
		t.Errorf("pack = %+v, want the clone under ~/.dotai/packs", res.Pack)
	}
	if _, err := os.Stat(filepath.Join(res.Pack.Path, "dotai.yaml")); err != nil {
		t.Errorf("the clone has no manifest: %v", err)
	}
	if len(res.Warnings) != 0 {
		t.Errorf("warnings = %+v, want none for a tag", res.Warnings)
	}
	if got := packLeftovers(t, home, "acme"); len(got) != 0 {
		t.Errorf("the add left %v", got)
	}
	if _, err := os.Stat(filepath.Join(home, ".dotai", "lock")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("the add left its lock: %v", err)
	}
	list, err := app.ListPacks(home)
	if err != nil || len(list.Packs) != 1 || list.Packs[0].ID != "github.com/acme/pack" {
		t.Errorf("ListPacks() = %+v, %v", list.Packs, err)
	}
}

func TestAddGitPackOfABranchWarnsThatItCanMove(t *testing.T) {
	gitHome(t)
	home := t.TempDir()
	newRemote(t, "https://github.com/acme/pack")
	res, err := app.AddGitPack(context.Background(), home, "git@github.com:acme/pack.git", app.PackGitOptions{})
	if err != nil {
		t.Fatalf("AddGitPack() error = %v", err)
	}
	if res.Pack.Source.FullRef != "refs/heads/main" || res.Pack.Source.Ref != "" {
		t.Errorf("source = %+v, want the default branch resolved to refs/heads/main", res.Pack.Source)
	}
	if !slices.ContainsFunc(res.Warnings, func(w app.PackWarning) bool { return w.Code == "ref-branch" }) {
		t.Errorf("warnings = %+v, want ref-branch", res.Warnings)
	}
}

func TestAddGitPackOfAnotherIdentityAtTheSameCommitWarns(t *testing.T) {
	gitHome(t)
	home := t.TempDir()
	first := newRemote(t, "https://github.com/acme/pack")
	runGit(t, first.dir, "tag", "v1")
	moved := &remoteRepo{dir: first.dir, url: "https://github.com/newowner/pack"}
	serveRemotes(t, first, moved)
	if _, err := app.AddGitPack(context.Background(), home, first.url, app.PackGitOptions{Ref: "v1"}); err != nil {
		t.Fatal(err)
	}
	res, err := app.AddGitPack(context.Background(), home, moved.url, app.PackGitOptions{Ref: "v1"})
	if err != nil {
		t.Fatalf("AddGitPack() error = %v", err)
	}
	if res.Outcome != app.PackAdded || !slices.ContainsFunc(res.Warnings, func(w app.PackWarning) bool {
		return w.Code == "pack-same-commit" && strings.Contains(w.Message, "github.com/acme/pack")
	}) {
		t.Errorf("result = %+v, want a new pack with pack-same-commit naming the other", res)
	}
}

func TestAddGitPackThatDoesNotValidateRegistersNothing(t *testing.T) {
	gitHome(t)
	home := t.TempDir()
	r := newRemote(t, "https://github.com/acme/pack")
	r.commit(t, map[string]string{"dotai.yaml": "schema: dotai.pack/v1\nname: x\n"})
	_, err := app.AddGitPack(context.Background(), home, r.url, app.PackGitOptions{})
	var invalid *app.PackInvalidError
	if !errors.As(err, &invalid) || len(invalid.Result.Diagnostics) == 0 {
		t.Fatalf("error = %v, want a PackInvalidError with the diagnostics", err)
	}
	assertNothingAdded(t, home)
}

func TestAddGitPackOfARefusedTreeRegistersNothing(t *testing.T) {
	gitHome(t)
	home := t.TempDir()
	r := newRemote(t, "https://github.com/acme/pack")
	blob := runGitIn(t, r.dir, "x\n", "hash-object", "-w", "--stdin")
	runGit(t, r.dir, "update-index", "--add", "--cacheinfo", "120000,"+blob+",base/link")
	runGit(t, r.dir, "commit", "-q", "-m", "link")
	_, err := app.AddGitPack(context.Background(), home, r.url, app.PackGitOptions{})
	if _, ok := errors.AsType[*app.PackTreeRefusedError](err); !ok {
		t.Fatalf("error = %v, want a PackTreeRefusedError", err)
	}
	assertNothingAdded(t, home)
}

func TestAddGitPackRefusesBeforeFetching(t *testing.T) {
	home := t.TempDir()
	for raw, ref := range map[string]string{
		"https://gitlab.com/acme/pack": "",
		"https://github.com/acme/pack": "--upload-pack=x",
	} {
		if _, err := app.AddGitPack(context.Background(), home, raw, app.PackGitOptions{Ref: ref}); err == nil {
			t.Errorf("AddGitPack(%s, %s) succeeded", raw, ref)
		}
	}
	assertNothingAdded(t, home)
}

func TestAddGitPackOfAnUnreachableRemoteFails(t *testing.T) {
	gitHome(t)
	home := t.TempDir()
	gone := &remoteRepo{dir: filepath.Join(t.TempDir(), "gone"), url: "https://github.com/acme/gone"}
	serveRemotes(t, gone)
	_, err := app.AddGitPack(context.Background(), home, gone.url, app.PackGitOptions{})
	if _, ok := errors.AsType[*app.FetchFailedError](err); !ok {
		t.Errorf("error = %v, want a FetchFailedError", err)
	}
	assertNothingAdded(t, home)
}

// assertNothingAdded checks that home has no registry and no clone, nor a
// temporary directory of one.
func assertNothingAdded(t *testing.T, home string) {
	t.Helper()
	if _, err := os.Stat(filepath.Join(home, ".dotai", "packs.json")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("packs.json was written: %v", err)
	}
	var found []string
	_ = filepath.WalkDir(filepath.Join(home, ".dotai", "packs"), func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if !d.IsDir() || strings.HasPrefix(d.Name(), ".") {
			found = append(found, p)
			if d.IsDir() {
				return filepath.SkipDir
			}
		}
		return nil
	})
	if len(found) > 0 {
		t.Errorf("~/.dotai/packs holds %v", found)
	}
}

func TestAPlacementThatFailsLeavesTheRegistryAndTheOldCloneAsTheyWere(t *testing.T) {
	gitHome(t)
	home := t.TempDir()
	r := newRemote(t, "https://github.com/acme/pack")
	// A clone left by a run that stopped before it wrote the registry.
	target := clonePath(home, "github.com/acme/pack")
	if err := os.MkdirAll(target, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(target, "left"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, step := range []string{"place", "write"} {
		t.Run(step, func(t *testing.T) {
			restore := app.SetPackFault(func(s string) error {
				if s == step {
					return errors.New("injected")
				}
				return nil
			})
			defer restore()
			_, err := app.AddGitPack(context.Background(), home, r.url, app.PackGitOptions{})
			if err == nil || !strings.Contains(err.Error(), "injected") {
				t.Fatalf("error = %v, want the injected failure", err)
			}
			if _, err := os.Stat(filepath.Join(target, "left")); err != nil {
				t.Errorf("the old directory was not put back: %v", err)
			}
			if got := packLeftovers(t, home, "acme"); len(got) != 0 {
				t.Errorf("the failed add left %v", got)
			}
			if _, err := os.Stat(filepath.Join(home, ".dotai", "packs.json")); !errors.Is(err, os.ErrNotExist) {
				t.Errorf("packs.json was written: %v", err)
			}
		})
	}
	// Without a fault, the leftover is replaced by the checked clone.
	if _, err := app.AddGitPack(context.Background(), home, r.url, app.PackGitOptions{}); err != nil {
		t.Fatalf("AddGitPack() error = %v", err)
	}
	if _, err := os.Stat(filepath.Join(target, "left")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("the leftover survived: %v", err)
	}
	if got := packLeftovers(t, home, "acme"); len(got) != 0 {
		t.Errorf("the add left %v", got)
	}
}

// TestRemovingAGitPackDeletesItsClone removes a git pack by its id and by
// the path of its clone: either way the entry goes, the clone is deleted
// and nothing is left over.
func TestRemovingAGitPackDeletesItsClone(t *testing.T) {
	for _, tt := range []struct {
		name   string
		remove func(home string, added app.PackAddResult) (app.PackRemoveResult, error)
	}{
		{"by id", func(home string, _ app.PackAddResult) (app.PackRemoveResult, error) {
			return app.RemovePackID(home, "github.com/acme/pack")
		}},
		{"by path", func(home string, added app.PackAddResult) (app.PackRemoveResult, error) {
			return app.RemovePack(home, added.Pack.Path)
		}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			gitHome(t)
			home := t.TempDir()
			newRemote(t, "https://github.com/acme/pack")
			added, err := app.AddGitPack(context.Background(), home, "https://github.com/acme/pack", app.PackGitOptions{})
			if err != nil {
				t.Fatal(err)
			}
			res, err := tt.remove(home, added)
			if err != nil {
				t.Fatalf("remove error = %v", err)
			}
			if len(res.Packs) != 0 || len(res.Removed) != 1 || res.Deleted != added.Pack.Path {
				t.Errorf("result = %+v, want the pack removed and its clone deleted", res)
			}
			if _, err := os.Stat(added.Pack.Path); !errors.Is(err, os.ErrNotExist) {
				t.Errorf("the clone is still there: %v", err)
			}
			if got := packLeftovers(t, home, "acme"); len(got) != 0 {
				t.Errorf("the removal left %v", got)
			}
		})
	}
}

func TestRemovePackIDNeverDeletesALocalPack(t *testing.T) {
	home, dir := t.TempDir(), packCopy(t)
	if _, err := app.AddLocalPack(home, dir); err != nil {
		t.Fatal(err)
	}
	res, err := app.RemovePackID(home, "local/minimal-pack")
	if err != nil || len(res.Packs) != 0 || res.Deleted != "" {
		t.Fatalf("RemovePackID() = %+v, %v, want the entry gone and nothing deleted", res, err)
	}
	if _, err := os.Stat(filepath.Join(dir, "dotai.yaml")); err != nil {
		t.Errorf("the local pack was touched: %v", err)
	}
}

func TestRemovePackIDKeepsAGitPackOutsideItsClonePath(t *testing.T) {
	home, dir := t.TempDir(), packCopy(t)
	writeRegistry(t, home, `{"schema":"dotai.packs/v2","packs":[{"id":"github.com/acme/pack","path":`+strconvQuote(dir)+
		`,"source":{"url":"https://github.com/acme/pack","ref":"","fullRef":"refs/heads/main","commit":"`+strings.Repeat("a", 40)+`"}}]}`)
	res, err := app.RemovePackID(home, "github.com/acme/pack")
	if err != nil || len(res.Packs) != 0 || res.Deleted != "" {
		t.Fatalf("RemovePackID() = %+v, %v, want the entry gone and nothing deleted", res, err)
	}
	if _, err := os.Stat(filepath.Join(dir, "dotai.yaml")); err != nil {
		t.Errorf("a directory outside ~/.dotai/packs was deleted: %v", err)
	}
}

func TestRemovePackIDOfAnUnknownPackIsAUsageError(t *testing.T) {
	home := t.TempDir()
	for _, id := range []string{"", "github.com/acme/none"} {
		if _, err := app.RemovePackID(home, id); !errors.As(err, new(*app.UsageError)) {
			t.Errorf("RemovePackID(%q) error = %v, want a usage error", id, err)
		}
	}
}
