//go:build !windows

package app

import (
	"errors"
	"io/fs"
	"os"
	"syscall"
)

// errCrossDevice is the error of a rename across volumes.
var errCrossDevice error = syscall.EXDEV

// crossDevice reports a rename that failed because it crosses volumes.
func crossDevice(err error) bool { return errors.Is(err, syscall.EXDEV) }

// copyLink makes the symbolic link to inside r to dest, a copy of a link.
func copyLink(r *os.Root, _ fs.FileMode, dest, to string) error { return r.Symlink(dest, to) }

// syncIn syncs the directory name inside r.
func syncIn(r *os.Root, name string) error {
	f, err := r.Open(name)
	if err != nil {
		return err
	}
	defer f.Close()
	return f.Sync()
}

// transient reports no error as transient: outside Windows, another
// program having a file open does not stop a move or a removal.
func transient(error) bool { return false }

// clearReadOnly does nothing: outside Windows, a read-only file can be
// replaced or removed when its directory is writable.
func clearReadOnly(*os.Root, string, fs.FileMode) error { return nil }

// linkMode reports a mode that Link reads as a link: a symbolic link.
func linkMode(mode fs.FileMode) bool { return mode&fs.ModeSymlink != 0 }

// canLink accepts every destination: a symbolic link can point anywhere.
func canLink(string) error { return nil }

// makeLink creates the symbolic link name inside r to dest.
func makeLink(r *os.Root, name, dest string) error { return r.Symlink(dest, name) }

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
