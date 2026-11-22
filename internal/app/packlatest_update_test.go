package app_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lartweib/dotai/internal/app"
)

func TestUpdatePackOfLatestRefusesToGoDownAndAnExplicitRefDoes(t *testing.T) {
	gitHome(t)
	home := t.TempDir()
	r, commits := releaseRemote(t, "https://github.com/acme/pack", "v1.0.0", "v1.1.0")
	if _, err := app.AddGitPack(context.Background(), home, "https://github.com/acme/pack", app.PackGitOptions{Ref: "latest"}); err != nil {
		t.Fatal(err)
	}
	// The release followed is deleted: the highest tag is now v1.0.0.
	runGit(t, r.dir, "tag", "-d", "v1.1.0")
	asked := false
	opts := app.PackGitOptions{Consent: func(app.PackUpdatePlan) (bool, error) { asked = true; return true, nil }}
	_, err := app.UpdatePack(context.Background(), home, "github.com/acme/pack", opts)
	down, ok := errors.AsType[*app.LatestDowngradeError](err)
	if !ok {
		t.Fatalf("UpdatePack() error = %v, want a LatestDowngradeError", err)
	}
	for _, want := range []string{"github.com/acme/pack", "v1.1.0", "v1.0.0", "--ref v1.0.0"} {
		if !strings.Contains(down.Error(), want) {
			t.Errorf("error %q does not mention %q", down, want)
		}
	}
	if _, _, err := app.PlanPackUpdate(context.Background(), home, "github.com/acme/pack", app.PackGitOptions{}); !errors.As(err, &down) {
		t.Errorf("PlanPackUpdate() error = %v, want the same refusal", err)
	}
	if _, err := app.AddGitPack(context.Background(), home, "https://github.com/acme/pack", app.PackGitOptions{Consent: agree}); !errors.As(err, &down) {
		t.Errorf("adding again without a ref: error = %v, want the same refusal", err)
	}
	list, _ := app.ListPacks(home)
	if asked || list.Packs[0].Source.FullRef != "refs/tags/v1.1.0" {
		t.Errorf("the refusal asked for consent (%v) or moved the pack to %+v", asked, list.Packs[0].Source)
	}
	if got := packLeftovers(t, home, "acme"); len(got) != 0 {
		t.Errorf("the refusal left %v", got)
	}
	// Going down on purpose, with the tag itself, works and stops following.
	res, err := app.AddGitPack(context.Background(), home, "https://github.com/acme/pack", app.PackGitOptions{Ref: "v1.0.0", Consent: agree})
	if err != nil {
		t.Fatalf("AddGitPack(v1.0.0) error = %v", err)
	}
	src := res.Pack.Source
	if res.Outcome != app.PackUpdated || src.Ref != "v1.0.0" || src.Commit != commits["v1.0.0"] || src.TracksLatest() {
		t.Errorf("result = %+v, want the pack on v1.0.0, no longer following latest", res)
	}
}

func TestUpdatePackOfLatestFollowsATagRecreatedAtTheSameVersion(t *testing.T) {
	gitHome(t)
	home := t.TempDir()
	r, _ := releaseRemote(t, "https://github.com/acme/pack", "v1.0.0")
	if _, err := app.AddGitPack(context.Background(), home, "https://github.com/acme/pack", app.PackGitOptions{Ref: "latest"}); err != nil {
		t.Fatal(err)
	}
	moved := r.commit(t, map[string]string{"base/dotai/tools/x.sh": "echo x\n"})
	runGit(t, r.dir, "tag", "-f", "v1.0.0")
	res, err := app.UpdatePack(context.Background(), home, "github.com/acme/pack", app.PackGitOptions{Consent: agree})
	if err != nil || res.Outcome != app.PackUpdated || res.Pack.Source.Commit != moved {
		t.Errorf("UpdatePack() = %+v, %v, want the moved tag shown and installed with consent", res.Pack.Source, err)
	}
}

// legacyLatest rewrites the registry so that its only pack records the ref
// latest, as one added before latest meant a release did.
func legacyLatest(t *testing.T, home string) {
	t.Helper()
	path := filepath.Join(home, ".dotai", "packs.json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Schema string `json:"schema"`
		Packs  []struct {
			ID     string         `json:"id"`
			Path   string         `json:"path"`
			Source map[string]any `json:"source"`
		} `json:"packs"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatal(err)
	}
	doc.Packs[0].Source["ref"] = "latest"
	out, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, out, 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestALegacyLatestBranchStaysAndAnExplicitRefLatestMovesToTheRelease(t *testing.T) {
	gitHome(t)
	home := t.TempDir()
	r, commits := releaseRemote(t, "https://github.com/acme/pack", "v1.0.0")
	runGit(t, r.dir, "checkout", "-q", "-b", "latest")
	if _, err := app.AddGitPack(context.Background(), home, "https://github.com/acme/pack", app.PackGitOptions{Ref: "refs/heads/latest"}); err != nil {
		t.Fatal(err)
	}
	legacyLatest(t, home)
	list, err := app.ListPacks(home)
	if err != nil || list.Packs[0].Source.Ref != "latest" || list.Packs[0].Source.TracksLatest() {
		t.Fatalf("registry = %+v, %v, want the ref latest on a branch", list.Packs, err)
	}
	// The branch moves on, past the release: an update follows the branch.
	onBranch := r.commit(t, map[string]string{"base/dotai/tools/b.sh": "echo b\n"})
	res, err := app.UpdatePack(context.Background(), home, "github.com/acme/pack", app.PackGitOptions{Consent: agree})
	if err != nil {
		t.Fatalf("UpdatePack() error = %v", err)
	}
	if src := res.Pack.Source; src.FullRef != "refs/heads/latest" || src.Commit != onBranch || src.TracksLatest() {
		t.Errorf("source after update = %+v, want the branch latest at %s", src, onBranch)
	}
	// Asking for latest again is a new request: the highest release.
	res, err = app.AddGitPack(context.Background(), home, "https://github.com/acme/pack", app.PackGitOptions{Ref: "latest", Consent: agree})
	if err != nil {
		t.Fatalf("AddGitPack(latest) error = %v", err)
	}
	if src := res.Pack.Source; src.FullRef != "refs/tags/v1.0.0" || src.Commit != commits["v1.0.0"] || !src.TracksLatest() {
		t.Errorf("source after --ref latest = %+v, want the release v1.0.0", src)
	}
}

func TestLatestWhoseHighestTagIsNotACommitIsAFetchFailureNamingTheTag(t *testing.T) {
	gitHome(t)
	home := t.TempDir()
	r, _ := releaseRemote(t, "https://github.com/acme/pack", "v1.0.0")
	blob := runGitIn(t, r.dir, "x\n", "hash-object", "-w", "--stdin")
	runGit(t, r.dir, "tag", "v2.0.0", blob)
	for _, ref := range []string{"latest", "v2.0.0"} {
		_, err := app.AddGitPack(context.Background(), home, "https://github.com/acme/pack", app.PackGitOptions{Ref: ref})
		fe, ok := errors.AsType[*app.FetchFailedError](err)
		if !ok || !strings.Contains(fe.Error(), "v2.0.0") {
			t.Errorf("--ref %s: error = %v, want a FetchFailedError naming v2.0.0", ref, err)
		}
		assertNothingAdded(t, home)
	}
}
