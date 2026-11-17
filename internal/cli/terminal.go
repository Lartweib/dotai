package cli

import (
	"fmt"
	"strings"
)

// What a pack or git controls, such as a path, a ref, a URL or git's
// stderr, reaches the terminal escaped, so that no escape sequence or
// carriage return can hide or rewrite what the user reads before a
// prompt.

// termSafe is s with every control character escaped, as \x1b or
// \u202e: a C0 control, DEL, a C1 control and a bidirectional override.
func termSafe(s string) string { return escapeControls(s, false) }

// termText is termSafe for text of several lines: it keeps a line feed, a
// tab and a carriage return before a line feed.
func termText(s string) string { return escapeControls(s, true) }

func escapeControls(s string, lines bool) string {
	var b strings.Builder
	for i, r := range s {
		switch {
		case lines && (r == '\n' || r == '\t' || r == '\r' && strings.HasPrefix(s[i+1:], "\n")):
			b.WriteRune(r)
		case r < 0x20 || r == 0x7f || r >= 0x80 && r <= 0x9f:
			fmt.Fprintf(&b, "\\x%02x", r)
		case r >= 0x202a && r <= 0x202e || r >= 0x2066 && r <= 0x2069:
			fmt.Fprintf(&b, "\\u%04x", r)
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}
