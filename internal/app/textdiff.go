package app

import (
	"fmt"
	"slices"
	"strings"
)

// A unified diff of two texts, for the update plan of a pack.

// diffContext is how many unchanged lines a hunk shows around a change.
const diffContext = 3

// maxDiffCells bounds the table of the longest common subsequence: past
// it, the lines between the common start and end show as all removed and
// all added, which is complete, if not minimal.
const maxDiffCells = 1 << 22

// diffLine is one line of an edit script: ' ', '-' or '+', and the line
// with its newline, if it has one.
type diffLine struct {
	op   byte
	text string
}

// unifiedDiff is the unified diff of old and new, named oldName and
// newName, "" for a file that is absent (/dev/null), with diffContext
// lines of context; "" when they are equal.
func unifiedDiff(oldName, newName, old, new string) string {
	if old == new {
		return ""
	}
	script := editScript(splitLines(old), splitLines(new))
	var b strings.Builder
	fmt.Fprintf(&b, "--- %s\n+++ %s\n", orDevNull(oldName), orDevNull(newName))
	// Each hunk runs from diffContext lines before a change to diffContext
	// after the last change within 2*diffContext of the one before.
	for i := 0; i < len(script); {
		if script[i].op == ' ' {
			i++
			continue
		}
		start := max(0, i-diffContext)
		end := i
		for j := i; j < len(script); j++ {
			if script[j].op != ' ' {
				end = j
			} else if j-end > 2*diffContext {
				break
			}
		}
		end = min(len(script), end+diffContext+1)
		oldLine, newLine := lineAt(script, start)
		var oldCount, newCount int
		for _, l := range script[start:end] {
			if l.op != '+' {
				oldCount++
			}
			if l.op != '-' {
				newCount++
			}
		}
		fmt.Fprintf(&b, "@@ -%s +%s @@\n", hunkRange(oldLine, oldCount), hunkRange(newLine, newCount))
		for _, l := range script[start:end] {
			b.WriteByte(l.op)
			b.WriteString(l.text)
			if !strings.HasSuffix(l.text, "\n") {
				b.WriteString("\n\\ No newline at end of file\n")
			}
		}
		i = end
	}
	return b.String()
}

func orDevNull(name string) string {
	if name == "" {
		return "/dev/null"
	}
	return name
}

// lineAt gives the old and new line numbers, from 1, of script[i].
func lineAt(script []diffLine, i int) (int, int) {
	o, n := 1, 1
	for _, l := range script[:i] {
		if l.op != '+' {
			o++
		}
		if l.op != '-' {
			n++
		}
	}
	return o, n
}

// hunkRange is the range of a hunk header: start,count, start alone when
// count is 1, and the line before when count is 0.
func hunkRange(start, count int) string {
	switch count {
	case 0:
		return fmt.Sprintf("%d,0", start-1)
	case 1:
		return fmt.Sprint(start)
	}
	return fmt.Sprintf("%d,%d", start, count)
}

// splitLines splits s into lines that keep their newline.
func splitLines(s string) []string {
	var out []string
	for s != "" {
		i := strings.IndexByte(s, '\n')
		if i < 0 {
			out = append(out, s)
			break
		}
		out = append(out, s[:i+1])
		s = s[i+1:]
	}
	return out
}

// editScript turns a into b: the common start and end, and in between the
// longest common subsequence when its table is small enough, otherwise
// all of a removed and all of b added.
func editScript(a, b []string) []diffLine {
	var head, tail []diffLine
	for len(a) > 0 && len(b) > 0 && a[0] == b[0] {
		head = append(head, diffLine{' ', a[0]})
		a, b = a[1:], b[1:]
	}
	for len(a) > 0 && len(b) > 0 && a[len(a)-1] == b[len(b)-1] {
		tail = append(tail, diffLine{' ', a[len(a)-1]})
		a, b = a[:len(a)-1], b[:len(b)-1]
	}
	slices.Reverse(tail)
	var mid []diffLine
	if (len(a)+1)*(len(b)+1) <= maxDiffCells {
		mid = lcsScript(a, b)
	} else {
		for _, l := range a {
			mid = append(mid, diffLine{'-', l})
		}
		for _, l := range b {
			mid = append(mid, diffLine{'+', l})
		}
	}
	return append(append(head, mid...), tail...)
}

// lcsScript is the edit script of a into b by their longest common
// subsequence, removals before additions.
func lcsScript(a, b []string) []diffLine {
	n, m := len(a), len(b)
	// lcs[i][j] is the length of the longest common subsequence of a[i:]
	// and b[j:].
	lcs := make([][]int32, n+1)
	for i := range lcs {
		lcs[i] = make([]int32, m+1)
	}
	for i := n - 1; i >= 0; i-- {
		for j := m - 1; j >= 0; j-- {
			if a[i] == b[j] {
				lcs[i][j] = lcs[i+1][j+1] + 1
			} else {
				lcs[i][j] = max(lcs[i+1][j], lcs[i][j+1])
			}
		}
	}
	var out []diffLine
	i, j := 0, 0
	for i < n && j < m {
		switch {
		case a[i] == b[j]:
			out = append(out, diffLine{' ', a[i]})
			i++
			j++
		case lcs[i+1][j] >= lcs[i][j+1]:
			out = append(out, diffLine{'-', a[i]})
			i++
		default:
			out = append(out, diffLine{'+', b[j]})
			j++
		}
	}
	for ; i < n; i++ {
		out = append(out, diffLine{'-', a[i]})
	}
	for ; j < m; j++ {
		out = append(out, diffLine{'+', b[j]})
	}
	return out
}
