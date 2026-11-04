package server_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
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
	return server.Config{Home: home, CacheDir: filepath.Join(tmp, "cache"), Token: testToken, Port: testPort}
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
	w := serve(h, request("GET", "/api/install/plan?pack="+url.QueryEscape(pack)+"", testToken))
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
	w := serve(h, request("GET", "/api/apply/plan?project="+url.QueryEscape(cfg.Home)+"&profile=web", testToken))
	wantFailure(t, w, http.StatusConflict, "project-refused")
}
