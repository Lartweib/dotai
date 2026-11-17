package app

import (
	"bytes"
	"fmt"
	"slices"
	"strconv"
	"strings"
)

// checkPackTree checks the output of git ls-tree -r -z -l of a commit: it
// refuses a link (120000), a submodule (160000), any other mode than a
// file's, a path with a .git component (dotGitSegment), and more files or
// bytes than a copy of a pack takes.
func checkPackTree(out []byte) []TreeProblem {
	var problems []TreeProblem
	var files int
	var total int64
	for rec := range bytes.SplitSeq(out, []byte{0}) {
		if len(rec) == 0 {
			continue
		}
		meta, p, ok := strings.Cut(string(rec), "\t")
		f := strings.Fields(meta)
		if !ok || len(f) != 4 {
			problems = append(problems, TreeProblem{Path: p, Problem: fmt.Sprintf("is listed as %q, which dotai cannot read", meta)})
			continue
		}
		mode, size := f[0], f[3]
		switch mode {
		case "100644", "100755":
			files++
			n, err := strconv.ParseInt(size, 10, 64)
			if err != nil {
				problems = append(problems, TreeProblem{Path: p, Problem: fmt.Sprintf("has the size %q, which dotai cannot read", size)})
				continue
			}
			total += n
		case "120000":
			problems = append(problems, TreeProblem{Path: p, Problem: "is a symbolic link: a pack holds no links"})
			continue
		case "160000":
			problems = append(problems, TreeProblem{Path: p, Problem: "is a submodule: a pack holds no submodules"})
			continue
		default:
			problems = append(problems, TreeProblem{Path: p, Problem: fmt.Sprintf("has the mode %s, not a file's", mode)})
			continue
		}
		if slices.ContainsFunc(strings.Split(p, "/"), dotGitSegment) {
			problems = append(problems, TreeProblem{Path: p, Problem: "is inside a .git directory"})
		}
	}
	if files > maxCopyFiles || total > maxCopyBytes {
		problems = append(problems, TreeProblem{Problem: fmt.Sprintf("the tree holds %d files and %d bytes, more than the %d files or %d MiB a copy of a pack takes", files, total, maxCopyFiles, maxCopyBytes>>20)})
	}
	return problems
}

// dotGitSegment reports whether seg, a component of a path, names .git on
// some file system: in any case, with the trailing dots and spaces Windows
// drops, as an NTFS stream (.git:...), as its 8.3 short name (git~N), or
// with the code points HFS+ ignores.
func dotGitSegment(seg string) bool {
	s := strings.Map(func(r rune) rune {
		if hfsIgnorable(r) {
			return -1
		}
		return r
	}, seg)
	s = strings.ToLower(strings.TrimRight(s, ". "))
	if s == ".git" || strings.HasPrefix(s, ".git:") {
		return true
	}
	n, ok := strings.CutPrefix(s, "git~")
	return ok && n != "" && strings.Trim(n, "0123456789") == ""
}

// hfsIgnorable reports whether HFS+ ignores r in a file name.
func hfsIgnorable(r rune) bool {
	return r >= 0x200c && r <= 0x200f || r >= 0x202a && r <= 0x202e || r >= 0x206a && r <= 0x206f || r == 0xfeff
}
