package rulesync_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/lartweib/dotai/internal/rulesync"
)

func TestArgs(t *testing.T) {
	for _, tt := range []struct {
		global  bool
		targets []string
		want    []string
	}{
		{false, []string{"claudecode"}, []string{"generate", "--targets", "claudecode", "--features", "*"}},
		{true, []string{"claudecode", "opencode"}, []string{"generate", "--targets", "claudecode,opencode", "--features", "*", "--global"}},
	} {
		if got := rulesync.Args(tt.global, tt.targets); !reflect.DeepEqual(got, tt.want) {
			t.Errorf("Args(%v, %v) = %q, want %q", tt.global, tt.targets, got, tt.want)
		}
	}
}

func TestEnv(t *testing.T) {
	home := filepath.Join("staging", "user")
	got := rulesync.Env([]string{"PATH=/bin", "HOME=/real", "UserProfile=C:\\real", "APPDATA=C:\\real\\a"}, home)
	want := []string{
		"PATH=/bin",
		"HOME=" + home,
		"USERPROFILE=" + home,
		"XDG_CONFIG_HOME=" + filepath.Join(home, ".config"),
		"APPDATA=" + filepath.Join(home, "AppData", "Roaming"),
		"LOCALAPPDATA=" + filepath.Join(home, "AppData", "Local"),
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Env() = %q, want %q", got, want)
	}
}

// verified returns a binary in a temp directory that matches the 9.9.9
// checksum table.
func verified(t *testing.T) rulesync.Result {
	t.Helper()
	rulesync.SetReleases(t, map[string]map[string]rulesync.Asset{
		"9.9.9": {"linux/amd64": {Name: asset, SHA256: sum(binary)}},
	})
	path := filepath.Join(t.TempDir(), asset)
	if err := os.WriteFile(path, binary, 0o755); err != nil {
		t.Fatal(err)
	}
	return rulesync.Result{Version: "9.9.9", Asset: asset, Path: path, SHA256: sum(binary)}
}

func TestRun(t *testing.T) {
	bin := verified(t)
	dir := t.TempDir()
	var calls int
	exec := func(_ context.Context, name string, args, env []string, cwd string) ([]byte, []byte, error) {
		calls++
		if name != bin.Path || cwd != dir || !reflect.DeepEqual(args, rulesync.Args(true, []string{"opencode"})) {
			t.Errorf("exec(%q, %q, %q), want the binary, the args of Args and dir", name, args, cwd)
		}
		if !strings.Contains(strings.Join(env, "\n"), "HOME="+dir+"\n") {
			t.Errorf("env %q does not set HOME to dir", env)
		}
		return []byte("Written 1 rule\n"), nil, nil
	}
	if err := rulesync.Run(context.Background(), exec, bin, dir, true, []string{"opencode"}); err != nil || calls != 1 {
		t.Errorf("Run() = %v after %d calls, want nil after 1", err, calls)
	}
}

func TestRunRefusesAModifiedBinary(t *testing.T) {
	bin := verified(t)
	if err := os.WriteFile(bin.Path, []byte("tampered"), 0o755); err != nil {
		t.Fatal(err)
	}
	exec := func(context.Context, string, []string, []string, string) ([]byte, []byte, error) {
		t.Error("exec called with a modified binary")
		return nil, nil, nil
	}
	err := rulesync.Run(context.Background(), exec, bin, t.TempDir(), false, []string{"opencode"})
	if err == nil || !strings.Contains(err.Error(), "checksum mismatch") {
		t.Errorf("Run() = %v, want a checksum mismatch", err)
	}
}

func TestRunReportsTheEndOfStderr(t *testing.T) {
	bin := verified(t)
	stderr := strings.Repeat("warning\n", 20) + "Failed to parse frontmatter in rules/a.md\n"
	exec := func(context.Context, string, []string, []string, string) ([]byte, []byte, error) {
		return nil, []byte(stderr), errors.New("exit status 1")
	}
	err := rulesync.Run(context.Background(), exec, bin, t.TempDir(), false, []string{"opencode"})
	if err == nil || !strings.Contains(err.Error(), "exit status 1") || !strings.HasSuffix(err.Error(), "Failed to parse frontmatter in rules/a.md") {
		t.Fatalf("Run() = %v, want the exit status and the last stderr line", err)
	}
	if n := strings.Count(err.Error(), "warning"); n > 9 {
		t.Errorf("error has %d warning lines, want only the tail of stderr", n)
	}
}
