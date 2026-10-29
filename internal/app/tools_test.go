package app_test

import (
	"errors"
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
