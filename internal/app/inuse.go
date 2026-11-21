package app

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"runtime"
	"strings"
)

// inUseHint explains a move, a rename or a removal that the system refused
// for access, on Windows, where it is nearly always another program holding
// a handle on the path or inside it: a terminal whose working directory is
// there, an editor or a tool watching the folder. It returns the text to put
// after the cause, "" for any other error. The error is recognized by its
// code, ERROR_ACCESS_DENIED or ERROR_SHARING_VIOLATION, and not by the
// system's words for it, which depend on its language.
func inUseHint(cause error) string {
	if runtime.GOOS != "windows" {
		return ""
	}
	name, ok := movedPath(cause)
	if !ok || !(transient(cause) || errors.Is(cause, fs.ErrPermission)) {
		return ""
	}
	return fmt.Sprintf("%s is probably in use by another program, such as a terminal, an editor or a tool watching that folder: "+
		"close it and try again; to find which one, open Resource Monitor (resmon), go to CPU, then Associated Handles, and search for %s",
		name, name)
}

// movedPath is the path a rename or a removal that failed was on, and
// whether the error is one.
func movedPath(err error) (string, bool) {
	var link *os.LinkError
	if errors.As(err, &link) && strings.HasPrefix(link.Op, "rename") {
		return link.Old, true
	}
	var pe *fs.PathError
	if errors.As(err, &pe) && (strings.HasPrefix(pe.Op, "rename") || strings.HasPrefix(pe.Op, "remove") || strings.HasPrefix(pe.Op, "unlink")) {
		return pe.Path, true
	}
	return "", false
}

// withHint is msg with the in-use hint for cause after it, when there is one.
func withHint(msg string, cause error) string {
	if hint := inUseHint(cause); hint != "" {
		return msg + "; " + hint
	}
	return msg
}
