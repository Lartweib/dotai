package plan_test

import (
	"testing"

	"github.com/lartweib/dotai/internal/plan"
)

const (
	beginSite = "# >>> dotai /work/site: managed by dotai, rewritten by each apply"
	endSite   = "# <<< dotai /work/site"
)

func TestExcludeBlockIsWrittenWholeAndRemovedExactly(t *testing.T) {
	const project = "/work/site"
	before := "# git ls-files --others --exclude-from=.git/info/exclude\n*.log\n"
	out := plan.WithExcludeBlock([]byte(before), project, []string{"/.dotai/", "/.claude/rules/web.md"})
	want := before + beginSite + "\n/.dotai/\n/.claude/rules/web.md\n" + endSite + "\n"
	if string(out) != want {
		t.Fatalf("WithExcludeBlock() = %q, want %q", out, want)
	}
	edited := string(out) + "build/\n"
	again := plan.WithExcludeBlock([]byte(edited), project, []string{"/.dotai/"})
	if string(again) != before+beginSite+"\n/.dotai/\n"+endSite+"\nbuild/\n" {
		t.Errorf("WithExcludeBlock() again = %q, want the block rewritten in place and the rest kept", again)
	}
	if got := plan.WithoutExcludeBlock(out, project); string(got) != before {
		t.Errorf("WithoutExcludeBlock() = %q, want %q", got, before)
	}
	if got := plan.WithoutExcludeBlock([]byte(before), project); string(got) != before {
		t.Errorf("WithoutExcludeBlock() without a block = %q, want it unchanged", got)
	}
	if got := plan.WithExcludeBlock(out, project, nil); string(got) != before {
		t.Errorf("WithExcludeBlock() without entries = %q, want the block removed", got)
	}
	crlf := plan.WithExcludeBlock([]byte("a\r\n"), project, []string{"/.dotai/"})
	if string(crlf) != "a\r\n"+beginSite+"\r\n/.dotai/\r\n"+endSite+"\r\n" {
		t.Errorf("CRLF file = %q, want its line endings", crlf)
	}
	if got := plan.WithoutExcludeBlock(crlf, project); string(got) != "a\r\n" {
		t.Errorf("CRLF removal = %q", got)
	}
}

// TestExcludeBlockKeepsTheLineBreakItAddedWithTheFile checks that the line
// break dotai adds to a last line without one is recorded in the file, so
// that removing the blocks of two worktrees in either order gives back the
// file byte for byte, and a line of the user's after a block keeps it.
func TestExcludeBlockKeepsTheLineBreakItAddedWithTheFile(t *testing.T) {
	const before = "*.log"
	a := plan.WithExcludeBlock([]byte(before), "/work/a", []string{"/.dotai/"})
	ab := plan.WithExcludeBlock(a, "/work/b", []string{"/.dotai/"})
	if string(a)[:len(before)+1] != before+"\n" {
		t.Fatalf("WithExcludeBlock() = %q, want a line break after the user's last line", a)
	}
	for _, order := range [][]string{{"/work/a", "/work/b"}, {"/work/b", "/work/a"}} {
		got := plan.WithoutExcludeBlock(plan.WithoutExcludeBlock(ab, order[0]), order[1])
		if string(got) != before {
			t.Errorf("removing %v = %q, want %q", order, got, before)
		}
	}
	rewritten := plan.WithExcludeBlock(a, "/work/a", []string{"/.dotai/", "/x"})
	if got := plan.WithoutExcludeBlock(rewritten, "/work/a"); string(got) != before {
		t.Errorf("rewritten block removed = %q, want %q", got, before)
	}
	user := string(a) + "build/"
	if got := plan.WithoutExcludeBlock([]byte(user), "/work/a"); string(got) != before+"\nbuild/" {
		t.Errorf("removal before a line of the user's = %q, want the line break kept", got)
	}
}
