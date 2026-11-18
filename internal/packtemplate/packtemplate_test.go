package packtemplate_test

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"flag"
	"io/fs"
	"os"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/lartweib/dotai/internal/packtemplate"
)

var update = flag.Bool("update", false, "append the hashes of the current template version to hashes.txt")

// example is the template's own answers: Fill with them writes the
// embedded template as it is.
var example = packtemplate.Answers{
	Name:      "example-pack",
	Harnesses: []string{"claudecode", "antigravity-cli", "opencode"},
	Profiles: []packtemplate.Profile{{
		Name:        "example",
		Description: "An example profile, to replace with one for a kind of project, such as web apps.",
		Hints:       []string{"package.json"},
		Mods:        []string{"example-mod"},
	}},
}

func TestFillWithTheTemplatesOwnAnswersWritesTheEmbeddedTemplate(t *testing.T) {
	files, err := packtemplate.Fill(example)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string][]byte{}
	for _, f := range files {
		got[f.Path] = f.Data
	}
	want := map[string][]byte{}
	err = fs.WalkDir(packtemplate.FS(), ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		want[p], err = fs.ReadFile(packtemplate.FS(), p)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	for p, data := range want {
		if !bytes.Equal(got[p], data) {
			t.Errorf("%s:\ngot  %q\nwant %q", p, got[p], data)
		}
	}
	for p := range got {
		if _, ok := want[p]; !ok {
			t.Errorf("Fill wrote %s, which the template does not hold", p)
		}
	}
}

func TestFillGivesEachProfileAndModItsOwnFiles(t *testing.T) {
	files, err := packtemplate.Fill(packtemplate.Answers{
		Name:      "my-pack",
		Harnesses: []string{"opencode", "antigravity-cli"},
		Requires:  []string{"opencode"},
		Profiles: []packtemplate.Profile{
			{Name: "web", Description: `Web apps: "quoted" and #hash`, Hints: []string{"*.config.js", "package.json"}, Mods: []string{"panel"}},
			{Name: "go", Description: "Go services.", Mods: []string{"panel", "lint"}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	byPath := map[string]packtemplate.File{}
	var paths []string
	for _, f := range files {
		byPath[f.Path] = f
		paths = append(paths, f.Path)
	}
	for _, p := range []string{"profiles/web/.rulesync/rules/web.md", "profiles/go/.rulesync/rules/go.md", "mods/panel/hooks/hooks.json", "mods/lint/hooks/example-mod.js"} {
		if _, ok := byPath[p]; !ok {
			t.Errorf("Fill wrote no %s; it wrote %v", p, paths)
		}
	}
	for _, p := range paths {
		if strings.Contains(p, "/example/") || strings.Contains(p, "example-mod/") {
			t.Errorf("Fill wrote the template's own profile or mod %s", p)
		}
	}
	if settings := string(byPath["base/dotai/settings.yaml"].Data); strings.Contains(settings, "claudecode:") {
		t.Errorf("settings.yaml keeps the block of a harness the pack does not name:\n%s", settings)
	}
	if mode := byPath["base/dotai/tools/example-tool.sh"].Mode; mode&0o111 == 0 {
		t.Errorf("the .sh tool has mode %v, want it executable", mode)
	}
	manifest := string(byPath["dotai.yaml"].Data)
	for _, want := range []string{"name: my-pack\n", "harnesses: [opencode, antigravity-cli]\n", "requires: [opencode]\n", `description: "Web apps: \"quoted\" and #hash"`, `hints: ["*.config.js", package.json]`, "mods: [panel, lint]"} {
		if !strings.Contains(manifest, want) {
			t.Errorf("dotai.yaml lacks %q:\n%s", want, manifest)
		}
	}
}

func TestFillRefusesANameThatIsNotAPathSegment(t *testing.T) {
	for _, a := range []packtemplate.Answers{
		{Name: "../x", Harnesses: []string{"opencode"}},
		{Name: "ok", Harnesses: []string{"opencode"}, Profiles: []packtemplate.Profile{{Name: "../web", Description: "d"}}},
		{Name: "ok", Harnesses: []string{"opencode"}, Profiles: []packtemplate.Profile{{Name: "web", Description: "d", Mods: []string{"a/b"}}}},
	} {
		if _, err := packtemplate.Fill(a); err == nil {
			t.Errorf("Fill(%+v) = nil error, want a refusal", a)
		}
	}
}

func TestEveryFileFillCanWriteIsAKnownTemplateFile(t *testing.T) {
	for _, harnesses := range [][]string{{"claudecode"}, {"opencode"}, {"antigravity-cli", "opencode"}} {
		a := example
		a.Harnesses = harnesses
		files, err := packtemplate.Fill(a)
		if err != nil {
			t.Fatal(err)
		}
		for _, f := range files {
			root := !strings.Contains(f.Path, "/")
			if got := packtemplate.Unchanged(f.Data); got == root {
				t.Errorf("%v: Unchanged(%s) = %v, want %v: the files at the top are not installed", harnesses, f.Path, got, !root)
			}
			// A .ps1 is checked out with CRLF already (.gitattributes).
			lf := bytes.ReplaceAll(f.Data, []byte("\r\n"), []byte("\n"))
			crlf := bytes.ReplaceAll(lf, []byte("\n"), []byte("\r\n"))
			if !root && !packtemplate.Unchanged(crlf) {
				t.Errorf("Unchanged(%s with CRLF) = false: line endings are normalized", f.Path)
			}
		}
	}
	if packtemplate.Unchanged([]byte("an edited file\n")) {
		t.Error("Unchanged holds a file of the user's own")
	}
}

// TestHashesHoldTheCurrentTemplate fails when the embedded template
// changes without its hashes: a released version's hashes stay in
// hashes.txt forever, so a change to the template is a new Version whose
// hashes are appended (go test ./internal/packtemplate -update).
func TestHashesHoldTheCurrentTemplate(t *testing.T) {
	data, err := os.ReadFile("hashes.txt")
	if err != nil {
		t.Fatal(err)
	}
	lines := packtemplate.ParseHashes(data)
	last := 0
	var current []string
	for _, l := range lines {
		if l.Version < last || l.Version < 1 || l.Version > packtemplate.Version {
			t.Fatalf("hashes.txt: version %d after %d, with the current version %d: versions only grow, and a version's hashes are never removed", l.Version, last, packtemplate.Version)
		}
		last = l.Version
		if l.Version == packtemplate.Version {
			current = append(current, l.Hash+" "+l.Path)
		}
	}
	want := packtemplate.CurrentHashes()
	if slices.Equal(current, want) {
		return
	}
	if *update && len(current) == 0 {
		var b strings.Builder
		b.Write(data)
		for _, h := range want {
			b.WriteString(strconv.Itoa(packtemplate.Version) + " " + h + "\n")
		}
		if err := os.WriteFile("hashes.txt", []byte(b.String()), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	t.Errorf("hashes.txt holds for version %d:\n%s\nthe embedded template has:\n%s\n"+
		"a released template's hashes stay: bump packtemplate.Version, then run go test ./internal/packtemplate -update to append the new ones",
		packtemplate.Version, strings.Join(current, "\n"), strings.Join(want, "\n"))
}

// released pins each released version of the template: the SHA-256 in
// hex of its lines of hashes.txt, "<hash> <path>" each, sorted and joined
// by newlines (versionDigest). A released version's lines never change
// and are never removed, so its pin never changes either; releasing a
// new version adds its pin here.
var released = map[int]string{
	1: "bf6f4e28637a32431eefbb0ba75ce1253a5134196149545d8473bc76963d2a4b",
}

// versionDigest is the pin of version v of lines.
func versionDigest(lines []packtemplate.HashLine, v int) string {
	var vs []string
	for _, l := range lines {
		if l.Version == v {
			vs = append(vs, l.Hash+" "+l.Path)
		}
	}
	slices.Sort(vs)
	sum := sha256.Sum256([]byte(strings.Join(vs, "\n")))
	return hex.EncodeToString(sum[:])
}

// TestReleasedHashesNeverChange fails when a line of a released version
// of hashes.txt is edited or deleted, or a line is added to one: a pack
// made from that release would no longer be recognized, or a file it
// never wrote would be. Every version before the current one is released.
func TestReleasedHashesNeverChange(t *testing.T) {
	data, err := os.ReadFile("hashes.txt")
	if err != nil {
		t.Fatal(err)
	}
	lines := packtemplate.ParseHashes(data)
	for v := 1; v < packtemplate.Version; v++ {
		if _, ok := released[v]; !ok {
			t.Errorf("version %d comes before the current version %d but has no pin in released", v, packtemplate.Version)
		}
	}
	for v, pin := range released {
		if got := versionDigest(lines, v); got != pin {
			t.Errorf("the lines of version %d of hashes.txt hash to %s, want the pin %s: a released version's lines are never edited, removed or added to", v, got, pin)
		}
	}
}
