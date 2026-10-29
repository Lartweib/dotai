package app_test

import (
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
			if got := string(app.ExpandPaths([]byte(tt.in), tools)); got != tt.want {
				t.Errorf("ExpandPaths(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
	if got := string(app.ExpandPaths([]byte("${DOTAI_TOOLS}"), tools)); strings.Contains(got, `\`) || !filepath.IsAbs(filepath.FromSlash(got)) {
		t.Errorf("expanded to %q, want an absolute path with forward slashes", got)
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
	info, err := os.Stat(filepath.Join(out, "user", ".dotai", "tools", "hello.sh"))
	if err == nil && runtime.GOOS != "windows" && info.Mode().Perm()&0o111 == 0 {
		t.Errorf("hello.sh mode = %v, want it executable", info.Mode())
	}
}
