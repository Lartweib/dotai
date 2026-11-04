package server_test

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/lartweib/dotai/internal/app"
	"github.com/lartweib/dotai/internal/proc"
	"github.com/lartweib/dotai/internal/rulesync"
	"github.com/lartweib/dotai/internal/server"
)

// appHome is a temporary home directory, with the home variables and the
// temporary directory pointed into temporary directories, no relocation
// variable set and fake claude, agy and opencode commands alone on PATH,
// found by name and never run, so that the use cases see no real harness
// and write nothing outside them.
func appHome(t *testing.T) server.Config {
	t.Helper()
	home, tmp := t.TempDir(), t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	for _, kv := range os.Environ() {
		name, _, _ := strings.Cut(kv, "=")
		upper := strings.ToUpper(name)
		if upper == "CLAUDE_CONFIG_DIR" || strings.HasPrefix(upper, "XDG_") || strings.HasPrefix(upper, "OPENCODE_") {
			t.Setenv(name, "")
			os.Unsetenv(name)
		}
	}
	bin := t.TempDir()
	for _, name := range []string{"claude", "agy", "opencode"} {
		file, data := filepath.Join(bin, name), "#!/bin/sh\nexit 0\n"
		if runtime.GOOS == "windows" {
			file, data = file+".exe", ""
		}
		if err := os.WriteFile(file, []byte(data), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", bin)
	for _, v := range []string{"TMPDIR", "TMP", "TEMP"} {
		t.Setenv(v, tmp)
	}
	return server.Config{Home: home, CacheDir: filepath.Join(tmp, "cache"), Session: testToken, Port: testPort}
}

// fakePorts never downloads or runs rulesync, and sees no process running.
var fakePorts = app.Ports{
	FetchRulesync: func(_ context.Context, _, version string, _ app.Progress) (rulesync.Result, error) {
		return rulesync.Result{Version: version}, nil
	},
	RunRulesync: func(context.Context, rulesync.Result, []string, ...rulesync.Job) error { return nil },
	Processes:   func() ([]proc.Process, error) { return nil, nil },
}

func TestTheAppEndpointsReadATemporaryHome(t *testing.T) {
	cfg := appHome(t)
	h := server.New(cfg, server.AppDeps(fakePorts), server.Web())
	for _, target := range []string{"/api/status", "/api/backups", "/api/doctor"} {
		w := serve(h, request("GET", target, testToken))
		if w.Code != http.StatusOK {
			t.Errorf("GET %s = %d, want 200; body %s", target, w.Code, w.Body)
			continue
		}
		if e := decode(t, w); !e.OK {
			t.Errorf("GET %s = %+v, want a success", target, e)
		}
	}
	var st app.Status
	if err := json.Unmarshal(decode(t, serve(h, request("GET", "/api/status", testToken))).Data, &st); err != nil || st.State != nil || st.StatePath != filepath.Join(cfg.Home, ".dotai", "state.json") {
		t.Errorf("status = %+v, %v, want no install in the temporary home", st, err)
	}
	w := serve(h, request("GET", "/api/restore/preview", testToken))
	wantFailure(t, w, http.StatusInternalServerError, "internal")
	if _, err := os.Lstat(filepath.Join(cfg.Home, ".dotai")); !os.IsNotExist(err) {
		t.Errorf("~/.dotai = %v, want the read endpoints to write nothing", err)
	}
}

func TestPlanInstallThroughTheAppPlansTheExamplePack(t *testing.T) {
	cfg := appHome(t)
	pack, err := filepath.Abs(filepath.Join("..", "..", "examples", "minimal-pack"))
	if err != nil {
		t.Fatal(err)
	}
	h := server.New(cfg, server.AppDeps(fakePorts), server.Web())
	w := serve(h, post("/api/install/plan", `{"pack":`+jsonString(pack)+`}`, testToken))
	if w.Code != http.StatusOK {
		t.Fatalf("plan install = %d; body %s", w.Code, w.Body)
	}
	var p app.InstallPlan
	if err := json.Unmarshal(decode(t, w).Data, &p); err != nil || p.Pack == nil || p.Pack.Name != "minimal-pack" || p.Mode != app.ModeIsolated {
		t.Errorf("plan = %+v, %v, want the isolated plan of minimal-pack", p, err)
	}
	if _, err := os.Lstat(filepath.Join(cfg.Home, ".dotai")); !os.IsNotExist(err) {
		t.Errorf("~/.dotai = %v, want the plan to write nothing", err)
	}
}

func TestPlanProjectThroughTheAppRefusesTheHomeDirectory(t *testing.T) {
	cfg := appHome(t)
	h := server.New(cfg, server.AppDeps(fakePorts), server.Web())
	w := serve(h, post("/api/apply/plan", `{"project":`+jsonString(cfg.Home)+`,"profiles":["web"]}`, testToken))
	wantFailure(t, w, http.StatusConflict, "project-refused")
}

// planData posts body to the plan or preview target and returns its data
// and digest.
func planData(t *testing.T, h http.Handler, r *http.Request) (json.RawMessage, string) {
	t.Helper()
	w := serve(h, r)
	if w.Code != http.StatusOK {
		t.Fatalf("%s %s = %d; body %s", r.Method, r.URL, w.Code, w.Body)
	}
	data := decode(t, w).Data
	return data, digestOf(t, data)
}

func TestInstallThenRestoreThroughTheAppWriteOnlyInTheHome(t *testing.T) {
	cfg := appHome(t)
	pack, err := filepath.Abs(filepath.Join("..", "..", "examples", "minimal-pack"))
	if err != nil {
		t.Fatal(err)
	}
	h := server.New(cfg, server.AppDeps(fakePorts), server.Web())
	body := `{"pack":` + jsonString(pack)
	_, digest := planData(t, h, post("/api/install/plan", body+`}`, testToken))
	w := serve(h, post("/api/install", body+`,"digest":`+jsonString(digest)+`}`, testToken))
	if w.Code != http.StatusOK {
		t.Fatalf("install = %d; body %s", w.Code, w.Body)
	}
	var res app.InstallResult
	if err := json.Unmarshal(decode(t, w).Data, &res); err != nil || res.Backup == nil {
		t.Fatalf("install = %+v, %v, want a backup", res, err)
	}
	for _, op := range res.Ops {
		if rel, err := filepath.Rel(cfg.Home, op.Target); err != nil || strings.HasPrefix(rel, "..") {
			t.Errorf("install wrote %s, outside the home %s", op.Target, cfg.Home)
		}
	}
	if _, err := os.Stat(filepath.Join(cfg.Home, ".dotai", "state.json")); err != nil {
		t.Fatalf("state = %v, want the install recorded", err)
	}
	// The same digest again: the plan now keeps every file, so it changed.
	w = serve(h, post("/api/install", body+`,"digest":`+jsonString(digest)+`}`, testToken))
	wantFailure(t, w, http.StatusConflict, server.CodePlanChanged)

	_, digest = planData(t, h, request("GET", "/api/restore/preview", testToken))
	w = serve(h, post("/api/restore", `{"digest":`+jsonString(digest)+`}`, testToken))
	if w.Code != http.StatusOK {
		t.Fatalf("restore = %d; body %s", w.Code, w.Body)
	}
	var st app.Status
	if err := json.Unmarshal(decode(t, serve(h, request("GET", "/api/status", testToken))).Data, &st); err != nil || st.State != nil {
		t.Errorf("status after the restore = %+v, %v, want no install", st, err)
	}
}

func TestARestoreRefusesAPreviewThatChanged(t *testing.T) {
	cfg := appHome(t)
	pack, err := filepath.Abs(filepath.Join("..", "..", "examples", "minimal-pack"))
	if err != nil {
		t.Fatal(err)
	}
	h := server.New(cfg, server.AppDeps(fakePorts), server.Web())
	_, digest := planData(t, h, post("/api/install/plan", `{"pack":`+jsonString(pack)+`}`, testToken))
	if w := serve(h, post("/api/install", `{"pack":`+jsonString(pack)+`,"digest":`+jsonString(digest)+`}`, testToken)); w.Code != http.StatusOK {
		t.Fatalf("install = %d; body %s", w.Code, w.Body)
	}
	data, digest := planData(t, h, request("GET", "/api/restore/preview", testToken))
	var preview app.RestoreResult
	if err := json.Unmarshal(data, &preview); err != nil || len(preview.Ops) == 0 {
		t.Fatalf("preview = %s, %v", data, err)
	}
	// Another program edits a file the restore removes.
	target := preview.Ops[0].Target
	if err := os.WriteFile(target, []byte("edited\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	w := serve(h, post("/api/restore", `{"digest":`+jsonString(digest)+`,"yes":true}`, testToken))
	wantFailure(t, w, http.StatusConflict, server.CodePlanChanged)
	if got, err := os.ReadFile(target); err != nil || string(got) != "edited\n" {
		t.Errorf("%s = %q, %v, want the edit kept", target, got, err)
	}
	if _, err := os.Stat(filepath.Join(cfg.Home, ".dotai", "state.json")); err != nil {
		t.Errorf("state = %v, want the install still recorded", err)
	}
}

func TestApplyRestoreAndFixAProjectThroughTheApp(t *testing.T) {
	cfg := appHome(t)
	pack, err := filepath.Abs(filepath.Join("..", "..", "examples", "minimal-pack"))
	if err != nil {
		t.Fatal(err)
	}
	project := t.TempDir()
	h := server.New(cfg, server.AppDeps(fakePorts), server.Web())
	body := `{"project":` + jsonString(project) + `,"pack":` + jsonString(pack) + `,"profiles":["web"]`
	_, digest := planData(t, h, post("/api/apply/plan", body+`}`, testToken))
	w := serve(h, post("/api/apply", body+`,"digest":`+jsonString(digest)+`}`, testToken))
	if w.Code != http.StatusOK {
		t.Fatalf("apply = %d; body %s", w.Code, w.Body)
	}
	if _, err := os.Stat(filepath.Join(project, ".dotai", "state.json")); err != nil {
		t.Fatalf("marker = %v, want the apply written", err)
	}

	data, digest := planData(t, h, request("GET", "/api/doctor", testToken))
	w = serve(h, post("/api/doctor/fix", `{"digest":`+jsonString(digest)+`}`, testToken))
	if w.Code != http.StatusOK && w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("doctor fix = %d; body %s; found %s", w.Code, w.Body, data)
	}

	_, digest = planData(t, h, request("GET", "/api/restore/project/preview?project="+project, testToken))
	w = serve(h, post("/api/restore/project", `{"project":`+jsonString(project)+`,"digest":`+jsonString(digest)+`}`, testToken))
	if w.Code != http.StatusOK {
		t.Fatalf("restore --project = %d; body %s", w.Code, w.Body)
	}
	if _, err := os.Lstat(filepath.Join(project, ".dotai")); !os.IsNotExist(err) {
		t.Errorf("project .dotai = %v, want it removed by the restore", err)
	}
}
