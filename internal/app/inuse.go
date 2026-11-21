package app

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"strings"
)

// atPath is a failure of an operation on the entry at an absolute path,
// which the install file system adds to a refused rename or removal, since
// the error of the system names it relative to a root, or by the temporary
// file a write renames. It words itself as the failure does.
type atPath struct {
	path string
	err  error
}

func (e *atPath) Error() string { return e.err.Error() }
func (e *atPath) Unwrap() error { return e.err }

// at is err, when it is one, tagged with the absolute path of the entry
// name inside root, a slash path, that the operation was on.
func at(root, name string, err error) error {
	if err == nil {
		return nil
	}
	return &atPath{path: filepath.Join(root, filepath.FromSlash(name)), err: err}
}

// inUseHint explains a move, a rename or a removal that the system refused
// for access, on Windows, where it is nearly always another program holding
// a handle on the path or inside it: a terminal whose working directory is
// there, an editor or a tool watching the folder. It returns the text to put
// after the cause, "" for any other error. The error is recognized by its
// code, ERROR_ACCESS_DENIED or ERROR_SHARING_VIOLATION, and not by the
// system's words for it, which depend on its language. windows is whether
// the system is Windows.
func inUseHint(cause error, windows bool) string {
	if !windows {
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

// movedPath is the path a rename or a removal that failed was on, as the
// person sees it, absolute with the separators of the system when the
// install file system gave it, and whether the error is one. A write
// renames a temporary file over its target, and the target is the path
// that is held, not the temporary file.
func movedPath(err error) (string, bool) {
	var held *atPath
	if errors.As(err, &held) {
		return held.path, true
	}
	var link *os.LinkError
	if errors.As(err, &link) && strings.HasPrefix(link.Op, "rename") {
		if isTempOf(path.Base(filepath.ToSlash(link.Old)), path.Base(filepath.ToSlash(link.New))) {
			return filepath.FromSlash(link.New), true
		}
		return filepath.FromSlash(link.Old), true
	}
	var pe *fs.PathError
	if errors.As(err, &pe) && (strings.HasPrefix(pe.Op, "rename") || strings.HasPrefix(pe.Op, "remove") || strings.HasPrefix(pe.Op, "unlink")) {
		return filepath.FromSlash(pe.Path), true
	}
	return "", false
}

// withHint is msg with the in-use hint for cause after it, when there is one.
func withHint(msg string, cause error) string {
	if hint := inUseHint(cause, runtime.GOOS == "windows"); hint != "" {
		return msg + "; " + hint
	}
	return msg
}
