//go:build !windows

package app

import "path/filepath"

// resolvePath is the path p is on disk, with every link on its way
// followed, or p as it is when it cannot be resolved.
func resolvePath(p string) string {
	if r, err := filepath.EvalSymlinks(p); err == nil {
		return filepath.Clean(r)
	}
	return p
}
