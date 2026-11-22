package app

import (
	"fmt"
	"path/filepath"
	"slices"
	"strings"

	"github.com/lartweib/dotai/internal/harness"
	"github.com/lartweib/dotai/internal/plan"
)

// The running-harness check (decisions 0038 and 0053). A replace install
// and the restore of one refuse while a harness runs when they would wipe,
// or put back, its configuration whole, and, for a harness they do not
// wipe, only when they write a file that harness rewrites while it runs
// (harness.Real.LiveWritten). Anything else the harness reads when it
// starts, so it picks the change up in a new session.

// CodeHarnessRunningUpdate warns that a harness runs while the install
// updates files it does not rewrite while it runs: it blocks nothing, and
// the harness picks the changes up in new sessions. Harness is the harness.
const CodeHarnessRunningUpdate = "harness-running-update"

// RunningHarness is a process that shows a harness running.
type RunningHarness struct {
	Harness string `json:"harness"`
	PID     int    `json:"pid"`
	Name    string `json:"name"`
}

// RunningFile is a file that a running harness rewrites and that an
// install or restore would write (decision 0053).
type RunningFile struct {
	Harness string `json:"harness"`
	Path    string `json:"path"`
}

// RunningError is a replace install, or the restore of one, refused while
// a harness it writes into is running, or when dotai could not list the
// processes (Err) to tell. A harness it wipes, or puts back whole, is
// refused whole; one it does not is refused only for the files it rewrites
// while it runs that the operation writes: Files, which name the harness
// and the file. Harnesses are those refused, or all those checked when
// Running and Files are empty.
type RunningError struct {
	Operation string // install or restore
	Running   []RunningHarness
	Harnesses []string
	Files     []RunningFile
	Err       error
}

func (e *RunningError) Error() string {
	what, config := "the replace install would rewrite", true
	if e.Operation == "restore" {
		what = "the restore would put back"
	}
	// Only files, when every harness it refuses for is refused for one.
	if len(e.Files) > 0 && e.onlyFiles() {
		n := len(e.Harnesses)
		if e.Err == nil {
			n = len(e.Running)
		}
		what = fmt.Sprintf("the %s would write %s, %s while %s", e.Operation, e.fileList(),
			pick(len(e.Files), "a file", "files")+" "+pick(n, "it rewrites", "they rewrite"), pick(n, "it runs", "they run"))
		config = false
	}
	if e.Err != nil {
		cfg := ""
		if config {
			cfg = fmt.Sprintf(" %s configuration while %s", pick(len(e.Harnesses), "its", "their"), pick(len(e.Harnesses), "it runs", "they run"))
		}
		return fmt.Sprintf("dotai could not check whether %s %s running (%v), and %s%s: "+
			"the %s wrote nothing; close %s, then run again with --ignore-running",
			strings.Join(e.Harnesses, ", "), pick(len(e.Harnesses), "is", "are"), e.Err, what, cfg,
			e.Operation, pick(len(e.Harnesses), "it", "them"))
	}
	var items []string
	for _, id := range e.Harnesses {
		var procs []string
		for _, r := range e.Running {
			if r.Harness == id {
				procs = append(procs, fmt.Sprintf("%s %d", r.Name, r.PID))
			}
		}
		if len(procs) > 0 {
			items = append(items, fmt.Sprintf("%s (%s)", id, strings.Join(procs, ", ")))
		}
	}
	n := len(items)
	cfg := ""
	if config {
		cfg = fmt.Sprintf(" %s configuration under %s", pick(n, "its", "their"), pick(n, "it", "them"))
	}
	return fmt.Sprintf("%s %s running, and %s%s; the %s wrote nothing: "+
		"close %s, then run again; --ignore-running skips this check when dotai mistakes another program for a harness",
		strings.Join(items, ", "), pick(n, "is", "are"), what, cfg, e.Operation, pick(n, "it", "them"))
}

// onlyFiles reports whether every harness it refuses for is refused for a
// file it writes, and none for a wipe.
func (e *RunningError) onlyFiles() bool {
	for _, id := range e.Harnesses {
		if !slices.ContainsFunc(e.Files, func(f RunningFile) bool { return f.Harness == id }) {
			return false
		}
	}
	return true
}

// fileList is the paths of Files, comma separated.
func (e *RunningError) fileList() string {
	var paths []string
	for _, f := range e.Files {
		paths = append(paths, f.Path)
	}
	return strings.Join(paths, ", ")
}

// pick is one for a count of 1, and many for any other.
func pick(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

func (e *RunningError) Unwrap() error { return e.Err }

// listRunning lists the processes of h that show one of ids running,
// besides dotai itself. A host that lists no processes, as in tests, finds
// none.
func listRunning(h host, ids []string) ([]RunningHarness, error) {
	if h.processes == nil || len(ids) == 0 {
		return nil, nil
	}
	adapters, err := harness.Lookup(ids)
	if err != nil {
		return nil, err
	}
	procs, err := h.processes()
	if err != nil {
		return nil, err
	}
	var found []RunningHarness
	for _, a := range adapters {
		for _, p := range procs {
			if p.PID != h.self && runsHarness(a.Real, p, h.goos == "windows") {
				found = append(found, RunningHarness{Harness: a.ID, PID: p.PID, Name: p.Name})
			}
		}
	}
	return found, nil
}

// runCheck is what the processes show about the harnesses an install or a
// restore writes into: found, the processes that show one running, or why
// they could not be listed, err.
type runCheck struct {
	found []RunningHarness
	err   error
}

// checkRunning lists the processes of the harnesses ids for h, once.
func checkRunning(h host, ids []string) runCheck {
	found, err := listRunning(h, ids)
	return runCheck{found: found, err: err}
}

// writeTo is a target an install or a restore writes, of a harness.
type writeTo struct{ harness, target string }

// liveFiles are the targets of writes that rewrite a file of their
// harness's LiveWritten, for the harnesses ids but those in wiped, which
// are refused whole.
func liveFiles(h host, home string, ids, wiped []string, writes []writeTo) []RunningFile {
	var out []RunningFile
	for _, id := range ids {
		if slices.Contains(wiped, id) {
			continue
		}
		adapters, err := harness.Lookup([]string{id})
		if err != nil {
			continue
		}
		for _, w := range writes {
			if w.harness != id {
				continue
			}
			rel, err := filepath.Rel(home, w.target)
			f := RunningFile{Harness: id, Path: w.target}
			if err == nil && adapters[0].Real.WritesLive(filepath.ToSlash(rel), h.goos == "windows") && !slices.Contains(out, f) {
				out = append(out, f)
			}
		}
	}
	return out
}

// guard decides, from what the processes show, whether an operation may go
// on (decision 0053): ids are the harnesses it writes into, wiped those it
// wipes, which it must not do while one runs, and writes the files it
// writes. It gives the *RunningError that refuses it, nil when it may go
// on, the harnesses it goes on for although they run, and risky, those it
// wipes or writes a file of that they rewrite while they run. A harness is
// refused for a wipe, or for such a file; when the processes could not be
// listed, that refuses as a running harness does, and nothing else does.
func guard(h host, home, operation string, ids, wiped []string, writes []writeTo, c runCheck) (refused *RunningError, allowed []RunningHarness, risky []string) {
	files := liveFiles(h, home, ids, wiped, writes)
	for _, id := range ids {
		if slices.Contains(wiped, id) || slices.ContainsFunc(files, func(f RunningFile) bool { return f.Harness == id }) {
			risky = append(risky, id)
		}
	}
	if c.err != nil {
		if len(risky) == 0 {
			return nil, nil, nil
		}
		return &RunningError{Operation: operation, Harnesses: risky, Files: filesOf(files, risky), Err: c.err}, nil, risky
	}
	var blocking []RunningHarness
	for _, r := range c.found {
		if slices.Contains(risky, r.Harness) {
			blocking = append(blocking, r)
		} else {
			allowed = append(allowed, r)
		}
	}
	if len(blocking) == 0 {
		return nil, allowed, risky
	}
	var stopped []string
	for _, id := range risky {
		if slices.ContainsFunc(blocking, func(r RunningHarness) bool { return r.Harness == id }) {
			stopped = append(stopped, id)
		}
	}
	return &RunningError{Operation: operation, Running: blocking, Harnesses: stopped, Files: filesOf(files, stopped)}, nil, risky
}

// filesOf are the files of all that belong to the harnesses ids.
func filesOf(all []RunningFile, ids []string) []RunningFile {
	var out []RunningFile
	for _, f := range all {
		if slices.Contains(ids, f.Harness) {
			out = append(out, f)
		}
	}
	return out
}

// installWrites are the targets of ops that an install writes: every
// operation that is not unchanged.
func installWrites(ops []plan.Op) []writeTo {
	var out []writeTo
	for _, op := range ops {
		if op.Action != plan.Unchanged && op.Harness != "" {
			out = append(out, writeTo{op.Harness, op.Target})
		}
	}
	return out
}

// wipedHarnesses are the harnesses of p that the install wipes: those that
// are not a later install, or all of them when it resets (decision 0051).
func (p planned) wipedHarnesses() []string {
	if p.plan.Reset {
		return p.harnesses
	}
	var out []string
	for _, id := range p.harnesses {
		if !slices.Contains(p.plan.Reinstall, id) {
			out = append(out, id)
		}
	}
	return out
}

// guardInstall applies the running-harness check to the replace install p
// with the processes c, as guard does.
func (p planned) guardInstall(h host, home string, c runCheck) (*RunningError, []RunningHarness, []string) {
	return guard(h, home, "install", p.harnesses, p.wipedHarnesses(), installWrites(p.ops), c)
}

// runningWarnings are the warnings, one per harness that runs, of an
// install that goes on while they do: they pick the update up in new
// sessions.
func runningWarnings(running []RunningHarness) []plan.Warning {
	var out []plan.Warning
	for _, r := range running {
		if slices.ContainsFunc(out, func(w plan.Warning) bool { return w.Harness == r.Harness }) {
			continue
		}
		out = append(out, plan.Warning{
			Code: CodeHarnessRunningUpdate, Harness: r.Harness,
			Message: fmt.Sprintf("%s is running (%s %d): this update writes no file it rewrites while it runs, so the install goes on; the running session keeps what it loaded and new sessions pick the changes up",
				r.Harness, r.Name, r.PID),
		})
	}
	return out
}
