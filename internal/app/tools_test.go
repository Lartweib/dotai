package app_test

import (
	"cmp"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/lartweib/dotai/internal/app"
)

func TestExpandPaths(t *testing.T) {
	home, project := filepath.Join(t.TempDir(), "home"), filepath.Join(t.TempDir(), "project")
	tools := app.ToolsIn(home, project)
	user, proj := filepath.ToSlash(filepath.Join(home, ".dotai", "tools")), filepath.ToSlash(filepath.Join(project, ".dotai", "tools"))
	for _, tt := range []struct{ name, in, want string }{
		{"both tokens", `{"command": "${DOTAI_TOOLS}/hello.sh ${DOTAI_PROJECT_TOOLS}/lint"}`, `{"command": "` + user + `/hello.sh ` + proj + `/lint"}`},
		{"other variables", "${OTHER} $DOTAI_TOOLS ${DOTAI_TOOLS ${dotai_tools}", "${OTHER} $DOTAI_TOOLS ${DOTAI_TOOLS ${dotai_tools}"},
		{"not UTF-8", "\xff${DOTAI_TOOLS}", "\xff${DOTAI_TOOLS}"},
		{"NUL byte", "\x00${DOTAI_TOOLS}", "\x00${DOTAI_TOOLS}"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got, err := app.ExpandPaths([]byte(tt.in), tools); err != nil || string(got) != tt.want {
				t.Errorf("ExpandPaths(%q) = %q, %v, want %q", tt.in, got, err, tt.want)
			}
		})
	}
	if got, _ := app.ExpandPaths([]byte("${DOTAI_TOOLS}"), tools); strings.Contains(string(got), `\`) || !filepath.IsAbs(filepath.FromSlash(string(got))) {
		t.Errorf("expanded to %q, want an absolute path with forward slashes", got)
	}
}

func TestExpandPathsUnknownDirectory(t *testing.T) {
	errHome, errWd := errors.New("$HOME is not defined"), errors.New("getwd failed")
	tools := app.ToolPaths{UserErr: errHome, ProjectErr: errWd}
	for _, in := range []string{"no tokens", "\xff${DOTAI_TOOLS}"} {
		if got, err := app.ExpandPaths([]byte(in), tools); err != nil || string(got) != in {
			t.Errorf("ExpandPaths(%q) = %q, %v, want it unchanged", in, got, err)
		}
	}
	for token, cause := range map[string]error{"${DOTAI_TOOLS}": errHome, "${DOTAI_PROJECT_TOOLS}": errWd} {
		_, err := app.ExpandPaths([]byte("run "+token+"/x"), tools)
		if !errors.Is(err, cause) || !strings.Contains(err.Error(), token) {
			t.Errorf("ExpandPaths(%s) error = %v, want one that names the token and %v", token, err, cause)
		}
	}
}

func TestStageFailsOnlyWhenATokenNeedsAnUnknownDirectory(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "dotai.yaml"), "schema: dotai.pack/v1\nname: p\nversion: 1.0.0\nengine: '>=0.1.0'\nrulesync: 41.0.0\nharnesses: [opencode]\n")
	rule := filepath.Join(dir, "base", ".rulesync", "rules", "r.md")
	writeFile(t, rule, "No tokens.\n")
	f := quiet(t)
	f.tools = app.ToolPaths{UserErr: errors.New("$HOME is not defined")}
	if res, err := f.stage(dir, filepath.Join(t.TempDir(), "s"), app.Selection{}, false); err != nil || len(res.Diagnostics) > 0 {
		t.Fatalf("Stage() = %+v, %v, want a staging without tokens to need no home", res, err)
	}
	writeFile(t, rule, "Run ${DOTAI_TOOLS}/x.\n")
	out := filepath.Join(t.TempDir(), "s")
	_, err := f.stage(dir, out, app.Selection{}, false)
	if err == nil || !strings.Contains(err.Error(), "user/.rulesync/rules/r.md") || !strings.Contains(err.Error(), "${DOTAI_TOOLS}") || !strings.Contains(err.Error(), "$HOME is not defined") {
		t.Errorf("Stage() error = %v, want one that names the file, the token and the cause", err)
	}
	if _, err := os.Stat(out); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("out exists after a failed expansion: %v", err)
	}
}

func TestStageInstallsToolsAndExpandsPaths(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "dotai.yaml"), "schema: dotai.pack/v1\nname: p\nversion: 1.0.0\nengine: '>=0.1.0'\nrulesync: 41.0.0\nharnesses: [opencode]\nprofiles:\n  p:\n    path: profiles/p\n    description: P.\n")
	writeFile(t, filepath.Join(dir, "base", ".rulesync", "rules", "r.md"), "Run ${DOTAI_TOOLS}/hello.sh.\n")
	writeFile(t, filepath.Join(dir, "base", "dotai", "tools", "logo.bin"), "\xff${DOTAI_TOOLS}")
	writeFile(t, filepath.Join(dir, "profiles", "p", "dotai", "tools", "lint.sh"), "echo ${DOTAI_PROJECT_TOOLS}\n")
	hello := filepath.Join(dir, "base", "dotai", "tools", "hello.sh")
	writeFile(t, hello, "echo hello\n")
	if err := os.Chmod(hello, 0o755); err != nil {
		t.Fatal(err)
	}
	f := quiet(t)
	f.tools = app.ToolsIn(filepath.Join(dir, "home"), filepath.Join(dir, "project"))
	out := filepath.Join(t.TempDir(), "s")
	res, err := f.stage(dir, out, app.Selection{Profiles: []string{"p"}}, false)
	if err != nil || len(res.Diagnostics) > 0 {
		t.Fatalf("Stage() = %+v, %v", res, err)
	}
	for _, want := range []struct{ scope, path, layer, data string }{
		{app.ScopeUser, ".dotai/tools/hello.sh", "base", "echo hello\n"},
		{app.ScopeUser, ".dotai/tools/logo.bin", "base", "\xff${DOTAI_TOOLS}"},
		{app.ScopeUser, ".rulesync/rules/r.md", "base", "Run " + filepath.ToSlash(f.tools.User) + "/hello.sh.\n"},
		{app.ScopeProject, ".dotai/tools/lint.sh", "profiles/p", "echo " + filepath.ToSlash(f.tools.Project) + "\n"},
	} {
		if !slices.ContainsFunc(res.Files, func(s app.StagedFile) bool {
			return reflect.DeepEqual(s, app.StagedFile{Scope: want.scope, Path: want.path, Layer: want.layer})
		}) {
			t.Errorf("files = %+v, want %s/%s from %s", res.Files, want.scope, want.path, want.layer)
		}
		if data, err := os.ReadFile(filepath.Join(out, want.scope, filepath.FromSlash(want.path))); err != nil || string(data) != want.data {
			t.Errorf("%s/%s = %q, %v, want %q", want.scope, want.path, data, err, want.data)
		}
	}
	if runtime.GOOS == "windows" {
		return // no execute bits
	}
	for name, exec := range map[string]bool{"user/.dotai/tools/hello.sh": true, "user/.dotai/tools/logo.bin": false, "project/.dotai/tools/lint.sh": false} {
		info, err := os.Stat(filepath.Join(out, filepath.FromSlash(name)))
		if err != nil || (info.Mode().Perm()&0o111 != 0) != exec {
			t.Errorf("%s mode = %v, %v, want executable %v", name, info, err, exec)
		}
	}
}

// TestToolCommands checks the tool command variable of decision 0037 for
// each harness on each OS: PowerShell runs the .ps1 on Windows and sh the
// .sh elsewhere, from the tools of the settings' scope. For a harness that
// runs it through a shell, the absolute path is in double quotes, so that
// spaces work, and a directory with a character the quotes do not protect
// is refused; for agy, which splits the command itself, the path is bare,
// and one with whitespace or a quote is refused. ${DOTAI_TOOLS} still
// expands beside it.
func TestToolCommands(t *testing.T) {
	tests := []struct {
		name, os, user, project, scope, in, want, err string
		harness                                       string // claudecode when empty
	}{
		{"windows", "windows", `C:\Users\Ana\.dotai\tools`, "", app.ScopeUser, "${DOTAI_TOOL:hello}",
			`powershell.exe -NoProfile -ExecutionPolicy Bypass -File "C:\Users\Ana\.dotai\tools\hello.ps1"`, "", ""},
		{"windows with spaces", "windows", `C:\Users\Ana Maria\.dotai\tools`, "", app.ScopeUser, "${DOTAI_TOOL:hello} --short",
			`powershell.exe -NoProfile -ExecutionPolicy Bypass -File "C:\Users\Ana Maria\.dotai\tools\hello.ps1" --short`, "", ""},
		{"linux", "linux", "/home/ana/.dotai/tools", "", app.ScopeUser, "${DOTAI_TOOL:hello}", `sh "/home/ana/.dotai/tools/hello.sh"`, "", ""},
		{"macos with spaces", "darwin", "/Users/Ana Maria/.dotai/tools", "", app.ScopeUser, "${DOTAI_TOOL:hello}", `sh "/Users/Ana Maria/.dotai/tools/hello.sh"`, "", ""},
		{"project scope", "linux", "/home/ana/.dotai/tools", "/work/p/.dotai/tools", app.ScopeProject, "${DOTAI_TOOL:lint}", `sh "/work/p/.dotai/tools/lint.sh"`, "", ""},
		{"beside the path variable", "linux", "/home/ana/.dotai/tools", "", app.ScopeUser, "${DOTAI_TOOL:a} ${DOTAI_TOOLS}/b.conf",
			`sh "/home/ana/.dotai/tools/a.sh" /home/ana/.dotai/tools/b.conf`, "", ""},
		{"a dollar on linux", "linux", "/home/$ana/.dotai/tools", "", app.ScopeUser, "${DOTAI_TOOL:hello}", "", `holds '$'`, ""},
		{"a backslash on linux", "linux", `/home/a\b/.dotai/tools`, "", app.ScopeUser, "${DOTAI_TOOL:hello}", "", `holds '\\'`, ""},
		{"a percent on windows", "windows", `C:\Users\100%\.dotai\tools`, "", app.ScopeUser, "${DOTAI_TOOL:hello}", "", `holds '%'`, ""},
		{"not a tool name", "linux", "/home/ana/.dotai/tools", "", app.ScopeUser, "${DOTAI_TOOL:../x}", "", "does not name a tool", ""},
		{"no closing brace", "linux", "/home/ana/.dotai/tools", "", app.ScopeUser, "${DOTAI_TOOL:hello --short", "", "has no closing brace", ""},
		{"a network path on windows", "windows", `\\server\share\ana\.dotai\tools`, "", app.ScopeUser, "${DOTAI_TOOL:hello}", "", `starts with \\ or //`, ""},
		{"a network path with slashes on windows", "windows", `//server/share/ana/.dotai/tools`, "", app.ScopeUser, "${DOTAI_TOOL:hello}", "", `starts with \\ or //`, ""},
		{"a long local path on windows", "windows", `\\?\C:\Users\Ana\.dotai\tools`, "", app.ScopeUser, "${DOTAI_TOOL:hello}", "", `starts with \\ or //`, ""},
		{"a path from the root on linux", "linux", "//home/ana/.dotai/tools", "", app.ScopeUser, "${DOTAI_TOOL:hello}", `sh "//home/ana/.dotai/tools/hello.sh"`, "", ""},
		{"opencode, as a shell reads it", "linux", "/home/ana maria/.dotai/tools", "", app.ScopeUser, "${DOTAI_TOOL:hello}", `sh "/home/ana maria/.dotai/tools/hello.sh"`, "", "opencode"},

		// agy splits the command itself and passes quotes on: the path is
		// bare, and one with whitespace or a quote is refused.
		{"agy on windows", "windows", `C:\Users\Ana\.dotai\tools`, "", app.ScopeUser, "${DOTAI_TOOL:hello}",
			`powershell.exe -NoProfile -ExecutionPolicy Bypass -File C:\Users\Ana\.dotai\tools\hello.ps1`, "", "antigravity-cli"},
		{"agy on linux", "linux", "/home/ana/.dotai/tools", "", app.ScopeUser, "${DOTAI_TOOL:hello} --short", `sh /home/ana/.dotai/tools/hello.sh --short`, "", "antigravity-cli"},
		{"agy on macos", "darwin", "/Users/ana/.dotai/tools", "", app.ScopeUser, "${DOTAI_TOOL:hello}", `sh /Users/ana/.dotai/tools/hello.sh`, "", "antigravity-cli"},
		{"agy on windows with spaces", "windows", `C:\Users\Ana Maria\.dotai\tools`, "", app.ScopeUser, "${DOTAI_TOOL:hello}", "",
			`cannot be expanded for antigravity-cli: the tools directory C:\Users\Ana Maria\.dotai\tools holds ' '`, "antigravity-cli"},
		{"agy on macos with spaces", "darwin", "/Users/Ana Maria/.dotai/tools", "", app.ScopeUser, "${DOTAI_TOOL:hello}", "", "takes the path bare", "antigravity-cli"},
		{"agy with an ampersand", "windows", `C:\Users\A&B\.dotai\tools`, "", app.ScopeUser, "${DOTAI_TOOL:hello}", "", `holds '&'`, "antigravity-cli"},
		{"agy with an exclamation mark", "linux", "/home/hi!/.dotai/tools", "", app.ScopeUser, "${DOTAI_TOOL:hello}", "", `holds '!'`, "antigravity-cli"},
		{"claudecode with an ampersand, quoted", "linux", "/home/a&b/.dotai/tools", "", app.ScopeUser, "${DOTAI_TOOL:hello}", `sh "/home/a&b/.dotai/tools/hello.sh"`, "", ""},
		{"agy with a tab", "linux", "/home/ana\tm/.dotai/tools", "", app.ScopeUser, "${DOTAI_TOOL:hello}", "", `holds '\t'`, "antigravity-cli"},
		{"agy with a quote", "linux", "/home/o'neil/.dotai/tools", "", app.ScopeUser, "${DOTAI_TOOL:hello}", "", `holds '\''`, "antigravity-cli"},
		{"agy with a network path", "windows", `\\server\share\ana\.dotai\tools`, "", app.ScopeUser, "${DOTAI_TOOL:hello}", "", `starts with \\ or //`, "antigravity-cli"},
		{"agy with a percent", "windows", `C:\Users\100%\.dotai\tools`, "", app.ScopeUser, "${DOTAI_TOOL:hello}", "", `holds '%'`, "antigravity-cli"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tools := app.ToolPaths{User: tt.user, Project: tt.project, OS: tt.os}
			got, err := app.ExpandSettingIn(tt.in, tools, tt.scope, cmp.Or(tt.harness, "claudecode"))
			switch {
			case tt.err != "" && (err == nil || !strings.Contains(err.Error(), tt.err)):
				t.Errorf("ExpandSettingIn(%q) error = %v, want one with %q", tt.in, err, tt.err)
			case tt.err == "" && (err != nil || got != tt.want):
				t.Errorf("ExpandSettingIn(%q) = %q, %v, want %q", tt.in, got, err, tt.want)
			}
		})
	}
	cause := errors.New("$HOME is not defined")
	if _, err := app.ExpandSettingIn("${DOTAI_TOOL:hello}", app.ToolPaths{UserErr: cause}, app.ScopeUser, "claudecode"); !errors.Is(err, cause) {
		t.Errorf("ExpandSettingIn() without a home error = %v, want %v", err, cause)
	}
}

// TestInstallRunsAToolCommand checks the example pack's agy statusLine
// (decision 0037): the installed settings run the script for this OS by
// its absolute path, bare, since agy splits the command itself, and the
// dry run shows the same command.
func TestInstallRunsAToolCommand(t *testing.T) {
	home, _ := installHome(t)
	tools := filepath.Join(home, ".dotai", "tools")
	if strings.ContainsAny(tools, " \t") {
		t.Skipf("the temporary directory %s has a space, which agy cannot take", tools)
	}
	want := `sh ` + filepath.ToSlash(tools) + `/hello.sh`
	if runtime.GOOS == "windows" {
		want = `powershell.exe -NoProfile -ExecutionPolicy Bypass -File ` + tools + `\hello.ps1`
	}
	p, err := quiet(t).planInstall(examplePack, "", home)
	if err != nil {
		t.Fatal(err)
	}
	wantTool := []app.ToolCommand{{Scope: app.ScopeUser, Harness: "antigravity-cli", Key: "statusLine", Tool: "hello", Command: want}}
	for _, h := range p.Harnesses {
		if h.ID == "antigravity-cli" && !reflect.DeepEqual(h.Tools, wantTool) || h.ID != "antigravity-cli" && len(h.Tools) != 0 {
			t.Errorf("%s tools in the dry run = %+v, want agy's statusLine only: %+v", h.ID, h.Tools, wantTool)
		}
	}
	if _, err := quiet(t).install(newClockFS(t), examplePack, home); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(at(home, ".dotai/isolated/gemini/antigravity-cli/settings.json"))
	if err != nil {
		t.Fatal(err)
	}
	var settings struct {
		StatusLine struct{ Command string } `json:"statusLine"`
	}
	if err := json.Unmarshal(data, &settings); err != nil {
		t.Fatal(err)
	}
	if settings.StatusLine.Command != want {
		t.Errorf("agy's statusLine command = %q, want %q", settings.StatusLine.Command, want)
	}
	for _, script := range []string{"hello.sh", "hello.ps1"} {
		if _, err := os.Stat(filepath.Join(tools, script)); err != nil {
			t.Errorf("%s: %v, want it installed", script, err)
		}
	}
}

// TestAgyRefusesAToolCommandFromAHomeWithSpaces checks that the example
// pack's agy statusLine fails the dry run and the install in a home
// directory with a space, naming agy and the tools directory, before
// anything is written: agy would split the command inside the path.
func TestAgyRefusesAToolCommandFromAHomeWithSpaces(t *testing.T) {
	home, _ := installHome(t)
	home = filepath.Join(home, "Ana Maria")
	want := "cannot be expanded for antigravity-cli: the tools directory " + filepath.Join(home, ".dotai", "tools") + " holds ' '"
	if _, err := quiet(t).planInstall(examplePack, "", home); err == nil || !strings.Contains(err.Error(), want) {
		t.Errorf("PlanInstall() error = %v, want one with %q", err, want)
	}
	if _, err := quiet(t).install(newClockFS(t), examplePack, home); err == nil || !strings.Contains(err.Error(), want) {
		t.Errorf("Install() error = %v, want one with %q", err, want)
	}
	if _, err := os.Lstat(at(home, ".dotai")); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("~/.dotai = %v, want nothing written", err)
	}
}

// TestAToolCommandWithoutItsScriptWritesNothing checks that a pack whose
// settings run a tool it ships for one OS only fails the dry run and the
// install with a diagnostic naming the missing script, before anything is
// written.
func TestAToolCommandWithoutItsScriptWritesNothing(t *testing.T) {
	home, _ := installHome(t)
	dir := copyPack(t)
	if err := os.Remove(filepath.Join(dir, "base", "dotai", "tools", "hello.ps1")); err != nil {
		t.Fatal(err)
	}
	want := "base/dotai/settings.yaml:6:41: tool-missing: setting antigravity-cli.statusLine runs tool hello, but no layer of its scope provides dotai/tools/hello.ps1"
	p, err := quiet(t).planInstall(dir, "", home)
	if err != nil || len(p.Diagnostics) != 1 || !strings.HasPrefix(p.Diagnostics[0].String(), want) {
		t.Errorf("PlanInstall() = %+v, %v, want %q", p.Diagnostics, err, want)
	}
	res, err := quiet(t).install(newClockFS(t), dir, home)
	if err != nil || len(res.Diagnostics) != 1 || res.Diagnostics[0].Code != "tool-missing" {
		t.Errorf("Install() = %+v, %v, want the diagnostic", res.Diagnostics, err)
	}
	if _, err := os.Lstat(at(home, ".dotai")); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("~/.dotai = %v, want nothing written", err)
	}
}
