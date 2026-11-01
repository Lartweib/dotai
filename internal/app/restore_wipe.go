package app

import (
	"cmp"
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"slices"
	"strings"

	"github.com/lartweib/dotai/internal/backup"
	"github.com/lartweib/dotai/internal/harness"
	"github.com/lartweib/dotai/internal/plan"
	"github.com/lartweib/dotai/internal/plan/jsonc"
)

// A restore of a replace install moves each entry it wiped back to its
// path, and puts back the keys it deleted from each split file (decision
// 0038). The installs are undone newest first, as their files are: each
// entry of an install goes back once the restore has removed the pack's
// files and put back the entries of the installs after it. Whatever stands
// at its path then, such as a file the user added since in a wiped area,
// is a conflict: with consent it is moved into the restore's own backup,
// under moved/<install backup id>/, before the entry goes back.

// RestoreWipe is an entry a restore moves back from the backup From, to
// its Path, of its Kind. Conflict marks a path that holds something else,
// which the restore moves into its own backup first.
type RestoreWipe struct {
	Harness  string `json:"harness"`
	Path     string `json:"path"`
	Kind     string `json:"kind"`
	From     string `json:"from"`
	Conflict bool   `json:"conflict"`
}

// restoreMove is a move a restore makes: an entry back from the install
// backup at from to its path to, leaving no empty directory below stop,
// the directory of that backup that held it, or, with aside set, the
// entry at from out of the way into the restore's own backup, at the
// slash path file.
type restoreMove struct {
	from, to string
	stop     string
	file     string
	aside    bool
}

// vtree is what stands at and below the paths a restore moves entries
// back to, by absolute path, as it plans each step.
type vtree map[string]backup.Item

// under is every path of v at or below p, in walk order.
func (v vtree) under(p string) []string {
	var out []string
	for k := range v {
		if samePath(k, p) || below(p, k) {
			out = append(out, k)
		}
	}
	slices.SortFunc(out, func(a, b string) int {
		return slices.Compare(strings.Split(a, string(filepath.Separator)), strings.Split(b, string(filepath.Separator)))
	})
	return out
}

// add records items, the items of an entry at p.
func (v vtree) add(p string, items []backup.Item) {
	for _, it := range items {
		name := p
		if it.Path != "" {
			name = filepath.Join(p, filepath.FromSlash(it.Path))
		}
		v[name] = it
	}
}

// remove forgets p and everything below it.
func (v vtree) remove(p string) {
	for _, k := range v.under(p) {
		delete(v, k)
	}
}

// itemsAt are the items of v at and below p, relative to p.
func (v vtree) itemsAt(p string) []backup.Item {
	var out []backup.Item
	for _, k := range v.under(p) {
		it := v[k]
		it.Path = ""
		if rel, err := filepath.Rel(p, k); err == nil && rel != "." {
			it.Path = filepath.ToSlash(rel)
		}
		out = append(out, it)
	}
	return out
}

// layoutsOf are the layouts an operation of mode may write in: a replace
// install also removes the files of the isolated one before it.
func layoutsOf(mode string) []harness.Mode {
	if mode == ModeReplace {
		return []harness.Mode{harness.Replace, harness.Isolated}
	}
	return []harness.Mode{harness.Isolated}
}

// wipedPaths are the entries the installs of chain moved into their
// backups; a restore's own, what it moved out of the way, are left there.
func wipedPaths(chain []undone) []string {
	var out []string
	for _, u := range chain {
		if u.m.Operation != backup.OperationInstall {
			continue
		}
		for _, w := range u.m.Wipes {
			out = append(out, w.Path)
		}
	}
	return out
}

// inWiped reports whether target is at or below one of paths.
func inWiped(paths []string, target string) bool {
	return slices.ContainsFunc(paths, func(p string) bool { return samePath(p, target) || below(p, target) })
}

// planWipes plans, once r.p.ops are planned, the moves that put back the
// entries the installs of chain wiped, and adds them, with the conflicts
// they meet, to res: newest install first, each entry of an install in
// the order it was wiped. An entry that is still in its backup goes back
// to its path once what stands there, after the ops and the entries put
// back before it, is moved out of the way, a conflict. One that is no
// longer in its backup was put back by a restore that was killed, or never
// moved by an install that was: its path must hold it as its backup
// records it. It gives the entries moved out of the way, which the
// restore's backup records.
func (r *restorer) planWipes(res *RestoreResult, chain []undone) ([]backup.Wipe, error) {
	v := vtree{}
	paths := wipedPaths(chain)
	for _, p := range paths {
		if err := r.realParents(p); err != nil {
			return nil, err
		}
		items, err := r.items(r.ctx, p)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, err
		}
		v.add(p, items)
	}
	// What the ops leave: a removed file goes with the directories it
	// leaves empty, up to its root, as the op removes them.
	for _, op := range r.p.ops {
		if !inWiped(paths, op.Target) {
			continue
		}
		if op.Action != plan.Remove {
			v[op.Target] = backup.Item{Kind: KindFile, SHA256: op.After, Exec: op.Exec}
			if op.Link != "" {
				v[op.Target] = backup.Item{Kind: KindLink, Link: op.Link}
			}
			continue
		}
		delete(v, op.Target)
		root, _, err := installRoot(r.home, op.Target, r.layouts...)
		if err != nil {
			return nil, err
		}
		for dir := filepath.Dir(op.Target); below(root, dir); dir = filepath.Dir(dir) {
			if it, ok := v[dir]; !ok || it.Kind != KindDir || len(v.under(dir)) > 1 {
				break
			}
			delete(v, dir)
		}
	}
	var aside []backup.Wipe
	for _, u := range chain {
		if u.m.Operation != backup.OperationInstall {
			continue
		}
		for _, w := range u.m.Wipes {
			from := filepath.Join(u.dir, filepath.FromSlash(w.File))
			if _, err := r.d.Lstat(from); errors.Is(err, fs.ErrNotExist) {
				if !r.sameItems(v.itemsAt(w.Path), w.Items) {
					return nil, fmt.Errorf("the backup %s no longer holds %s, and %s does not hold it either: restore cannot put it back; nothing was written", u.m.ID, w.File, w.Path)
				}
				continue
			} else if err != nil {
				return nil, err
			}
			for dir := filepath.Dir(w.Path); below(r.home, dir); dir = filepath.Dir(dir) {
				if it, ok := v[dir]; ok && it.Kind != KindDir {
					return nil, fmt.Errorf("%s is not a directory once the entries after it are put back, so restore cannot put %s back; nothing was written", dir, w.Path)
				}
			}
			rw := RestoreWipe{Harness: w.Harness, Path: w.Path, Kind: w.Kind, From: u.m.ID}
			if there := v.itemsAt(w.Path); len(there) > 0 {
				file, err := backup.MovedFile(r.home, u.m.ID, w.Path)
				if err != nil {
					return nil, err
				}
				rw.Conflict = true
				aside = append(aside, backup.Wipe{Harness: w.Harness, Path: w.Path, Kind: there[0].Kind, File: file, Items: there})
				r.moves = append(r.moves, restoreMove{from: w.Path, file: file, aside: true})
				rc := RestoreConflict{Target: w.Path, Harness: w.Harness, Backup: u.m.ID}
				if there[0].Kind == KindFile {
					rc.Current = there[0].SHA256
				}
				res.Conflicts = append(res.Conflicts, rc)
				v.remove(w.Path)
			}
			r.moves = append(r.moves, restoreMove{from: from, to: w.Path, stop: filepath.Join(u.dir, backup.FilesDir)})
			v.add(w.Path, w.Items)
			res.Wipes = append(res.Wipes, rw)
		}
	}
	return aside, nil
}

// sameItems reports whether a and b are the same items, leaving the exec
// bit out where the file system has none.
func (r *restorer) sameItems(a, b []backup.Item) bool {
	return slices.EqualFunc(a, b, func(x, y backup.Item) bool {
		if r.p.ignoreExec {
			x.Exec, y.Exec = false, false
		}
		return x == y
	})
}

// alreadyBack gives the function that reports whether target, holding
// seen, is part of an entry an install of chain wiped that is no longer in
// its backup, and holds what that entry held there: a restore that was
// killed moved it back already, or an install that was killed never moved
// it. It is the user's own, which the restore leaves as it is.
func (r *restorer) alreadyBack(chain []undone) (func(target string, seen vfile) bool, error) {
	in := map[string]backup.Item{}
	for _, u := range chain {
		if u.m.Operation != backup.OperationInstall {
			continue
		}
		for _, w := range u.m.Wipes {
			if _, err := r.d.Lstat(filepath.Join(u.dir, filepath.FromSlash(w.File))); err == nil {
				continue
			} else if !errors.Is(err, fs.ErrNotExist) {
				return nil, err
			}
			vtree(in).add(w.Path, w.Items)
		}
	}
	return func(target string, seen vfile) bool {
		it, ok := in[target]
		return ok && seen.exists && it.Kind == KindFile && r.same(seen.cur, it.SHA256, it.Exec)
	}, nil
}

// realParents checks each directory between the home directory and the
// one that holds p: a real directory or missing, never a link through
// which a move would go elsewhere.
func (r *restorer) realParents(p string) error {
	rel, err := filepath.Rel(r.home, filepath.Dir(p))
	if err != nil || !filepath.IsLocal(rel) && rel != "." {
		return fmt.Errorf("%s is not under the home directory %s", p, r.home)
	}
	if rel == "." {
		return nil
	}
	dir := r.home
	for seg := range strings.SplitSeq(rel, string(filepath.Separator)) {
		dir = filepath.Join(dir, seg)
		if err := checkDir(r.d, dir); errors.Is(err, fs.ErrNotExist) {
			return nil
		} else if err != nil {
			return err
		}
	}
	return nil
}

// planSplits plans, from what each split file of chain holds now, the
// write that puts back the keys the installs of chain deleted, newest
// first, and adds the keys and their conflicts to res. A file that holds
// what an install left there gets the bytes it had before back, so that a
// restore with nothing changed since is exact; a file that holds what was
// there before is left; a missing one gets its copy back whole. Otherwise
// each key goes back to the value it had before, every other key stays,
// and a key that holds something else now is a conflict.
func (r *restorer) planSplits(res *RestoreResult, chain []undone) error {
	var targets []string
	for _, u := range chain {
		if u.m.Operation != backup.OperationInstall {
			continue
		}
		for _, s := range u.m.Splits {
			if !slices.Contains(targets, s.Target) {
				targets = append(targets, s.Target)
			}
		}
	}
	for _, target := range targets {
		cur, held, err := r.d.Read(r.ctx, target)
		if errors.Is(err, fs.ErrNotExist) {
			cur, held, err = nil, plan.Current{}, nil
		}
		if err != nil {
			return err
		}
		end, harnessID := cur, ""
		var keys [][]string
		reported := map[string]bool{}
		for _, u := range chain {
			if u.m.Operation != backup.OperationInstall {
				continue
			}
			i := slices.IndexFunc(u.m.Splits, func(s backup.Split) bool { return s.Target == target })
			if i < 0 {
				continue
			}
			s := u.m.Splits[i]
			harnessID = cmp.Or(harnessID, s.Harness)
			prior, err := r.d.ReadFile(u.dir, s.File) // checked when the backup was loaded
			if err != nil {
				return err
			}
			for _, k := range s.Keys {
				if !slices.ContainsFunc(keys, func(p []string) bool { return slices.Equal(p, k) }) {
					res.WipeKeys = append(res.WipeKeys, WipeKey{Harness: s.Harness, File: target, Key: k})
					keys = append(keys, k)
				}
			}
			switch {
			case end == nil, contentHash(end) == s.After.SHA256:
				end = prior
				continue
			case contentHash(end) == s.Before.SHA256:
				continue
			}
			doc, err := jsonc.Parse(end)
			if err != nil {
				if !reported[""] {
					reported[""] = true
					res.Conflicts = append(res.Conflicts, RestoreConflict{Target: target, Harness: s.Harness, Backup: u.m.ID, Expected: s.After.SHA256, Current: contentHash(end)})
				}
				end = prior
				continue
			}
			was, err := jsonc.Parse(prior)
			if err != nil {
				return &CorruptBackupError{ID: u.m.ID, File: s.File, Err: err}
			}
			for _, k := range s.Keys {
				name := plan.Key{Path: k}.Name()
				pv, put := was.Get(k)
				cv, there := doc.Get(k)
				if there && !reported[name] && (!put || canonical(cv) != canonical(pv)) {
					reported[name] = true
					res.Conflicts = append(res.Conflicts, RestoreConflict{Target: target, Harness: s.Harness, Backup: u.m.ID, Key: name, Current: contentHash([]byte(canonical(cv)))})
				}
				if put {
					err = doc.Set(k, pv)
				} else {
					err = doc.Delete(k)
				}
				if err != nil {
					return settingsError(target, err)
				}
			}
			end = doc.Bytes()
		}
		if cur != nil && string(end) == string(cur) {
			continue
		}
		r.splits = append(r.splits, splitWrite{harness: harnessID, target: target, before: held, prior: cur, data: end, keys: keys})
	}
	return nil
}

// canonical is value, valid JSON, in the form where equal values are
// equal strings.
func canonical(value []byte) string {
	c, err := jsonc.Canonical(value)
	if err != nil {
		return string(value)
	}
	return string(c)
}

// wipeTargets refuses an entry of m that is not one a replace install
// wipes, inside an area of an adapter or one of its leak entries, and a
// split file that is not one an adapter declares, as only an edited
// manifest can hold.
func wipeTargets(home string, m backup.Manifest) error {
	for _, w := range m.Wipes {
		if !slices.ContainsFunc(harness.All(), func(a harness.Adapter) bool {
			return slices.ContainsFunc(a.Real.Areas, func(ar harness.Area) bool { return below(filepath.Join(home, filepath.FromSlash(ar.Dir)), w.Path) }) ||
				slices.ContainsFunc(a.Real.Leaks, func(l harness.LeakEntry) bool {
					return samePath(filepath.Join(home, filepath.FromSlash(l.Path)), w.Path)
				})
		}) {
			return fmt.Errorf("%s is not an entry a replace install wipes", w.Path)
		}
	}
	for _, s := range m.Splits {
		if !slices.ContainsFunc(harness.All(), func(a harness.Adapter) bool {
			return slices.ContainsFunc(a.Real.Split, func(sf harness.SplitFile) bool {
				return samePath(filepath.Join(home, filepath.FromSlash(sf.Path)), s.Target)
			})
		}) {
			return fmt.Errorf("%s is not a file a replace install rewrites by key", s.Target)
		}
	}
	return nil
}

// verifyWipes checks each entry the install backup m in dir holds against
// what its manifest records, and the copy of each split file. An entry no
// longer in the backup is checked where it goes back, when the restore is
// planned.
func (r *restorer) verifyWipes(m backup.Manifest, dir string) error {
	for _, w := range m.Wipes {
		name := filepath.Join(dir, filepath.FromSlash(w.File))
		if _, err := r.d.Lstat(name); errors.Is(err, fs.ErrNotExist) {
			continue
		}
		ok, err := r.holds(r.ctx, name, w.Items)
		if err != nil {
			return &CorruptBackupError{ID: m.ID, File: w.File, Err: err}
		}
		if !ok {
			return &CorruptBackupError{ID: m.ID, File: w.File}
		}
	}
	for _, s := range m.Splits {
		if s.File != "" {
			if err := r.verify(m.ID, dir, s.File, s.Before.SHA256); err != nil {
				return err
			}
		}
	}
	return nil
}

// moveBack carries out r.moves in order, with dir the restore's backup,
// and stops at the first failure; it gives the moves made, so that they
// can be undone.
func (r *restorer) moveBack(dir string) ([]moved, error) {
	var done []moved
	for _, mv := range r.moves {
		to := mv.to
		if mv.aside {
			to = filepath.Join(dir, filepath.FromSlash(mv.file))
		}
		m, err := r.moveEntry(mv.from, to)
		if err != nil {
			if len(m.dirs) > 0 {
				done = append(done, moved{dirs: m.dirs})
			}
			return done, err
		}
		done = append(done, m)
		for d := filepath.Dir(mv.from); !mv.aside && below(mv.stop, d); d = filepath.Dir(d) {
			if rel, err := backup.Rel(r.home, d); err != nil || r.d.RemoveIn(r.home, rel) != nil {
				break // not empty
			}
		}
	}
	return done, nil
}
