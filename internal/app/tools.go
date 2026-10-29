package app

import (
	"bytes"
	"path/filepath"
	"unicode/utf8"

	"github.com/lartweib/dotai/internal/merge"
)

// ToolPaths are the install paths the path variables of decision 0030
// stand for.
type ToolPaths struct {
	User    string // ${DOTAI_TOOLS}: the user-scope tools directory
	Project string // ${DOTAI_PROJECT_TOOLS}: the project's tools directory
}

// ToolsIn returns the tools directories of a home and a project directory.
func ToolsIn(home, project string) ToolPaths {
	dir := filepath.FromSlash(merge.ToolsDir)
	return ToolPaths{User: filepath.Join(home, dir), Project: filepath.Join(project, dir)}
}

// expandPaths replaces the path variables in data when it is UTF-8 text
// without a NUL byte, and returns any other data as it is. The paths are
// written with forward slashes, which every harness accepts and which need
// no escaping inside a JSON string.
func expandPaths(data []byte, tools ToolPaths) []byte {
	if !utf8.Valid(data) || bytes.IndexByte(data, 0) >= 0 {
		return data
	}
	data = bytes.ReplaceAll(data, []byte("${DOTAI_TOOLS}"), []byte(filepath.ToSlash(tools.User)))
	return bytes.ReplaceAll(data, []byte("${DOTAI_PROJECT_TOOLS}"), []byte(filepath.ToSlash(tools.Project)))
}
