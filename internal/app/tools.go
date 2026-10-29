package app

import (
	"bytes"
	"fmt"
	"path/filepath"
	"unicode/utf8"

	"github.com/lartweib/dotai/internal/merge"
)

// ToolPaths are the install paths the path variables of decision 0030
// stand for. A directory that could not be found has its error instead;
// it matters only to a staged file that uses its variable.
type ToolPaths struct {
	User    string // ${DOTAI_TOOLS}: the user-scope tools directory
	Project string // ${DOTAI_PROJECT_TOOLS}: the project's tools directory

	UserErr, ProjectErr error
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
func expandPaths(data []byte, tools ToolPaths) ([]byte, error) {
	if !utf8.Valid(data) || bytes.IndexByte(data, 0) >= 0 {
		return data, nil
	}
	for _, v := range []struct {
		token, path string
		err         error
	}{{"${DOTAI_TOOLS}", tools.User, tools.UserErr}, {"${DOTAI_PROJECT_TOOLS}", tools.Project, tools.ProjectErr}} {
		if !bytes.Contains(data, []byte(v.token)) {
			continue
		}
		if v.err != nil {
			return nil, fmt.Errorf("%s cannot be expanded: %w", v.token, v.err)
		}
		data = bytes.ReplaceAll(data, []byte(v.token), []byte(filepath.ToSlash(v.path)))
	}
	return data, nil
}
