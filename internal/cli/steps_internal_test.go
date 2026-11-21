package cli

import (
	"bytes"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/lartweib/dotai/internal/app"
)

// clock is a time source the test moves by hand.
type clock struct{ t time.Time }

func (c *clock) now() time.Time { return c.t }

// TestStepPrinterPlain checks the lines outside a terminal: each phase, each
// entry of a slow phase, and only now and then the many small writes, with
// the last of each phase and a closing done.
func TestStepPrinterPlain(t *testing.T) {
	var out bytes.Buffer
	c := &clock{t: time.Unix(0, 0)}
	p := &stepPrinter{w: &out, width: 80, now: c.now}
	p.report(app.Step{Phase: app.PhaseBackup})
	for i := 1; i <= 3; i++ {
		p.report(app.Step{Phase: app.PhaseWipe, N: i, Total: 3, Item: fmt.Sprintf("~/.claude/e%d", i)})
	}
	for i := 1; i <= 100; i++ {
		c.t = c.t.Add(10 * time.Millisecond) // a second in all
		p.report(app.Step{Phase: app.PhaseWrite, N: i, Total: 100, Item: fmt.Sprintf("~/f%d", i)})
	}
	p.finish(true)
	want := "backing up...\nwiping 1/3 ~/.claude/e1\nwiping 2/3 ~/.claude/e2\nwiping 3/3 ~/.claude/e3\n" +
		"writing 1/100 ~/f1\nwriting 100/100 ~/f100\ndone\n"
	if out.String() != want {
		t.Errorf("lines =\n%s\nwant\n%s", out.String(), want)
	}
}

// TestStepPrinterOnATerminal checks that a terminal gets one line per
// phase, rewritten in place, cut to its width, and a failure ends the line
// without saying done.
func TestStepPrinterOnATerminal(t *testing.T) {
	var out bytes.Buffer
	p := &stepPrinter{w: &out, tty: true, width: 30, now: time.Now}
	p.report(app.Step{Phase: app.PhaseWipe, N: 1, Total: 2, Item: "~/a"})
	p.report(app.Step{Phase: app.PhaseWipe, N: 2, Total: 2, Item: "~/" + strings.Repeat("long", 20)})
	p.report(app.Step{Phase: app.PhaseWrite, N: 1, Total: 1, Item: "~/b"})
	p.finish(false)
	got := out.String()
	if strings.Count(got, "\n") != 2 || strings.Contains(got, "done") ||
		!strings.Contains(got, "\r\x1b[2Kwiping 1/2 ~/a\r\x1b[2K") || !strings.Contains(got, "\n\r\x1b[2Kwriting 1/1 ~/b\n") {
		t.Errorf("terminal output = %q, want the wipes on one rewritten line, the writes on the next, no done", got)
	}
	for _, line := range strings.Split(got, "\r\x1b[2K") {
		if len([]rune(strings.TrimSpace(line))) >= 30 {
			t.Errorf("line %q is not cut to the width of the terminal", line)
		}
	}
}

// TestStepPrinterIsSilentWithJSON checks that --json has no printer, and
// that a missing printer is safe to call.
func TestStepPrinterIsSilentWithJSON(t *testing.T) {
	var out bytes.Buffer
	p := newStepPrinter(Env{Stderr: &out}, true)
	p.report(app.Step{Phase: app.PhaseWipe, N: 1, Total: 1, Item: "~/a"})
	p.finish(true)
	if p != nil || out.Len() != 0 {
		t.Errorf("printer = %v, output = %q, want none with --json", p, out.String())
	}
	if p := newStepPrinter(Env{Stderr: &out}, false); p == nil || p.tty {
		t.Errorf("printer on a buffer = %+v, want one that is not a terminal", p)
	}
}
