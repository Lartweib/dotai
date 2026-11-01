package proc

import (
	"os"
	"path/filepath"
	"testing"
)

// TestProcInfo checks a process whose executable is a link to a versioned
// binary, as the native install of Claude Code is: its name is the
// binary's, and the name it was started as comes from its command line.
func TestProcInfo(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "versions", "2.1.30")
	if err := os.MkdirAll(filepath.Dir(bin), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(bin, filepath.Join(dir, "exe")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "cmdline"), []byte("/home/u/.local/bin/claude\x00--resume\x00"), 0o644); err != nil {
		t.Fatal(err)
	}
	p, ok := procInfo(dir)
	if !ok || p.Name != "2.1.30" || p.Argv0 != "claude" || p.Args != "/home/u/.local/bin/claude --resume" {
		t.Errorf("procInfo() = %+v, %v, want 2.1.30 started as claude with its arguments", p, ok)
	}
}
