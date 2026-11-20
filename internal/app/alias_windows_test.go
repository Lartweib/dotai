package app_test

import (
	"testing"

	"golang.org/x/sys/windows"
)

// projectAliases are the other spellings of a directory this OS has,
// each made by a function that returns the directory under it.
func projectAliases() map[string]func(t *testing.T, dir string) string {
	return map[string]func(t *testing.T, dir string) string{"linked parent": linkedParent, "8.3 short name": shortName}
}

// shortName is dir spelled with 8.3 short names, as GetShortPathName
// gives it; the test is skipped where the volume makes none.
func shortName(t *testing.T, dir string) string {
	t.Helper()
	long, err := windows.UTF16PtrFromString(dir)
	if err != nil {
		t.Fatal(err)
	}
	buf := make([]uint16, windows.MAX_LONG_PATH)
	n, err := windows.GetShortPathName(long, &buf[0], uint32(len(buf)))
	if err != nil || n == 0 || int(n) > len(buf) {
		t.Fatalf("GetShortPathName(%s) = %d, %v", dir, n, err)
	}
	short := windows.UTF16ToString(buf[:n])
	if short == dir {
		t.Skipf("the volume of %s makes no 8.3 short names", dir)
	}
	return short
}
