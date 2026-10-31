package app

import (
	"errors"
	"fmt"
	"path/filepath"
	"runtime"
	"slices"
	"strings"

	"github.com/lartweib/dotai/internal/plan"
	"github.com/lartweib/dotai/internal/state"
)

// The links to shared skills (decision 0036) sit where a skill directory
// of the pack can sit too, so one may replace the other between two
// installs or restores. The operations below a link's target and the
// link's own then have to run in an order the target allows: the files of
// the directory go before the link is made there, and the link goes
// before the files are written there.

// foldCase is whether paths are compared without case, as the file
// systems of this OS usually do.
var foldCase = plan.FoldsCase(runtime.GOOS)

// realDirAt reports an error of inspectTarget for a link target that is a
// real directory, which a plan that removes what it holds may still turn
// into a link (checkLinkDirs).
func realDirAt(target string, err error) bool {
	nr, ok := errors.AsType[*NotRegularError](err)
	return ok && nr.Link && nr.Path == target && nr.Mode.IsDir()
}

// LinkDirError is a real directory where a plan makes a link that holds
// File, which the plan does not remove: a file dotai did not install, such
// as a cache a harness wrote into the pack's skill directory, or a link.
type LinkDirError struct {
	Dir, File string
}

func (e *LinkDirError) Error() string {
	return fmt.Sprintf("%s holds %s, which dotai did not install, where the link to the shared skill goes; dotai does not remove what it did not write: "+
		"move it away, then run again", e.Dir, e.File)
}

// checkLinkDirs refuses, with a *LinkDirError, a link target in dirs, the
// link targets that are real directories, that ops make a link while it
// holds anything they do not remove, found without following a link.
// Every other one is taken: the ops below it come first (orderLinks), and
// the directory, empty once they are done, gives way to the link.
func checkLinkDirs(d installFS, ops []plan.Op, dirs map[string]bool) error {
	for _, op := range ops {
		if !dirs[op.Target] || op.Link == "" {
			continue
		}
		files, err := d.Files(op.Target)
		if err != nil {
			return err
		}
		for _, f := range files {
			name := filepath.Join(op.Target, filepath.FromSlash(f))
			if !slices.ContainsFunc(ops, func(o plan.Op) bool { return o.Action == plan.Remove && samePath(o.Target, name) }) {
				return &LinkDirError{Dir: op.Target, File: name}
			}
		}
	}
	return nil
}

// pendingRemove reports whether the journal of s, if any, removes the link
// at target: an install killed after it did may have written below it.
func pendingRemove(s *state.State, target string) bool {
	return s != nil && s.Pending != nil && slices.ContainsFunc(s.Pending.Ops, func(op state.PendingOp) bool {
		return op.Target == target && op.After == nil && op.Before != nil && op.Before.Link != ""
	})
}

// orderLinks gives the order to carry out ops in: as they are, except that
// an op that removes a link comes before the ops below its target, and one
// that makes a link after them. Each entry is an index into ops.
func orderLinks(ops []plan.Op) []int {
	order := make([]int, len(ops))
	for i := range order {
		order[i] = i
	}
	for i, op := range ops {
		if !isLink(op) {
			continue
		}
		at := slices.Index(order, i)
		isBelow := func(j int) bool { return below(op.Target, ops[j].Target) }
		if op.Link == "" { // it removes a link
			if first := slices.IndexFunc(order, isBelow); first >= 0 && first < at {
				order = slices.Insert(slices.Delete(order, at, at+1), first, i)
			}
			continue
		}
		if last := lastIndex(order, isBelow); last > at {
			order = slices.Insert(slices.Delete(order, at, at+1), last, i)
		}
	}
	return order
}

func lastIndex(s []int, f func(int) bool) int {
	for i := len(s) - 1; i >= 0; i-- {
		if f(s[i]) {
			return i
		}
	}
	return -1
}

// below reports whether target is inside the directory dir, without case
// where the OS folds it.
func below(dir, target string) bool {
	prefix := dir + string(filepath.Separator)
	if foldCase {
		return len(target) > len(prefix) && strings.EqualFold(target[:len(prefix)], prefix)
	}
	return strings.HasPrefix(target, prefix)
}

// samePath reports whether a and b name one path, without case where the
// OS folds it.
func samePath(a, b string) bool { return a == b || foldCase && strings.EqualFold(a, b) }

// permute is s in order.
func permute[T any](s []T, order []int) []T {
	out := make([]T, len(s))
	for i, j := range order {
		out[i] = s[j]
	}
	return out
}
