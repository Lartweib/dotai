package cli

import (
	"fmt"
	"io"
	"os"
	"time"
	"unicode/utf8"

	"golang.org/x/term"

	"github.com/lartweib/dotai/internal/app"
)

// stepPrinter tells a person, on stderr, how far an install or a restore
// is: one line per phase and a running count of what it moves or writes,
// so that a wipe of gigabytes can be told from a hang. On a terminal the
// count is rewritten in place on one line; otherwise each line is
// printed, and the phases with many small steps are spaced out in time,
// not to flood a log.
type stepPrinter struct {
	w     io.Writer
	tty   bool
	width int
	now   func() time.Time

	phase    app.Phase
	started  bool      // some step was printed
	open     bool      // the last line is an in-place one, not ended yet
	lastLine time.Time // the last line printed outside a terminal
}

// quietEvery is how long outside a terminal the steps of a phase of many
// small steps, backup and write, go without a line.
const quietEvery = time.Second

// newStepPrinter is the printer of env's stderr for an operation, or nil
// with --json, which prints nothing here.
func newStepPrinter(env Env, jsonOut bool) *stepPrinter {
	if jsonOut {
		return nil
	}
	p := &stepPrinter{w: env.Stderr, width: 80, now: time.Now}
	if f, ok := env.Stderr.(*os.File); ok && os.Getenv("TERM") != "dumb" && isTerminal(f) {
		p.tty = true
		if w, _, err := term.GetSize(int(f.Fd())); err == nil && w > 20 {
			p.width = w
		}
	}
	return p
}

// verbs say what each phase is doing, in the present.
var verbs = map[app.Phase]string{
	app.PhaseBackup:   "backing up",
	app.PhaseWipe:     "wiping",
	app.PhaseRecord:   "recording",
	app.PhaseWrite:    "writing",
	app.PhaseRestore:  "restoring",
	app.PhaseRollback: "rolling back",
}

// report prints a step. It is the function an operation gets in its
// context (app.WithSteps); it is nil-safe for the printer of --json.
func (p *stepPrinter) report(s app.Step) {
	if p == nil {
		return
	}
	text := verbs[s.Phase]
	if s.Total > 0 && s.N > 0 {
		text += fmt.Sprintf(" %d/%d", s.N, s.Total)
	}
	if s.Item != "" {
		text += " " + termSafe(s.Item)
	} else if s.Total == 0 {
		text += "..."
	}
	changed := s.Phase != p.phase || !p.started
	p.phase, p.started = s.Phase, true
	if p.tty {
		p.inPlace(text, changed)
		return
	}
	if s.N == 0 && !changed {
		return // the same phase announced again
	}
	slow := s.Phase == app.PhaseWipe || s.Phase == app.PhaseRecord || s.Phase == app.PhaseRestore || s.Phase == app.PhaseRollback
	last := s.N == s.Total
	if changed || slow || last || p.now().Sub(p.lastLine) >= quietEvery {
		fmt.Fprintln(p.w, text)
		p.lastLine = p.now()
	}
}

// inPlace rewrites the current line with text, cut to the width of the
// terminal; a new phase starts a new line.
func (p *stepPrinter) inPlace(text string, newPhase bool) {
	if p.open && newPhase {
		fmt.Fprintln(p.w)
	}
	if utf8.RuneCountInString(text) > p.width-1 {
		r := []rune(text)
		text = "..." + string(r[len(r)-(p.width-4):])
	}
	fmt.Fprintf(p.w, "\r\x1b[2K%s", text)
	p.open = true
}

// finish ends the printing: it ends an in-place line and, when ok and some
// step was printed, says "done".
func (p *stepPrinter) finish(ok bool) {
	if p == nil || !p.started {
		return
	}
	if p.open {
		fmt.Fprintln(p.w)
		p.open = false
	}
	if ok {
		fmt.Fprintln(p.w, "done")
	}
}
