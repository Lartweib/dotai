package plan

import (
	"bytes"
	"strings"
)

// dotai keeps the exclude entries of a project in one block of the
// exclude file (decision 0041), between a begin and an end line that name
// dotai and the project, the top of its worktree, since the worktrees of a
// repository share the file. The block is rewritten whole by each apply,
// and everything outside it is left byte for byte.

// excludeBegin and excludeEnd are the lines around the block of project.
func excludeBegin(project string) string {
	return "# >>> dotai " + project + ": managed by dotai, rewritten by each apply"
}

func excludeEnd(project string) string { return "# <<< dotai " + project }

// WithExcludeBlock is content, an exclude file, with the block of project
// holding entries: the block already there rewritten in place, or a new one
// at the end, or no block when entries is empty. newline reports a line
// break added to content's last line before a new block, which
// WithoutExcludeBlock removes again.
func WithExcludeBlock(content []byte, project string, entries []string) (out []byte, newline bool) {
	eol := "\n"
	if i := bytes.IndexByte(content, '\n'); i > 0 && content[i-1] == '\r' {
		eol = "\r\n"
	}
	var block strings.Builder
	if len(entries) > 0 {
		block.WriteString(excludeBegin(project) + eol)
		for _, e := range entries {
			block.WriteString(e + eol)
		}
		block.WriteString(excludeEnd(project) + eol)
	}
	if start, end, ok := findBlock(content, project); ok {
		return append(append(append([]byte{}, content[:start]...), block.String()...), content[end:]...), false
	}
	if block.Len() == 0 {
		return content, false
	}
	out = append([]byte{}, content...)
	if len(out) > 0 && out[len(out)-1] != '\n' {
		out, newline = append(out, eol...), true
	}
	return append(out, block.String()...), newline
}

// WithoutExcludeBlock is content without the block of project; with
// newline, the line break dotai added before it goes too.
func WithoutExcludeBlock(content []byte, project string, newline bool) []byte {
	start, end, ok := findBlock(content, project)
	if !ok {
		return content
	}
	out := append(append([]byte{}, content[:start]...), content[end:]...)
	if newline && end == len(content) {
		out = bytes.TrimSuffix(out, []byte("\r\n"))
		if len(out) == start {
			out = bytes.TrimSuffix(out, []byte("\n"))
		}
	}
	return out
}

// findBlock finds the block of project in content: the offset of its begin
// line and the offset just past the line break of its end line, or the end
// of content.
func findBlock(content []byte, project string) (start, end int, ok bool) {
	begin, last := excludeBegin(project), excludeEnd(project)
	at, in := 0, false
	for at < len(content) {
		next := bytes.IndexByte(content[at:], '\n')
		lineEnd := len(content)
		if next >= 0 {
			lineEnd = at + next + 1
		}
		line := strings.TrimRight(string(content[at:lineEnd]), "\r\n")
		switch {
		case !in && line == begin:
			start, in = at, true
		case in && line == last:
			return start, lineEnd, true
		}
		at = lineEnd
	}
	return 0, 0, false
}
