package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestResolvePathGivesADrivePath checks that a resolved path is a plain
// drive path, without the \\?\ prefix of the final path Windows gives,
// with its 8.3 short names in their long form and in the case the
// directory has on disk, whatever form the temporary directory is given
// in, as a CI runner's 8.3 TEMP is.
func TestResolvePathGivesADrivePath(t *testing.T) {
	given := t.TempDir()
	base := resolvePath(given)
	if strings.HasPrefix(base, `\\`) || strings.Contains(base, "~") || !filepath.IsAbs(base) {
		t.Fatalf("resolvePath(%s) = %s, want a drive path in long form", given, base)
	}
	dir := filepath.Join(given, "Home")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(base, "Home")
	for _, p := range []string{dir, filepath.Join(given, "home"), filepath.Join(base, "home")} {
		if got := resolvePath(p); got != want {
			t.Errorf("resolvePath(%s) = %s, want %s", p, got, want)
		}
	}
	if got := resolvePath(want); got != want {
		t.Errorf("resolvePath(%s) = %s, want it unchanged", want, got)
	}
	missing := filepath.Join(given, "missing")
	if got := resolvePath(missing); got != missing {
		t.Errorf("resolvePath of a missing path = %s, want it as given", got)
	}
}
