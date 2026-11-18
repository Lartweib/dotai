package server_test

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lartweib/dotai/internal/server"
)

// newBody is the body of the wizard's plan and action for a pack in dir.
func newBody(dir, extra string) string {
	return `{"dir":` + jsonString(dir) + `,"name":"my-pack","harnesses":["claudecode","opencode"],` +
		`"profiles":[{"name":"web","description":"Web apps.","hints":["package.json"],"mods":["panel"]}]` + extra + `}`
}

func TestTheWizardPlansThenWritesThePackOfTheDigest(t *testing.T) {
	cfg := appHome(t)
	h := server.New(cfg, server.AppDeps(fakePorts), server.Web())
	dir := filepath.Join(t.TempDir(), "my-pack")

	w := serve(h, post("/api/packs/new/plan", newBody(dir, ""), testToken))
	if w.Code != http.StatusOK {
		t.Fatalf("plan = %d; body %s", w.Code, w.Body)
	}
	var plan struct {
		Digest string   `json:"digest"`
		Files  []string `json:"files"`
		Skill  string   `json:"skill"`
	}
	e := decode(t, w)
	if err := json.Unmarshal(e.Data, &plan); err != nil {
		t.Fatal(err)
	}
	if e.Command != "pack new" || !strings.HasPrefix(plan.Digest, "sha256:") || len(plan.Files) == 0 || !strings.Contains(plan.Skill, "### web") {
		t.Fatalf("plan = %s, want the files, the skill and a digest", e.Data)
	}
	if _, err := os.Stat(dir); err == nil {
		t.Fatal("the plan wrote the pack")
	}

	wantFailure(t, serve(h, post("/api/packs/new", newBody(dir, ""), testToken)), http.StatusBadRequest, "usage")
	changed := serve(h, post("/api/packs/new", newBody(dir, `,"digest":"sha256:0"`), testToken))
	wantFailure(t, changed, http.StatusConflict, "plan-changed")
	if _, err := os.Stat(dir); err == nil {
		t.Fatal("a changed plan wrote the pack")
	}

	w = serve(h, post("/api/packs/new", newBody(dir, `,"digest":`+jsonString(plan.Digest)), testToken))
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"outcome": "added"`) {
		t.Fatalf("new = %d; body %s", w.Code, w.Body)
	}
	if _, err := os.Stat(filepath.Join(dir, "dotai.yaml")); err != nil {
		t.Errorf("the pack was not written: %v", err)
	}
	if list := packList(t, h, request("GET", "/api/packs", testToken)); len(list.Packs) != 1 || list.Packs[0].ID != "local/my-pack" {
		t.Errorf("packs = %+v, want the new one", list.Packs)
	}
	wantFailure(t, serve(h, post("/api/packs/new/plan", newBody(dir, ""), testToken)), http.StatusConflict, "new-refused")
}
