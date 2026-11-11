package server_test

import (
	"encoding/json"
	"io/fs"
	"maps"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lartweib/dotai/internal/app"
	"github.com/lartweib/dotai/internal/server"
)

// snapshot is every file under dirs with its content.
func snapshot(t *testing.T, dirs ...string) map[string]string {
	t.Helper()
	out := map[string]string{}
	for _, dir := range dirs {
		err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return err
			}
			data, err := os.ReadFile(p)
			out[p] = string(data)
			return err
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	return out
}

func examplePack(t *testing.T) string {
	t.Helper()
	pack, err := filepath.Abs(filepath.Join("..", "..", "examples", "minimal-pack"))
	if err != nil {
		t.Fatal(err)
	}
	return pack
}

// unchanged fails t when the files under dirs are not before.
func unchanged(t *testing.T, before map[string]string, dirs ...string) {
	t.Helper()
	if after := snapshot(t, dirs...); !maps.Equal(after, before) {
		t.Errorf("files changed after a refused action:\nbefore %v\nafter  %v", before, after)
	}
}

func TestAStaleInstallWritesNothing(t *testing.T) {
	cfg := appHome(t)
	h := server.New(cfg, server.AppDeps(fakePorts), server.Web())
	body := `{"pack":` + jsonString(examplePack(t))
	data, digest := planData(t, h, post("/api/install/plan", body+`}`, testToken))
	var p app.InstallPlan
	if err := json.Unmarshal(data, &p); err != nil || len(p.Ops) == 0 {
		t.Fatalf("plan = %s, %v", data, err)
	}
	// Another program writes a file the plan creates whole.
	target := ""
	for _, op := range p.Ops {
		if op.Kind == app.KindTools {
			target = op.Target
		}
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, []byte("theirs\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	before := snapshot(t, cfg.Home)
	w := serve(h, post("/api/install", body+`,"digest":`+jsonString(digest)+`,"yes":true}`, testToken))
	wantFailure(t, w, http.StatusConflict, server.CodePlanChanged)
	unchanged(t, before, cfg.Home)
	if _, err := os.Stat(filepath.Join(cfg.Home, ".dotai", "state.json")); !os.IsNotExist(err) {
		t.Errorf("state = %v, want no install recorded", err)
	}
}

func TestAStaleApplyAndAStaleRestoreOfTheProjectWriteNothing(t *testing.T) {
	cfg := appHome(t)
	project := t.TempDir()
	h := server.New(cfg, server.AppDeps(fakePorts), server.Web())
	body := `{"project":` + jsonString(project) + `,"pack":` + jsonString(examplePack(t)) + `,"profiles":["web"]`
	data, digest := planData(t, h, post("/api/apply/plan", body+`}`, testToken))
	var p app.ProjectPlan
	if err := json.Unmarshal(data, &p); err != nil || len(p.Ops) == 0 {
		t.Fatalf("plan = %s, %v", data, err)
	}
	if err := os.MkdirAll(filepath.Dir(p.Ops[0].Target), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p.Ops[0].Target, []byte("theirs\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	before := snapshot(t, cfg.Home, project)
	wantFailure(t, serve(h, post("/api/apply", body+`,"digest":`+jsonString(digest)+`,"yes":true}`, testToken)), http.StatusConflict, server.CodePlanChanged)
	unchanged(t, before, cfg.Home, project)

	if err := os.Remove(p.Ops[0].Target); err != nil {
		t.Fatal(err)
	}
	_, digest = planData(t, h, post("/api/apply/plan", body+`}`, testToken))
	if w := serve(h, post("/api/apply", body+`,"digest":`+jsonString(digest)+`}`, testToken)); w.Code != http.StatusOK {
		t.Fatalf("apply = %d; body %s", w.Code, w.Body)
	}
	_, digest = planData(t, h, request("GET", "/api/restore/project/preview?project="+project, testToken))
	// Another program edits a file the restore removes.
	if err := os.WriteFile(p.Ops[0].Target, []byte("edited\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	before = snapshot(t, cfg.Home, project)
	wantFailure(t, serve(h, post("/api/restore/project", `{"project":`+jsonString(project)+`,"digest":`+jsonString(digest)+`,"yes":true}`, testToken)), http.StatusConflict, server.CodePlanChanged)
	unchanged(t, before, cfg.Home, project)
}

// lookGit is the git on PATH, looked up before appHome replaces PATH, or
// skips t without git.
func lookGit(t *testing.T) string {
	t.Helper()
	git, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git is not installed")
	}
	return git
}

// gitProject is a git repository, with git added to the fake commands of
// appHome.
func gitProject(t *testing.T, git string) string {
	t.Helper()
	cfg := os.Getenv("PATH")
	t.Setenv("PATH", cfg+string(os.PathListSeparator)+filepath.Dir(git))
	project := t.TempDir()
	if out, err := exec.Command(git, "init", "-q", project).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v %s", err, out)
	}
	return project
}

func TestDoctorFixWritesTheExcludeBlockOnlyForTheFindingsItShowed(t *testing.T) {
	git := lookGit(t)
	cfg := appHome(t)
	project := gitProject(t, git)
	h := server.New(cfg, server.AppDeps(fakePorts), server.Web())
	body := `{"project":` + jsonString(project) + `,"pack":` + jsonString(examplePack(t)) + `,"profiles":["web"]`
	_, digest := planData(t, h, post("/api/apply/plan", body+`}`, testToken))
	if w := serve(h, post("/api/apply", body+`,"digest":`+jsonString(digest)+`}`, testToken)); w.Code != http.StatusOK {
		t.Fatalf("apply = %d; body %s", w.Code, w.Body)
	}
	exclude := filepath.Join(project, ".git", "info", "exclude")
	applied, err := os.ReadFile(exclude)
	if err != nil || !strings.Contains(string(applied), ".dotai") {
		t.Fatalf("exclude = %q, %v, want the project's block", applied, err)
	}
	_, clean := planData(t, h, request("GET", "/api/doctor", testToken))
	if err := os.WriteFile(exclude, []byte("# the user's own\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// The digest of the doctor run before the block went: refused.
	before := snapshot(t, cfg.Home, project)
	wantFailure(t, serve(h, post("/api/doctor/fix", `{"digest":`+jsonString(clean)+`}`, testToken)), http.StatusConflict, server.CodePlanChanged)
	unchanged(t, before, cfg.Home, project)

	w := serve(h, request("GET", "/api/doctor", testToken))
	wantFailure(t, w, http.StatusUnprocessableEntity, "invalid")
	var found struct {
		Findings []app.Finding `json:"findings"`
		Digest   string        `json:"digest"`
	}
	if err := json.Unmarshal(decode(t, w).Data, &found); err != nil || len(found.Findings) != 1 || found.Findings[0].Code != app.CodeExcludeMissing || !found.Findings[0].Fixable {
		t.Fatalf("doctor = %s, %v, want the missing exclude block, fixable", decode(t, w).Data, err)
	}
	w = serve(h, post("/api/doctor/fix", `{"digest":`+jsonString(found.Digest)+`}`, testToken))
	if w.Code != http.StatusOK {
		t.Fatalf("doctor fix = %d; body %s", w.Code, w.Body)
	}
	var fixed app.DoctorResult
	if err := json.Unmarshal(decode(t, w).Data, &fixed); err != nil || len(fixed.Findings) != 1 || !fixed.Findings[0].Fixed || len(fixed.Backups) != 1 {
		t.Errorf("fix = %s, %v, want the finding fixed with a backup", decode(t, w).Data, err)
	}
	if got, _ := os.ReadFile(exclude); !strings.Contains(string(got), ".dotai") || !strings.Contains(string(got), "# the user's own") {
		t.Errorf("exclude after the fix = %q, want the block back and the user's line kept", got)
	}
}
