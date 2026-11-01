package app

import (
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"slices"
	"strings"

	"github.com/lartweib/dotai/internal/harness"
	"github.com/lartweib/dotai/internal/plan"
	"github.com/lartweib/dotai/internal/proc"
)

// A replace install wipes what the user and other tools put in each
// harness's own configuration (decision 0038), so before it writes it is
// refused while a harness it resets is running, and it needs the user's
// consent whenever it wipes or overwrites anything dotai does not own.

// RunningHarness is a process that shows a harness running.
type RunningHarness struct {
	Harness string `json:"harness"`
	PID     int    `json:"pid"`
	Name    string `json:"name"`
}

// RunningError is a replace install, or the restore of one, refused while
// a harness it writes into is running, or when dotai could not list the
// processes (Err) to tell.
type RunningError struct {
	Operation string // install or restore
	Running   []RunningHarness
	Harnesses []string // the harnesses checked
	Err       error
}

func (e *RunningError) Error() string {
	if e.Err != nil {
		return fmt.Sprintf("dotai could not check whether %s is running (%v), and a replace %s must not write under a running harness: "+
			"close it, then run again with --ignore-running", strings.Join(e.Harnesses, ", "), e.Err, e.Operation)
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
	verb := "are"
	if len(items) == 1 {
		verb = "is"
	}
	return fmt.Sprintf("%s %s running, and the replace %s would rewrite its configuration under it; nothing was written: "+
		"close it, then run again; --ignore-running skips this check when dotai mistakes another program for a harness",
		strings.Join(items, ", "), verb, e.Operation)
}

func (e *RunningError) Unwrap() error { return e.Err }

// running lists the processes of h that show one of ids running, besides
// dotai itself. A host that lists no processes, as in tests, finds none.
func running(h host, operation string, ids []string) error {
	if h.processes == nil || len(ids) == 0 {
		return nil
	}
	adapters, err := harness.Lookup(ids)
	if err != nil {
		return err
	}
	procs, err := h.processes()
	if err != nil {
		return &RunningError{Operation: operation, Harnesses: ids, Err: err}
	}
	var found []RunningHarness
	for _, a := range adapters {
		for _, p := range procs {
			if p.PID != h.self && a.Real.RunsAs(p.Name, h.goos == "windows") {
				found = append(found, RunningHarness{Harness: a.ID, PID: p.PID, Name: p.Name})
			}
		}
	}
	if len(found) > 0 {
		return &RunningError{Operation: operation, Running: found, Harnesses: ids}
	}
	return nil
}

// ConsentError is a replace install that wipes or overwrites what dotai
// does not own, and was not allowed to: Plan is its plan, with Entries
// wiped entries that hold something, Keys keys of split files and Files
// files replaced that dotai does not own.
type ConsentError struct {
	Plan                 InstallPlan
	Entries, Keys, Files int
}

func (e *ConsentError) Error() string {
	var what []string
	if e.Entries > 0 {
		what = append(what, plural(e.Entries, "entry", "entries")+" of the harnesses' own configuration")
	}
	if e.Keys > 0 {
		what = append(what, plural(e.Keys, "key", "keys")+" of ~/.claude.json")
	}
	if e.Files > 0 {
		what = append(what, plural(e.Files, "file", "files")+" it would overwrite")
	}
	return "the replace install wipes " + strings.Join(what, ", ") + " that dotai does not own, " +
		"into its backup, and nothing was written: run it again with --yes to wipe them, after reading the plan"
}

func plural(n int, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return fmt.Sprintf("%d %s", n, many)
}

// foreign counts what the replace plan p wipes or overwrites that dotai
// does not own: each wiped entry that holds a file or a link, found
// without following one, each key of a split file, and each file it
// replaces that the previous install did not own. A wiped directory that
// holds only empty directories holds nothing. An isolated plan has none.
func foreign(d installFS, p planned) (entries, keys, files int, err error) {
	if p.plan.Mode != ModeReplace {
		return 0, 0, 0, nil
	}
	for _, w := range p.plan.Wipes {
		full, err := holdsSomething(d, w.Path, w.Kind)
		if err != nil {
			return 0, 0, 0, err
		}
		if full {
			entries++
		}
	}
	for _, op := range p.ops {
		if op.Action == plan.Replace && !ownedAt(ownedTargets(p.prev), op.Target) {
			files++
		}
	}
	return entries, len(p.plan.WipeKeys), files, nil
}

// holdsSomething reports whether the entry name of kind holds a file or a
// link at or below it.
func holdsSomething(d installFS, name, kind string) (bool, error) {
	if kind != KindDir {
		return true, nil
	}
	names, err := d.List(name)
	if err != nil {
		return false, err
	}
	for _, n := range names {
		child := filepath.Join(name, n)
		mode, err := d.Lstat(child)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return false, err
		}
		if full, err := holdsSomething(d, child, entryKind(mode)); err != nil || full {
			return full, err
		}
	}
	return false, nil
}

// replaceHarnesses are the harnesses whose own configuration the restore
// res writes into: those of its wipes and keys, and those of its ops in a
// harness's own directory.
func replaceHarnesses(home string, res RestoreResult) []string {
	var ids []string
	add := func(id string) {
		if id != "" && !slices.Contains(ids, id) {
			ids = append(ids, id)
		}
	}
	for _, w := range res.Wipes {
		add(w.Harness)
	}
	for _, k := range res.WipeKeys {
		add(k.Harness)
	}
	isolated := filepath.Join(home, filepath.FromSlash(harness.IsolatedDir))
	tools := filepath.Join(home, dotaiDir)
	for _, op := range res.Ops {
		if !below(isolated, op.Target) && !below(tools, op.Target) {
			add(op.Harness)
		}
	}
	return ids
}

// processList is how the host lists its processes.
type processList = func() ([]proc.Process, error)
