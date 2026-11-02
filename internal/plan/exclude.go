package plan

import (
	"bytes"
	"strings"
)

// dotai keeps the exclude entries of a project in one block of the
// exclude file (decision 0041), between a begin and an end line that name
// dotai and the project, the top of its worktree, since the worktrees of a
// repository share the file. The block is rewritten whole by each apply,
// and everything outside it is left byte for byte. When the file's last
// line had no line break, dotai adds one before its block and says so on
// the block's begin line, so that whichever block goes last takes it away
// again, whatever the other worktrees did meanwhile.

// addedBreak ends the begin line of a block before which dotai added the
// line break of the line above.
const addedBreak = "; dotai added the line break above"

// excludeBegin and excludeEnd are the lines around the block of project.
func excludeBegin(project string) string {
	return "# >>> dotai " + project + ": managed by dotai, rewritten by each apply"
}

func excludeEnd(project string) string { return "# <<< dotai " + project }

// block is the block of project in an exclude file: the offsets of its
// begin line and just past its end line, and whether dotai added the line
// break before it.
type block struct {
	start, end int
	added      bool
}

// WithExcludeBlock is content, an exclude file, with the block of project
// holding entries: the block already there rewritten in place, or a new one
// at the end, or no block when entries is empty.
func WithExcludeBlock(content []byte, project string, entries []string) []byte {
	b, ok := findBlock(content, project)
	if len(entries) == 0 {
		return WithoutExcludeBlock(content, project)
	}
	eol := lineEnd(content)
	text := func(added bool) string {
		var s strings.Builder
		s.WriteString(excludeBegin(project))
		if added {
			s.WriteString(addedBreak)
		}
		s.WriteString(eol)
		for _, e := range entries {
			s.WriteString(e + eol)
		}
		s.WriteString(excludeEnd(project) + eol)
		return s.String()
	}
	if ok {
		return append(append(append([]byte{}, content[:b.start]...), text(b.added)...), content[b.end:]...)
	}
	out := append([]byte{}, content...)
	added := len(out) > 0 && out[len(out)-1] != '\n'
	if added {
		out = append(out, eol...)
	}
	return append(out, text(added)...)
}

// WithoutExcludeBlock is content without the block of project. A line
// break dotai added before it goes too when the block is the last thing in
// the file; before another block of dotai's, that block takes it over.
func WithoutExcludeBlock(content []byte, project string) []byte {
	b, ok := findBlock(content, project)
	if !ok {
		return content
	}
	rest := content[b.end:]
	if b.added && len(rest) > 0 {
		if next := firstLine(rest); strings.HasPrefix(next, "# >>> dotai ") && !strings.HasSuffix(next, addedBreak) {
			rest = append([]byte(next+addedBreak), rest[len(next):]...)
		}
	}
	out := append(append([]byte{}, content[:b.start]...), rest...)
	if b.added && b.end == len(content) {
		if !bytes.HasSuffix(out, []byte("\r\n")) || len(out) < 2 {
			out = bytes.TrimSuffix(out, []byte("\n"))
		} else {
			out = out[:len(out)-2]
		}
	}
	return out
}

// lineEnd is the line ending of content: that of its first line, "\n" by
// default.
func lineEnd(content []byte) string {
	if i := bytes.IndexByte(content, '\n'); i > 0 && content[i-1] == '\r' {
		return "\r\n"
	}
	return "\n"
}

// firstLine is the first line of content without its line break.
func firstLine(content []byte) string {
	line := content
	if i := bytes.IndexByte(content, '\n'); i >= 0 {
		line = content[:i]
	}
	return strings.TrimSuffix(string(line), "\r")
}

// findBlock finds the block of project in content.
func findBlock(content []byte, project string) (block, bool) {
	begin, last := excludeBegin(project), excludeEnd(project)
	at, in := 0, false
	var b block
	for at < len(content) {
		next := bytes.IndexByte(content[at:], '\n')
		lineEnd := len(content)
		if next >= 0 {
			lineEnd = at + next + 1
		}
		line := strings.TrimRight(string(content[at:lineEnd]), "\r\n")
		switch {
		case !in && (line == begin || line == begin+addedBreak):
			b, in = block{start: at, added: line != begin}, true
		case in && line == last:
			b.end = lineEnd
			return b, true
		}
		at = lineEnd
	}
	return block{}, false
}
