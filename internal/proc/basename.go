//go:build !windows

package proc

import (
	"path/filepath"
	"strings"
)

// baseName is the executable name of path, which may be a full path with
// either separator. The Windows process snapshot gives the file name
// itself, so only the other lists need it.
func baseName(path string) string {
	path = strings.TrimSpace(path)
	if i := strings.LastIndexAny(path, `/\`); i >= 0 {
		path = path[i+1:]
	}
	return filepath.Base(path)
}
