package app_test

import "testing"

// heavy marks case i of a kill, crash or rollback matrix, each case of
// which installs and restores a whole replace home on disk. With
// go test -short only the first case runs, so that a quick loop still
// covers the matrix; the full suite, as CI runs it, runs every case.
func heavy(t *testing.T, i int) {
	t.Helper()
	if testing.Short() && i > 0 {
		t.Skip("-short runs only the first case of this matrix")
	}
}
