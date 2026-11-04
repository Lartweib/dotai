package server_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/lartweib/dotai/internal/app"
	"github.com/lartweib/dotai/internal/envelope"
	"github.com/lartweib/dotai/internal/server"
)

// digestOf is the digest field of data.
func digestOf(t *testing.T, data json.RawMessage) string {
	t.Helper()
	var d struct {
		Digest string `json:"digest"`
	}
	if err := json.Unmarshal(data, &d); err != nil {
		t.Fatalf("data %s: %v", data, err)
	}
	return d.Digest
}

func TestTheDigestNamesTheJSONOfAValue(t *testing.T) {
	a, err := server.Digest(app.InstallPlan{Mode: app.ModeIsolated})
	if err != nil || !strings.HasPrefix(a, "sha256:") || len(a) != len("sha256:")+64 {
		t.Fatalf("Digest() = %q, %v, want sha256: and 64 hex digits", a, err)
	}
	if b, _ := server.Digest(app.InstallPlan{Mode: app.ModeIsolated}); b != a {
		t.Errorf("Digest of an equal plan = %q, want %q", b, a)
	}
	if c, _ := server.Digest(app.InstallPlan{Mode: app.ModeReplace}); c == a {
		t.Errorf("Digest of another plan = %q, want it to differ", c)
	}
}

func TestThePlansAndPreviewsCarryTheDigestOfTheirData(t *testing.T) {
	deps := fakeDeps()
	plan := app.InstallPlan{Mode: app.ModeReplace, Variant: "tmux", Notices: []string{"<b>&</b>"}}
	deps.PlanInstall = func(context.Context, string, string, string, app.InstallOptions, app.Progress) (app.InstallPlan, error) {
		return plan, nil
	}
	h := server.New(testConfig(t), deps, testWeb)
	w := serve(h, post("/api/install/plan", `{"pack":`+jsonString(abs("pack"))+`}`, testToken))
	if w.Code != http.StatusOK {
		t.Fatalf("plan = %d; body %s", w.Code, w.Body)
	}
	data := decode(t, w).Data
	want, _ := server.Digest(plan)
	if got := digestOf(t, data); got != want {
		t.Errorf("digest = %q, want %q", got, want)
	}
	var back app.InstallPlan
	if err := json.Unmarshal(data, &back); err != nil || back.Variant != "tmux" || back.Notices[0] != "<b>&</b>" {
		t.Errorf("data = %s, %v, want the plan with its fields", data, err)
	}
	for _, r := range []*http.Request{
		post("/api/apply/plan", `{"project":`+jsonString(abs("repo"))+`}`, testToken),
		request("GET", "/api/restore/preview", testToken),
		request("GET", "/api/restore/project/preview?project="+abs("repo"), testToken),
		request("GET", "/api/doctor", testToken),
	} {
		w := serve(h, r)
		if d := digestOf(t, decode(t, w).Data); !strings.HasPrefix(d, "sha256:") {
			t.Errorf("%s %s digest = %q, want one", r.Method, r.URL, d)
		}
	}
}

func TestADoctorWithProblemsCarriesTheDigestOfItsFindings(t *testing.T) {
	deps := fakeDeps()
	res := app.DoctorResult{Findings: []app.Finding{{Code: app.CodeExcludeMissing, Fixable: true}}, Projects: []string{}, Backups: []app.BackupRef{}}
	deps.Doctor = func(context.Context, string, app.DoctorOptions) (app.DoctorResult, error) { return res, nil }
	w := serve(server.New(testConfig(t), deps, testWeb), request("GET", "/api/doctor", testToken))
	wantFailure(t, w, http.StatusUnprocessableEntity, envelope.CodeInvalid)
	want, _ := server.Digest(res)
	if got := digestOf(t, decode(t, w).Data); got != want {
		t.Errorf("digest = %q, want %q", got, want)
	}
}

// action is a write endpoint with a body that names a plan of digest.
type action struct {
	path, command string
	body          func(digest string) string
}

var actions = []action{
	{"/api/install", "install", func(d string) string {
		return `{"pack":` + jsonString(abs("pack")) + `,"mode":"isolated","digest":` + jsonString(d) + `}`
	}},
	{"/api/apply", "apply", func(d string) string {
		return `{"project":` + jsonString(abs("repo")) + `,"profiles":["web"],"digest":` + jsonString(d) + `}`
	}},
	{"/api/restore", "restore", func(d string) string { return `{"id":"","digest":` + jsonString(d) + `}` }},
	{"/api/restore/project", "restore", func(d string) string {
		return `{"project":` + jsonString(abs("repo")) + `,"digest":` + jsonString(d) + `}`
	}},
	{"/api/doctor/fix", "doctor", func(d string) string { return `{"digest":` + jsonString(d) + `}` }},
}

// digests are the digests of the plans the fakes of fakeDeps confirm.
func digests(t *testing.T) map[string]string {
	t.Helper()
	out := map[string]string{}
	for path, v := range map[string]any{
		"/api/install":         app.InstallPlan{Mode: app.ModeIsolated},
		"/api/apply":           app.ProjectPlan{},
		"/api/restore":         app.RestoreResult{},
		"/api/restore/project": app.ProjectRestoreResult{},
		"/api/doctor/fix":      app.DoctorResult{Findings: []app.Finding{}},
	} {
		d, err := server.Digest(v)
		if err != nil {
			t.Fatal(err)
		}
		out[path] = d
	}
	return out
}

func TestAnActionCarriesOutThePlanItsDigestNames(t *testing.T) {
	h := server.New(testConfig(t), fakeDeps(), testWeb)
	want := digests(t)
	for _, a := range actions {
		t.Run(a.path, func(t *testing.T) {
			w := serve(h, post(a.path, a.body(want[a.path]), testToken))
			if w.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200; body %s", w.Code, w.Body)
			}
			if e := decode(t, w); !e.OK || e.Command != a.command {
				t.Errorf("envelope = %+v, want a success of %q", e, a.command)
			}
		})
	}
}

func TestAnActionRefusesAPlanThatChangedSinceItWasShown(t *testing.T) {
	h := server.New(testConfig(t), fakeDeps(), testWeb)
	want := digests(t)
	for _, a := range actions {
		t.Run(a.path, func(t *testing.T) {
			w := serve(h, post(a.path, a.body("sha256:"+strings.Repeat("0", 64)), testToken))
			wantFailure(t, w, http.StatusConflict, server.CodePlanChanged)
			e := decode(t, w)
			if e.Command != a.command || digestOf(t, e.Data) != want[a.path] {
				t.Errorf("envelope = %+v, want the new plan with its digest %s", e, want[a.path])
			}
		})
	}
}

func TestAnActionNeedsADigest(t *testing.T) {
	h := server.New(testConfig(t), fakeDeps(), testWeb)
	for _, a := range actions {
		wantFailure(t, serve(h, post(a.path, a.body(""), testToken)), http.StatusBadRequest, envelope.CodeUsage)
		wantFailure(t, serve(h, post(a.path, `{"digest":"x","extra":1}`, testToken)), http.StatusBadRequest, envelope.CodeUsage)
	}
	wantFailure(t, serve(h, post("/api/install", `{"digest":"x"}`, testToken)), http.StatusBadRequest, envelope.CodeUsage)
	wantFailure(t, serve(h, post("/api/restore/project", `{"digest":"x","project":"rel"}`, testToken)), http.StatusBadRequest, envelope.CodeUsage)
}

func TestAnActionIsGuardedAsEveryRequest(t *testing.T) {
	h := server.New(testConfig(t), fakeDeps(), testWeb)
	want := digests(t)
	for _, a := range actions {
		wantFailure(t, serve(h, post(a.path, a.body(want[a.path]), "")), http.StatusUnauthorized, server.CodeUnauthorized)
		r := post(a.path, a.body(want[a.path]), testToken)
		r.Header.Set("Origin", "http://evil.example")
		wantFailure(t, serve(h, r), http.StatusForbidden, server.CodeForbidden)
		w := serve(h, request("GET", a.path, testToken))
		wantFailure(t, w, http.StatusMethodNotAllowed, server.CodeMethodNotAllowed)
		if w.Header().Get("Allow") != "POST" {
			t.Errorf("GET %s Allow = %q, want POST", a.path, w.Header().Get("Allow"))
		}
	}
}

func TestTheConsentOfAnActionIsItsYes(t *testing.T) {
	deps := fakeDeps()
	var asked []bool
	deps.Install = func(_ context.Context, _, _, _ string, opts app.InstallOptions, _ app.Progress, _ app.Warn, consent app.Consent) (app.InstallResult, error) {
		p := app.InstallPlan{Mode: app.ModeIsolated}
		if err := opts.Confirm(p); err != nil {
			return app.InstallResult{}, err
		}
		ok, _ := consent(p)
		asked = append(asked, ok)
		if !ok {
			return app.InstallResult{}, &app.ConsentError{Plan: p, Files: 1}
		}
		return app.InstallResult{InstallPlan: p}, nil
	}
	h := server.New(testConfig(t), deps, testWeb)
	d := digests(t)["/api/install"]
	body := `{"pack":` + jsonString(abs("pack")) + `,"digest":` + jsonString(d)
	wantFailure(t, serve(h, post("/api/install", body+`}`, testToken)), http.StatusConflict, envelope.CodeConsent)
	if w := serve(h, post("/api/install", body+`,"yes":true}`, testToken)); w.Code != http.StatusOK {
		t.Errorf("install with yes = %d; body %s", w.Code, w.Body)
	}
	if len(asked) != 2 || asked[0] || !asked[1] {
		t.Errorf("consent answered %v, want false then true", asked)
	}
}

func TestAnActionWaitsForNoPlanAndNoOtherAction(t *testing.T) {
	deps := fakeDeps()
	started, release := make(chan struct{}), make(chan struct{})
	deps.PlanInstall = func(context.Context, string, string, string, app.InstallOptions, app.Progress) (app.InstallPlan, error) {
		close(started)
		<-release
		return app.InstallPlan{Mode: app.ModeIsolated}, nil
	}
	h := server.New(testConfig(t), deps, testWeb)
	first := make(chan int, 1)
	go func() {
		first <- serve(h, post("/api/install/plan", `{"pack":`+jsonString(abs("pack"))+`}`, testToken)).Code
	}()
	<-started
	want := digests(t)
	for _, a := range actions {
		wantFailure(t, serve(h, post(a.path, a.body(want[a.path]), testToken)), http.StatusTooManyRequests, server.CodeBusy)
	}
	close(release)
	if code := <-first; code != http.StatusOK {
		t.Errorf("plan = %d, want 200", code)
	}
}

func TestTheActionsPassTheirRequestToTheUseCases(t *testing.T) {
	cfg := testConfig(t)
	deps := fakeDeps()
	var got []string
	deps.Install = func(_ context.Context, cache, dir, home string, opts app.InstallOptions, _ app.Progress, _ app.Warn, _ app.Consent) (app.InstallResult, error) {
		got = append(got, "install "+dir+" "+opts.Variant+" "+opts.Mode+" "+strings.Join(opts.Harnesses, ","))
		if cache != cfg.CacheDir || home != cfg.Home || opts.IgnoreRunning {
			t.Errorf("Install(%q, %q, %+v), want the configured directories and the running check", cache, home, opts)
		}
		return app.InstallResult{}, opts.Confirm(app.InstallPlan{Mode: app.ModeIsolated})
	}
	deps.Restore = func(_ context.Context, _, id string, opts app.RestoreOptions, _ app.Warn, _ func(app.RestoreResult) (bool, error)) (app.RestoreResult, error) {
		got = append(got, "restore "+id)
		if opts.IgnoreRunning {
			t.Error("restore skips the running check")
		}
		return app.RestoreResult{}, opts.Confirm(app.RestoreResult{})
	}
	deps.RestoreProject = func(_ context.Context, cwd, _ string, opts app.ProjectRestoreOptions, _ func(app.ProjectRestoreResult) (bool, error)) (app.ProjectRestoreResult, error) {
		got = append(got, "restore --project "+cwd)
		return app.ProjectRestoreResult{}, opts.Confirm(app.ProjectRestoreResult{})
	}
	deps.Doctor = func(_ context.Context, _ string, opts app.DoctorOptions) (app.DoctorResult, error) {
		got = append(got, "doctor fix="+map[bool]string{true: "yes", false: "no"}[opts.Fix])
		return app.DoctorResult{Findings: []app.Finding{}}, opts.Confirm(app.DoctorResult{Findings: []app.Finding{}})
	}
	h := server.New(cfg, deps, testWeb)
	d := digests(t)
	for _, r := range []*http.Request{
		post("/api/install", `{"pack":`+jsonString(abs("pack"))+`,"variant":"tmux","mode":"replace","harnesses":["claudecode"],"digest":`+jsonString(d["/api/install"])+`}`, testToken),
		post("/api/restore", `{"id":"20261029T101500.000Z_install","digest":`+jsonString(d["/api/restore"])+`}`, testToken),
		post("/api/restore/project", `{"project":`+jsonString(abs("repo"))+`,"digest":`+jsonString(d["/api/restore/project"])+`}`, testToken),
		post("/api/doctor/fix", `{"digest":`+jsonString(d["/api/doctor/fix"])+`}`, testToken),
	} {
		if w := serve(h, r); w.Code != http.StatusOK {
			t.Errorf("%s = %d; body %s", r.URL, w.Code, w.Body)
		}
	}
	want := []string{"install " + abs("pack") + " tmux replace claudecode", "restore 20261029T101500.000Z_install", "restore --project " + abs("repo"), "doctor fix=yes"}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("use cases ran %q, want %q", got, want)
	}
}

func TestAFixThatLeavesProblemsIsInvalidWithTheResult(t *testing.T) {
	deps := fakeDeps()
	res := app.DoctorResult{Findings: []app.Finding{{Code: app.CodeFileChanged}, {Code: app.CodeExcludeMissing, Fixable: true, Fixed: true}}}
	deps.Doctor = func(_ context.Context, _ string, opts app.DoctorOptions) (app.DoctorResult, error) {
		if err := opts.Confirm(app.DoctorResult{Findings: []app.Finding{}}); err != nil {
			return app.DoctorResult{}, err
		}
		return res, nil
	}
	w := serve(server.New(testConfig(t), deps, testWeb), post("/api/doctor/fix", `{"digest":`+jsonString(digests(t)["/api/doctor/fix"])+`}`, testToken))
	wantFailure(t, w, http.StatusUnprocessableEntity, envelope.CodeInvalid)
	if !strings.Contains(string(decode(t, w).Data), `"fixed": true`) {
		t.Errorf("data = %s, want the result with the fix", decode(t, w).Data)
	}
}

func TestThePackEndpointValidatesThePack(t *testing.T) {
	deps := fakeDeps()
	var gotDir string
	deps.Validate = func(dir string, _ app.Selection) (app.ValidateResult, error) {
		gotDir = dir
		if strings.HasSuffix(dir, "bad") {
			return app.ValidateResult{Diagnostics: []app.Diagnostic{{Code: "x"}}}, nil
		}
		return app.ValidateResult{Valid: true, Pack: &app.PackSummary{Name: "minimal-pack"}}, nil
	}
	h := server.New(testConfig(t), deps, testWeb)
	w := serve(h, request("GET", "/api/pack?path="+abs("pack"), testToken))
	if w.Code != http.StatusOK || gotDir != abs("pack") || decode(t, w).Command != "validate" {
		t.Errorf("GET /api/pack = %d for %q; body %s", w.Code, gotDir, w.Body)
	}
	wantFailure(t, serve(h, request("GET", "/api/pack?path="+abs("bad"), testToken)), http.StatusUnprocessableEntity, envelope.CodeInvalid)
	wantFailure(t, serve(h, request("GET", "/api/pack", testToken)), http.StatusBadRequest, envelope.CodeUsage)
	wantFailure(t, serve(h, request("GET", "/api/pack?path=rel", testToken)), http.StatusBadRequest, envelope.CodeUsage)
}

func TestAClosedTabDoesNotCancelAnAction(t *testing.T) {
	deps := fakeDeps()
	started := make(chan struct{})
	finished := make(chan error, 1)
	deps.Install = func(ctx context.Context, _, _, _ string, opts app.InstallOptions, _ app.Progress, _ app.Warn, _ app.Consent) (app.InstallResult, error) {
		close(started)
		select {
		case <-ctx.Done():
			finished <- ctx.Err()
		case <-time.After(300 * time.Millisecond):
			finished <- nil
		}
		return app.InstallResult{}, opts.Confirm(app.InstallPlan{Mode: app.ModeIsolated})
	}
	h := server.New(testConfig(t), deps, testWeb)
	reqCtx, closeTab := context.WithCancel(context.Background())
	r := post("/api/install", actions[0].body(digests(t)["/api/install"]), testToken).WithContext(reqCtx)
	go serve(h, r)
	<-started
	closeTab()
	if err := <-finished; err != nil {
		t.Errorf("the install saw %v when the tab closed, want it to go on", err)
	}
}

func TestAPlanStopsWhenItsRequestEnds(t *testing.T) {
	deps := fakeDeps()
	finished := make(chan error, 1)
	deps.PlanInstall = func(ctx context.Context, _, _, _ string, _ app.InstallOptions, _ app.Progress) (app.InstallPlan, error) {
		<-ctx.Done()
		finished <- ctx.Err()
		return app.InstallPlan{}, ctx.Err()
	}
	h := server.New(testConfig(t), deps, testWeb)
	reqCtx, closeTab := context.WithCancel(context.Background())
	go serve(h, post("/api/install/plan", `{"pack":`+jsonString(abs("pack"))+`}`, testToken).WithContext(reqCtx))
	closeTab()
	select {
	case err := <-finished:
		if err == nil {
			t.Error("the plan ended without its context done")
		}
	case <-time.After(5 * time.Second):
		t.Error("the plan went on after its request ended")
	}
}

func TestShutdownCancelsAnActionAndWaitsForItsRollback(t *testing.T) {
	cfg := testConfig(t)
	life, shutdown := context.WithCancel(context.Background())
	cfg.Context = life
	deps := fakeDeps()
	started := make(chan struct{})
	var rolledBack atomic.Bool
	deps.Install = func(ctx context.Context, _, _, _ string, _ app.InstallOptions, _ app.Progress, _ app.Warn, _ app.Consent) (app.InstallResult, error) {
		close(started)
		<-ctx.Done()
		time.Sleep(200 * time.Millisecond) // the rollback
		rolledBack.Store(true)
		return app.InstallResult{}, ctx.Err()
	}
	h := server.New(cfg, deps, testWeb)
	done := make(chan struct{})
	go func() {
		serve(h, post("/api/install", actions[0].body(digests(t)["/api/install"]), testToken))
		close(done)
	}()
	<-started
	shutdown()
	if !server.WaitForActions(h, 5*time.Second) || !rolledBack.Load() {
		t.Error("the wait ended before the action rolled back")
	}
	<-done
}

func TestAFixThatFailsPartwayGivesWhatItFoundAndFixed(t *testing.T) {
	deps := fakeDeps()
	deps.Doctor = func(_ context.Context, _ string, opts app.DoctorOptions) (app.DoctorResult, error) {
		if err := opts.Confirm(app.DoctorResult{Findings: []app.Finding{}}); err != nil {
			return app.DoctorResult{}, err
		}
		res := app.DoctorResult{Findings: []app.Finding{{Code: app.CodeExcludeMissing, Fixable: true, Fixed: true}, {Code: app.CodeExcludeMissing, Fixable: true}}}
		return res, &app.ApplyError{Operation: "fix", Cause: errors.New("disk full"), Backup: "b"}
	}
	w := serve(server.New(testConfig(t), deps, testWeb), post("/api/doctor/fix", `{"digest":`+jsonString(digests(t)["/api/doctor/fix"])+`}`, testToken))
	wantFailure(t, w, http.StatusInternalServerError, envelope.CodeInternal)
	var res app.DoctorResult
	if err := json.Unmarshal(decode(t, w).Data, &res); err != nil || len(res.Findings) != 2 || !res.Findings[0].Fixed {
		t.Errorf("data = %s, %v, want what was found and fixed before the failure", decode(t, w).Data, err)
	}
}

func TestTheHarnessesEndpointNamesEachHarness(t *testing.T) {
	w := serve(server.New(testConfig(t), fakeDeps(), testWeb), request("GET", "/api/harnesses", testToken))
	if w.Code != http.StatusOK {
		t.Fatalf("GET /api/harnesses = %d; body %s", w.Code, w.Body)
	}
	var data struct {
		Harnesses []app.HarnessInfo `json:"harnesses"`
	}
	if err := json.Unmarshal(decode(t, w).Data, &data); err != nil || len(data.Harnesses) != 1 || data.Harnesses[0].Name != "Claude Code" {
		t.Errorf("data = %s, %v, want each harness with its name", decode(t, w).Data, err)
	}
}
