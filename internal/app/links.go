package app

import (
	"errors"
	"path/filepath"
	"runtime"
	"slices"
	"strings"

	"github.com/lartweib/dotai/internal/plan"
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

// checkLinkDirs gives the error of dirs, the link targets that are real
// directories, for one that ops make a link without removing anything
// below it: that directory is not dotai's to replace. A directory the ops
// empty is taken: the link is made once it is gone, and one that still
// holds anything then stops the write as changed.
func checkLinkDirs(ops []plan.Op, dirs map[string]error) error {
	for _, op := range ops {
		err, ok := dirs[op.Target]
		if !ok || op.Link == "" {
			continue
		}
		if !slices.ContainsFunc(ops, func(o plan.Op) bool { return o.Action == plan.Remove && below(op.Target, o.Target) }) {
			return err
		}
	}
	return nil
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

// below reports whether target is inside the directory dir.
func below(dir, target string) bool {
	return strings.HasPrefix(target, dir+string(filepath.Separator))
}

// permute is s in order.
func permute[T any](s []T, order []int) []T {
	out := make([]T, len(s))
	for i, j := range order {
		out[i] = s[j]
	}
	return out
}
