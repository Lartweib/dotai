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
	"testing"
	"testing/fstest"

	"github.com/lartweib/dotai/internal/app"
	"github.com/lartweib/dotai/internal/envelope"
	"github.com/lartweib/dotai/internal/server"
)

const (
	testToken = "the-token-of-this-run"
	testPort  = 4321
	testHost  = "127.0.0.1:4321"
)

// testConfig is the configuration every handler of these tests serves.
func testConfig(t *testing.T) server.Config {
	return server.Config{Home: t.TempDir(), CacheDir: t.TempDir(), Token: testToken, Port: testPort}
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
		Doctor: func(context.Context, string, app.DoctorOptions) (app.DoctorResult, error) {
			return app.DoctorResult{Findings: []app.Finding{}}, nil
		},
	}
}

// request is a request for the test server: its Host, and the token in
// the Authorization header unless auth is "".
func request(method, target, auth string) *http.Request {
	r := httptest.NewRequest(method, target, nil)
	r.Host = testHost
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

func TestAPIRequiresTheToken(t *testing.T) {
	h := server.New(testConfig(t), fakeDeps(), testWeb)
	cookie := &http.Cookie{Name: "dotai_ui_4321", Value: testToken}
	tests := []struct {
		name   string
		req    func() *http.Request
		status int
	}{
		{"no token", func() *http.Request { return request("GET", "/api/version", "") }, http.StatusUnauthorized},
		{"wrong token", func() *http.Request { return request("GET", "/api/version", "nope") }, http.StatusUnauthorized},
		{"token in the query of the API", func() *http.Request { return request("GET", "/api/version?token="+testToken, "") }, http.StatusUnauthorized},
		{"token without the Bearer scheme", func() *http.Request {
			r := request("GET", "/api/version", "")
			r.Header.Set("Authorization", testToken)
			return r
		}, http.StatusUnauthorized},
		{"cookie of another port", func() *http.Request {
			r := request("GET", "/api/version", "")
			r.AddCookie(&http.Cookie{Name: "dotai_ui_1234", Value: testToken})
			return r
		}, http.StatusUnauthorized},
		{"bearer token", func() *http.Request { return request("GET", "/api/version", testToken) }, http.StatusOK},
		{"cookie", func() *http.Request {
			r := request("GET", "/api/version", "")
			r.AddCookie(cookie)
			return r
		}, http.StatusOK},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := serve(h, tt.req())
			if tt.status != http.StatusOK {
				wantFailure(t, w, tt.status, server.CodeUnauthorized)
				return
			}
			if w.Code != http.StatusOK {
				t.Errorf("status = %d, want 200; body %s", w.Code, w.Body)
			}
		})
	}
}

func TestTheTokenInTheAddressSetsTheCookie(t *testing.T) {
	h := server.New(testConfig(t), fakeDeps(), testWeb)
	w := serve(h, request("GET", "/?token="+testToken, ""))
	if w.Code != http.StatusSeeOther || w.Header().Get("Location") != "/" {
		t.Fatalf("GET /?token = %d to %q, want a redirect to /", w.Code, w.Header().Get("Location"))
	}
	cookies := w.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatalf("cookies = %v, want one", cookies)
	}
	c := cookies[0]
	if c.Name != "dotai_ui_4321" || c.Value != testToken || !c.HttpOnly || c.SameSite != http.SameSiteStrictMode || c.Path != "/" {
		t.Errorf("cookie = %+v, want the token, HttpOnly and SameSite=Strict", c)
	}
	page := request("GET", "/", "")
	page.AddCookie(c)
	if w := serve(h, page); w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "<title>dotai</title>") {
		t.Errorf("GET / with the cookie = %d %s, want the index page", w.Code, w.Body)
	}
	if w := serve(h, request("GET", "/", "")); w.Code != http.StatusUnauthorized || strings.Contains(w.Body.String(), "<title>") {
		t.Errorf("GET / without the cookie = %d %s, want it refused", w.Code, w.Body)
	}
	if w := serve(h, request("GET", "/?token=nope", "")); w.Code != http.StatusUnauthorized || len(w.Result().Cookies()) != 0 {
		t.Errorf("GET /?token=nope = %d with cookies %v, want it refused without a cookie", w.Code, w.Result().Cookies())
	}
}

func TestEveryResponseCarriesTheSecurityHeaders(t *testing.T) {
	h := server.New(testConfig(t), fakeDeps(), testWeb)
	for _, r := range []*http.Request{request("GET", "/api/version", testToken), request("GET", "/api/version", "")} {
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
			r = request("GET", "/?token="+testToken, "")
			r.Host = host
			if w := serve(h, r); w.Code != http.StatusForbidden || len(w.Result().Cookies()) != 0 {
				t.Errorf("GET /?token for %q = %d with cookies %v, want it refused", host, w.Code, w.Result().Cookies())
			}
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
		})
	}
	for _, site := range []string{"cross-site", "same-site"} {
		r := request("GET", "/api/version", testToken)
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

var readEndpoints = []string{
	"/api/version", "/api/status", "/api/install/plan", "/api/apply/plan", "/api/backups",
	"/api/restore/preview", "/api/restore/project/preview", "/api/doctor",
}

func TestTheReadEndpointsTakeOnlyGet(t *testing.T) {
	h := server.New(testConfig(t), fakeDeps(), testWeb)
	for _, path := range readEndpoints {
		for _, method := range []string{"POST", "PUT", "PATCH", "DELETE", "HEAD"} {
			w := serve(h, request(method, path, testToken))
			if w.Code != http.StatusMethodNotAllowed || w.Header().Get("Allow") != "GET" {
				t.Errorf("%s %s = %d, Allow %q, want 405 with Allow GET", method, path, w.Code, w.Header().Get("Allow"))
				continue
			}
			if method != "HEAD" {
				wantFailure(t, w, http.StatusMethodNotAllowed, server.CodeMethodNotAllowed)
			}
		}
	}
}

func TestTheEndpointsAnswerWithTheEnvelope(t *testing.T) {
	h := server.New(testConfig(t), fakeDeps(), testWeb)
	commands := map[string]string{
		"/api/version": "version", "/api/status": "status", "/api/install/plan?pack=" + abs("pack"): "install",
		"/api/apply/plan?project=" + abs("repo"): "apply", "/api/backups": "backups", "/api/restore/preview": "restore",
		"/api/restore/project/preview?project=" + abs("repo"): "restore", "/api/doctor": "doctor",
	}
	for target, command := range commands {
		w := serve(h, request("GET", target, testToken))
		if w.Code != http.StatusOK || w.Header().Get("Cache-Control") != "no-store" {
			t.Errorf("GET %s = %d, Cache-Control %q, want 200, not cached; body %s", target, w.Code, w.Header().Get("Cache-Control"), w.Body)
			continue
		}
		e := decode(t, w)
		if !e.OK || e.Command != command || e.Error != nil || len(e.Data) == 0 || e.Data[0] != '{' {
			t.Errorf("GET %s = %+v, want a success of %q with an object", target, e, command)
		}
	}
	var v app.VersionInfo
	if err := json.Unmarshal(decode(t, serve(h, request("GET", "/api/version", testToken))).Data, &v); err != nil || v.Version != "1.2.3" {
		t.Errorf("version data = %+v, %v, want the version", v, err)
	}
	wantFailure(t, serve(h, request("GET", "/api/nope", testToken)), http.StatusNotFound, server.CodeNotFound)
}

// abs is an absolute path on this OS.
func abs(name string) string {
	if runtime.GOOS == "windows" {
		return `C:\work\` + name
	}
	return "/work/" + name
}

func TestPlanInstallTakesItsOptionsFromTheQuery(t *testing.T) {
	cfg := testConfig(t)
	deps := fakeDeps()
	var gotCache, gotDir, gotHome string
	var gotOpts app.InstallOptions
	deps.PlanInstall = func(_ context.Context, cacheDir, dir, home string, opts app.InstallOptions, _ app.Progress) (app.InstallPlan, error) {
		gotCache, gotDir, gotHome, gotOpts = cacheDir, dir, home, opts
		return app.InstallPlan{Mode: app.ModeIsolated}, nil
	}
	h := server.New(cfg, deps, testWeb)
	q := "?pack=" + abs("pack") + "&variant=tmux&mode=replace&harness=claudecode,opencode&harness=antigravity-cli"
	if w := serve(h, request("GET", "/api/install/plan"+q, testToken)); w.Code != http.StatusOK {
		t.Fatalf("status = %d; body %s", w.Code, w.Body)
	}
	want := app.InstallOptions{Variant: "tmux", Mode: "replace", Harnesses: []string{"claudecode", "opencode", "antigravity-cli"}}
	if gotCache != cfg.CacheDir || gotDir != abs("pack") || gotHome != cfg.Home || !reflect.DeepEqual(gotOpts, want) {
		t.Errorf("PlanInstall(%q, %q, %q, %+v), want the configured directories and %+v", gotCache, gotDir, gotHome, gotOpts, want)
	}
	if w := serve(h, request("GET", "/api/install/plan?pack="+abs("pack"), testToken)); w.Code != http.StatusOK || gotOpts.Harnesses != nil {
		t.Errorf("without harness: status %d, harnesses %#v, want nil, the saved selection", w.Code, gotOpts.Harnesses)
	}
	wantFailure(t, serve(h, request("GET", "/api/install/plan", testToken)), http.StatusBadRequest, envelope.CodeUsage)
	wantFailure(t, serve(h, request("GET", "/api/install/plan?pack=relative/pack", testToken)), http.StatusBadRequest, envelope.CodeUsage)
}

func TestPlanProjectTakesItsOptionsFromTheQuery(t *testing.T) {
	cfg := testConfig(t)
	deps := fakeDeps()
	var gotDir, gotCwd string
	var gotOpts app.ApplyOptions
	deps.PlanProject = func(_ context.Context, _, dir, cwd, _ string, opts app.ApplyOptions, _ app.Progress) (app.ProjectPlan, error) {
		gotDir, gotCwd, gotOpts = dir, cwd, opts
		return app.ProjectPlan{}, nil
	}
	h := server.New(cfg, deps, testWeb)
	if w := serve(h, request("GET", "/api/apply/plan?project="+abs("repo")+"&profile=web,go&profile=docs&variant=tmux", testToken)); w.Code != http.StatusOK {
		t.Fatalf("status = %d; body %s", w.Code, w.Body)
	}
	want := app.ApplyOptions{Profiles: []string{"web", "go", "docs"}, Variant: "tmux"}
	if gotDir != "" || gotCwd != abs("repo") || !reflect.DeepEqual(gotOpts, want) {
		t.Errorf("PlanProject(%q, %q, %+v), want no pack, the project and %+v", gotDir, gotCwd, gotOpts, want)
	}
	wantFailure(t, serve(h, request("GET", "/api/apply/plan", testToken)), http.StatusBadRequest, envelope.CodeUsage)
	wantFailure(t, serve(h, request("GET", "/api/apply/plan?project="+abs("repo")+"&pack=rel", testToken)), http.StatusBadRequest, envelope.CodeUsage)
}

func TestAPackWithProblemsIsInvalidWithItsPlan(t *testing.T) {
	deps := fakeDeps()
	deps.PlanInstall = func(context.Context, string, string, string, app.InstallOptions, app.Progress) (app.InstallPlan, error) {
		return app.InstallPlan{Diagnostics: []app.Diagnostic{{Code: "path-unmapped"}}}, nil
	}
	w := serve(server.New(testConfig(t), deps, testWeb), request("GET", "/api/install/plan?pack="+abs("pack"), testToken))
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
			w := serve(server.New(testConfig(t), deps, testWeb), request("GET", "/api/install/plan?pack="+abs("pack"), testToken))
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
