package cli

import (
	"io"
	"os"

	"golang.org/x/term"
)

// ANSI styles of the text report.
const (
	styleBold  = "1"
	styleDim   = "2"
	styleRed   = "31"
	styleGreen = "32"
)

// isTerminal reports whether f is a terminal that shows ANSI colors.
var isTerminal = func(f *os.File) bool {
	return term.IsTerminal(int(f.Fd())) && enableANSI(f)
}

// useColor reports whether the text written to w may be colored: only on a
// terminal, and never when NO_COLOR is set (https://no-color.org).
func useColor(w io.Writer) bool {
	f, ok := w.(*os.File)
	return ok && os.Getenv("NO_COLOR") == "" && isTerminal(f)
}

// paint wraps s in an ANSI style when on is true.
func paint(on bool, style, s string) string {
	if !on {
		return s
	}
	return "\x1b[" + style + "m" + s + "\x1b[0m"
}
