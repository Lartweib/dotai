package app

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"path"
	"path/filepath"
	"slices"
	"strings"

	"github.com/lartweib/dotai/internal/backup"
	"github.com/lartweib/dotai/internal/harness"
)

// A replace install moves each entry it wipes into its backup by a rename
// inside the home directory (decision 0038). A rename cannot cross
// volumes, as when ~/.dotai or a harness directory is a mount point of its
// own: the move then falls back to a copy into a temporary name next to
// its destination, synced, checked against the source by SHA-256, kind
// and link destination, then renamed into place, and the source, first
// renamed to a temporary name next to it, is deleted last. Links are
// copied as links, never followed.
//
// Each such move is written first to ~/.dotai/crossing.json, its journal,
// so that a move cut short by a kill is settled before the next install or
// restore plans anything: once the copy has its destination's name the
// move is done and the source's leftovers go; before that the copy goes,
// and the source gets its name back.

// crossingFile is the journal of the moves across volumes under way, in
// the dotai directory.
const crossingFile = "crossing.json"

// crossing is a move across volumes under way: From and To are the move's
// source and destination, Temp the copy's name until it is complete, and
// Trash the source's name once the copy is checked; slash paths relative
// to the home directory.
type crossing struct {
	From  string `json:"from"`
	To    string `json:"to"`
	Temp  string `json:"temp"`
	Trash string `json:"trash"`
}

// readCrossings reads the journal of the moves across volumes, nil when
// there is none.
func (r runner) readCrossings() ([]crossing, error) {
	dir := filepath.Join(r.home, dotaiDir)
	if _, err := r.d.Lstat(filepath.Join(dir, crossingFile)); errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	} else if err != nil {
		return nil, err
	}
	data, err := r.d.ReadFile(dir, crossingFile)
	if err != nil {
		return nil, err
	}
	var cs []crossing
	if err := json.Unmarshal(data, &cs); err != nil {
		return nil, fmt.Errorf("%s: %w", filepath.Join(dir, crossingFile), err)
	}
	for _, c := range cs {
		if err := r.checkCrossing(c); err != nil {
			abs := func(rel string) string { return filepath.Join(r.home, filepath.FromSlash(rel)) }
			return nil, fmt.Errorf("%s is the journal of a move across volumes that was cut short, but it names a move dotai does not make (%v): "+
				"from %s, to %s, temp %s, trash %s; dotai deleted nothing: inspect those paths, put back by hand what belongs elsewhere, then delete %s",
				filepath.Join(dir, crossingFile), err, abs(c.From), abs(c.To), abs(c.Temp), abs(c.Trash), filepath.Join(dir, crossingFile))
		}
	}
	return cs, nil
}

// checkCrossing refuses a move that dotai does not make: one end must be
// an entry a replace install wipes, inside an adapter's area or one of its
// leak entries, and the other a path inside a backup, either way round;
// Temp must be a temporary name next to To, and Trash one next to From,
// as tempName gives them. Every path must be a clean slash path inside the
// home directory.
func (r runner) checkCrossing(c crossing) error {
	for _, p := range []string{c.From, c.To, c.Temp, c.Trash} {
		if p == "" || !filepath.IsLocal(filepath.FromSlash(p)) || path.Clean(p) != p {
			return fmt.Errorf("%q is not a clean path inside the home directory", p)
		}
	}
	inBackup := func(p string) bool {
		rest, ok := strings.CutPrefix(p, dotaiDir+"/"+backupsDir+"/")
		id, below, _ := strings.Cut(rest, "/")
		_, valid := backup.ParseID(id)
		return ok && valid && below != ""
	}
	if !(inBackup(c.From) && r.wiped(c.To) || inBackup(c.To) && r.wiped(c.From)) {
		return errors.New("one end must be an entry a replace install wipes and the other a path inside a backup")
	}
	if path.Dir(c.Temp) != path.Dir(c.To) || !isTempOf(path.Base(c.Temp), path.Base(c.To)) {
		return errors.New("temp is not a temporary name next to to")
	}
	if path.Dir(c.Trash) != path.Dir(c.From) || !isTempOf(path.Base(c.Trash), path.Base(c.From)) {
		return errors.New("trash is not a temporary name next to from")
	}
	return nil
}

// wiped reports whether the slash path rel is an entry a replace install
// wipes, or a path inside one: below an area of an adapter, or one of its
// leak entries or below it.
func (r runner) wiped(rel string) bool {
	p := filepath.Join(r.home, filepath.FromSlash(rel))
	return slices.ContainsFunc(harness.All(), func(a harness.Adapter) bool {
		return slices.ContainsFunc(a.Real.Areas, func(ar harness.Area) bool { return below(filepath.Join(r.home, filepath.FromSlash(ar.Dir)), p) }) ||
			slices.ContainsFunc(a.Real.Leaks, func(l harness.LeakEntry) bool {
				leak := filepath.Join(r.home, filepath.FromSlash(l.Path))
				return samePath(leak, p) || below(leak, p)
			})
	})
}

// writeCrossings writes the journal cs, or removes it when cs is empty.
func (r runner) writeCrossings(cs []crossing) error {
	dir := filepath.Join(r.home, dotaiDir)
	if len(cs) == 0 {
		if ok, err := r.exists(dotaiDir + "/" + crossingFile); err != nil || !ok {
			return err
		}
		return r.d.RemoveIn(dir, crossingFile)
	}
	data, err := json.MarshalIndent(cs, "", "  ")
	if err != nil {
		return err
	}
	return r.d.WriteIn(dir, crossingFile, append(data, '\n'), 0o600)
}

// exists reports whether the slash path rel under the home directory is
// there, a link included, never followed.
func (r runner) exists(rel string) (bool, error) {
	_, err := r.d.Lstat(filepath.Join(r.home, filepath.FromSlash(rel)))
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	return err == nil, err
}

// settleCrossing settles the move c, cut short at any step: when its copy
// has its destination's name, the move is done and the source's leftovers
// under Trash go; otherwise the partial copy goes and the source gets its
// name back. Nothing at From or To is ever deleted.
func (r runner) settleCrossing(c crossing) error {
	done, err := r.exists(c.To)
	if err != nil {
		return err
	}
	if done {
		return r.removeAllIn(c.Trash)
	}
	if err := r.removeAllIn(c.Temp); err != nil {
		return err
	}
	trashed, err := r.exists(c.Trash)
	if err != nil || !trashed {
		return err
	}
	if back, err := r.exists(c.From); err != nil {
		return err
	} else if back {
		return fmt.Errorf("%s holds what was at %s, where something else stands now: move it back by hand", filepath.Join(r.home, filepath.FromSlash(c.Trash)), filepath.Join(r.home, filepath.FromSlash(c.From)))
	}
	return r.d.MoveIn(r.home, c.Trash, c.From)
}

// removeAllIn removes the slash path rel under the home directory and
// everything in it, never following a link; a missing one is no error.
func (r runner) removeAllIn(rel string) error {
	if ok, err := r.exists(rel); err != nil || !ok {
		return err
	}
	return r.d.RemoveAllIn(r.home, rel)
}

// settleCrossings settles, under the lock, the moves across volumes that
// a killed install or restore left in the journal, before the next one
// plans anything. A home without the journal takes no lock.
func settleCrossings(r runner) error {
	if cs, err := r.readCrossings(); err != nil || cs == nil {
		return err
	}
	var warnings []string
	return r.locked(&warnings, func() error {
		cs, err := r.readCrossings()
		if err != nil {
			return err
		}
		for len(cs) > 0 {
			if err := r.settleCrossing(cs[0]); err != nil {
				return fmt.Errorf("a move across volumes that was cut short could not be settled: %w", err)
			}
			cs = cs[1:]
			if err := r.writeCrossings(cs); err != nil {
				return err
			}
		}
		return nil
	})
}

// moveAcross moves the entry at the slash path src to the missing dst,
// both under the home directory, across volumes, as the comment at the
// top of this file describes, and gives a warning about the copy.
func (r runner) moveAcross(src, dst string) (string, error) {
	tmp, err := tempName(dst)
	if err != nil {
		return "", err
	}
	trash, err := tempName(src)
	if err != nil {
		return "", err
	}
	c := crossing{From: src, To: dst, Temp: tmp, Trash: trash}
	cs, err := r.readCrossings()
	if err != nil {
		return "", err
	}
	if err := r.writeCrossings(append(cs, c)); err != nil {
		return "", err
	}
	abs := func(rel string) string { return filepath.Join(r.home, filepath.FromSlash(rel)) }
	size, err := r.d.CopyIn(r.home, src, tmp)
	if err == nil {
		err = r.sameTree(abs(src), abs(tmp))
	}
	if err == nil {
		err = r.d.MoveIn(r.home, src, trash) // next to it, on its volume
	}
	if err == nil {
		err = r.d.MoveIn(r.home, tmp, dst)
	}
	if err != nil {
		err = fmt.Errorf("%s could not be copied across volumes to %s: %w", abs(src), abs(dst), err)
		if sErr := r.settleCrossing(c); sErr != nil {
			return "", errors.Join(err, sErr) // the journal keeps it for the next run
		}
		return "", errors.Join(err, r.dropCrossing(c))
	}
	warning := fmt.Sprintf("%s is on another volume than %s, so it was copied there (%s) and then deleted, instead of moved", abs(src), abs(dst), byteSize(size))
	if err := r.removeAllIn(trash); err != nil {
		// The move is done; the journal keeps the leftovers for the next run.
		return warning + fmt.Sprintf("; its leftovers in %s could not be deleted (%v), and the next install or restore deletes them", abs(trash), err), nil
	}
	return warning, r.dropCrossing(c)
}

// dropCrossing removes c from the journal.
func (r runner) dropCrossing(c crossing) error {
	cs, err := r.readCrossings()
	if err != nil {
		return err
	}
	return r.writeCrossings(slices.DeleteFunc(cs, func(x crossing) bool { return x == c }))
}

// sameTree checks that the copy at b holds what a holds: the same paths of
// the same kinds, the same content and exec bit, where the file system
// keeps one, and the same link destinations.
func (r runner) sameTree(a, b string) error {
	want, err := r.items(r.ctx, a)
	if err != nil {
		return err
	}
	ok, err := r.holds(r.ctx, b, want)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("the copy does not hold what %s holds", a)
	}
	return nil
}

// byteSize is n bytes as people read it.
func byteSize(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for m := n / unit; m >= unit; m /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(n)/float64(div), "KMGTPE"[exp])
}
