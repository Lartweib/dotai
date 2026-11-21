package app_test

import (
	"cmp"
	"context"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/lartweib/dotai/internal/app"
	"github.com/lartweib/dotai/internal/pack"
)

// TestInstallClaudeMCPServersWithRealRulesync installs a copy of the
// example pack with three stdio MCP servers through the pinned rulesync,
// which renders Claude Code's user-scope servers as .claude.json at the
// root of the staging tree (decision 0049): isolated, they land in the
// isolated .claude.json; in replace mode, in ~/.claude.json, whose login
// and other keys stay, and a restore gives that file back byte for byte.
// It needs the network, so it runs only with DOTAI_E2E=1.
func TestInstallClaudeMCPServersWithRealRulesync(t *testing.T) {
	if os.Getenv("DOTAI_E2E") != "1" {
		t.Skip("set DOTAI_E2E=1 to run rulesync")
	}
	cache, err := filepath.Abs(cmp.Or(os.Getenv("DOTAI_CACHE_DIR"), t.TempDir()))
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(t.TempDir(), "pack")
	if err := os.CopyFS(dir, os.DirFS(examplePack)); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(dir, "base", ".rulesync", "mcp.jsonc"), `{
  "mcpServers": {
    "engram": {"type": "stdio", "command": "engram", "args": ["mcp"]},
    "otea": {"type": "stdio", "command": "otea", "args": ["mcp"]},
    "codegraph": {"type": "stdio", "command": "codegraph", "args": ["serve"]}
  }
}
`)
	want := []string{"codegraph", "engram", "otea"}

	t.Run("isolated", func(t *testing.T) {
		home, _ := installHome(t)
		if _, err := app.InstallWithRulesync(context.Background(), cache, dir, home, app.InstallOptions{Harnesses: pack.Harnesses}); err != nil {
			t.Fatalf("Install() error = %v", err)
		}
		if got := servers(t, at(home, isolatedClaudeJSON)); !reflect.DeepEqual(got, want) {
			t.Errorf("isolated .claude.json servers = %v, want %v", got, want)
		}
		if _, err := os.Lstat(at(home, ".claude.json")); !os.IsNotExist(err) {
			t.Errorf("~/.claude.json = %v, want it untouched", err)
		}
	})

	t.Run("replace", func(t *testing.T) {
		home, _ := installHome(t)
		original := "{\n  \"oauthAccount\": {\"emailAddress\": \"a@b.c\"},\n  \"mcpServers\": {\"mine\": {\"command\": \"mine\"}},\n  \"numStartups\": 3\n}\n"
		writeFile(t, at(home, ".claude.json"), original)
		opts := app.InstallOptions{Harnesses: pack.Harnesses, Mode: app.ModeReplace}
		if _, err := app.InstallWithRulesync(context.Background(), cache, dir, home, opts); err != nil {
			t.Fatalf("Install() error = %v", err)
		}
		got := settingsOf(t, at(home, ".claude.json"))
		if got["numStartups"] != 3.0 || got["oauthAccount"] == nil || !reflect.DeepEqual(servers(t, at(home, ".claude.json")), want) {
			t.Errorf("~/.claude.json = %v, want its keys kept and the pack's servers only", got)
		}
		if _, err := restore(newClockFS(t), home, "", nil); err != nil {
			t.Fatalf("Restore() error = %v", err)
		}
		if data, _ := os.ReadFile(at(home, ".claude.json")); string(data) != original {
			t.Errorf("~/.claude.json after the restore = %q, want %q", data, original)
		}
	})
}
