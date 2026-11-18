package app_test

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/lartweib/dotai/internal/app"
)

// agyPack writes a pack of one rule for agy and returns its directory.
func agyPack(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "dotai.yaml"), "schema: dotai.pack/v1\nname: p\nversion: 1.0.0\nengine: '>=0.1.0'\nrulesync: 43.9.2\nharnesses: [antigravity-cli]\n")
	writeFile(t, filepath.Join(dir, "base", ".rulesync", "rules", "r.md"), "# R\n")
	return dir
}

// jsonText is s as it is written inside a JSON string.
func jsonText(t *testing.T, s string) string {
	t.Helper()
	b, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	return string(b[1 : len(b)-1])
}

// realHome writes home in data where rulesyncHome writes the scope's home
// directory, in the same form, as the stage must; in a JSON file, inJSON,
// the home directory is written as a JSON string holds it.
func withRealHome(t *testing.T, data, home string, inJSON bool) string {
	t.Helper()
	slash, native := filepath.ToSlash(home), filepath.FromSlash(home)
	if inJSON {
		slash, native = jsonText(t, slash), jsonText(t, native)
	}
	return strings.NewReplacer("<home-native>", native, "<home-json>", native, "<home-upper>", slash, "<home>", slash).Replace(data)
}

// noStagingPath fails the test when a file under out names a staging tree.
func noStagingPath(t *testing.T, out string) {
	t.Helper()
	for _, name := range tree(t, out) {
		data, err := os.ReadFile(filepath.Join(out, filepath.FromSlash(name)))
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(strings.ToLower(string(data)), strings.ToLower(app.StageMarker)) {
			t.Errorf("%s = %q, names a staging tree", name, data)
		}
	}
}

// TestStageWritesTheRealHomeInEachForm checks that the home directory
// rulesync ran with becomes the real one in each form rulesync can write
// it: with forward slashes, with the OS's separators, escaped in a JSON
// string, and, on Windows, in any case, for a home directory with spaces
// or letters outside ASCII too.
func TestStageWritesTheRealHomeInEachForm(t *testing.T) {
	files := map[string]string{
		"user/.gemini/antigravity-cli/settings.json": `{"permissions":{"deny":["read_file(<home>/.ssh)","read_file(<home-json>\\.aws)","read_file(<home>)"]}}`,
		"user/.gemini/notes.md":                      "<home-native>\n<home-native>\\notes and <home>/x.\n",
	}
	if runtime.GOOS == "windows" {
		files["user/.gemini/upper.md"] = "<home-upper>/y \"<home-upper>\"\n"
	}
	for _, base := range []string{"home", "Ana Lopez", "Jürgen Ñandú 日本"} {
		t.Run(base, func(t *testing.T) {
			out := filepath.Join(t.TempDir(), "staging")
			home := filepath.Join(t.TempDir(), base)
			f := &fakeRulesync{t: t, tools: app.ToolsIn(home, "project"), files: files}
			if _, err := f.stage(agyPack(t), out, app.Selection{}, false); err != nil {
				t.Fatalf("Stage() = %v", err)
			}
			for name, data := range files {
				want := withRealHome(t, data, home, strings.HasSuffix(name, ".json"))
				if got, err := os.ReadFile(filepath.Join(out, filepath.FromSlash(name))); err != nil || string(got) != want {
					t.Errorf("%s = %q, %v, want %q", name, got, err, want)
				}
			}
			noStagingPath(t, out)
		})
	}
}

// TestStageFailsOnAStagingPathItCannotRewrite checks that a file rulesync
// wrote that still names the staging tree after the rewrite fails the
// stage, naming the file, rather than installing a path into a tree that
// is gone.
func TestStageFailsOnAStagingPathItCannotRewrite(t *testing.T) {
	cases := map[string]string{
		"user/.gemini/antigravity-cli/settings.json": `{"permissions":{"deny":["read_file(<home-escaped-slash>\/.ssh)"]}}`,
		"user/.gemini/notes.md":                      "<home>2/notes\n",
		"user/.gemini/data.bin":                      "\x00<home>/.ssh\n",
	}
	if runtime.GOOS == "windows" {
		cases["user/.gemini/escaped.md"] = `<home-json>\\notes`
	}
	for name, data := range cases {
		t.Run(name, func(t *testing.T) {
			out := filepath.Join(t.TempDir(), "staging")
			f := &fakeRulesync{t: t, tools: app.ToolsIn(filepath.Join(t.TempDir(), "home"), "project"), files: map[string]string{name: data}}
			_, err := f.stage(agyPack(t), out, app.Selection{}, false)
			if err == nil || !strings.Contains(err.Error(), name) || !strings.Contains(err.Error(), "staging tree") {
				t.Fatalf("Stage() error = %v, want one that names %s and the staging tree", err, name)
			}
			if _, err := os.Stat(out); !errors.Is(err, os.ErrNotExist) {
				t.Errorf("out was written: %v", err)
			}
		})
	}
}

// unsafeHome is a home directory that a command cannot hold in double
// quotes: with $ and %, and, where a file name can hold them, " and \.
func unsafeHome(t *testing.T) string {
	if runtime.GOOS == "windows" {
		return filepath.Join(t.TempDir(), "a$b%c")
	}
	return filepath.Join(t.TempDir(), `a$b%c"d\e`)
}

// TestStageWritesAHomeACommandCannotHoldIntoData checks that a home
// directory a command cannot hold still becomes the real one in data, a
// JSON permission target or markdown, escaped as a JSON string where it
// lands in one.
func TestStageWritesAHomeACommandCannotHoldIntoData(t *testing.T) {
	out := filepath.Join(t.TempDir(), "staging")
	home := unsafeHome(t)
	files := map[string]string{
		"user/.gemini/antigravity-cli/settings.json": `{"permissions":{"deny":["read_file(<home>/.ssh)","read_file(<home-json>\\.aws)"]}}`,
		"user/.gemini/notes.md":                      "# <home>/notes\n",
	}
	f := &fakeRulesync{t: t, tools: app.ToolsIn(home, "project"), files: files}
	if _, err := f.stage(agyPack(t), out, app.Selection{}, false); err != nil {
		t.Fatalf("Stage() = %v", err)
	}
	for name, data := range files {
		want := withRealHome(t, data, home, strings.HasSuffix(name, ".json"))
		if got, err := os.ReadFile(filepath.Join(out, filepath.FromSlash(name))); err != nil || string(got) != want {
			t.Errorf("%s = %q, %v, want %q", name, got, err, want)
		}
	}
	settings, err := os.ReadFile(filepath.Join(out, "user", ".gemini", "antigravity-cli", "settings.json"))
	if err != nil || !json.Valid(settings) {
		t.Errorf("agy settings = %q, %v, want valid JSON", settings, err)
	}
}

// TestStageFailsWhenAHomeACommandCannotHoldLandsInACommand checks that a
// home directory a command cannot hold fails the stage where it would land
// in a hook command, or in a file whose commands dotai cannot tell apart,
// with an error that names the file and the character, not ${DOTAI_HOME}.
func TestStageFailsWhenAHomeACommandCannotHoldLandsInACommand(t *testing.T) {
	for name, data := range map[string]string{
		"user/.claude/settings.json": `{"hooks":{"Stop":[{"hooks":[{"type":"command","command":"cat <home>/x"}]}]}}`,
		"user/.config/plugin.js":     `const notes = "<home>/notes";`,
	} {
		t.Run(name, func(t *testing.T) {
			out := filepath.Join(t.TempDir(), "staging")
			f := &fakeRulesync{t: t, tools: app.ToolsIn(unsafeHome(t), "project"), files: map[string]string{name: data}}
			_, err := f.stage(agyPack(t), out, app.Selection{}, false)
			if err == nil || !strings.Contains(err.Error(), name) || !strings.Contains(err.Error(), `holds '$'`) || strings.Contains(err.Error(), "DOTAI_HOME") {
				t.Fatalf("Stage() error = %v, want one that names %s and '$', not ${DOTAI_HOME}", err, name)
			}
			if _, err := os.Stat(out); !errors.Is(err, os.ErrNotExist) {
				t.Errorf("out was written: %v", err)
			}
		})
	}
}

// modeFS checks that a file is rewritten with the mode it has.
type modeFS struct {
	app.OSStageFS
	t *testing.T
}

func (m modeFS) WriteFile(dir, name string, data []byte, perm os.FileMode) error {
	if fi, err := os.Stat(filepath.Join(dir, filepath.FromSlash(name))); err == nil && fi.Mode().Perm() != perm {
		m.t.Errorf("%s rewritten with mode %v, want its mode %v", name, perm, fi.Mode().Perm())
	}
	return m.OSStageFS.WriteFile(dir, name, data, perm)
}

// TestStageKeepsTheModeOfAFileItRewrites checks that a file rulesync wrote
// keeps its mode, an executable one included, when its home directory
// becomes the real one.
func TestStageKeepsTheModeOfAFileItRewrites(t *testing.T) {
	out := filepath.Join(t.TempDir(), "staging")
	name := "user/.gemini/hooks/run.sh"
	f := &fakeRulesync{t: t, tools: app.ToolsIn(filepath.Join(t.TempDir(), "home"), "project"),
		files: map[string]string{name: "cat \"<home>/x\"\n"}, modes: map[string]os.FileMode{name: 0o755}}
	if _, err := f.stageWith(modeFS{t: t}, agyPack(t), out, app.Selection{}, false); err != nil {
		t.Fatalf("Stage() = %v", err)
	}
	if runtime.GOOS == "windows" {
		t.Skip("Windows has no executable bit")
	}
	if fi, err := os.Stat(filepath.Join(out, filepath.FromSlash(name))); err != nil || fi.Mode().Perm() != 0o755 {
		t.Errorf("%s mode = %v, %v, want 0755", name, fi.Mode().Perm(), err)
	}
}

// TestStageKeepsTheBytesOfTheFilesDotaiWrote checks that a file dotai
// staged is not rewritten, nor checked, after rulesync: only the files
// rulesync wrote are.
func TestStageKeepsTheBytesOfTheFilesDotaiWrote(t *testing.T) {
	out := filepath.Join(t.TempDir(), "staging")
	home := filepath.Join(t.TempDir(), "home")
	name := "user/.rulesync/rules/r.md"
	// rulesync never writes a staged file; this one stands in for a staged
	// file that names the home directory rulesync ran with.
	f := &fakeRulesync{t: t, tools: app.ToolsIn(home, "project"), files: map[string]string{name: "# <home>/notes\n"}}
	if _, err := f.stage(agyPack(t), out, app.Selection{}, false); err != nil {
		t.Fatalf("Stage() = %v", err)
	}
	got, err := os.ReadFile(filepath.Join(out, filepath.FromSlash(name)))
	if err != nil || !strings.Contains(string(got), app.StageMarker) || strings.Contains(string(got), filepath.ToSlash(home)) {
		t.Errorf("%s = %q, %v, want the bytes the fake wrote, with the staging tree", name, got, err)
	}
}
