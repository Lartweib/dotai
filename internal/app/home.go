package app

import (
	"fmt"
	"path/filepath"
)

// requireAbs returns the home directory home cleaned, and fails when it is
// not absolute, as every use case that takes a home directory requires.
func requireAbs(home string) (string, error) {
	if !filepath.IsAbs(home) {
		return "", fmt.Errorf("home directory %q is not absolute", home)
	}
	return filepath.Clean(home), nil
}
