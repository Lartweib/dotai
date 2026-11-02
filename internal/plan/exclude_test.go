package plan_test

import (
	"testing"

	"github.com/lartweib/dotai/internal/plan"
)

func TestExcludeBlockIsWrittenWholeAndRemovedExactly(t *testing.T) {
	const project = "/work/site"
	before := "# git ls-files --others --exclude-from=.git/info/exclude\n*.log"
	out, newline := plan.WithExcludeBlock([]byte(before), project, []string{"/.dotai/", "/.claude/rules/web.md"})
	want := before + "\n# >>> dotai /work/site: managed by dotai, rewritten by each apply\n/.dotai/\n/.claude/rules/web.md\n# <<< dotai /work/site\n"
	if string(out) != want || !newline {
		t.Fatalf("WithExcludeBlock() = %q, %v, want %q, true", out, newline, want)
	}
	edited := string(out) + "build/\n"
	again, nl := plan.WithExcludeBlock([]byte(edited), project, []string{"/.dotai/"})
	if string(again) != before+"\n# >>> dotai /work/site: managed by dotai, rewritten by each apply\n/.dotai/\n# <<< dotai /work/site\nbuild/\n" || nl {
		t.Errorf("WithExcludeBlock() again = %q, %v, want the block rewritten in place and the rest kept", again, nl)
	}
	other, _ := plan.WithExcludeBlock(again, "/work/other", []string{"/.dotai/"})
	if got := plan.WithoutExcludeBlock(other, "/work/other", false); string(got) != string(again) {
		t.Errorf("another worktree's block: removal = %q, want %q", got, again)
	}
	if got := plan.WithoutExcludeBlock(out, project, true); string(got) != before {
		t.Errorf("WithoutExcludeBlock() = %q, want %q", got, before)
	}
	if got := plan.WithoutExcludeBlock([]byte(before), project, true); string(got) != before {
		t.Errorf("WithoutExcludeBlock() without a block = %q, want it unchanged", got)
	}
	crlf, _ := plan.WithExcludeBlock([]byte("a\r\n"), project, []string{"/.dotai/"})
	if string(crlf) != "a\r\n# >>> dotai /work/site: managed by dotai, rewritten by each apply\r\n/.dotai/\r\n# <<< dotai /work/site\r\n" {
		t.Errorf("CRLF file = %q, want its line endings", crlf)
	}
	if got := plan.WithoutExcludeBlock(crlf, project, false); string(got) != "a\r\n" {
		t.Errorf("CRLF removal = %q", got)
	}
}
