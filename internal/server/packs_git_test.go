package server_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/lartweib/dotai/internal/app"
	"github.com/lartweib/dotai/internal/server"
)

// updatePlan is the plan the fakes of the git pack use cases show.
var updatePlan = app.PackUpdatePlan{
	ID: "github.com/acme/pack", Path: "/h/.dotai/packs/github.com/acme/pack",
	From:    app.PackSource{URL: "https://github.com/acme/pack", FullRef: "refs/heads/main", Commit: strings.Repeat("a", 40)},
	To:      app.PackSource{URL: "https://github.com/acme/pack", FullRef: "refs/heads/main", Commit: strings.Repeat("b", 40)},
	Changes: []app.ExecChange{{Path: "base/dotai/tools/x.sh", Change: app.ChangeAdded, Diff: "+x\n"}},
}

// carryOut is a fake git use case that does what the real one does with
// opts: it shows the plan to Confirm, asks Consent, and reports what it
// was asked through got.
func carryOut(got *app.PackGitOptions) func(context.Context, string, string, app.PackGitOptions) (app.PackAddResult, error) {
	return func(_ context.Context, _, _ string, opts app.PackGitOptions) (app.PackAddResult, error) {
		*got = opts
		if opts.Confirm != nil {
			if err := opts.Confirm(updatePlan); err != nil {
				return app.PackAddResult{}, err
			}
		}
		ok := false
		if opts.Consent != nil {
			ok, _ = opts.Consent(updatePlan)
		}
		if !ok {
			return app.PackAddResult{}, &app.PackConsentError{Plan: updatePlan}
		}
		return app.PackAddResult{Pack: app.PackEntry{ID: updatePlan.ID}, Outcome: app.PackUpdated, Update: &updatePlan, Warnings: []app.PackWarning{}}, nil
	}
}

func TestAddingAGitPackAsksGitNothing(t *testing.T) {
	deps := fakeDeps()
	var got app.PackGitOptions
	var url string
	deps.AddGitPack = func(_ context.Context, _, u string, opts app.PackGitOptions) (app.PackAddResult, error) {
		got, url = opts, u
		return app.PackAddResult{Pack: app.PackEntry{ID: "github.com/acme/pack"}, Outcome: app.PackAdded, Warnings: []app.PackWarning{}}, nil
	}
	h := server.New(testConfig(t), deps, testWeb)
	w := serve(h, post("/api/packs/add", `{"url":"https://github.com/acme/pack","ref":"v1"}`, testToken))
	if w.Code != http.StatusOK {
		t.Fatalf("add = %d; body %s", w.Code, w.Body)
	}
	if url != "https://github.com/acme/pack" || got.Ref != "v1" || got.Interactive {
		t.Errorf("AddGitPack(%s, %+v), want the URL, the ref and no prompt", url, got)
	}
	if e := decode(t, w); e.Command != "pack add" {
		t.Errorf("command = %q", e.Command)
	}
	wantFailure(t, serve(h, post("/api/packs/add", `{"ref":"v1"}`, testToken)), http.StatusBadRequest, "usage")
}

func TestAddingAPackAlreadyAddedWithoutADigestGivesThePlanToConfirm(t *testing.T) {
	deps := fakeDeps()
	var got app.PackGitOptions
	deps.AddGitPack = carryOut(&got)
	h := server.New(testConfig(t), deps, testWeb)
	w := serve(h, post("/api/packs/add", `{"url":"https://github.com/acme/pack"}`, testToken))
	wantFailure(t, w, http.StatusConflict, "consent-required")
	want, _ := server.Digest(updatePlan)
	if d := digestOf(t, decode(t, w).Data); d != want {
		t.Errorf("digest = %q, want the plan's %q", d, want)
	}
	w = serve(h, post("/api/packs/add", `{"url":"https://github.com/acme/pack","digest":`+jsonString(want)+`}`, testToken))
	if w.Code != http.StatusOK {
		t.Fatalf("add with the digest = %d; body %s", w.Code, w.Body)
	}
}

func TestThePlanOfAnUpdateCarriesItsDigest(t *testing.T) {
	deps := fakeDeps()
	var id string
	deps.PlanPackUpdate = func(_ context.Context, _, i string, opts app.PackGitOptions) (app.PackUpdatePlan, []app.PackWarning, error) {
		id = i
		if opts.Interactive {
			t.Error("the plan lets git prompt")
		}
		return updatePlan, []app.PackWarning{{Code: "pack-cleanup-failed", Message: "m"}}, nil
	}
	h := server.New(testConfig(t), deps, testWeb)
	w := serve(h, post("/api/packs/update/plan", `{"id":"github.com/acme/pack"}`, testToken))
	if w.Code != http.StatusOK {
		t.Fatalf("plan = %d; body %s", w.Code, w.Body)
	}
	e := decode(t, w)
	want, _ := server.Digest(updatePlan)
	if id != "github.com/acme/pack" || digestOf(t, e.Data) != want || e.Command != "pack update" {
		t.Errorf("plan of %q = %s, want the plan with digest %s", id, e.Data, want)
	}
	var back struct {
		app.PackUpdatePlan
		Notices []app.PackWarning `json:"notices"`
	}
	if err := json.Unmarshal(e.Data, &back); err != nil || len(back.Changes) != 1 || len(back.Notices) != 1 || !strings.Contains(string(e.Data), `"binary":`) {
		t.Errorf("plan = %+v, %v", back, err)
	}
	wantFailure(t, serve(h, post("/api/packs/update/plan", `{}`, testToken)), http.StatusBadRequest, "usage")
}

func TestAnUpdateCarriesOutOnlyThePlanOfItsDigest(t *testing.T) {
	deps := fakeDeps()
	var got app.PackGitOptions
	update := carryOut(&got)
	deps.UpdatePack = func(ctx context.Context, home, id string, opts app.PackGitOptions) (app.PackAddResult, error) {
		return update(ctx, home, id, opts)
	}
	h := server.New(testConfig(t), deps, testWeb)
	want, _ := server.Digest(updatePlan)
	w := serve(h, post("/api/packs/update", `{"id":"github.com/acme/pack","digest":`+jsonString(want)+`}`, testToken))
	if w.Code != http.StatusOK {
		t.Fatalf("update = %d; body %s", w.Code, w.Body)
	}
	if got.Interactive || got.Confirm == nil {
		t.Errorf("opts = %+v, want no prompt and a confirm", got)
	}
	w = serve(h, post("/api/packs/update", `{"id":"github.com/acme/pack","digest":"sha256:other"}`, testToken))
	wantFailure(t, w, http.StatusConflict, "plan-changed")
	if d := digestOf(t, decode(t, w).Data); d != want {
		t.Errorf("the plan-changed digest = %q, want %q", d, want)
	}
	wantFailure(t, serve(h, post("/api/packs/update", `{"id":"github.com/acme/pack"}`, testToken)), http.StatusBadRequest, "usage")
}

func TestRemovalTakesAnIdentityOrAPath(t *testing.T) {
	deps := fakeDeps()
	var byID, byPath string
	deps.RemovePackID = func(_, id string) (app.PackRemoveResult, error) {
		byID = id
		return app.PackRemoveResult{Removed: []app.PackEntry{}, Warnings: []app.PackWarning{}}, nil
	}
	deps.RemovePack = func(_, path string) (app.PackRemoveResult, error) {
		byPath = path
		return app.PackRemoveResult{Removed: []app.PackEntry{}, Warnings: []app.PackWarning{}}, nil
	}
	h := server.New(testConfig(t), deps, testWeb)
	if w := serve(h, post("/api/packs/remove", `{"id":"github.com/acme/pack"}`, testToken)); w.Code != http.StatusOK || byID != "github.com/acme/pack" {
		t.Errorf("remove by id = %d, %q; body %s", w.Code, byID, w.Body)
	} else if e := decode(t, w); e.Command != "pack remove" {
		t.Errorf("command = %q", e.Command)
	}
	if w := serve(h, post("/api/packs/remove", `{"path":"relative/pack"}`, testToken)); w.Code != http.StatusOK || byPath != "relative/pack" {
		t.Errorf("remove by path = %d, %q", w.Code, byPath)
	}
	for _, body := range []string{`{}`, `{"id":"x","path":"y"}`} {
		wantFailure(t, serve(h, post("/api/packs/remove", body, testToken)), http.StatusBadRequest, "usage")
	}
}

func TestAFetchPastTheOperationLimitPointsToTheCommandLine(t *testing.T) {
	deps := fakeDeps()
	deps.AddGitPack = func(context.Context, string, string, app.PackGitOptions) (app.PackAddResult, error) {
		return app.PackAddResult{}, fmt.Errorf("git fetch was stopped by the time limit of the operation: %w", context.DeadlineExceeded)
	}
	h := server.New(testConfig(t), deps, testWeb)
	w := serve(h, post("/api/packs/add", `{"url":"https://github.com/acme/pack"}`, testToken))
	if w.Code != http.StatusInternalServerError || !strings.Contains(w.Body.String(), "dotai pack add") {
		t.Errorf("add = %d; body %s, want the command line suggested", w.Code, w.Body)
	}
	wantTimedOut(t, w)
}

// wantTimedOut checks that w is the internal failure of a fetch stopped by
// the time limit, marked in its data so that the page can say so in the
// viewer's language.
func wantTimedOut(t *testing.T, w *httptest.ResponseRecorder) {
	t.Helper()
	e := decode(t, w)
	var data struct {
		TimedOut bool `json:"timedOut"`
	}
	if e.Error == nil || e.Error.Code != "internal" || json.Unmarshal(e.Data, &data) != nil || !data.TimedOut {
		t.Errorf("envelope = %+v, data %s, want internal with timedOut", e, e.Data)
	}
}

func TestAnUpdatePastTheOperationLimitIsMarkedAsTimedOut(t *testing.T) {
	deps := fakeDeps()
	late := fmt.Errorf("git fetch was stopped by the time limit of the operation: %w", context.DeadlineExceeded)
	deps.PlanPackUpdate = func(context.Context, string, string, app.PackGitOptions) (app.PackUpdatePlan, []app.PackWarning, error) {
		return app.PackUpdatePlan{}, nil, late
	}
	deps.UpdatePack = func(context.Context, string, string, app.PackGitOptions) (app.PackAddResult, error) {
		return app.PackAddResult{}, late
	}
	h := server.New(testConfig(t), deps, testWeb)
	wantTimedOut(t, serve(h, post("/api/packs/update/plan", `{"id":"github.com/acme/pack"}`, testToken)))
	wantTimedOut(t, serve(h, post("/api/packs/update", `{"id":"github.com/acme/pack","digest":"sha256:x"}`, testToken)))
}

func TestAnotherFailureOfAFetchCarriesNoData(t *testing.T) {
	deps := fakeDeps()
	deps.AddGitPack = func(context.Context, string, string, app.PackGitOptions) (app.PackAddResult, error) {
		return app.PackAddResult{}, errors.New("git broke")
	}
	h := server.New(testConfig(t), deps, testWeb)
	w := serve(h, post("/api/packs/add", `{"url":"https://github.com/acme/pack"}`, testToken))
	if e := decode(t, w); e.Error == nil || e.Error.Code != "internal" || len(e.Data) != 0 {
		t.Errorf("envelope = %+v, data %s, want internal without data", e, e.Data)
	}
}
