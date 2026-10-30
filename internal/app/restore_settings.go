package app

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"slices"

	"github.com/lartweib/dotai/internal/backup"
	"github.com/lartweib/dotai/internal/plan"
	"github.com/lartweib/dotai/internal/state"
)

// A harness's settings file is undone key by key (decision 0035): dotai
// owns only the keys it put there, so a restore puts back the keys each
// install touched and leaves every key the harness wrote.

// settingsStep is an op of an install backup on a harness's settings file:
// the index of that install in the chain, the install and the op.
type settingsStep struct {
	i  int
	u  undone
	op backup.Op
}

// settingsEnd is what undoing the steps on one settings file leads to: the
// content it ends with, nil when it is gone, its exec bit, the backup whose
// copy that content comes from, and the keys or the whole file found
// changed on the way.
type settingsEnd struct {
	data      []byte
	exec      bool
	from      string
	conflicts []RestoreConflict
}

// undoSettings follows the settings target of harness h from what it
// holds, data (nil when it is missing), through steps, newest first. Each
// step expects the keys its install owned after it to hold what that
// install wrote, and puts back, for every key the install owned before or
// after it, what the file held before the install, from the copy in its
// backup; every other key stays. A step on a file its install created
// deletes those keys, and the file goes when no key is left; a file that
// is missing gets its copy back. A key the install owned after it that
// holds something else, or a key it deleted that holds anything, and not
// what the step puts back either, is a conflict; a missing key is not. An
// install before decision 0035 owned the whole file, and its step is
// undone whole, as plan undoes any file.
func (r *restorer) undoSettings(s state.State, chain []undone, h, target string, data []byte, exec bool, steps []settingsStep) (settingsEnd, error) {
	end := settingsEnd{data: data, exec: exec}
	reported := map[string]bool{} // the keys already found changed, "" for the whole file
	conflict := func(st settingsStep, key, expected, current string) {
		if !reported[key] {
			reported[key] = true
			end.conflicts = append(end.conflicts, RestoreConflict{Target: target, Harness: h, Backup: st.u.m.ID, Key: key, Expected: expected, Current: current})
		}
	}
	for _, st := range steps {
		op := st.op
		var copied []byte
		if op.File != "" {
			var err error
			if copied, err = r.d.ReadFile(st.u.dir, op.File); err != nil { // checked when the backup was loaded
				return settingsEnd{}, err
			}
		}
		// An interrupted operation may not have written the file yet.
		found := st.i == 0 && s.Pending != nil && op.Before != nil && end.data != nil && contentHash(end.data) == op.Before.SHA256
		after, whole := keysAfter(s, chain, st.i, target)
		if _, err := plan.SettingsKeys(h, end.data); err != nil && !found {
			whole = true // no longer a JSON object: it can only be put back whole
		}
		if whole {
			switch {
			case end.data == nil, found, op.After != nil && contentHash(end.data) == op.After.SHA256, copied != nil && contentHash(end.data) == op.Before.SHA256:
			default:
				expected := ""
				if op.After != nil {
					expected = op.After.SHA256
				}
				conflict(st, "", expected, contentHash(end.data))
			}
			end.data, end.from = copied, st.u.m.ID
			if op.Before != nil {
				end.exec = op.Before.Exec
			}
			continue
		}
		before, err := keysBefore(st.u.prior, h, target, copied)
		if err != nil {
			return settingsEnd{}, fmt.Errorf("the backup %s: %w", st.u.m.ID, err)
		}
		if end.data != nil && !found {
			have, err := plan.SettingsKeys(h, end.data)
			if err != nil {
				return settingsEnd{}, settingsError(target, err)
			}
			back, _ := plan.SettingsKeys(h, copied) // checked when the backup was loaded
			for _, k := range after {
				cur, ok := keyAt(have, k.Path)
				if prev, put := keyAt(back, k.Path); ok && cur.SHA256 != k.SHA256 && (!put || prev.SHA256 != cur.SHA256) {
					conflict(st, k.Name(), k.SHA256, cur.SHA256)
				}
			}
			// A key the install deleted, written again since with a value
			// the restore would overwrite.
			for _, k := range before {
				if _, owned := keyAt(after, k.Path); owned {
					continue
				}
				cur, ok := keyAt(have, k.Path)
				if prev, put := keyAt(back, k.Path); ok && (!put || prev.SHA256 != cur.SHA256) {
					conflict(st, k.Name(), "", cur.SHA256)
				}
			}
		}
		var paths [][]string
		for _, k := range slices.Concat(after, before) {
			if !slices.ContainsFunc(paths, func(p []string) bool { return slices.Equal(p, k.Path) }) {
				paths = append(paths, k.Path)
			}
		}
		switch {
		case copied == nil && end.data == nil:
		case copied == nil:
			if end.data, err = plan.WriteKeys(h, end.data, nil, paths); err != nil {
				return settingsEnd{}, settingsError(target, err)
			}
			if left, _ := plan.SettingsKeys(h, end.data); len(left) == 0 {
				end.data = nil
			}
		case end.data == nil:
			end.data, end.exec, end.from = copied, op.Before.Exec, st.u.m.ID
		default:
			if end.data, err = plan.WriteKeys(h, end.data, copied, paths); err != nil {
				return settingsEnd{}, settingsError(target, err)
			}
			end.from = st.u.m.ID
		}
	}
	return end, nil
}

// keysAfter gives the keys dotai owned in target once the install at index
// i of chain was carried out, as the state after it records them: the
// journal of s for an interrupted one, s for the newest one, or else the
// state before the install after it. whole reports a record from before
// decision 0035, which owned the whole file.
func keysAfter(s state.State, chain []undone, i int, target string) (keys []plan.Key, whole bool) {
	if i == 0 && s.Pending != nil {
		for _, op := range s.Pending.Ops {
			if op.Target != target {
				continue
			}
			f, owned := fileIn(&s, target)
			switch {
			case op.Keys != nil:
				return op.Keys, false
			case op.After == nil, owned && f.Keys != nil:
				return nil, false // removed, or released
			}
			return nil, true
		}
		return nil, false
	}
	after := &s
	if i > 0 {
		after = chain[i-1].prior
	}
	f, ok := fileIn(after, target)
	if !ok {
		return nil, false
	}
	return f.Keys, f.Keys == nil
}

// keysBefore gives the keys dotai owned in target before an install, as
// prior, the state that install started from, records them; a record from
// before decision 0035 owned every key of the file, which copied, the
// copy in the backup, holds.
func keysBefore(prior *state.State, h, target string, copied []byte) ([]plan.Key, error) {
	f, ok := fileIn(prior, target)
	switch {
	case !ok:
		return nil, nil
	case f.Keys != nil:
		return f.Keys, nil
	}
	return plan.SettingsKeys(h, copied)
}

// fileIn is the record of target in s, nil for no state.
func fileIn(s *state.State, target string) (state.File, bool) {
	if s == nil {
		return state.File{}, false
	}
	i := slices.IndexFunc(s.Files, func(f state.File) bool { return f.Target == target })
	if i < 0 {
		return state.File{}, false
	}
	return s.Files[i], true
}

// keyAt is the key of keys at path.
func keyAt(keys []plan.Key, path []string) (plan.Key, bool) {
	i := slices.IndexFunc(keys, func(k plan.Key) bool { return slices.Equal(k.Path, path) })
	if i < 0 {
		return plan.Key{}, false
	}
	return keys[i], true
}

// settingsError is a settings file that restore cannot edit key by key.
func settingsError(target string, err error) error {
	return fmt.Errorf("%s: %w; fix it or move it away, then restore again", target, err)
}

// contentHash is the SHA-256 in hex of data.
func contentHash(data []byte) string {
	h := sha256.Sum256(data)
	return hex.EncodeToString(h[:])
}
