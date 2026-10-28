package cli

import (
	"os"

	"golang.org/x/sys/windows"
)

// EnableANSI turns on virtual terminal processing when f is a console, so
// it shows ANSI colors, and returns a function that restores the original
// console mode. It is a no-op for anything else.
func EnableANSI(f *os.File) (restore func()) {
	h := windows.Handle(f.Fd())
	var mode uint32
	if windows.GetConsoleMode(h, &mode) != nil || mode&windows.ENABLE_VIRTUAL_TERMINAL_PROCESSING != 0 {
		return func() {}
	}
	if windows.SetConsoleMode(h, mode|windows.ENABLE_VIRTUAL_TERMINAL_PROCESSING) != nil {
		return func() {}
	}
	return func() { _ = windows.SetConsoleMode(h, mode) }
}

// ansiEnabled reports whether the console f processes ANSI sequences.
func ansiEnabled(f *os.File) bool {
	var mode uint32
	return windows.GetConsoleMode(windows.Handle(f.Fd()), &mode) == nil && mode&windows.ENABLE_VIRTUAL_TERMINAL_PROCESSING != 0
}
