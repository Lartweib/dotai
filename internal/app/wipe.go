package app

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"path"
	"path/filepath"
	"slices"
	"strings"

	"github.com/lartweib/dotai/internal/backup"
	"github.com/lartweib/dotai/internal/plan"
	"github.com/lartweib/dotai/internal/plan/jsonc"
	"github.com/lartweib/dotai/internal/state"
)

// A replace install moves each entry it wipes into its backup, and deletes
// the configuration keys of a file that mixes configuration and state,
// such as ~/.claude.json, keeping the rest of it (decision 0038). Both go
// through the backup and the journal of the install, and are rolled back
// with it: a moved entry goes back to its path, a split file gets its
// bytes back. A restore moves the entries back the same way.

// RefusedError is a replace install that its plan refuses (decision
// 0038), which writes nothing.
type RefusedError struct {
	Refusals []Refusal
	Plan     InstallPlan
}

func (e *RefusedError) Error() string {
	msgs := make([]string, len(e.Refusals))
	for i, r := range e.Refusals {
		msgs[i] = r.Message
	}
	return "the replace install is refused, and the install wrote nothing: " + strings.Join(msgs, "; ")
}

// splitWrite is a split file an operation rewrites by key: its harness,
// target, what it holds before (prior, with its state before) and the
// content it is given, data, and the keys it deletes or puts back. A
// folded one is also the target of a settings op of the install, which
// writes the wipe together with dotai's keys (decision 0049): the backup
// records it, with the op's copy as its own, and nothing else writes it.
type splitWrite struct {
	harness, target string
	before          plan.Current
	prior, data     []byte
	keys            [][]string
	folded          bool
}

// extra is what an operation writes besides its plan's ops: the entries it
// moves into its backup, whose items the backup fills in, and the split
// files it rewrites, whose copies it keeps.
type extra struct {
	wipes  []backup.Wipe
	splits []splitWrite
}

// keyWipes are the split files keys plans to wipe, each with the content
// it is given: what it holds now, read again, with those keys deleted
// through the JSONC edit layer, so that every other byte stays. A file
// that holds none of them any more is left out.
func (r runner) keyWipes(keys []WipeKey) ([]splitWrite, error) {
	var out []splitWrite
	for _, k := range keys {
		i := slices.IndexFunc(out, func(s splitWrite) bool { return s.target == k.File })
		if i < 0 {
			out = append(out, splitWrite{harness: k.Harness, target: k.File})
			i = len(out) - 1
		}
		out[i].keys = append(out[i].keys, k.Key)
	}
	for i, s := range out {
		data, cur, err := r.d.Read(r.ctx, s.target)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", s.target, err)
		}
		wiped, err := deleteKeys(data, s.keys)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", s.target, err)
		}
		out[i].before, out[i].prior, out[i].data = cur, data, wiped
	}
	return slices.DeleteFunc(out, func(s splitWrite) bool { return string(s.data) == string(s.prior) }), nil
}

// deleteKeys is data, a JSON object, with each key of keys deleted through
// the JSONC edit layer, so that every other byte stays.
func deleteKeys(data []byte, keys [][]string) ([]byte, error) {
	doc, err := jsonc.Parse(data)
	if err != nil {
		return nil, fmt.Errorf("no longer a JSON object: %w", err)
	}
	for _, k := range keys {
		if err := doc.Delete(k); err != nil {
			return nil, fmt.Errorf("deleting %s: %w", strings.Join(k, "."), err)
		}
	}
	return doc.Bytes(), nil
}

// items lists the entry at name and, for a real directory, everything
// inside it, in walk order, without following a link: what a backup
// records of a wiped entry.
func (r runner) items(ctx context.Context, name string) ([]backup.Item, error) {
	var out []backup.Item
	var walk func(name, rel string) error
	walk = func(name, rel string) error {
		mode, err := r.d.Lstat(name)
		if err != nil {
			return err
		}
		it := backup.Item{Path: rel, Kind: entryKind(mode)}
		switch it.Kind {
		case KindLink:
			if cur, err := r.d.Link(name); err == nil {
				it.Link = cur.Link
			}
		case KindFile:
			cur, err := r.d.Hash(ctx, name)
			if err != nil {
				return err
			}
			it.SHA256, it.Exec = cur.Hash, cur.Exec
		}
		out = append(out, it)
		if it.Kind != KindDir {
			return nil
		}
		names, err := r.d.List(name)
		if err != nil {
			return err
		}
		slices.Sort(names)
		for _, n := range names {
			if err := walk(filepath.Join(name, n), path.Join(rel, n)); err != nil {
				return err
			}
		}
		return nil
	}
	if err := walk(name, ""); err != nil {
		return nil, err
	}
	return out, nil
}

// holds reports whether the entry at name holds exactly items: the same
// paths of the same kinds, the same content and exec bit, where the file
// system keeps one, and the same link destinations.
func (r runner) holds(ctx context.Context, name string, items []backup.Item) (bool, error) {
	got, err := r.items(ctx, name)
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	if err != nil || len(got) != len(items) {
		return false, err
	}
	for i, it := range items {
		g := got[i]
		if r.p.ignoreExec {
			g.Exec, it.Exec = false, false
		}
		if g != it {
			return false, nil
		}
	}
	return true, nil
}

// backupExtra copies into the backup directory dir what ex rewrites, and
// records in m what it moves and rewrites: each split file's copy, checked
// against what it held when it was read, and each wiped entry's items,
// which must still be there, of the kind the plan found. It gives the
// directories under dir that hold copies.
func (r runner) backupExtra(dir string, m *backup.Manifest, ex *extra) (map[string]bool, error) {
	dirs := map[string]bool{}
	for _, s := range ex.splits {
		rel, err := backup.Rel(r.home, s.target)
		if err != nil {
			return nil, err
		}
		rec := backup.Split{Harness: s.harness, Target: s.target, Keys: s.keys, After: backup.Content{SHA256: contentHash(s.data), Exec: s.before.Exec}}
		if s.prior != nil { // a restore puts back a file that is missing whole
			if contentHash(s.prior) != s.before.Hash {
				return nil, &ChangedError{Path: s.target}
			}
			rec.File, rec.Before = path.Join(backup.FilesDir, rel), backup.Content{SHA256: s.before.Hash, Exec: s.before.Exec}
			if s.folded {
				// The op's copy, of the same content, is the split's.
				if !slices.ContainsFunc(m.Ops, func(op backup.Op) bool {
					return op.Target == s.target && op.File == rec.File && op.Before != nil && op.Before.SHA256 == s.before.Hash
				}) {
					return nil, &ChangedError{Path: s.target}
				}
				m.Splits = append(m.Splits, rec)
				continue
			}
			if err := r.d.WriteSynced(dir, rec.File, s.prior, 0o600); err != nil {
				return nil, err
			}
			for d := path.Dir(rec.File); d != "."; d = path.Dir(d) {
				dirs[d] = true
			}
		}
		m.Splits = append(m.Splits, rec)
	}
	// An install's entries are recorded once they are moved
	// (recordItems), so that a file a harness still running changes before
	// its entry moves is recorded as it is in the backup; a restore knows
	// what it will move out of the way.
	for _, w := range ex.wipes {
		if w.Items != nil {
			continue
		}
		mode, err := r.d.Lstat(w.Path)
		if errors.Is(err, fs.ErrNotExist) || err == nil && entryKind(mode) != w.Kind {
			return nil, &ChangedError{Path: w.Path}
		}
		if err != nil {
			return nil, err
		}
	}
	m.Wipes = slices.Clone(ex.wipes)
	return dirs, nil
}

// recordItems records in the manifest of the backup in dir the items of
// each entry of ex once it is moved there, read from the backup, and
// writes the manifest again, before the install writes the pack's files.
// An install killed before then leaves a manifest without them, which a
// restore reads from the backup in turn.
func (r runner) recordItems(dir string, ex *extra) error {
	for i, w := range ex.wipes {
		r.step(PhaseRecord, i+1, len(ex.wipes), r.tilde(w.Path))
		items, err := r.items(r.ctx, filepath.Join(dir, filepath.FromSlash(w.File)))
		if err != nil {
			return err
		}
		ex.wipes[i].Items = items
	}
	data, err := r.d.ReadFile(dir, backup.ManifestName)
	if err != nil {
		return err
	}
	m, err := backup.Decode(data)
	if err != nil {
		return err
	}
	m.Wipes = ex.wipes
	if data, err = backup.Encode(m); err != nil {
		return err
	}
	return r.d.WriteIn(dir, backup.ManifestName, data, 0o600)
}

// moved is an entry an operation moved, from and to absolute paths, so
// that it can be moved back; dirs are the directories it made for it,
// outermost first, removed again when empty.
type moved struct {
	from, to string
	dirs     []string
}

// moveEntry moves the entry at from, a file, a directory with everything
// in it or a link, never followed, to the missing path to, both under the
// home directory, making the directories above to that are missing. The
// move is a rename inside the home directory, which keeps links,
// junctions, permissions, timestamps and empty directories; across
// volumes, it falls back to a copy (moveAcross).
func (r runner) moveEntry(from, to string) (moved, error) {
	m := moved{from: from, to: to}
	if err := r.ctx.Err(); err != nil {
		return m, err
	}
	src, err := backup.Rel(r.home, from)
	if err != nil {
		return m, err
	}
	dst, err := backup.Rel(r.home, to)
	if err != nil {
		return m, err
	}
	if m.dirs, err = r.parents(to); err != nil {
		return m, err
	}
	if len(m.dirs) > 0 {
		if err := r.d.MkdirIn(r.home, path.Dir(dst)); err != nil {
			return m, err
		}
	}
	if _, err := r.d.Lstat(to); err == nil {
		return m, &ChangedError{Path: to}
	}
	err = r.d.MoveIn(r.home, src, dst)
	if err != nil && crossDevice(err) {
		var warning string
		if warning, err = r.moveAcross(src, dst); warning != "" && r.warnings != nil {
			*r.warnings = append(*r.warnings, warning)
		}
	}
	return m, err
}

// wipe moves each entry of wipes, still of the kind its backup records,
// into the backup directory dir, in order, and stops at the first failure.
// It gives the entries it moved, and the one that failed with the
// directories it made.
func (r runner) wipe(dir string, wipes []backup.Wipe) ([]moved, error) {
	var done []moved
	for i, w := range wipes {
		r.step(PhaseWipe, i+1, len(wipes), r.tilde(w.Path))
		mode, err := r.d.Lstat(w.Path)
		if errors.Is(err, fs.ErrNotExist) || err == nil && entryKind(mode) != w.Kind {
			return done, &ChangedError{Path: w.Path}
		}
		if err != nil {
			return done, err
		}
		m, err := r.moveEntry(w.Path, filepath.Join(dir, filepath.FromSlash(w.File)))
		if err != nil {
			if len(m.dirs) > 0 {
				done = append(done, moved{dirs: m.dirs})
			}
			return done, err
		}
		done = append(done, m)
	}
	return done, nil
}

// unmove moves back, last first, each entry of done, with a context of its
// own as rollback has: an entry goes back only to a path that is missing,
// and one that something took since is left where it is and reported. The
// directories made for it are removed when they are empty.
func (r runner) unmove(done []moved) error {
	r, cancel := r.detached()
	defer cancel()
	var errs []error
	n, total := 0, 0
	for _, m := range done {
		if m.from != "" {
			total++
		}
	}
	for _, m := range slices.Backward(done) {
		if m.from != "" {
			n++
			r.step(PhaseRollback, n, total, r.tilde(m.from))
			if _, err := r.d.Lstat(m.from); err == nil {
				errs = append(errs, fmt.Errorf("%s was left in %s: something took its place since", m.from, m.to))
			} else if _, err := r.moveEntry(m.to, m.from); err != nil {
				errs = append(errs, fmt.Errorf("%s could not be moved back from %s: %w", m.from, m.to, err))
			}
		}
		for _, d := range slices.Backward(m.dirs) {
			if rel, err := backup.Rel(r.home, d); err == nil {
				_ = r.d.RemoveIn(r.home, rel)
			}
		}
	}
	return errors.Join(errs...)
}

// writeSplits rewrites each split file of splits, still holding what it
// held when it was read, with its new content, in order, and stops at the
// first failure. It gives the ones it wrote.
func (r runner) writeSplits(splits []splitWrite) ([]splitWrite, error) {
	var done []splitWrite
	for _, s := range splits {
		if err := r.ctx.Err(); err != nil {
			return done, err
		}
		if s.folded { // its op writes it
			continue
		}
		cur, err := r.d.Hash(r.ctx, s.target)
		switch {
		case s.prior == nil && errors.Is(err, fs.ErrNotExist):
		case s.prior == nil && err == nil, errors.Is(err, fs.ErrNotExist), err == nil && !r.same(cur, s.before.Hash, s.before.Exec):
			return done, &ChangedError{Path: s.target}
		case err != nil:
			return done, err
		}
		mode := splitMode(s.before.Exec)
		if s.prior == nil {
			mode = 0o600
		}
		if err := r.d.WriteIn(filepath.Dir(s.target), filepath.Base(s.target), s.data, mode); err != nil {
			return done, err
		}
		done = append(done, s)
	}
	return done, nil
}

// unsplit puts back, last first, what each split file of done held before,
// unless something else changed or removed it since, which is left and
// reported.
func (r runner) unsplit(done []splitWrite) error {
	r, cancel := r.detached()
	defer cancel()
	var errs []error
	for _, s := range slices.Backward(done) {
		cur, err := r.d.Hash(r.ctx, s.target)
		switch {
		case s.prior == nil && err == nil && cur.Hash == contentHash(s.data):
			if err := r.d.RemoveIn(filepath.Dir(s.target), filepath.Base(s.target)); err != nil {
				errs = append(errs, err)
			}
			continue
		case s.prior == nil && errors.Is(err, fs.ErrNotExist):
			continue
		case s.prior != nil && err == nil && r.same(cur, s.before.Hash, s.before.Exec):
			continue
		case errors.Is(err, fs.ErrNotExist):
			errs = append(errs, fmt.Errorf("%s is missing: it was removed after the operation wrote it, so it was not put back; its copy is in the backup", s.target))
			continue
		case err == nil && cur.Hash != contentHash(s.data):
			errs = append(errs, fmt.Errorf("%s was left as it is: it changed after the operation wrote it", s.target))
			continue
		case err != nil:
			errs = append(errs, err)
			continue
		}
		if err := r.d.WriteIn(filepath.Dir(s.target), filepath.Base(s.target), s.prior, splitMode(s.before.Exec)); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// splitMode is the mode a split file is written with: at most the
// permissions of the file there, which WriteIn keeps, with its exec bit.
func splitMode(exec bool) fs.FileMode {
	if exec {
		return 0o777
	}
	return 0o666
}

// resets reports whether p wipes anything of a harness's own
// configuration: an entry or a key (decision 0038).
func (p planned) resets() bool {
	return p.plan.Mode == ModeReplace && (len(p.plan.Wipes) > 0 || len(p.plan.WipeKeys) > 0)
}

// resetWork is what the replace install of r.p does besides its ops: the
// entries it moves into its backup, at their paths under its FilesDir,
// and the split files whose keys it deletes. An install in the isolated
// mode has none.
func (r runner) resetWork() (*extra, error) {
	ex := &extra{}
	if r.p.plan.Mode != ModeReplace {
		return ex, nil
	}
	var err error
	if ex.splits, err = r.keyWipes(r.p.plan.WipeKeys); err != nil {
		return nil, err
	}
	for i, s := range ex.splits {
		ex.splits[i].folded = slices.ContainsFunc(r.p.ops, func(op plan.Op) bool {
			return op.Target == s.target && op.Settings && op.Action != plan.Unchanged
		})
	}
	for _, w := range r.p.plan.Wipes {
		rel, err := backup.Rel(r.home, w.Path)
		if err != nil {
			return nil, err
		}
		ex.wipes = append(ex.wipes, backup.Wipe{Harness: w.Harness, Path: w.Path, Kind: w.Kind, File: path.Join(backup.FilesDir, rel)})
	}
	return ex, nil
}

// pinnedChain is the start of order, the history the install state leads
// to, newest first, that is made of replace installs: its last backup is
// the pinned one, the first replace backup of the history, which holds the
// configuration from before dotai, or what a killed first replace install
// had moved (decision 0038). Retention never prunes any of it. The pin is
// read from the history, never recorded, so that an install that rolled
// back, which the history no longer leads to, is never pinned, and a
// restore that undoes the pinned backup releases it. It is empty when the
// newest install is not a replace install.
func (r runner) pinnedChain(order []string) []string {
	root := filepath.Join(r.home, dotaiDir, backupsDir)
	n := 0
	for i, id := range order {
		data, err := r.d.ReadFile(filepath.Join(root, id), backup.ManifestName)
		if err != nil {
			break
		}
		m, err := backup.Decode(data)
		if err != nil || m.Operation != backup.OperationInstall || m.Mode != ModeReplace {
			break
		}
		n = i + 1
	}
	return order[:n]
}

// journal is base with the journal of an operation that carries out ops
// and ex with the backup id.
func journal(base state.State, id string, ops []plan.Op, ex *extra) state.State {
	s := state.Journal(base, id, ops)
	for _, w := range ex.wipes {
		s.Pending.Wipes = append(s.Pending.Wipes, w.Path)
	}
	for _, sp := range ex.splits {
		if !sp.folded { // its op is in the journal
			s.Pending.Splits = append(s.Pending.Splits, sp.target)
		}
	}
	return s
}
