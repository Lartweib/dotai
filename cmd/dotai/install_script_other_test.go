//go:build !windows

package main

import "testing"

// install.ps1 runs on Windows only; these keep the shared test compiling.

func testUserEnvKey(t *testing.T) string { t.Helper(); t.Fatal("Windows only"); return "" }

func setTestUserPath(t *testing.T, _, _ string, _ bool) { t.Helper(); t.Fatal("Windows only") }

func testUserPathValue(t *testing.T, _ string) (string, bool, bool) {
	t.Helper()
	t.Fatal("Windows only")
	return "", false, false
}

func lockFile(t *testing.T, _ string) func() { t.Helper(); t.Fatal("Windows only"); return nil }
