package rulesync

import "testing"

// SetReleases replaces the checksum table for the duration of t.
func SetReleases(t *testing.T, r map[string]map[string]Asset) {
	t.Helper()
	old := releases
	releases = r
	t.Cleanup(func() { releases = old })
}
