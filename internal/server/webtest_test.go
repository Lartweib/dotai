package server_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestTheWebModulesPassTheirTests runs the tests of the frontend's pure
// modules, webtest/*.test.mjs, with node --test. The frontend needs no
// build and no package; node only runs these tests. Without node they are
// skipped on a workstation, and fail in CI, which sets CI and installs it.
func TestTheWebModulesPassTheirTests(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		if os.Getenv("CI") != "" {
			t.Fatal("node is not on PATH, and CI runs these tests")
		}
		t.Skip("node is not on PATH")
	}
	files, err := filepath.Glob(filepath.Join("webtest", "*.test.mjs"))
	if err != nil || len(files) == 0 {
		t.Fatalf("webtest holds no tests: %v", err)
	}
	args := []string{"--test"}
	for _, f := range files {
		args = append(args, filepath.ToSlash(f))
	}
	cmd := exec.Command(node, args...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Errorf("node --test: %v\n%s", err, out)
	}
}
