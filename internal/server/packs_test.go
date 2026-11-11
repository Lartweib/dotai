package server_test

import (
	"encoding/json"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"testing"

	"github.com/lartweib/dotai/internal/app"
	"github.com/lartweib/dotai/internal/server"
)

// copiedExample copies the example pack into a directory of its own.
func copiedExample(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "minimal-pack")
	if err := os.CopyFS(dir, os.DirFS(filepath.Join("..", "..", "examples", "minimal-pack"))); err != nil {
		t.Fatal(err)
	}
	return dir
}

func packList(t *testing.T, h http.Handler, r *http.Request) app.PackList {
	t.Helper()
	w := serve(h, r)
	if w.Code != http.StatusOK {
		t.Fatalf("%s %s = %d; body %s", r.Method, r.URL, w.Code, w.Body)
	}
	var list app.PackList
	if err := json.Unmarshal(decode(t, w).Data, &list); err != nil {
		t.Fatal(err)
	}
	return list
}

func TestThePackRegistryImportsListsAndRemovesThroughTheApp(t *testing.T) {
	cfg := appHome(t)
	h := server.New(cfg, server.AppDeps(fakePorts), server.Web())
	dir := copiedExample(t)
	if list := packList(t, h, request("GET", "/api/packs", testToken)); len(list.Packs) != 0 {
		t.Fatalf("packs of a new home = %+v", list.Packs)
	}
	list := packList(t, h, post("/api/packs/import", `{"path":`+jsonString(dir)+`}`, testToken))
	if len(list.Packs) != 1 || list.Packs[0].Name != "minimal-pack" {
		t.Errorf("after the import = %+v", list.Packs)
	}
	if list := packList(t, h, request("GET", "/api/packs", testToken)); len(list.Packs) != 1 {
		t.Errorf("listed = %+v", list.Packs)
	}
	if list := packList(t, h, post("/api/packs/remove", `{"path":`+jsonString(dir)+`}`, testToken)); len(list.Packs) != 0 {
		t.Errorf("after the removal = %+v", list.Packs)
	}
	if _, err := os.Stat(filepath.Join(dir, "dotai.yaml")); err != nil {
		t.Errorf("the removal touched the pack: %v", err)
	}
}

func TestImportingWhatIsNotAPackFails(t *testing.T) {
	cfg := appHome(t)
	h := server.New(cfg, server.AppDeps(fakePorts), server.Web())
	wantFailure(t, serve(h, post("/api/packs/import", `{"path":"pack"}`, testToken)), http.StatusBadRequest, "usage")
	wantFailure(t, serve(h, post("/api/packs/import", `{"path":`+jsonString(t.TempDir())+`}`, testToken)), http.StatusUnprocessableEntity, "invalid")
}

func TestCopyPackThroughTheAppCopiesWithoutImporting(t *testing.T) {
	cfg := appHome(t)
	h := server.New(cfg, server.AppDeps(fakePorts), server.Web())
	dir := copiedExample(t)
	to := dir + "-copy"
	w := serve(h, post("/api/packs/copy", `{"from":`+jsonString(dir)+`,"to":`+jsonString(to)+`}`, testToken))
	if w.Code != http.StatusOK {
		t.Fatalf("copy = %d; body %s", w.Code, w.Body)
	}
	if _, err := os.Stat(filepath.Join(to, "dotai.yaml")); err != nil {
		t.Errorf("the copy has no manifest: %v", err)
	}
	if list := packList(t, h, request("GET", "/api/packs", testToken)); len(list.Packs) != 0 {
		t.Errorf("the copy was imported: %+v", list.Packs)
	}
	again := serve(h, post("/api/packs/copy", `{"from":`+jsonString(dir)+`,"to":`+jsonString(to)+`}`, testToken))
	wantFailure(t, again, http.StatusConflict, "copy-refused")
}

func TestPackSummaryListsTheLayersOfTheExamplePack(t *testing.T) {
	cfg := appHome(t)
	h := server.New(cfg, server.AppDeps(fakePorts), server.Web())
	dir := copiedExample(t)
	w := serve(h, request("GET", "/api/pack/summary?path="+url.QueryEscape(dir), testToken))
	if w.Code != http.StatusOK {
		t.Fatalf("summary = %d; body %s", w.Code, w.Body)
	}
	var sum app.PackContents
	if err := json.Unmarshal(decode(t, w).Data, &sum); err != nil || sum.Name != "minimal-pack" || len(sum.Layers) != 5 {
		t.Errorf("summary = %+v, %v", sum, err)
	}
	wantFailure(t, serve(h, request("GET", "/api/pack/summary", testToken)), http.StatusBadRequest, "usage")
}

func TestACorruptRegistryAnswersRegistryInvalid(t *testing.T) {
	cfg := appHome(t)
	h := server.New(cfg, server.AppDeps(fakePorts), server.Web())
	name := filepath.Join(cfg.Home, ".dotai", "packs.json")
	if err := os.MkdirAll(filepath.Dir(name), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(name, []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	wantFailure(t, serve(h, request("GET", "/api/packs", testToken)), http.StatusConflict, "registry-invalid")
}

func TestARelativeRegistryEntryCanBeRemoved(t *testing.T) {
	cfg := appHome(t)
	h := server.New(cfg, server.AppDeps(fakePorts), server.Web())
	name := filepath.Join(cfg.Home, ".dotai", "packs.json")
	if err := os.MkdirAll(filepath.Dir(name), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(name, []byte(`{"schema":"dotai.packs/v1","packs":[{"path":"rel"}]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if list := packList(t, h, post("/api/packs/remove", `{"path":"rel"}`, testToken)); len(list.Packs) != 0 {
		t.Errorf("after the removal = %+v", list.Packs)
	}
}
