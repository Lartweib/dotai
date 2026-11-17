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

// agree consents to every update.
func agree(app.PackUpdatePlan) (bool, error) { return true, nil }

// addedRemote adds the pack of a new remote of url to home, at ref.
func addedRemote(t *testing.T, home, url, ref string) *remoteRepo {
	t.Helper()
	r := newRemote(t, url)
	if ref != "" && ref != "main" {
		runGit(t, r.dir, "tag", ref)
	}
	if _, err := app.AddGitPack(context.Background(), home, url, app.PackGitOptions{Ref: ref}); err != nil {
		t.Fatalf("AddGitPack() error = %v", err)
	}
	return r
}

func TestUpdatePackShowsTheExecutableChangesAndSwapsTheClone(t *testing.T) {
	gitHome(t)
	home := t.TempDir()
	r := addedRemote(t, home, "https://github.com/acme/pack", "")
	first := r.head(t)
	second := r.commit(t, map[string]string{
		"base/dotai/tools/new.sh":          "echo new\n",
		"base/.rulesync/rules/overview.md": "a rule changed\n",
	})
	var shown *app.PackUpdatePlan
	res, err := app.UpdatePack(context.Background(), home, "github.com/acme/pack", app.PackGitOptions{
		Consent: func(p app.PackUpdatePlan) (bool, error) { shown = &p; return true, nil },
	})
	if err != nil {
		t.Fatalf("UpdatePack() error = %v", err)
	}
	if shown == nil || shown.From.Commit != first || shown.To.Commit != second {
		t.Fatalf("the consent saw %+v, want the move from %s to %s", shown, first, second)
	}
	if len(shown.Changes) != 1 || shown.Changes[0].Path != "base/dotai/tools/new.sh" || shown.Changes[0].Change != app.ChangeAdded {
		t.Errorf("changes = %+v, want the new tool alone", shown.Changes)
	}
	if res.Outcome != app.PackUpdated || res.Update == nil || res.Pack.Source.Commit != second || res.Pack.Source.FullRef != "refs/heads/main" {
		t.Errorf("result = %+v, want the pack updated to %s", res, second)
	}
	if _, err := os.Stat(filepath.Join(res.Pack.Path, "base", "dotai", "tools", "new.sh")); err != nil {
		t.Errorf("the clone was not replaced: %v", err)
	}
	if got := packLeftovers(t, home, "acme"); len(got) != 0 {
		t.Errorf("the update left %v", got)
	}
}

func TestUpdatePackWithoutConsentChangesNothing(t *testing.T) {
	gitHome(t)
	home := t.TempDir()
	r := addedRemote(t, home, "https://github.com/acme/pack", "")
	first := r.head(t)
	r.commit(t, map[string]string{"base/.rulesync/mcp.jsonc": "{\"mcpServers\":{}}\n"})
	for name, consent := range map[string]func(app.PackUpdatePlan) (bool, error){
		"none":    nil,
		"refused": func(app.PackUpdatePlan) (bool, error) { return false, nil },
	} {
		_, err := app.UpdatePack(context.Background(), home, "github.com/acme/pack", app.PackGitOptions{Consent: consent})
		ce, ok := errors.AsType[*app.PackConsentError](err)
		if !ok || len(ce.Plan.Changes) != 1 {
			t.Fatalf("%s: error = %v, want a PackConsentError with the plan", name, err)
		}
		list, _ := app.ListPacks(home)
		if list.Packs[0].Source.Commit != first {
			t.Errorf("%s: the registry moved to %s", name, list.Packs[0].Source.Commit)
		}
		if got := packLeftovers(t, home, "acme"); len(got) != 0 {
			t.Errorf("%s: the refused update left %v", name, got)
		}
	}
}

func TestUpdatePackStopsWhenConfirmRefusesThePlan(t *testing.T) {
	gitHome(t)
	home := t.TempDir()
	r := addedRemote(t, home, "https://github.com/acme/pack", "")
	r.commit(t, map[string]string{"mods/hello/hooks/register.js": "changed()\n"})
	stop := errors.New("not the plan shown")
	_, err := app.UpdatePack(context.Background(), home, "github.com/acme/pack", app.PackGitOptions{
		Confirm: func(app.PackUpdatePlan) error { return stop },
		Consent: agree,
	})
	if !errors.Is(err, stop) {
		t.Errorf("error = %v, want the confirm's", err)
	}
}

func TestUpdatePackAtTheSameCommitIsUnchanged(t *testing.T) {
	gitHome(t)
	home := t.TempDir()
	addedRemote(t, home, "https://github.com/acme/pack", "v1")
	res, err := app.UpdatePack(context.Background(), home, "github.com/acme/pack", app.PackGitOptions{})
	if err != nil || res.Outcome != app.PackUnchanged || res.Pack.Source.FullRef != "refs/tags/v1" {
		t.Errorf("UpdatePack() = %+v, %v, want unchanged at v1 without asking", res, err)
	}
}

func TestPlanPackUpdateChangesNothing(t *testing.T) {
	gitHome(t)
	home := t.TempDir()
	r := addedRemote(t, home, "https://github.com/acme/pack", "")
	first := r.head(t)
	second := r.commit(t, map[string]string{"base/dotai/settings.yaml": "claudecode:\n  model: x\n"})
	p, _, err := app.PlanPackUpdate(context.Background(), home, "github.com/acme/pack", app.PackGitOptions{})
	if err != nil {
		t.Fatalf("PlanPackUpdate() error = %v", err)
	}
	if p.From.Commit != first || p.To.Commit != second || len(p.Changes) != 1 || !strings.Contains(p.Changes[0].Diff, "+  model: x") {
		t.Errorf("plan = %+v", p)
	}
	if list, _ := app.ListPacks(home); list.Packs[0].Source.Commit != first {
		t.Error("the plan moved the pack")
	}
	if got := packLeftovers(t, home, "acme"); len(got) != 0 {
		t.Errorf("the plan left %v", got)
	}
}

func TestAddingTheSameIdentityAgainIsAnUpdate(t *testing.T) {
	gitHome(t)
	home := t.TempDir()
	r := addedRemote(t, home, "https://github.com/acme/pack", "v1")
	second := r.commit(t, map[string]string{"base/.rulesync/rules/more.md": "# more\n"})
	runGit(t, r.dir, "tag", "v2")
	// Another form and case of the URL, another ref.
	res, err := app.AddGitPack(context.Background(), home, "git@github.com:ACME/pack.git", app.PackGitOptions{Ref: "v2", Consent: agree})
	if err != nil {
		t.Fatalf("AddGitPack() again error = %v", err)
	}
	want := app.PackSource{URL: "git@github.com:ACME/pack.git", Ref: "v2", FullRef: "refs/tags/v2", Commit: second}
	if res.Outcome != app.PackUpdated || *res.Pack.Source != want || len(res.Packs) != 1 {
		t.Errorf("result = %+v, want the one pack moved to v2 with the URL last used", res)
	}
	// Without --ref, the recorded ref stays.
	res, err = app.AddGitPack(context.Background(), home, "https://github.com/acme/pack", app.PackGitOptions{Consent: agree})
	if err != nil || res.Outcome != app.PackUnchanged || res.Pack.Source.Ref != "v2" || res.Pack.Source.URL != "https://github.com/acme/pack" {
		t.Errorf("AddGitPack() without a ref = %+v, %v, want v2 kept and the URL last used", res.Pack.Source, err)
	}
}

func TestAnUpdateThatFailsToPlaceKeepsTheOldClone(t *testing.T) {
	gitHome(t)
	home := t.TempDir()
	r := addedRemote(t, home, "https://github.com/acme/pack", "")
	first := r.head(t)
	r.commit(t, map[string]string{"base/dotai/tools/x.sh": "x\n"})
	restore := app.SetPackFault(func(s string) error {
		if s == "write" {
			return errors.New("injected")
		}
		return nil
	})
	defer restore()
	if _, err := app.UpdatePack(context.Background(), home, "github.com/acme/pack", app.PackGitOptions{Consent: agree}); err == nil {
		t.Fatal("UpdatePack() succeeded despite the fault")
	}
	list, _ := app.ListPacks(home)
	if list.Packs[0].Source.Commit != first {
		t.Errorf("the registry moved")
	}
	if _, err := os.Stat(filepath.Join(list.Packs[0].Path, "base", "dotai", "tools", "x.sh")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("the new clone stayed in place: %v", err)
	}
	if _, err := os.Stat(filepath.Join(list.Packs[0].Path, "dotai.yaml")); err != nil {
		t.Errorf("the old clone was not put back: %v", err)
	}
	if got := packLeftovers(t, home, "acme"); len(got) != 0 {
		t.Errorf("the failed update left %v", got)
	}
}

func TestUpdatePackRefusesALocalOrUnknownPack(t *testing.T) {
	home, dir := t.TempDir(), packCopy(t)
	if _, err := app.AddLocalPack(home, dir); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"local/minimal-pack", "github.com/acme/none", ""} {
		if _, err := app.UpdatePack(context.Background(), home, id, app.PackGitOptions{}); !errors.As(err, new(*app.UsageError)) {
			t.Errorf("UpdatePack(%q) error = %v, want a usage error", id, err)
		}
	}
}

func TestUpdatePackOfAPackWhoseCloneIsGoneFetchesItAgain(t *testing.T) {
	gitHome(t)
	home := t.TempDir()
	addedRemote(t, home, "https://github.com/acme/pack", "v1")
	clone := clonePath(home, "github.com/acme/pack")
	if err := os.RemoveAll(clone); err != nil {
		t.Fatal(err)
	}
	res, err := app.UpdatePack(context.Background(), home, "github.com/acme/pack", app.PackGitOptions{})
	if err != nil {
		t.Fatalf("UpdatePack() error = %v", err)
	}
	if res.Update == nil || !res.Update.Missing || !slices.ContainsFunc(res.Warnings, func(w app.PackWarning) bool { return w.Code == "pack-clone-missing" }) {
		t.Errorf("result = %+v, want the missing clone reported", res)
	}
	if _, err := os.Stat(filepath.Join(clone, "dotai.yaml")); err != nil {
		t.Errorf("the clone was not fetched again: %v", err)
	}
	// Adding it again recovers it too.
	if err := os.RemoveAll(clone); err != nil {
		t.Fatal(err)
	}
	if _, err := app.AddGitPack(context.Background(), home, "https://github.com/acme/pack", app.PackGitOptions{}); err != nil {
		t.Fatalf("AddGitPack() again error = %v", err)
	}
	if _, err := os.Stat(filepath.Join(clone, "dotai.yaml")); err != nil {
		t.Errorf("the clone was not fetched again: %v", err)
	}
}
