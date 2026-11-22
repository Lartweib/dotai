package app_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/lartweib/dotai/internal/app"
)

// releaseRemote is a remote whose commits carry the tags in order: each
// entry is a tag, "annotated:<name>" for an annotated one, on a commit of
// its own, and the commits are returned by tag name.
func releaseRemote(t *testing.T, url string, tags ...string) (*remoteRepo, map[string]string) {
	t.Helper()
	r := newRemote(t, url)
	commits := map[string]string{}
	for _, tag := range tags {
		name, annotated := strings.CutPrefix(tag, "annotated:")
		commits[name] = r.commit(t, map[string]string{"base/.rulesync/rules/release.md": "release " + name + "\n"})
		if annotated {
			runGit(t, r.dir, "tag", "-a", "-m", "release "+name, name)
		} else {
			runGit(t, r.dir, "tag", name)
		}
	}
	return r, commits
}

func TestLatestFollowsTheHighestStrictSemVerTag(t *testing.T) {
	for name, tags := range map[string][]string{
		"annotated tag highest":   {"v1.9.0", "annotated:v1.10.0"},
		"lightweight tag highest": {"annotated:v1.9.0", "v1.10.0"},
	} {
		t.Run(name, func(t *testing.T) {
			gitHome(t)
			home := t.TempDir()
			r, commits := releaseRemote(t, "https://github.com/acme/pack", tags...)
			// The tags after the release are ones latest must not follow: a
			// pre-release, a build suffix, a short or a padded version, a
			// version without the v and a name that is not a version.
			for _, junk := range []string{"v2.0.0-rc.1", "v2.0.0+build.5", "v3", "v3.1", "v04.0.0", "2.0.0", "release-5.0.0", "v9.0.0.0", "latest"} {
				r.commit(t, map[string]string{"base/.rulesync/rules/junk.md": junk + "\n"})
				runGit(t, r.dir, "tag", junk)
			}
			res, err := app.AddGitPack(context.Background(), home, "https://github.com/acme/pack", app.PackGitOptions{Ref: "latest"})
			if err != nil {
				t.Fatalf("AddGitPack() error = %v", err)
			}
			want := app.PackSource{URL: "https://github.com/acme/pack", Ref: "latest", FullRef: "refs/tags/v1.10.0", Commit: commits["v1.10.0"]}
			if res.Outcome != app.PackAdded || *res.Pack.Source != want {
				t.Errorf("source = %+v, want %+v", res.Pack.Source, want)
			}
			if !res.Pack.Source.TracksLatest() {
				t.Error("the added pack does not track latest")
			}
			for _, w := range res.Warnings {
				t.Errorf("warning %+v: a release tag cannot move like a branch", w)
			}
		})
	}
}

func TestLatestWithoutAReleaseTagIsRefusedAndAddsNothing(t *testing.T) {
	gitHome(t)
	home := t.TempDir()
	releaseRemote(t, "https://github.com/acme/pack", "v1.0.0-rc.1", "annotated:v1.1", "2.0.0")
	_, err := app.AddGitPack(context.Background(), home, "https://github.com/acme/pack", app.PackGitOptions{Ref: "latest"})
	nr, ok := errors.AsType[*app.NoReleaseTagError](err)
	if !ok {
		t.Fatalf("error = %v, want a NoReleaseTagError", err)
	}
	for _, want := range []string{"https://github.com/acme/pack", "vX.Y.Z", "--ref <branch|tag|sha>", "default branch"} {
		if !strings.Contains(nr.Error(), want) {
			t.Errorf("error %q does not mention %q", nr, want)
		}
	}
	assertNothingAdded(t, home)
	if got := packLeftovers(t, home, "acme"); len(got) != 0 {
		t.Errorf("the refusal left %v", got)
	}
}

func TestUpdatePackOfLatestMovesToANewerReleaseAndShowsTheChanges(t *testing.T) {
	gitHome(t)
	home := t.TempDir()
	r, commits := releaseRemote(t, "https://github.com/acme/pack", "v1.0.0")
	if _, err := app.AddGitPack(context.Background(), home, "https://github.com/acme/pack", app.PackGitOptions{Ref: "latest"}); err != nil {
		t.Fatal(err)
	}
	// A pre-release below the release and an unreleased commit above it
	// are not what latest follows.
	r.commit(t, map[string]string{"base/dotai/tools/rc.sh": "echo rc\n"})
	runGit(t, r.dir, "tag", "v1.1.0-rc.1")
	second := r.commit(t, map[string]string{"base/dotai/tools/new.sh": "echo new\n"})
	runGit(t, r.dir, "tag", "-a", "-m", "release", "v1.1.0")
	r.commit(t, map[string]string{"base/dotai/tools/unreleased.sh": "echo no\n"})

	var shown *app.PackUpdatePlan
	res, err := app.UpdatePack(context.Background(), home, "github.com/acme/pack", app.PackGitOptions{
		Consent: func(p app.PackUpdatePlan) (bool, error) { shown = &p; return true, nil },
	})
	if err != nil {
		t.Fatalf("UpdatePack() error = %v", err)
	}
	if shown == nil || shown.From.Commit != commits["v1.0.0"] || shown.To.Commit != second || shown.To.FullRef != "refs/tags/v1.1.0" {
		t.Fatalf("the consent saw %+v, want the move from v1.0.0 to v1.1.0 at %s", shown, second)
	}
	if len(shown.Changes) != 2 {
		t.Errorf("changes = %+v, want the two tools added since v1.0.0", shown.Changes)
	}
	src := res.Pack.Source
	if res.Outcome != app.PackUpdated || src.Ref != "latest" || src.FullRef != "refs/tags/v1.1.0" || src.Commit != second || !src.TracksLatest() {
		t.Errorf("result = %+v, want the pack updated to v1.1.0 and still tracking latest", res)
	}
	list, _ := app.ListPacks(home)
	if got := list.Packs[0].Source; *got != *src {
		t.Errorf("the registry says %+v, want %+v", got, src)
	}
}

func TestUpdatePackOfLatestWithoutANewerReleaseIsUnchanged(t *testing.T) {
	gitHome(t)
	home := t.TempDir()
	r, commits := releaseRemote(t, "https://github.com/acme/pack", "v1.0.0")
	if _, err := app.AddGitPack(context.Background(), home, "https://github.com/acme/pack", app.PackGitOptions{Ref: "latest"}); err != nil {
		t.Fatal(err)
	}
	// A new commit and a pre-release are not a newer release.
	r.commit(t, map[string]string{"base/dotai/tools/x.sh": "echo x\n"})
	runGit(t, r.dir, "tag", "v1.1.0-rc.1")
	asked := false
	res, err := app.UpdatePack(context.Background(), home, "github.com/acme/pack", app.PackGitOptions{
		Consent: func(app.PackUpdatePlan) (bool, error) { asked = true; return true, nil },
	})
	if err != nil {
		t.Fatalf("UpdatePack() error = %v", err)
	}
	if res.Outcome != app.PackUnchanged || asked || res.Pack.Source.Commit != commits["v1.0.0"] || res.Pack.Source.Ref != "latest" {
		t.Errorf("result = %+v (asked %v), want unchanged at v1.0.0 without asking", res, asked)
	}
}

func TestUpdatePackOfAnExplicitTagStaysAtItsTag(t *testing.T) {
	gitHome(t)
	home := t.TempDir()
	r, commits := releaseRemote(t, "https://github.com/acme/pack", "v1.0.0")
	if _, err := app.AddGitPack(context.Background(), home, "https://github.com/acme/pack", app.PackGitOptions{Ref: "v1.0.0"}); err != nil {
		t.Fatal(err)
	}
	r.commit(t, map[string]string{"base/dotai/tools/x.sh": "echo x\n"})
	runGit(t, r.dir, "tag", "v2.0.0")
	res, err := app.UpdatePack(context.Background(), home, "github.com/acme/pack", app.PackGitOptions{})
	if err != nil {
		t.Fatalf("UpdatePack() error = %v", err)
	}
	src := res.Pack.Source
	if res.Outcome != app.PackUnchanged || src.Ref != "v1.0.0" || src.Commit != commits["v1.0.0"] || src.TracksLatest() {
		t.Errorf("result = %+v, want the pack left at v1.0.0", res)
	}
}

func TestAddingAgainWithRefLatestStartsFollowingReleases(t *testing.T) {
	gitHome(t)
	home := t.TempDir()
	r, _ := releaseRemote(t, "https://github.com/acme/pack", "v1.0.0")
	if _, err := app.AddGitPack(context.Background(), home, "https://github.com/acme/pack", app.PackGitOptions{Ref: "v1.0.0"}); err != nil {
		t.Fatal(err)
	}
	second := r.commit(t, map[string]string{"base/dotai/tools/x.sh": "echo x\n"})
	runGit(t, r.dir, "tag", "v1.2.0")
	res, err := app.AddGitPack(context.Background(), home, "https://github.com/acme/pack", app.PackGitOptions{Ref: "latest", Consent: agree})
	if err != nil {
		t.Fatalf("AddGitPack() error = %v", err)
	}
	if res.Outcome != app.PackUpdated || res.Pack.Source.Ref != "latest" || res.Pack.Source.FullRef != "refs/tags/v1.2.0" || res.Pack.Source.Commit != second {
		t.Errorf("result = %+v, want the pack updated to v1.2.0 and tracking latest", res)
	}
	// Adding it again without a ref keeps following.
	third := r.commit(t, map[string]string{"base/dotai/tools/y.sh": "echo y\n"})
	runGit(t, r.dir, "tag", "v1.3.0")
	res, err = app.AddGitPack(context.Background(), home, "https://github.com/acme/pack", app.PackGitOptions{Consent: agree})
	if err != nil || res.Pack.Source.Ref != "latest" || res.Pack.Source.Commit != third {
		t.Errorf("AddGitPack() = %+v, %v, want the pack on v1.3.0, still tracking latest", res.Pack.Source, err)
	}
}

func TestTracksLatestOnlyForTheReleaseTagLatestResolved(t *testing.T) {
	for name, tc := range map[string]struct {
		src  app.PackSource
		want bool
	}{
		"latest at a release tag":     {app.PackSource{Ref: "latest", FullRef: "refs/tags/v1.2.3"}, true},
		"a branch named latest":       {app.PackSource{Ref: "latest", FullRef: "refs/heads/latest"}, false},
		"a tag named latest":          {app.PackSource{Ref: "latest", FullRef: "refs/tags/latest"}, false},
		"an explicit tag":             {app.PackSource{Ref: "v1.2.3", FullRef: "refs/tags/v1.2.3"}, false},
		"the default branch":          {app.PackSource{Ref: "", FullRef: "refs/heads/main"}, false},
		"latest with no resolved tag": {app.PackSource{Ref: "latest"}, false},
	} {
		if got := tc.src.TracksLatest(); got != tc.want {
			t.Errorf("%s: TracksLatest() = %v, want %v", name, got, tc.want)
		}
	}
	if got := (app.PackSource{Ref: "latest", FullRef: "refs/tags/v0.1.0"}).Tracking(); got != "latest → v0.1.0" {
		t.Errorf("Tracking() = %q, want latest → v0.1.0", got)
	}
	if got := (app.PackSource{Ref: "v0.1.0", FullRef: "refs/tags/v0.1.0"}).Tracking(); got != "" {
		t.Errorf("Tracking() of an explicit tag = %q, want none", got)
	}
}
