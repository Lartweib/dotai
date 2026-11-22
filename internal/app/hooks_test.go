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

// TestHookToolCommands checks the tool command variable in a hook command
// (decision 0037) for each block and harness. A hook of a tool-scoped
// block is written as its harness reads a command: Claude Code and
// OpenCode run it through a shell, so the path is in double quotes; agy
// runs it through cmd /c, which gets every quote as \", so the path is
// bare. The shared hooks reach every harness the stage renders, so they
// are bare as soon as agy is one of them, and then with forward slashes,
// which bash, Bun Shell and cmd all take. A path that cannot be bare is
// a HookPathError naming the hook and the harness.
func TestHookToolCommands(t *testing.T) {
	all := []string{"claudecode", "antigravity-cli", "opencode"}
	tests := []struct {
		name, os, dir, block string
		harnesses            []string
		want                 string
		unquotable           string // the harness of a HookPathError
		err                  string // another error
	}{
		{name: "shared, shells only, windows with spaces", os: "windows", dir: `C:\Users\Ana Maria\.dotai\tools`, harnesses: []string{"claudecode", "opencode"},
			want: `powershell.exe -NoProfile -ExecutionPolicy Bypass -File "C:\Users\Ana Maria\.dotai\tools\hello.ps1" --x`},
		{name: "shared, shells only, linux", os: "linux", dir: "/home/ana/.dotai/tools", harnesses: []string{"opencode"},
			want: `sh "/home/ana/.dotai/tools/hello.sh" --x`},
		{name: "shared with agy, windows", os: "windows", dir: `C:\Users\Ana\.dotai\tools`, harnesses: all,
			want: `powershell.exe -NoProfile -ExecutionPolicy Bypass -File C:/Users/Ana/.dotai/tools/hello.ps1 --x`},
		{name: "shared, agy alone, macos", os: "darwin", dir: "/Users/ana/.dotai/tools", harnesses: []string{"antigravity-cli"},
			want: `sh /Users/ana/.dotai/tools/hello.sh --x`},
		{name: "shared with agy, windows with spaces", os: "windows", dir: `C:\Users\Ana Maria\.dotai\tools`, harnesses: all, unquotable: "antigravity-cli"},
		{name: "shared with agy, linux with parentheses", os: "linux", dir: "/home/a(b)/.dotai/tools", harnesses: all, unquotable: "antigravity-cli"},
		{name: "claudecode block, linux with spaces", os: "linux", dir: "/home/ana maria/.dotai/tools", block: "claudecode", harnesses: all,
			want: `sh "/home/ana maria/.dotai/tools/hello.sh" --x`},
		{name: "opencode block, windows", os: "windows", dir: `C:\Users\Ana\.dotai\tools`, block: "opencode", harnesses: all,
			want: `powershell.exe -NoProfile -ExecutionPolicy Bypass -File "C:\Users\Ana\.dotai\tools\hello.ps1" --x`},
		{name: "agy block, windows", os: "windows", dir: `C:\Users\Ana\.dotai\tools`, block: "antigravity-cli", harnesses: all,
			want: `powershell.exe -NoProfile -ExecutionPolicy Bypass -File C:/Users/Ana/.dotai/tools/hello.ps1 --x`},
		{name: "agy block, a quote", os: "linux", dir: `/home/o"neil/.dotai/tools`, block: "antigravity-cli", harnesses: all, unquotable: "antigravity-cli"},
		{name: "agy block, an exclamation mark", os: "windows", dir: `C:\Users\Hi!\.dotai\tools`, block: "antigravity-cli", harnesses: all, unquotable: "antigravity-cli"},
		{name: "shared, no harness rendered", os: "linux", dir: "/home/ana/.dotai/tools", harnesses: nil, want: `${DOTAI_TOOL:hello} --x`},
		{name: "agy block, an ampersand", os: "windows", dir: `C:\Users\A&B\.dotai\tools`, block: "antigravity-cli", harnesses: all, unquotable: "antigravity-cli"},
		{name: "agy block, spaces, agy not rendered", os: "linux", dir: "/home/ana maria/.dotai/tools", block: "antigravity-cli", harnesses: []string{"claudecode"},
			want: `${DOTAI_TOOL:hello} --x`},
		{name: "a block of no harness", os: "linux", dir: "/home/ana/.dotai/tools", block: "cursor", harnesses: all, want: `${DOTAI_TOOL:hello} --x`},
		{name: "quoted, a dollar", os: "linux", dir: "/home/$ana/.dotai/tools", block: "claudecode", harnesses: all, err: `holds '$'`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := app.ExpandHooksIn(hookFile(t, tt.block, "${DOTAI_TOOL:hello} --x"), app.ToolPaths{User: tt.dir, OS: tt.os}, app.ScopeUser, tt.harnesses)
			var hp *app.HookPathError
			switch {
			case tt.unquotable != "":
				if !errors.As(err, &hp) || hp.Harness != tt.unquotable || hp.Hook != "hook "+strings.TrimPrefix(tt.block+".hooks.stop", ".") {
					t.Fatalf("ExpandHooksIn() error = %#v, want a HookPathError for %s", err, tt.unquotable)
				}
				if msg := err.Error(); !strings.Contains(msg, tt.dir) || !strings.Contains(msg, tt.unquotable) {
					t.Errorf("error = %q, want it to name the directory and the harness", msg)
				}
			case tt.err != "":
				if err == nil || errors.As(err, &hp) || !strings.Contains(err.Error(), tt.err) {
					t.Errorf("ExpandHooksIn() error = %v, want one with %q", err, tt.err)
				}
			case err != nil:
				t.Fatalf("ExpandHooksIn() error = %v", err)
			default:
				if c := hookCommand(t, got, tt.block); c != tt.want {
					t.Errorf("command = %q, want %q", c, tt.want)
				}
			}
		})
	}
	got, err := app.ExpandHooksIn(hookFile(t, "", "${DOTAI_TOOL:lint}"), app.ToolPaths{User: "/home/ana/.dotai/tools", Project: "/work/p/.dotai/tools", OS: "linux"}, app.ScopeProject, []string{"claudecode"})
	if c := hookCommand(t, got, ""); err != nil || c != `sh "/work/p/.dotai/tools/lint.sh"` {
		t.Errorf("project command = %q, %v, want the project's tool", c, err)
	}
}

// TestSharedHookSkipsTheHarnessesThatShadowIt checks that the form of a
// shared hook is chosen only over the harnesses that render it: one whose
// own block declares the same event gets that block's hooks instead
// (rulesync 43), so an agy block for sessionStart lets the shared
// sessionStart be quoted for Claude Code and OpenCode, from a tools
// directory with a space, and a shared hook that no rendered harness gets
// is left as written.
func TestSharedHookSkipsTheHarnessesThatShadowIt(t *testing.T) {
	data := []byte(`{"hooks": {"sessionStart": [{"command": "${DOTAI_TOOL:hello} --x"}]}, "antigravity-cli": {"hooks": {"sessionStart": [{"command": "echo agy"}]}}}`)
	tools := app.ToolPaths{User: `C:\Users\Ana Maria\.dotai\tools`, OS: "windows"}
	got, err := app.ExpandHooksIn(data, tools, app.ScopeUser, []string{"claudecode", "antigravity-cli", "opencode"})
	if err != nil {
		t.Fatalf("ExpandHooksIn() error = %v, want none: agy does not render the shared sessionStart", err)
	}
	if c, want := hookEvent(t, got, "sessionStart"), `powershell.exe -NoProfile -ExecutionPolicy Bypass -File "C:\Users\Ana Maria\.dotai\tools\hello.ps1" --x`; c != want {
		t.Errorf("shared command = %q, want %q", c, want)
	}
	got, err = app.ExpandHooksIn(data, tools, app.ScopeUser, []string{"antigravity-cli"})
	if c := hookEvent(t, got, "sessionStart"); err != nil || c != "${DOTAI_TOOL:hello} --x" {
		t.Errorf("shared command with agy alone = %q, %v, want it as written", c, err)
	}
}

// hookEvent is the command of the only shared hook of event in data.
func hookEvent(t *testing.T, data []byte, event string) string {
	t.Helper()
	var f struct {
		Hooks map[string][]struct{ Command string } `json:"hooks"`
	}
	if err := json.Unmarshal(data, &f); err != nil || len(f.Hooks[event]) != 1 {
		t.Fatalf("%s: %v", data, err)
	}
	return f.Hooks[event][0].Command
}

// hookFile is a merged hooks file with one stop hook that runs command, in
// block, "" for the shared hooks.
func hookFile(t *testing.T, block, command string) []byte {
	t.Helper()
	var v any = map[string]any{"hooks": map[string]any{"stop": []any{map[string]any{"command": command}}}}
	if block != "" {
		v = map[string]any{block: v}
	}
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

// hookCommand is the command of the stop hook of hookFile.
func hookCommand(t *testing.T, data []byte, block string) string {
	t.Helper()
	var v map[string]json.RawMessage
	if err := json.Unmarshal(data, &v); err != nil {
		t.Fatalf("%s: %v", data, err)
	}
	raw := data
	if block != "" {
		raw = v[block]
	}
	var f struct {
		Hooks struct{ Stop []struct{ Command string } } `json:"hooks"`
	}
	if err := json.Unmarshal(raw, &f); err != nil || len(f.Hooks.Stop) != 1 {
		t.Fatalf("%s: %v", data, err)
	}
	return f.Hooks.Stop[0].Command
}

// TestStageRefusesAnUnquotableHookPath checks that a hook that runs a tool
// for agy from a tools directory with a space fails the stage with a
// hook-path-unquotable diagnostic at the layer's hooks.jsonc, naming the
// hook and agy, and writes nothing.
func TestStageRefusesAnUnquotableHookPath(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "dotai.yaml"), "schema: dotai.pack/v1\nname: p\nversion: 1.0.0\nengine: '>=0.1.0'\nrulesync: 43.12.0\nharnesses: [claudecode, antigravity-cli]\n")
	writeFile(t, filepath.Join(dir, "base", ".rulesync", "hooks.jsonc"), `{"hooks": {"sessionStart": [{"command": "${DOTAI_TOOL:hello}"}]}}`)
	writeFile(t, filepath.Join(dir, "base", "dotai", "tools", "hello.sh"), "echo hello\n")
	writeFile(t, filepath.Join(dir, "base", "dotai", "tools", "hello.ps1"), "'hello'\n")
	f := quiet(t)
	f.tools = app.ToolsIn(filepath.Join(dir, "Ana Maria"), filepath.Join(dir, "project"))
	out := filepath.Join(t.TempDir(), "s")
	res, err := f.stage(dir, out, app.Selection{}, false)
	if err != nil || len(res.Diagnostics) != 1 || len(res.Files) != 0 {
		t.Fatalf("Stage() = %+v, %v, want one diagnostic", res, err)
	}
	d := res.Diagnostics[0]
	if d.Code != app.CodeHookPathUnquotable || d.File != "base/.rulesync/hooks.jsonc" || !strings.Contains(d.Message, "hook hooks.sessionStart") || !strings.Contains(d.Message, "antigravity-cli") || d.Hint == "" {
		t.Errorf("diagnostic = %+v, want hook-path-unquotable at base/.rulesync/hooks.jsonc naming the hook and agy, with a hint", d)
	}
	if _, err := os.Stat(out); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("out exists after a refused hook: %v", err)
	}

	// From a home without a space, the staged hooks run the tool bare.
	f.tools = app.ToolsIn(filepath.Join(dir, "home"), filepath.Join(dir, "project"))
	if strings.ContainsAny(f.tools.User, " \t") {
		t.Skipf("the temporary directory %s has a space, which agy cannot take", f.tools.User)
	}
	if res, err = f.stage(dir, out, app.Selection{}, false); err != nil || len(res.Diagnostics) != 0 {
		t.Fatalf("Stage() = %+v, %v", res, err)
	}
	data, err := os.ReadFile(filepath.Join(out, "user", ".rulesync", "hooks.jsonc"))
	if err != nil {
		t.Fatal(err)
	}
	want := "sh " + filepath.ToSlash(f.tools.User) + "/hello.sh"
	if runtime.GOOS == "windows" {
		want = "powershell.exe -NoProfile -ExecutionPolicy Bypass -File " + filepath.ToSlash(f.tools.User) + "/hello.ps1"
	}
	var hooks struct {
		Hooks struct{ SessionStart []struct{ Command string } } `json:"hooks"`
	}
	if err := json.Unmarshal(data, &hooks); err != nil || len(hooks.Hooks.SessionStart) != 1 || hooks.Hooks.SessionStart[0].Command != want {
		t.Errorf("staged hooks = %s, %v, want the command %q", data, err, want)
	}
}
