//go:build !windows

package app

import (
	"io/fs"
	"os"
)

// transient reports no error as transient: outside Windows, another
// program having a file open does not stop a move or a removal.
func transient(error) bool { return false }

// clearReadOnly does nothing: outside Windows, a read-only file can be
// replaced or removed when its directory is writable.
func clearReadOnly(*os.Root, string, fs.FileMode) error { return nil }

// syncDir syncs the directory name, so that the entries written in it
// survive a crash.
func syncDir(name string) error {
	f, err := os.Open(name)
	if err != nil {
		return err
	}
	defer f.Close()
	return f.Sync()
}
