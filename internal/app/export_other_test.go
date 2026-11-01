//go:build !windows

package app

import "syscall"

// ErrCrossDevice is the error the OS gives a rename across volumes.
var ErrCrossDevice error = syscall.EXDEV
