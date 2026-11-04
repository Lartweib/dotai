package app

import (
	"os"
	"path/filepath"
	"testing"
)

// TestResolvePathGivesADrivePath checks that a resolved path is a plain
// drive path, without the \\?\ prefix of the final path Windows gives,
// and in the case the directory has on disk.
func TestResolvePathGivesADrivePath(t *testing.T) {
	base := t.TempDir()
	dir := filepath.Join(base, "Home")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if got := resolvePath(dir); got != dir {
		t.Errorf("resolvePath(%s) = %s, want it unchanged", dir, got)
	}
	if got := resolvePath(filepath.Join(base, "home")); got != dir {
		t.Errorf("resolvePath of the lowercased path = %s, want %s", got, dir)
	}
	if got := resolvePath(filepath.Join(base, "missing")); got != filepath.Join(base, "missing") {
		t.Errorf("resolvePath of a missing path = %s, want it as given", got)
	}
}
