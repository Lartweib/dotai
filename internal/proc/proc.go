// Package proc lists the processes running on this machine, for the check
// that refuses a replace install or its restore while a harness runs
// (decision 0038). It is best effort, by executable name only:
//
//   - A false positive is another program with a harness's name, such as an
//     unrelated tool called agy, or a harness of another user on the same
//     machine, where the OS lets dotai see it.
//   - A false negative is a harness that runs under another name, such as
//     a harness renamed or run through a wrapper, one inside a container or
//     on another machine sharing the home directory, and one that starts
//     after the check. On Windows the process snapshot has no command line,
//     so Claude Code or OpenCode installed through npm, which run as
//     node.exe, are not found there either; elsewhere the command line of a
//     node process tells them apart.
//
// It uses no cgo: the process snapshot API on Windows, /proc on Linux and
// /bin/ps elsewhere.
package proc

import (
	"path/filepath"
	"strings"
)

// Process is a running process: its id and its executable's name, the
// base name without directories, such as claude.exe or opencode; where the
// OS gives them cheaply, the name it was started as, the base name of its
// first argument, which differs when the executable is a link, as the
// native install of Claude Code is to a versioned binary, and its command
// line, "" otherwise.
type Process struct {
	PID   int    `json:"pid"`
	Name  string `json:"name"`
	Argv0 string `json:"-"`
	Args  string `json:"-"`
}

// List gives the processes running on this machine that dotai can see.
func List() ([]Process, error) { return list() }

// baseName is the executable name of path, which may be a full path with
// either separator.
func baseName(path string) string {
	path = strings.TrimSpace(path)
	if i := strings.LastIndexAny(path, `/\`); i >= 0 {
		path = path[i+1:]
	}
	return filepath.Base(path)
}
