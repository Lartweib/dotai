//go:build !windows

package cli

import "os"

// EnableANSI does nothing: terminals outside Windows show ANSI colors.
func EnableANSI(*os.File) (restore func()) { return func() {} }

func ansiEnabled(*os.File) bool { return true }
