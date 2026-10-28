//go:build !windows

package cli

import "os"

// enableANSI reports true: terminals outside Windows show ANSI colors.
func enableANSI(*os.File) bool { return true }
