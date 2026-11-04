package server_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"runtime"
	"strings"
	"sync"
	"testing"
	"testing/fstest"
	"time"

	"github.com/lartweib/dotai/internal/app"
	"github.com/lartweib/dotai/internal/envelope"
	"github.com/lartweib/dotai/internal/server"
)

const (
	testToken = "the-session-token-of-this-run"
	testCode  = "the-one-time-code-of-this-run"
	testPort  = 4321
	testHost  = "127.0.0.1:4321"
)

// testConfig is the configuration every handler of these tests serves.
func testConfig(t *testing.T) server.Config {
	return server.Config{Home: t.TempDir(), CacheDir: t.TempDir(), Port: testPort,
		Code: testCode, CodeExpires: time.Now().Add(2 * time.Minute), Session: testToken}
}

var testWeb = fstest.MapFS{"index.html": {Data: []byte("<!doctype html><title>dotai</title>")}}

// fakeDeps answers every use case with an empty result.
func fakeDeps() server.Deps {
	return server.Deps{
		Version: func() app.VersionInfo {
			return app.VersionInfo{Version: "1.2.3", GoVersion: "go1.26.5", OS: "linux", Arch: "amd64"}
		},
		Status: func(string) (app.Status, error) { return app.Status{Projects: []app.ProjectStatus{}}, nil },
		PlanInstall: func(context.Context, string, string, string, app.InstallOptions, app.Progress) (app.InstallPlan, error) {
			return app.InstallPlan{Mode: app.ModeIsolated}, nil
		},
		PlanProject: func(context.Context, string, string, string, string, app.ApplyOptions, app.Progress) (app.ProjectPlan, error) {
			return app.ProjectPlan{}, nil
		},
		Backups: func(string) (app.Backups, error) {
			return app.Backups{Home: []app.BackupInfo{}, Projects: []app.ProjectBackups{}}, nil
		},
		PreviewRestore: func(context.Context, string, string) (app.RestoreResult, error) { return app.RestoreResult{}, nil },
		PreviewProjectRestore: func(context.Context, string, string) (app.ProjectRestoreResult, error) {
			return app.ProjectRestoreResult{}, nil
		},
		Doctor: func(_ context.Context, _ string, opts app.DoctorOptions) (app.DoctorResult, error) {
			res := app.DoctorResult{Findings: []app.Finding{}}
			if opts.Fix && opts.Confirm != nil {
				return app.DoctorResult{}, opts.Confirm(res)
			}
			return res, nil
		},
		Harnesses: func() []app.HarnessInfo { return []app.HarnessInfo{{ID: "claudecode", Name: "Claude Code"}} },
		Validate: func(string, app.Selection) (app.ValidateResult, error) {
			return app.ValidateResult{Valid: true, Pack: &app.PackSummary{Name: "minimal-pack"}}, nil
		},
		Install: func(_ context.Context, _, _, _ string, opts app.InstallOptions, _ app.Progress, _ app.Warn, _ app.Consent) (app.InstallResult, error) {
			p := app.InstallPlan{Mode: app.ModeIsolated}
			return app.InstallResult{InstallPlan: p}, confirm(opts.Confirm, p)
		},
		Apply: func(_ context.Context, _, _, _, _ string, opts app.ApplyOptions, _ app.Progress, _ app.ApplyConsent) (app.ApplyResult, error) {
			return app.ApplyResult{}, confirm(opts.Confirm, app.ProjectPlan{})
		},
		Restore: func(_ context.Context, _, _ string, opts app.RestoreOptions, _ app.Warn, _ func(app.RestoreResult) (bool, error)) (app.RestoreResult, error) {
			return app.RestoreResult{}, confirm(opts.Confirm, app.RestoreResult{})
		},
		RestoreProject: func(_ context.Context, _, _ string, opts app.ProjectRestoreOptions, _ func(app.ProjectRestoreResult) (bool, error)) (app.ProjectRestoreResult, error) {
			return app.ProjectRestoreResult{}, confirm(opts.Confirm, app.ProjectRestoreResult{})
		},
	}
}

// confirm asks c with v, as the use cases do, when c is not nil.
func confirm[T any](c func(T) error, v T) error {
	if c == nil {
		return nil
	}
	return c(v)
}

// request is a request for the test server: its Host, and the session
// token in the Authorization header unless auth is "".
func request(method, target, auth string) *http.Request {
	r := httptest.NewRequest(method, target, nil)
	r.Host = testHost
	if auth != "" {
		r.Header.Set("Authorization", "Bearer "+auth)
	}
	return r
}

// post is a POST of the JSON body to target for the test server.
func post(target, body, auth string) *http.Request {
	r := httptest.NewRequest(http.MethodPost, target, strings.NewReader(body))
	r.Host = testHost
	r.Header.Set("Content-Type", "application/json")
	if auth != "" {
		r.Header.Set("Authorization", "Bearer "+auth)
	}
	return r
}

func serve(h http.Handler, r *http.Request) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

// decode decodes the body of w as an envelope, with its data raw.
func decode(t *testing.T, w *httptest.ResponseRecorder) (e struct {
	Schema  string          `json:"schema"`
	Command string          `json:"command"`
	OK      bool            `json:"ok"`
	Data    json.RawMessage `json:"data"`
	Error   *envelope.Error `json:"error"`
},
) {
	t.Helper()
	if ct := w.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Fatalf("Content-Type = %q, want JSON; body %s", ct, w.Body)
	}
	if err := json.Unmarshal(w.Body.Bytes(), &e); err != nil {
		t.Fatalf("body %s is not an envelope: %v", w.Body, err)
	}
	if e.Schema != envelope.Schema {
		t.Errorf("schema = %q, want %q", e.Schema, envelope.Schema)
	}
	return e
}

// wantFailure checks that w is a failure envelope of code with status.
func wantFailure(t *testing.T, w *httptest.ResponseRecorder, status int, code string) {
	t.Helper()
	if w.Code != status {
		t.Errorf("status = %d, want %d; body %s", w.Code, status, w.Body)
	}
	e := decode(t, w)
	if e.OK || e.Error == nil || e.Error.Code != code || e.Error.Message == "" {
		t.Errorf("envelope = %+v, want a failure %q with a message", e, code)
	}
}

// abs is an absolute path on this OS.
func abs(name string) string {
	if runtime.GOOS == "windows" {
		return `C:\work\` + name
	}
	return "/work/" + name
}

// jsonString is s as a JSON string.
func jsonString(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

func TestAPIRequiresTheSessionToken(t *testing.T) {
	h := server.New(testConfig(t), fakeDeps(), testWeb)
	tests := []struct {
		name string
		req  func() *http.Request
		ok   bool
	}{
		{"no token", func() *http.Request { return request("GET", "/api/version", "") }, false},
		{"wrong token", func() *http.Request { return request("GET", "/api/version", "nope") }, false},
		{"the one-time code", func() *http.Request { return request("GET", "/api/version", testCode) }, false},
		{"token in the query", func() *http.Request { return request("GET", "/api/version?token="+testToken, "") }, false},
		{"token without the Bearer scheme", func() *http.Request {
			r := request("GET", "/api/version", "")
			r.Header.Set("Authorization", testToken)
			return r
		}, false},
		{"token in a cookie", func() *http.Request {
			r := request("GET", "/api/version", "")
			r.AddCookie(&http.Cookie{Name: "dotai_ui_4321", Value: testToken})
			return r
		}, false},
		{"bearer token", func() *http.Request { return request("GET", "/api/version", testToken) }, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := serve(h, tt.req())
			if !tt.ok {
				wantFailure(t, w, http.StatusUnauthorized, server.CodeUnauthorized)
				return
			}
			if w.Code != http.StatusOK {
				t.Errorf("status = %d, want 200; body %s", w.Code, w.Body)
			}
		})
	}
}

func TestTheOneTimeCodeGivesTheSessionTokenOnce(t *testing.T) {
	h := server.New(testConfig(t), fakeDeps(), testWeb)
	wrong := serve(h, post("/api/session", `{"code":"nope"}`, ""))
	wantFailure(t, wrong, http.StatusUnauthorized, server.CodeUnauthorized)
	w := serve(h, post("/api/session", `{"code":`+jsonString(testCode)+`}`, ""))
	if w.Code != http.StatusOK || len(w.Result().Cookies()) != 0 {
		t.Fatalf("exchange = %d with cookies %v, want 200 and no cookie; body %s", w.Code, w.Result().Cookies(), w.Body)
	}
	e := decode(t, w)
	var data struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal(e.Data, &data); err != nil || !e.OK || e.Command != "ui" || data.Token != testToken {
		t.Errorf("exchange = %+v, %v, want the session token", e, err)
	}
	again := serve(h, post("/api/session", `{"code":`+jsonString(testCode)+`}`, ""))
	wantFailure(t, again, http.StatusUnauthorized, server.CodeUnauthorized)
	if msg := decode(t, again).Error.Message; !strings.Contains(msg, "already used") || !strings.Contains(msg, "restart dotai ui") {
		t.Errorf("second exchange message = %q, want it to say the link was already used", msg)
	}
}

func TestTheOneTimeCodeIsUsedOnceUnderConcurrency(t *testing.T) {
	h := server.New(testConfig(t), fakeDeps(), testWeb)
	var wg sync.WaitGroup
	var mu sync.Mutex
	won := 0
	for range 20 {
		wg.Go(func() {
			if w := serve(h, post("/api/session", `{"code":`+jsonString(testCode)+`}`, "")); w.Code == http.StatusOK {
				mu.Lock()
				won++
				mu.Unlock()
			}
		})
	}
	wg.Wait()
	if won != 1 {
		t.Errorf("%d exchanges succeeded, want exactly one", won)
	}
}

func TestTheOneTimeCodeExpires(t *testing.T) {
	cfg := testConfig(t)
	cfg.CodeExpires = time.Now().Add(-time.Second)
	w := serve(server.New(cfg, fakeDeps(), testWeb), post("/api/session", `{"code":`+jsonString(testCode)+`}`, ""))
	wantFailure(t, w, http.StatusUnauthorized, server.CodeUnauthorized)
	if msg := decode(t, w).Error.Message; !strings.Contains(msg, "expired") {
		t.Errorf("message = %q, want it to say the link expired", msg)
	}
}

func TestTheSessionExchangeTakesOnlyAJSONPost(t *testing.T) {
	h := server.New(testConfig(t), fakeDeps(), testWeb)
	w := serve(h, request("GET", "/api/session", ""))
	if w.Code != http.StatusMethodNotAllowed || w.Header().Get("Allow") != "POST" {
		t.Errorf("GET /api/session = %d, Allow %q, want 405 with Allow POST", w.Code, w.Header().Get("Allow"))
	}
	form := post("/api/session", "code="+testCode, "")
	form.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	wantFailure(t, serve(h, form), http.StatusBadRequest, envelope.CodeUsage)
	wantFailure(t, serve(h, post("/api/session", `{"code":"x","more":1}`, "")), http.StatusBadRequest, envelope.CodeUsage)
	// The code is still good after the refused requests.
	if w := serve(h, post("/api/session", `{"code":`+jsonString(testCode)+`}`, "")); w.Code != http.StatusOK {
		t.Errorf("exchange after refused requests = %d, want 200", w.Code)
	}
}

func TestTheFrontendIsServedWithoutAToken(t *testing.T) {
	h := server.New(testConfig(t), fakeDeps(), testWeb)
	if w := serve(h, request("GET", "/", "")); w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "<title>dotai</title>") {
		t.Errorf("GET / = %d %s, want the index page", w.Code, w.Body)
	}
	if w := serve(h, request("POST", "/", "")); w.Code != http.StatusMethodNotAllowed {
		t.Errorf("POST / = %d, want 405", w.Code)
	}
	r := request("GET", "/", "")
	r.Host = "evil.example:4321"
	if w := serve(h, r); w.Code != http.StatusForbidden {
		t.Errorf("GET / for another host = %d, want 403", w.Code)
	}
}

func TestTheEmbeddedPageExchangesTheCode(t *testing.T) {
	h := server.New(testConfig(t), fakeDeps(), server.Web())
	page := serve(h, request("GET", "/", "")).Body.String()
	if !strings.Contains(page, `<script type="module" src="app.js">`) {
		t.Fatalf("index.html = %s, want it to load app.js", page)
	}
	js := serve(h, request("GET", "/app.js", ""))
	if js.Code != http.StatusOK {
		t.Fatalf("GET /app.js = %d", js.Code)
	}
	for _, want := range []string{"/api/session", "history.replaceState", "sessionStorage", "Bearer "} {
		if !strings.Contains(js.Body.String(), want) {
			t.Errorf("app.js lacks %q", want)
		}
	}
}

func TestEveryResponseCarriesTheSecurityHeaders(t *testing.T) {
	h := server.New(testConfig(t), fakeDeps(), testWeb)
	for _, r := range []*http.Request{request("GET", "/api/version", testToken), request("GET", "/api/version", ""), request("GET", "/", "")} {
		w := serve(h, r)
		for k, want := range map[string]string{"X-Content-Type-Options": "nosniff", "X-Frame-Options": "DENY", "Referrer-Policy": "no-referrer"} {
			if got := w.Header().Get(k); got != want {
				t.Errorf("%s = %q, want %q", k, got, want)
			}
		}
		if !strings.Contains(w.Header().Get("Content-Security-Policy"), "frame-ancestors 'none'") {
			t.Errorf("Content-Security-Policy = %q, want no framing", w.Header().Get("Content-Security-Policy"))
		}
	}
}

func TestARequestForAnotherHostIsRefused(t *testing.T) {
	h := server.New(testConfig(t), fakeDeps(), testWeb)
	for _, host := range []string{"evil.example", "evil.example:4321", "127.0.0.1:9999", "127.0.0.1", "0.0.0.0:4321", "[::1]:4321", ""} {
		t.Run(host, func(t *testing.T) {
			r := request("GET", "/api/version", testToken)
			r.Host = host
			wantFailure(t, serve(h, r), http.StatusForbidden, server.CodeForbidden)
			r = post("/api/session", `{"code":`+jsonString(testCode)+`}`, "")
			r.Host = host
			wantFailure(t, serve(h, r), http.StatusForbidden, server.CodeForbidden)
		})
	}
	for _, host := range []string{"127.0.0.1:4321", "localhost:4321", "LOCALHOST:4321"} {
		r := request("GET", "/api/version", testToken)
		r.Host = host
		if w := serve(h, r); w.Code != http.StatusOK {
			t.Errorf("host %q = %d, want 200", host, w.Code)
		}
	}
}

func TestARequestFromAnotherOriginIsRefused(t *testing.T) {
	h := server.New(testConfig(t), fakeDeps(), testWeb)
	for _, origin := range []string{"http://evil.example", "null", "http://127.0.0.1:9999", "https://127.0.0.1:4321", "http://127.0.0.1.evil.example:4321"} {
		t.Run(origin, func(t *testing.T) {
			r := request("GET", "/api/version", testToken)
			r.Header.Set("Origin", origin)
			wantFailure(t, serve(h, r), http.StatusForbidden, server.CodeForbidden)
			r = post("/api/session", `{"code":`+jsonString(testCode)+`}`, "")
			r.Header.Set("Origin", origin)
			wantFailure(t, serve(h, r), http.StatusForbidden, server.CodeForbidden)
		})
	}
	for _, site := range []string{"cross-site", "same-site"} {
		r := post("/api/session", `{"code":`+jsonString(testCode)+`}`, "")
		r.Header.Set("Sec-Fetch-Site", site)
		wantFailure(t, serve(h, r), http.StatusForbidden, server.CodeForbidden)
	}
	for _, origin := range []string{"http://127.0.0.1:4321", "http://localhost:4321"} {
		r := request("GET", "/api/version", testToken)
		r.Header.Set("Origin", origin)
		r.Header.Set("Sec-Fetch-Site", "same-origin")
		if w := serve(h, r); w.Code != http.StatusOK {
			t.Errorf("origin %q = %d, want 200", origin, w.Code)
		}
	}
}

var getEndpoints = []string{
	"/api/version", "/api/pack", "/api/harnesses", "/api/status", "/api/backups", "/api/restore/preview", "/api/restore/project/preview", "/api/doctor",
}

var postEndpoints = []string{"/api/install/plan", "/api/apply/plan", "/api/install", "/api/apply", "/api/restore", "/api/restore/project", "/api/doctor/fix"}

func TestEachEndpointTakesOnlyItsMethod(t *testing.T) {
	h := server.New(testConfig(t), fakeDeps(), testWeb)
	for _, path := range getEndpoints {
		for _, method := range []string{"POST", "PUT", "PATCH", "DELETE", "HEAD"} {
			w := serve(h, request(method, path, testToken))
			if w.Code != http.StatusMethodNotAllowed || w.Header().Get("Allow") != "GET" {
				t.Errorf("%s %s = %d, Allow %q, want 405 with Allow GET", method, path, w.Code, w.Header().Get("Allow"))
			}
		}
	}
	for _, path := range postEndpoints {
		for _, method := range []string{"GET", "PUT", "DELETE", "HEAD"} {
			w := serve(h, request(method, path, testToken))
			if w.Code != http.StatusMethodNotAllowed || w.Header().Get("Allow") != "POST" {
				t.Errorf("%s %s = %d, Allow %q, want 405 with Allow POST", method, path, w.Code, w.Header().Get("Allow"))
			}
		}
	}
	wantFailure(t, serve(h, request("POST", "/api/version", testToken)), http.StatusMethodNotAllowed, server.CodeMethodNotAllowed)
}

func TestTheEndpointsAnswerWithTheEnvelope(t *testing.T) {
	h := server.New(testConfig(t), fakeDeps(), testWeb)
	reqs := map[*http.Request]string{
		request("GET", "/api/version", testToken):                                      "version",
		request("GET", "/api/status", testToken):                                       "status",
		post("/api/install/plan", `{"pack":`+jsonString(abs("pack"))+`}`, testToken):   "install",
		post("/api/apply/plan", `{"project":`+jsonString(abs("repo"))+`}`, testToken):  "apply",
		request("GET", "/api/backups", testToken):                                      "backups",
		request("GET", "/api/restore/preview", testToken):                              "restore",
		request("GET", "/api/restore/project/preview?project="+abs("repo"), testToken): "restore",
		request("GET", "/api/doctor", testToken):                                       "doctor",
	}
	for r, command := range reqs {
		w := serve(h, r)
		if w.Code != http.StatusOK || w.Header().Get("Cache-Control") != "no-store" {
			t.Errorf("%s %s = %d, Cache-Control %q, want 200, not cached; body %s", r.Method, r.URL, w.Code, w.Header().Get("Cache-Control"), w.Body)
			continue
		}
		e := decode(t, w)
		if !e.OK || e.Command != command || e.Error != nil || len(e.Data) == 0 || e.Data[0] != '{' {
			t.Errorf("%s %s = %+v, want a success of %q with an object", r.Method, r.URL, e, command)
		}
	}
	var v app.VersionInfo
	if err := json.Unmarshal(decode(t, serve(h, request("GET", "/api/version", testToken))).Data, &v); err != nil || v.Version != "1.2.3" {
		t.Errorf("version data = %+v, %v, want the version", v, err)
	}
	wantFailure(t, serve(h, request("GET", "/api/nope", testToken)), http.StatusNotFound, server.CodeNotFound)
}

func TestPlanInstallTakesItsOptionsFromTheBody(t *testing.T) {
	cfg := testConfig(t)
	deps := fakeDeps()
	var gotCache, gotDir, gotHome string
	var gotOpts app.InstallOptions
	deps.PlanInstall = func(_ context.Context, cacheDir, dir, home string, opts app.InstallOptions, _ app.Progress) (app.InstallPlan, error) {
		gotCache, gotDir, gotHome, gotOpts = cacheDir, dir, home, opts
		return app.InstallPlan{Mode: app.ModeIsolated}, nil
	}
	h := server.New(cfg, deps, testWeb)
	body := `{"pack":` + jsonString(abs("pack")) + `,"variant":"tmux","mode":"replace","harnesses":["claudecode","opencode"]}`
	if w := serve(h, post("/api/install/plan", body, testToken)); w.Code != http.StatusOK {
		t.Fatalf("status = %d; body %s", w.Code, w.Body)
	}
	want := app.InstallOptions{Variant: "tmux", Mode: "replace", Harnesses: []string{"claudecode", "opencode"}}
	if gotCache != cfg.CacheDir || gotDir != abs("pack") || gotHome != cfg.Home || !reflect.DeepEqual(gotOpts, want) {
		t.Errorf("PlanInstall(%q, %q, %q, %+v), want the configured directories and %+v", gotCache, gotDir, gotHome, gotOpts, want)
	}
	if w := serve(h, post("/api/install/plan", `{"pack":`+jsonString(abs("pack"))+`}`, testToken)); w.Code != http.StatusOK || gotOpts.Harnesses != nil {
		t.Errorf("without harnesses: status %d, harnesses %#v, want nil, the saved selection", w.Code, gotOpts.Harnesses)
	}
	for _, bad := range []string{`{}`, `{"pack":"relative/pack"}`, `{"pack":` + jsonString(abs("pack")) + `,"profile":["web"]}`, `not json`, ``} {
		wantFailure(t, serve(h, post("/api/install/plan", bad, testToken)), http.StatusBadRequest, envelope.CodeUsage)
	}
	noType := post("/api/install/plan", `{"pack":`+jsonString(abs("pack"))+`}`, testToken)
	noType.Header.Del("Content-Type")
	wantFailure(t, serve(h, noType), http.StatusBadRequest, envelope.CodeUsage)
}

func TestPlanProjectTakesItsOptionsFromTheBody(t *testing.T) {
	deps := fakeDeps()
	var gotDir, gotCwd string
	var gotOpts app.ApplyOptions
	deps.PlanProject = func(_ context.Context, _, dir, cwd, _ string, opts app.ApplyOptions, _ app.Progress) (app.ProjectPlan, error) {
		gotDir, gotCwd, gotOpts = dir, cwd, opts
		return app.ProjectPlan{}, nil
	}
	h := server.New(testConfig(t), deps, testWeb)
	body := `{"project":` + jsonString(abs("repo")) + `,"profiles":["web","docs"],"variant":"tmux"}`
	if w := serve(h, post("/api/apply/plan", body, testToken)); w.Code != http.StatusOK {
		t.Fatalf("status = %d; body %s", w.Code, w.Body)
	}
	want := app.ApplyOptions{Profiles: []string{"web", "docs"}, Variant: "tmux"}
	if gotDir != "" || gotCwd != abs("repo") || !reflect.DeepEqual(gotOpts, want) {
		t.Errorf("PlanProject(%q, %q, %+v), want no pack, the project and %+v", gotDir, gotCwd, gotOpts, want)
	}
	wantFailure(t, serve(h, post("/api/apply/plan", `{}`, testToken)), http.StatusBadRequest, envelope.CodeUsage)
	wantFailure(t, serve(h, post("/api/apply/plan", `{"project":`+jsonString(abs("repo"))+`,"pack":"rel"}`, testToken)), http.StatusBadRequest, envelope.CodeUsage)
}

func TestOnlyOnePlanRunsAtATime(t *testing.T) {
	deps := fakeDeps()
	started, release := make(chan struct{}), make(chan struct{})
	deps.PlanInstall = func(context.Context, string, string, string, app.InstallOptions, app.Progress) (app.InstallPlan, error) {
		close(started)
		<-release
		return app.InstallPlan{Mode: app.ModeIsolated}, nil
	}
	h := server.New(testConfig(t), deps, testWeb)
	body := `{"pack":` + jsonString(abs("pack")) + `}`
	first := make(chan int, 1)
	go func() { first <- serve(h, post("/api/install/plan", body, testToken)).Code }()
	<-started
	busy := serve(h, post("/api/apply/plan", `{"project":`+jsonString(abs("repo"))+`}`, testToken))
	wantFailure(t, busy, http.StatusTooManyRequests, server.CodeBusy)
	if w := serve(h, request("GET", "/api/status", testToken)); w.Code != http.StatusOK {
		t.Errorf("status while a plan runs = %d, want 200: only plans wait for each other", w.Code)
	}
	close(release)
	if code := <-first; code != http.StatusOK {
		t.Errorf("first plan = %d, want 200", code)
	}
	if w := serve(h, post("/api/apply/plan", `{"project":`+jsonString(abs("repo"))+`}`, testToken)); w.Code != http.StatusOK {
		t.Errorf("plan after the first one ended = %d, want 200", w.Code)
	}
}

func TestAPlanThatRunsTooLongIsStopped(t *testing.T) {
	cfg := testConfig(t)
	cfg.OperationTimeout = 20 * time.Millisecond
	deps := fakeDeps()
	deps.PlanInstall = func(ctx context.Context, _, _, _ string, _ app.InstallOptions, _ app.Progress) (app.InstallPlan, error) {
		<-ctx.Done()
		return app.InstallPlan{}, ctx.Err()
	}
	w := serve(server.New(cfg, deps, testWeb), post("/api/install/plan", `{"pack":`+jsonString(abs("pack"))+`}`, testToken))
	wantFailure(t, w, http.StatusInternalServerError, envelope.CodeInternal)
	if msg := decode(t, w).Error.Message; !strings.Contains(msg, "longer than") {
		t.Errorf("message = %q, want it to say the plan took too long", msg)
	}
}

func TestAPackWithProblemsIsInvalidWithItsPlan(t *testing.T) {
	deps := fakeDeps()
	deps.PlanInstall = func(context.Context, string, string, string, app.InstallOptions, app.Progress) (app.InstallPlan, error) {
		return app.InstallPlan{Diagnostics: []app.Diagnostic{{Code: "path-unmapped"}}}, nil
	}
	w := serve(server.New(testConfig(t), deps, testWeb), post("/api/install/plan", `{"pack":`+jsonString(abs("pack"))+`}`, testToken))
	wantFailure(t, w, http.StatusUnprocessableEntity, envelope.CodeInvalid)
	var p app.InstallPlan
	if err := json.Unmarshal(decode(t, w).Data, &p); err != nil || len(p.Diagnostics) != 1 {
		t.Errorf("data = %+v, %v, want the plan with its diagnostics", p, err)
	}
}

func TestErrorsGetTheCodesOfTheCommandLine(t *testing.T) {
	_, usage := app.PreviewRestore(context.Background(), t.TempDir(), "not-a-backup-id")
	tests := []struct {
		name   string
		err    error
		status int
		code   string
		data   bool
	}{
		{"internal", errors.New("boom"), http.StatusInternalServerError, envelope.CodeInternal, false},
		{"consent", &app.ConsentError{Plan: app.InstallPlan{Mode: app.ModeReplace}}, http.StatusConflict, envelope.CodeConsent, true},
		{"refused", &app.RefusedError{Plan: app.InstallPlan{Mode: app.ModeReplace}}, http.StatusConflict, envelope.CodeRefused, true},
		{"usage", usage, http.StatusBadRequest, envelope.CodeUsage, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			deps := fakeDeps()
			deps.PlanInstall = func(context.Context, string, string, string, app.InstallOptions, app.Progress) (app.InstallPlan, error) {
				return app.InstallPlan{}, tt.err
			}
			w := serve(server.New(testConfig(t), deps, testWeb), post("/api/install/plan", `{"pack":`+jsonString(abs("pack"))+`}`, testToken))
			wantFailure(t, w, tt.status, tt.code)
			if e := decode(t, w); (len(e.Data) > 0) != tt.data || e.Command != "install" {
				t.Errorf("envelope = %+v, want data %v for install", e, tt.data)
			}
		})
	}
}

func TestTheRestorePreviewsTakeTheirQuery(t *testing.T) {
	cfg := testConfig(t)
	deps := fakeDeps()
	var gotID, gotCwd string
	deps.PreviewRestore = func(_ context.Context, home, id string) (app.RestoreResult, error) {
		if home != cfg.Home {
			t.Errorf("home = %q, want %q", home, cfg.Home)
		}
		gotID = id
		return app.RestoreResult{}, nil
	}
	deps.PreviewProjectRestore = func(_ context.Context, cwd, _ string) (app.ProjectRestoreResult, error) {
		gotCwd = cwd
		return app.ProjectRestoreResult{}, &app.ConflictError{Conflicts: []app.RestoreConflict{{Target: "x"}}}
	}
	h := server.New(cfg, deps, testWeb)
	if w := serve(h, request("GET", "/api/restore/preview?id=20261029T101500.000Z_install", testToken)); w.Code != http.StatusOK || gotID != "20261029T101500.000Z_install" {
		t.Errorf("restore preview = %d with id %q, want the id passed", w.Code, gotID)
	}
	w := serve(h, request("GET", "/api/restore/project/preview?project="+abs("repo"), testToken))
	wantFailure(t, w, http.StatusInternalServerError, envelope.CodeInternal)
	if gotCwd != abs("repo") || !strings.Contains(string(decode(t, w).Data), `"conflicts"`) {
		t.Errorf("project preview of %q = %s, want the conflicts", gotCwd, w.Body)
	}
	wantFailure(t, serve(h, request("GET", "/api/restore/project/preview", testToken)), http.StatusBadRequest, envelope.CodeUsage)
}

func TestDoctorProblemsAreInvalidWithTheResult(t *testing.T) {
	deps := fakeDeps()
	var gotFix bool
	deps.Doctor = func(_ context.Context, _ string, opts app.DoctorOptions) (app.DoctorResult, error) {
		gotFix = opts.Fix
		return app.DoctorResult{Findings: []app.Finding{{Code: app.CodeExcludeMissing}, {Code: app.CodeExcludeMissing, Fixed: true}}}, nil
	}
	w := serve(server.New(testConfig(t), deps, testWeb), request("GET", "/api/doctor", testToken))
	wantFailure(t, w, http.StatusUnprocessableEntity, envelope.CodeInvalid)
	if e := decode(t, w); gotFix || e.Error.Message != "doctor found 1 problem" || !strings.Contains(string(e.Data), "exclude-missing") {
		t.Errorf("doctor = %+v, fix %v, want one problem with the findings, without --fix", e, gotFix)
	}
}

func TestTheEmbeddedFrontendLoadsUnderItsPolicy(t *testing.T) {
	h := server.New(testConfig(t), fakeDeps(), server.Web())
	page := serve(h, request("GET", "/", "")).Body.String()
	if strings.Contains(page, "style=") || strings.Contains(page, "<style") || strings.Contains(page, "<script>") {
		t.Errorf("index.html has inline styles or scripts, which the policy blocks")
	}
	types := map[string]string{
		"/styles.css": "text/css", "/app.js": "javascript", "/js/dom.js": "javascript", "/js/store.js": "javascript",
		"/js/packs.js": "javascript", "/js/tree.js": "javascript", "/js/plan.js": "javascript", "/js/projects.js": "javascript",
		"/js/backups.js": "javascript", "/js/doctor.js": "javascript", "/js/flows.js": "javascript", "/fonts/schibsted-grotesk-latin-400-normal.woff2": "font/woff2",
		"/fonts/spline-sans-mono-latin-400-normal.woff2": "font/woff2",
	}
	for path, want := range types {
		w := serve(h, request("GET", path, ""))
		if w.Code != http.StatusOK || !strings.Contains(w.Header().Get("Content-Type"), want) {
			t.Errorf("GET %s = %d %q, want 200 and %s", path, w.Code, w.Header().Get("Content-Type"), want)
		}
		if strings.HasSuffix(path, ".js") && (strings.Contains(w.Body.String(), "innerHTML") || strings.Contains(w.Body.String(), "eval(")) {
			t.Errorf("%s writes HTML or evaluates code", path)
		}
	}
	if csp := serve(h, request("GET", "/", "")).Header().Get("Content-Security-Policy"); !strings.HasPrefix(csp, "default-src 'self';") {
		t.Errorf("Content-Security-Policy = %q, want only the server's own origin", csp)
	}
}

func TestTheFrontendOpensFromThePageOnDisk(t *testing.T) {
	h := server.New(testConfig(t), fakeDeps(), testWeb)
	// The launcher page, a file, sends the browser here: a navigation
	// from another site, which only the API refuses.
	r := request("GET", "/", "")
	r.Header.Set("Sec-Fetch-Site", "cross-site")
	r.Header.Set("Sec-Fetch-Mode", "navigate")
	if w := serve(h, r); w.Code != http.StatusOK {
		t.Errorf("navigation from the launcher page = %d, want 200", w.Code)
	}
	r = request("GET", "/api/version", testToken)
	r.Header.Set("Sec-Fetch-Site", "cross-site")
	wantFailure(t, serve(h, r), http.StatusForbidden, server.CodeForbidden)
}
