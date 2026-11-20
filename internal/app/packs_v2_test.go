package app_test

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lartweib/dotai/internal/app"
)

// namedPack is a copy of the example pack whose manifest is named name.
func namedPack(t *testing.T, name string) string {
	t.Helper()
	dir := packCopy(t)
	manifest := filepath.Join(dir, "dotai.yaml")
	data, err := os.ReadFile(manifest)
	if err != nil {
		t.Fatal(err)
	}
	data = []byte(strings.Replace(string(data), "name: minimal-pack", "name: "+name, 1))
	if err := os.WriteFile(manifest, data, 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

// registryDoc reads packs.json of home as JSON.
func registryDoc(t *testing.T, home string) map[string]any {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(home, ".dotai", "packs.json"))
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatal(err)
	}
	return doc
}

func TestAV1RegistryReadsAsLocalPacks(t *testing.T) {
	home, dir := t.TempDir(), packCopy(t)
	file := writeRegistry(t, home, `{"schema":"dotai.packs/v1","packs":[{"path":`+strconvQuote(dir)+`}]}`)
	list, err := app.ListPacks(home)
	if err != nil {
		t.Fatalf("ListPacks() error = %v", err)
	}
	if len(list.Packs) != 1 || list.Packs[0].ID != "local/minimal-pack" || list.Packs[0].Source != nil {
		t.Errorf("packs = %+v, want local/minimal-pack without a source", list.Packs)
	}
	if got, _ := os.ReadFile(file); !strings.Contains(string(got), "dotai.packs/v1") {
		t.Errorf("listing rewrote the registry: %s", got)
	}
}

func TestAnEditOfAV1RegistryWritesV2WithTheIdentities(t *testing.T) {
	home, old, other := t.TempDir(), packCopy(t), namedPack(t, "other-pack")
	writeRegistry(t, home, `{"schema":"dotai.packs/v1","packs":[{"path":"relative/pack"},{"path":`+strconvQuote(old)+`}]}`)
	res, err := app.AddLocalPack(home, other)
	if err != nil {
		t.Fatalf("AddLocalPack() error = %v", err)
	}
	if res.Outcome != "added" || res.Pack.ID != "local/other-pack" || res.Pack.Path != other {
		t.Errorf("result = %+v, want local/other-pack added", res)
	}
	doc := registryDoc(t, home)
	if doc["schema"] != "dotai.packs/v2" {
		t.Errorf("schema = %v, want dotai.packs/v2", doc["schema"])
	}
	packs := doc["packs"].([]any)
	if len(packs) != 3 {
		t.Fatalf("packs = %v, want three", packs)
	}
	// An entry whose manifest cannot be read keeps no id: it reads as
	// local/<name> once it can be read.
	if e := packs[0].(map[string]any); e["path"] != "relative/pack" || e["id"] != nil {
		t.Errorf("the relative entry = %v, want its path alone", e)
	}
	if e := packs[1].(map[string]any); e["id"] != "local/minimal-pack" || e["source"] != nil {
		t.Errorf("the v1 entry = %v, want id local/minimal-pack and no source", e)
	}
	if e := packs[2].(map[string]any); e["id"] != "local/other-pack" {
		t.Errorf("the new entry = %v", e)
	}
}

func TestAV2RegistryListsAGitPackWithItsSource(t *testing.T) {
	home := t.TempDir()
	dir := filepath.Join(home, ".dotai", "packs", "github.com", "acme", "pack")
	if err := os.CopyFS(dir, os.DirFS(packCopy(t))); err != nil {
		t.Fatal(err)
	}
	writeRegistry(t, home, `{"schema":"dotai.packs/v2","packs":[{"id":"github.com/acme/pack","path":`+strconvQuote(dir)+
		`,"source":{"url":"https://github.com/Acme/Pack","ref":"v1","fullRef":"refs/tags/v1","commit":"`+strings.Repeat("a", 40)+`"}}]}`)
	list, err := app.ListPacks(home)
	if err != nil {
		t.Fatalf("ListPacks() error = %v", err)
	}
	want := app.PackSource{URL: "https://github.com/Acme/Pack", Ref: "v1", FullRef: "refs/tags/v1", Commit: strings.Repeat("a", 40)}
	if len(list.Packs) != 1 || list.Packs[0].ID != "github.com/acme/pack" || list.Packs[0].Source == nil || *list.Packs[0].Source != want ||
		list.Packs[0].Name != "minimal-pack" {
		t.Errorf("packs = %+v, want the git pack with its source", list.Packs)
	}
}

func TestAddLocalPackRefusesAnotherDirectoryWithATakenName(t *testing.T) {
	home, first, second := t.TempDir(), packCopy(t), packCopy(t)
	if _, err := app.AddLocalPack(home, first); err != nil {
		t.Fatal(err)
	}
	_, err := app.AddLocalPack(home, second)
	if _, ok := errors.AsType[*app.PackNameTakenError](err); !ok {
		t.Errorf("error = %v, want a PackNameTakenError", err)
	}
	if list, _ := app.ListPacks(home); len(list.Packs) != 1 {
		t.Errorf("packs = %+v, want the first alone", list.Packs)
	}
}

func TestAddLocalPackRefusesANewNameThatBreaksTheModRule(t *testing.T) {
	home := t.TempDir()
	for _, name := range []string{"My_Pack", "con"} {
		_, err := app.AddLocalPack(home, namedPack(t, name))
		if !errors.As(err, new(*app.UsageError)) {
			t.Errorf("AddLocalPack(%s) error = %v, want a usage error", name, err)
		}
	}
}

func TestALegacyNameThatBreaksTheModRuleIsKept(t *testing.T) {
	home, dir := t.TempDir(), namedPack(t, "My_Pack")
	writeRegistry(t, home, `{"schema":"dotai.packs/v1","packs":[{"path":`+strconvQuote(dir)+`}]}`)
	res, err := app.AddLocalPack(home, dir)
	if err != nil || res.Outcome != "unchanged" || res.Pack.ID != "local/My_Pack" {
		t.Errorf("AddLocalPack() of the listed legacy pack = %+v, %v, want it unchanged as local/My_Pack", res, err)
	}
	if _, err := app.AddLocalPack(home, namedPack(t, "other")); err != nil {
		t.Fatalf("another add: %v", err)
	}
	if got := registryDoc(t, home)["packs"].([]any)[0].(map[string]any)["id"]; got != "local/My_Pack" {
		t.Errorf("the legacy entry's id = %v, want local/My_Pack", got)
	}
}

func TestAHandEditedRegistryIsRefused(t *testing.T) {
	sha := strings.Repeat("a", 40)
	git := func(id, url string) string {
		return `{"id":"` + id + `","path":"/p/` + id + `","source":{"url":"` + url + `","ref":"","fullRef":"refs/heads/main","commit":"` + sha + `"}}`
	}
	for name, packs := range map[string]string{
		"a duplicate git identity":   git("github.com/acme/pack", "https://github.com/acme/pack") + "," + git("github.com/acme/pack", "git@github.com:acme/pack"),
		"a URL of another pack":      git("github.com/acme/pack", "https://github.com/evil/pack"),
		"a URL of another host":      git("github.com/acme/pack", "https://gitlab.com/acme/pack"),
		"a duplicate local identity": `{"id":"local/x","path":"/a"},{"id":"local/x","path":"/b"}`,
	} {
		t.Run(name, func(t *testing.T) {
			home := t.TempDir()
			writeRegistry(t, home, `{"schema":"dotai.packs/v2","packs":[`+packs+`]}`)
			if _, err := app.ListPacks(home); !errors.As(err, new(*app.RegistryInvalidError)) {
				t.Errorf("ListPacks() error = %v, want a RegistryInvalidError", err)
			}
		})
	}
}

func TestALocalPackRenamedInItsManifestIsListedWithAnError(t *testing.T) {
	home, dir := t.TempDir(), namedPack(t, "renamed")
	writeRegistry(t, home, `{"schema":"dotai.packs/v2","packs":[{"id":"local/before","path":`+strconvQuote(dir)+`}]}`)
	list, err := app.ListPacks(home)
	if err != nil || len(list.Packs) != 1 || list.Packs[0].ID != "local/before" || !strings.Contains(list.Packs[0].Error, "renamed") {
		t.Errorf("ListPacks() = %+v, %v, want local/before listed with an error naming the new name", list.Packs, err)
	}
}
