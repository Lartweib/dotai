package app

import (
	"bytes"
	"cmp"
	"fmt"
	"path/filepath"
	"runtime"
	"strings"
	"unicode/utf8"

	"go.yaml.in/yaml/v3"

	"github.com/lartweib/dotai/internal/merge"
)

// ToolPaths are the install paths the path variables of decision 0030
// stand for. A directory that could not be found has its error instead;
// it matters only to a staged file that uses its variable. OS is the OS
// the tool commands of decision 0037 are resolved for, runtime.GOOS when
// empty.
type ToolPaths struct {
	User    string // ${DOTAI_TOOLS}: the user-scope tools directory
	Project string // ${DOTAI_PROJECT_TOOLS}: the project's tools directory
	OS      string

	UserErr, ProjectErr error

	project bool // the settings being expanded are the project scope's
}

// inScope is t for the settings of scope, whose tool commands run the
// tools of that scope.
func (t ToolPaths) inScope(scope string) ToolPaths {
	t.project = scope == ScopeProject
	return t
}

// expandSetting expands a string value of a harness's settings: each tool
// command variable of decision 0037, then the path variables.
func expandSetting(s string, tools ToolPaths) (string, error) {
	dir, err := tools.User, tools.UserErr
	if tools.project {
		dir, err = tools.Project, tools.ProjectErr
	}
	s, cerr := merge.ExpandTools(s, func(name string) (string, error) {
		if err != nil {
			return "", fmt.Errorf("${DOTAI_TOOL:%s} cannot be expanded: %w", name, err)
		}
		return toolCommand(dir, name, cmp.Or(tools.OS, runtime.GOOS))
	})
	if cerr != nil {
		return "", cerr
	}
	data, err := expandPaths([]byte(s), tools)
	return string(data), err
}

// unquoted are the characters that some harness's shell does not take
// literally inside double quotes on goos: cmd.exe and PowerShell on
// Windows, a POSIX shell, or bash for Claude Code, everywhere.
func unquoted(goos string) string {
	if goos == "windows" {
		return "\"$`%"
	}
	return "\"$`\\"
}

// toolCommand is the command that runs the tool name from the tools
// directory dir on goos (decision 0037): its PowerShell script on Windows,
// through powershell.exe, which opens no window, and its shell script
// elsewhere, through sh. The script's absolute path is in double quotes,
// so that a directory with spaces works in every harness; a directory
// with a character the quotes do not protect is refused.
func toolCommand(dir, name, goos string) (string, error) {
	if i := strings.IndexAny(dir, unquoted(goos)); i >= 0 {
		return "", fmt.Errorf("${DOTAI_TOOL:%s} cannot be expanded: the tools directory %s holds %q, which a harness's shell would not take literally inside double quotes", name, dir, dir[i])
	}
	if goos == "windows" {
		script := strings.ReplaceAll(dir, "/", `\`) + `\` + name + ".ps1"
		return `powershell.exe -NoProfile -ExecutionPolicy Bypass -File "` + script + `"`, nil
	}
	return `sh "` + filepath.ToSlash(dir) + "/" + name + `.sh"`, nil
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

// ToolCommand is a tool command of decision 0037 that a harness's settings
// run, resolved for this OS: the scope of the settings, the harness, the
// top-level settings key that holds it, the tool's name and the command.
type ToolCommand struct {
	Scope   string `json:"-"`
	Harness string `json:"-"`
	Key     string `json:"key"`
	Tool    string `json:"tool"`
	Command string `json:"command"`
}

// toolCommands is every tool command the settings of scope run, by harness
// in the order of harnesses, then in the order of the settings, resolved
// as the settings files get them, so that a plan can show them.
func toolCommands(scope string, harnesses []string, settings map[string][]merge.Setting, tools ToolPaths) ([]ToolCommand, error) {
	tools = tools.inScope(scope)
	var out []ToolCommand
	for _, h := range harnesses {
		for _, s := range settings[h] {
			var err error
			walkStrings(s.Value, func(v string) {
				if err != nil {
					return
				}
				_, err = merge.ExpandTools(v, func(name string) (string, error) {
					c, err := expandSetting("${DOTAI_TOOL:"+name+"}", tools)
					if err == nil {
						out = append(out, ToolCommand{Scope: scope, Harness: h, Key: s.Key, Tool: name, Command: c})
					}
					return c, err
				})
			})
			if err != nil {
				return nil, fmt.Errorf("%s settings of %s: %w", scope, h, err)
			}
		}
	}
	return out, nil
}

// walkStrings calls f with each string value under n, keys left out.
func walkStrings(n *yaml.Node, f func(string)) {
	switch n.Kind {
	case yaml.MappingNode:
		for i := 1; i < len(n.Content); i += 2 {
			walkStrings(n.Content[i], f)
		}
	case yaml.SequenceNode:
		for _, c := range n.Content {
			walkStrings(c, f)
		}
	case yaml.ScalarNode:
		if n.Tag == "!!str" {
			f(n.Value)
		}
	}
}
