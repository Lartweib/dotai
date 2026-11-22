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

// runsHarness reports whether p shows the harness of r running: its
// executable's name, or the name it was started as, is one of r's
// Processes, or it is node running one of r's NodePackages, as an npm
// install does.
func runsHarness(r harness.Real, p proc.Process, fold bool) bool {
	node := false
	for _, name := range []string{p.Name, p.Argv0} {
		if name == "" {
			continue
		}
		if r.RunsAs(name, fold) {
			return true
		}
		n := name
		if fold {
			n = strings.TrimSuffix(strings.ToLower(n), ".exe")
		}
		node = node || n == "node"
	}
	return node && r.RunsUnderNode(p.Args)
}

// runningReminder asks to close every session of ids, which the
// running-harness check found none of: it finds a harness by its process
// name, and misses one started another way.
func runningReminder(ids []string) string {
	if len(ids) == 0 {
		return ""
	}
	return fmt.Sprintf("close every session of %s before going on: dotai found none running, but it finds them by process name and misses some launchers, such as an npm install on Windows",
		strings.Join(ids, ", "))
}

// ConsentError is an install whose plan asks to confirm something, and
// that was not allowed to: Plan is its plan, with Entries wiped entries
// that hold something, Keys keys of split files and Files files replaced
// that dotai does not own, in a replace install, and Templates the files
// of the pack's template it installs unchanged.
type ConsentError struct {
	Plan                 InstallPlan
	Entries, Keys, Files int
	Templates            []string
}

func (e *ConsentError) Error() string {
	var parts []string
	if e.Entries+e.Keys+e.Files > 0 || len(e.Templates) == 0 {
		parts = append(parts, fmt.Sprintf("the replace install would move into its backup what dotai does not own: %s it wipes, %s it overwrites and %s of ~/.claude.json it deletes",
			plural(e.Entries, "entry", "entries"), plural(e.Files, "file", "files"), plural(e.Keys, "key", "keys")))
	}
	if t := templateConsent("install", e.Templates); t != "" {
		parts = append(parts, t)
	}
	return strings.Join(parts, "; ") + "; the install wrote nothing: run it again with --yes to accept what the plan asks to confirm, after reading it"
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
	keys = len(p.plan.WipeKeys)
	for _, op := range p.ops {
		// A harness's home file is merged key by key, and the keys of it
		// that are wiped count as keys.
		if op.Action != plan.Replace {
			continue
		}
		if !(op.Settings && homeConfigSource(op.Source)) {
			if !ownedAt(ownedTargets(p.prev), op.Target) {
				files++
			}
			continue
		}
		// A reset wipes the keys it would overwrite, which asked consent as
		// keys; a later install that only updates (decision 0051) wipes none,
		// so each key it replaces that the previous install did not own is
		// one the user's.
		owned := ownedKeys(p.prev)[op.Target]
		for _, c := range op.Changes {
			if c.Action == plan.Replace && !slices.ContainsFunc(owned, func(k []string) bool { return (plan.Key{Path: k}).Name() == c.Key }) {
				keys++
			}
		}
	}
	return entries, keys, files, nil
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
// harness's own directory. wiped are those whose entries or keys it moves
// back, which it must not do while one runs, and writes the files it
// writes there (decision 0053).
func replaceHarnesses(home string, res RestoreResult) (ids, wiped []string, writes []writeTo) {
	add := func(id string) {
		if id != "" && !slices.Contains(ids, id) {
			ids = append(ids, id)
		}
	}
	wipe := func(id string) {
		add(id)
		if id != "" && !slices.Contains(wiped, id) {
			wiped = append(wiped, id)
		}
	}
	for _, w := range res.Wipes {
		wipe(w.Harness)
	}
	for _, k := range res.WipeKeys {
		wipe(k.Harness)
	}
	isolated := filepath.Join(home, filepath.FromSlash(harness.IsolatedDir))
	tools := filepath.Join(home, dotaiDir)
	for _, op := range res.Ops {
		if !below(isolated, op.Target) && !below(tools, op.Target) {
			id := writerOf(home, op.Harness, op.Target)
			add(id)
			writes = append(writes, writeTo{id, op.Target})
		}
	}
	return ids, wiped, writes
}

// processList is how the host lists its processes.
type processList = func() ([]proc.Process, error)

// homeConfigSource reports whether source is the staged HomeConfig of a
// harness (decision 0049).
func homeConfigSource(source string) bool {
	return slices.ContainsFunc(harness.All(), func(a harness.Adapter) bool { return a.HomeConfig != "" && source == a.HomeConfig })
}
