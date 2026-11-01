package app

import "golang.org/x/sys/windows"

// ErrCrossDevice is the error the OS gives a rename across volumes.
var ErrCrossDevice error = windows.ERROR_NOT_SAME_DEVICE
