package app

import (
	"errors"
	"io/fs"
	"os"

	"golang.org/x/sys/windows"
)

// transient reports an error that another program holding the file, such
// as an antivirus scan or a search indexer, causes for a moment.
func transient(err error) bool {
	return errors.Is(err, windows.ERROR_ACCESS_DENIED) || errors.Is(err, windows.ERROR_SHARING_VIOLATION)
}

// clearReadOnly clears the read-only attribute of name, which would stop a
// move over it or its removal.
func clearReadOnly(r *os.Root, name string, mode fs.FileMode) error {
	if mode&0o200 != 0 {
		return nil
	}
	return r.Chmod(name, mode|0o200)
}

// syncDir does nothing: Windows cannot sync a directory, and makes the
// entries of a directory durable with the files themselves.
func syncDir(string) error { return nil }
