//go:build !windows

package app_test

import "testing"

// projectAliases are the other spellings of a directory this OS has,
// each made by a function that returns the directory under it.
func projectAliases() map[string]func(t *testing.T, dir string) string {
	return map[string]func(t *testing.T, dir string) string{"linked parent": linkedParent}
}
