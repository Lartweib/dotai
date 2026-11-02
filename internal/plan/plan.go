// Package plan builds the plan of decision 0028: the exact file operations
// a command will carry out, decided before anything is written. It does no
// I/O; the caller hashes the staged files and the current targets.
//
// It covers the user scope of an install, isolated (decision 0031) or in
// the replace layout (decision 0038): each staged file is created,
// replaced or left unchanged at its install path,
// and each file an earlier install owned that the pack no longer stages is
// removed (decision 0032). A harness's settings file is planned key by key
// instead (decision 0035): the harness writes into it too, so dotai owns
// only the keys the pack puts there. A link to a shared skill (decision
// 0036) is planned like a file whose content is its destination.
//
// It also plans the project scope of a profile (decision 0041), in
// project.go: each file to its harness's local target, untouched where git
// tracks it, and listed in .git/info/exclude.
package plan

import (
	"cmp"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io/fs"
	"maps"
	"path"
	"path/filepath"
	"slices"
	"strings"

	"github.com/lartweib/dotai/internal/harness"
	"github.com/lartweib/dotai/internal/merge"
)

// ScopeUser is the scope of the user-level configuration.
const ScopeUser = "user"

// sourcesDir holds rulesync's sources in a staging tree. They are the
// input of the render, not part of the install, so they are never planned.
const sourcesDir = ".rulesync/"

// Action is what an operation does to its target.
type Action string

// Actions. A target with the staged content but another exec bit is
// replaced, so that it ends with the staged mode.
const (
	Create    Action = "create"    // the target is missing
	Replace   Action = "replace"   // the target has other content or another exec bit
	Unchanged Action = "unchanged" // the target already has the staged content and exec bit
	Remove    Action = "remove"    // an earlier install owned the target and the pack no longer stages it
)

// File is one file of the user-scope staging tree: its slash path inside
// the tree, the SHA-256 of its content in hex and whether it is
// executable. The tree holds the files of a scope only; the stage marker
// sits beside it and is never part of it.
//
// A File with Link is not in the tree: it is a link to install at the
// target of Path, pointing at Link, an absolute path (decision 0036). Its
// Hash and Exec are then LinkHash(Link) and false, whatever they hold.
type File struct {
	Path string
	Hash string
	Exec bool
	Link string
	// Data is the content of a harness's settings file, which is planned
	// key by key. A settings file without it is planned whole from its
	// hash, as when nothing is read yet.
	Data []byte
}

// Current is the state of a target that exists: the SHA-256 of its
// content in hex and whether it is executable. For a link, Link is its
// destination, Hash is LinkHash(Link) and Exec is false: the content of a
// link is where it points, so comparing hashes compares links too.
type Current struct {
	Hash string
	Exec bool
	Link string
}

// LinkHash is the hash that stands for the content of a link to dest: the
// SHA-256 in hex of "link:" followed by dest.
func LinkHash(dest string) string {
	h := sha256.Sum256([]byte("link:" + dest))
	return hex.EncodeToString(h[:])
}

// LinkCurrent is the state of a link to dest.
func LinkCurrent(dest string) Current { return Current{Hash: LinkHash(dest), Link: dest} }

// Owned is a target the previous install wrote, as its ownership record
// holds it: the harness it was installed for, "" for a tool, and the
// SHA-256 in hex and exec bit it was written with. An install that stopped
// before it finished leaves targets in two possible states: Also lists
// other contents that are dotai's too, such as what that install was
// writing, and Foreign the contents that mean the target is still the
// file dotai found there and never owned.
//
// Keys are the keys dotai owns in a settings file, nil for a file it owns
// whole, which is how an install before decision 0035 recorded a settings
// file too; AlsoKeys are those it owns when the file holds a content of
// Also, or holds those keys after the harness wrote more.
type Owned struct {
	Harness  string
	Hash     string
	Exec     bool
	Also     []Current
	Foreign  []Current
	Keys     []Key
	AlsoKeys []Key
}

// Input is what Build plans from. Disk holds the current state of each
// target that exists; a target missing from Disk does not exist. Its keys
// must be the targets Locate returns, which are also the Targets of the
// plan, and the keys of Owned. Owned is the ownership record of the
// previous install, by absolute target; it is empty before the first one.
// IgnoreExec leaves the exec bit out of the comparison, for a file system
// that has none, such as on Windows; the caller decides it, so that Build
// behaves the same on every OS. Content holds the content of each settings
// target in Disk that is staged with its Data or owned.
type Input struct {
	Home string // the absolute home directory
	// Mode is the layout the staged files install in, harness.Isolated
	// when empty, and PrevMode that of the install Owned records, Mode
	// when empty: an owned target may be in either.
	Mode, PrevMode harness.Mode
	Adapters       []harness.Adapter // the harnesses the install is for
	Files          []File
	Disk           map[string]Current
	Content        map[string][]byte
	Owned          map[string]Owned
	IgnoreExec     bool
	// FoldCase compares link destinations without case, for a file system
	// that ignores it, such as on Windows and macOS (FoldsCase).
	FoldCase bool
}

// Plan is the list of operations, in target order, and the targets of
// Input.Owned that exist and are dotai's, with their current state. Keys
// are the keys dotai owns now in each settings target of Owned; a settings
// file in which it owns none is not in Owned.
type Plan struct {
	Ops   []Op
	Owned map[string]Current
	Keys  map[string][]Key
}

// Op is one file operation. Harness is empty for a tool, which is not
// installed for one harness. Before and BeforeExec are the target's
// current hash and exec bit, empty and false when it is missing, so that
// execution and backup can check that it did not change after planning.
// After and Exec are what it will have; a Remove has no Source, After or
// Exec.
//
// Settings marks a harness's settings file planned key by key (decision
// 0035). Data is then what the target gets when that is not the staged
// file as it is: the target's own content with dotai's keys written into
// it. Keys are the keys dotai owns in the target once the op is carried
// out, none when it no longer owns the file, and Changes the keys the op
// writes or deletes.
//
// Link is the destination of a link the op leaves at its target, and
// BeforeLink that of the link the target is, "" when it is not one
// (decision 0036): Before and After are then their LinkHash.
type Op struct {
	Action     Action
	Scope      string
	Harness    string
	Source     string // slash path inside the scope's staging tree
	Target     string // absolute path
	Exec       bool
	Before     string
	BeforeExec bool
	After      string
	Settings   bool
	Data       []byte
	Keys       []Key
	Changes    []KeyChange
	Link       string
	BeforeLink string
}

// KindError is a target that is a link where the plan installs a file, or
// a file where it installs a link (Link set): dotai never replaces one
// with the other.
type KindError struct {
	Target string
	Link   bool
	Dest   string // where a link that dotai did not make points, if it is one
}

func (e *KindError) Error() string {
	is, want := "a link", "a file"
	switch {
	case e.Link && e.Dest != "":
		is, want = "a link to "+e.Dest, "its own link to a shared skill"
	case e.Link:
		is, want = "not a link", "a link"
	}
	return fmt.Sprintf("%s is %s where the install needs %s; dotai never replaces what it did not write: move or remove it, then plan again", e.Target, is, want)
}

// UnmappedError is a staged file that no harness of the pack and no tools
// rule installs. Paths lists every such file, sorted.
type UnmappedError struct {
	Paths []string
}

func (e *UnmappedError) Error() string {
	return "no harness of the pack installs the staged files " + strings.Join(e.Paths, ", ")
}

// EditedError is a target the previous install owned whose content or
// exec bit is no longer what it wrote, or a key it owns in a settings file
// whose value is no longer what it wrote: the user edited it, and the plan
// neither replaces nor removes it. Targets lists every such target, sorted,
// and Keys the name of every such key by settings file, sorted.
type EditedError struct {
	Targets []string
	Keys    map[string][]string
}

func (e *EditedError) Error() string {
	var parts []string
	switch len(e.Targets) {
	case 0:
	case 1:
		parts = append(parts, "the installed file "+e.Targets[0]+" was changed after dotai installed it, and dotai does not overwrite or remove it: "+
			"move it away or put back what dotai installed, then install again")
	default:
		parts = append(parts, "the installed files "+strings.Join(e.Targets, ", ")+" were changed after dotai installed them, and dotai does not overwrite or remove them: "+
			"move them away or put back what dotai installed, then install again")
	}
	for _, target := range slices.Sorted(maps.Keys(e.Keys)) {
		keys := e.Keys[target]
		if len(keys) == 1 {
			parts = append(parts, "the key "+keys[0]+" in "+target+" was changed after dotai installed it, and dotai does not overwrite it: "+
				"put back the value dotai installed, or delete the key to take the pack's, then install again")
			continue
		}
		parts = append(parts, "the keys "+strings.Join(keys, ", ")+" in "+target+" were changed after dotai installed them, and dotai does not overwrite them: "+
			"put back the values dotai installed, or delete the keys to take the pack's, then install again")
	}
	return strings.Join(parts, "; ")
}

// Build plans the install of the user-scope staging tree. A file under
// one of the adapters' staging paths installs into that harness's
// directory of the mode, a file under the tools directory into the user's
// tools directory, and rulesync's sources are skipped. Any other file is
// an UnmappedError, so that nothing staged is dropped silently. A path
// Windows cannot store is an error on every OS, and so are two files with
// one target, a harness given twice and, in the isolated mode, a harness
// without isolation.
//
// A target in Owned that no staged file installs is removed when it still
// exists; one that is missing needs nothing. An owned target outside the
// tools directory and the harness directories of Mode and PrevMode is an
// error, and so is an owned
// target that exists with a hash or exec bit that is neither what Owned
// records nor in its Also or Foreign: an EditedError, so that nothing the
// user changed is overwritten or removed. A target in its Foreign is not
// dotai's: it is neither removed nor in Plan.Owned.
//
// A harness's settings file that is staged with its Data, or owned with
// Keys, is planned key by key instead, as planSettings describes.
func Build(in Input) (Plan, error) {
	if !filepath.IsAbs(in.Home) {
		return Plan{}, fmt.Errorf("home directory %q is not absolute", in.Home)
	}
	seen := map[string]bool{}
	for _, a := range in.Adapters {
		if seen[a.ID] {
			return Plan{}, fmt.Errorf("harness %q is given twice", a.ID)
		}
		if in.mode() == harness.Isolated && a.Isolation.Level == harness.None {
			return Plan{}, fmt.Errorf("harness %q does not offer the isolated mode (decision 0018)", a.ID)
		}
		seen[a.ID] = true
	}
	var p Plan
	var unmapped []string
	sources := map[string]string{}         // the source of each target
	settings := map[string]*settingsFile{} // the targets planned key by key
	for _, f := range in.Files {
		if err := checkPath(f.Path); err != nil {
			return Plan{}, err
		}
		if strings.HasPrefix(f.Path, sourcesDir) {
			continue
		}
		if f.Link != "" {
			f.Hash, f.Exec, f.Data = LinkHash(f.Link), false, nil
		}
		h, target, ok := Locate(in.Home, in.mode(), in.Adapters, f.Path)
		if ok && f.Link != "" {
			if err := CheckLinkDest(in.Home, target, f.Link, in.FoldCase); err != nil {
				return Plan{}, err
			}
		}
		if !ok {
			unmapped = append(unmapped, f.Path)
			continue
		}
		if prev, ok := sources[target]; ok {
			return Plan{}, fmt.Errorf("staged files %s and %s install to the same target %s", prev, f.Path, target)
		}
		sources[target] = f.Path
		if sh, ok := SettingsOf(in.Home, target); ok && f.Data != nil {
			settings[target] = &settingsFile{harness: sh, staged: &f}
			continue
		}
		op := Op{Action: Create, Scope: ScopeUser, Harness: h, Source: f.Path, Target: target, Exec: f.Exec, After: f.Hash, Link: f.Link}
		if cur, ok := in.Disk[target]; ok {
			if (cur.Link != "") != (f.Link != "") {
				return Plan{}, &KindError{Target: target, Link: f.Link != ""}
			}
			if _, owned := in.Owned[target]; !owned && cur.Link != "" && CheckLinkDest(in.Home, target, cur.Link, in.FoldCase) != nil {
				// A link dotai did not make, to somewhere else: never
				// replaced, so that no backup records a destination
				// restore could not put back.
				return Plan{}, &KindError{Target: target, Link: true, Dest: cur.Link}
			}
			op.Before, op.BeforeExec, op.BeforeLink, op.Action = cur.Hash, cur.Exec, cur.Link, Replace
			if cur.Hash == f.Hash && (in.IgnoreExec || cur.Exec == f.Exec) {
				op.Action = Unchanged
			}
		}
		p.Ops = append(p.Ops, op)
	}
	if len(unmapped) > 0 {
		slices.Sort(unmapped)
		return Plan{}, &UnmappedError{Paths: unmapped}
	}
	edited := &EditedError{}
	for target, o := range in.Owned {
		if !installed(in.Home, target, in.modes()...) {
			return Plan{}, fmt.Errorf("owned target %s is not under %s, where an install writes", target, orList(installDirs(in.Home, in.modes()...)))
		}
		if st, ok := settings[target]; ok {
			st.owned = &o
			continue
		}
		if sh, ok := SettingsOf(in.Home, target); ok && o.Keys != nil {
			if _, staged := sources[target]; !staged {
				settings[target] = &settingsFile{harness: sh, owned: &o}
				continue
			}
		}
		cur, ok := in.Disk[target]
		if !ok {
			continue
		}
		mine := in.same(cur, Current{Hash: o.Hash, Exec: o.Exec}) || slices.ContainsFunc(o.Also, func(c Current) bool { return in.same(cur, c) })
		if !mine && slices.ContainsFunc(o.Foreign, func(c Current) bool { return in.same(cur, c) }) {
			continue
		}
		if !mine {
			edited.Targets = append(edited.Targets, target)
			continue
		}
		if p.Owned == nil {
			p.Owned = map[string]Current{}
		}
		p.Owned[target] = cur
		if _, staged := sources[target]; !staged {
			p.Ops = append(p.Ops, Op{Action: Remove, Scope: ScopeUser, Harness: o.Harness, Target: target, Before: cur.Hash, BeforeExec: cur.Exec, BeforeLink: cur.Link})
		}
	}
	for target, st := range settings {
		op, keys, err := in.planSettings(target, st, edited)
		if err != nil {
			return Plan{}, err
		}
		if op != nil {
			p.Ops = append(p.Ops, *op)
		}
		if len(keys) > 0 {
			if p.Owned == nil {
				p.Owned = map[string]Current{}
			}
			if p.Keys == nil {
				p.Keys = map[string][]Key{}
			}
			p.Owned[target], p.Keys[target] = in.Disk[target], keys
		}
	}
	if len(edited.Targets) > 0 || len(edited.Keys) > 0 {
		slices.Sort(edited.Targets)
		return Plan{}, edited
	}
	slices.SortFunc(p.Ops, func(a, b Op) int { return cmp.Compare(a.Target, b.Target) })
	return p, nil
}

// Locate returns the harness that installs the staged file source in mode,
// "" for a tool, and its absolute target under home with the OS separator.
// Build uses it, so a caller that builds Input.Disk with it gets the same
// keys. ok is false when nothing installs source. rulesync's sources are
// not special here; Build skips them before it calls Locate.
func Locate(home string, mode harness.Mode, adapters []harness.Adapter, source string) (h, target string, ok bool) {
	at := func(rel string) string { return filepath.Join(home, filepath.FromSlash(rel)) }
	if rest, ok := strings.CutPrefix(source, merge.ToolsDir+"/"); ok {
		return "", at(path.Join(merge.ToolsDir, rest)), true
	}
	for _, a := range adapters {
		if rest, ok := strings.CutPrefix(source, a.Staging+"/"); ok {
			for _, m := range a.MovesIn(mode) {
				if rest == m.From {
					rest = m.To
					break
				}
				if moved, ok := strings.CutPrefix(rest, m.From+"/"); ok {
					rest = path.Join(m.To, moved)
					break
				}
			}
			return a.ID, at(path.Join(a.InstallDir(mode), rest)), true
		}
	}
	return "", "", false
}

// MovedFrom reports whether target, an absolute path under home, is on the
// old side of a move of a directory of some harness, in either layout:
// where an install before the move put the files that now install
// elsewhere.
func MovedFrom(home, target string) bool {
	for _, a := range harness.All() {
		for _, mode := range modes {
			for _, m := range a.Moves {
				dir := filepath.Join(home, filepath.FromSlash(path.Join(a.InstallDir(mode), m.From)))
				if strings.HasPrefix(target, dir+string(filepath.Separator)) {
					return true
				}
			}
		}
	}
	return false
}

// modes are the install modes, each with its layout.
var modes = []harness.Mode{harness.Isolated, harness.Replace}

// mode is the layout the staged files install in.
func (in Input) mode() harness.Mode { return cmp.Or(in.Mode, harness.Isolated) }

// modes are the layouts an owned target may be in.
func (in Input) modes() []harness.Mode {
	if prev := cmp.Or(in.PrevMode, in.mode()); prev != in.mode() {
		return []harness.Mode{in.mode(), prev}
	}
	return []harness.Mode{in.mode()}
}

// same reports whether a and b have the same content and exec bit, unless
// the exec bit is ignored.
func (in Input) same(a, b Current) bool {
	return a.Hash == b.Hash && (in.IgnoreExec || a.Exec == b.Exec)
}

// installed reports whether target is a path inside one of the
// installDirs of modes under home, the only places an install writes.
func installed(home, target string, modes ...harness.Mode) bool {
	rel, err := filepath.Rel(home, target)
	if err != nil || !filepath.IsLocal(rel) {
		return false
	}
	rel = filepath.ToSlash(rel)
	return slices.ContainsFunc(installRels(modes...), func(dir string) bool { return strings.HasPrefix(rel, dir+"/") })
}

// installRels are the directories an install in one of modes writes in,
// relative to the home directory: the isolated root, or each harness's
// configuration directory, and then the tools directory.
func installRels(modes ...harness.Mode) []string {
	var dirs []string
	for _, m := range modes {
		if m != harness.Replace {
			dirs = append(dirs, harness.IsolatedDir)
			continue
		}
		for _, a := range harness.All() {
			dirs = append(dirs, a.Root(harness.Replace))
		}
	}
	return append(dirs, merge.ToolsDir)
}

// installDirs are the installRels of modes under home.
func installDirs(home string, modes ...harness.Mode) []string {
	var out []string
	for _, rel := range installRels(modes...) {
		out = append(out, filepath.Join(home, filepath.FromSlash(rel)))
	}
	return out
}

// orList joins items as "a, b or c".
func orList(items []string) string {
	if len(items) < 2 {
		return strings.Join(items, "")
	}
	return strings.Join(items[:len(items)-1], ", ") + " or " + items[len(items)-1]
}

// reserved are the device names Windows refuses as a file name, with or
// without an extension.
var reserved = []string{"CON", "PRN", "AUX", "NUL",
	"COM1", "COM2", "COM3", "COM4", "COM5", "COM6", "COM7", "COM8", "COM9",
	"LPT1", "LPT2", "LPT3", "LPT4", "LPT5", "LPT6", "LPT7", "LPT8", "LPT9"}

// forbidden are the characters Windows refuses in a file name, besides
// the control characters and the separators.
const forbidden = `:<>"|?*`

// checkPath rejects a staged path that is not a slash path inside the
// tree, or that Windows cannot store: a backslash, which separates there,
// or a segment with a control character such as NUL, a character in
// forbidden or a device name.
func checkPath(name string) error {
	if !fs.ValidPath(name) || name == "." || strings.Contains(name, `\`) {
		return fmt.Errorf("staged path %q is not a slash path inside the tree", name)
	}
	for seg := range strings.SplitSeq(name, "/") {
		base, _, _ := strings.Cut(seg, ".")
		control := strings.ContainsFunc(seg, func(r rune) bool { return r < 0x20 })
		if control || strings.ContainsAny(seg, forbidden) || slices.Contains(reserved, strings.ToUpper(base)) {
			return fmt.Errorf("staged path %q: %q is not a valid file name on Windows", name, seg)
		}
	}
	return nil
}

// ValidName reports whether name can be one segment of a staged path: a
// file name every OS dotai supports can store.
func ValidName(name string) bool {
	return !strings.Contains(name, "/") && checkPath(name) == nil
}

// FoldsCase reports whether the file systems of goos usually compare
// names without case, as on Windows and macOS.
func FoldsCase(goos string) bool { return goos == "windows" || goos == "darwin" }

// LinkDestError is a link whose destination is not the one dotai links
// there: the entry of the shared skills folder named as the link (decision
// 0036). A state or a backup that records one was edited.
type LinkDestError struct {
	Target, Dest, Want string
}

func (e *LinkDestError) Error() string {
	return fmt.Sprintf("the link %s points at %q, but dotai links it only to %s: it was edited or is not one dotai made, so dotai stops", e.Target, e.Dest, e.Want)
}

// LinkDest is the only destination of a link at target under home: the
// entry of the shared skills folder of its name.
func LinkDest(home, target string) string {
	return filepath.Join(home, filepath.FromSlash(harness.SharedSkillsDir), filepath.Base(target))
}

// CheckLinkDest refuses dest, the destination of a link at target, with a
// *LinkDestError unless it is LinkDest(home, target), clean, compared
// without case when fold is set.
func CheckLinkDest(home, target, dest string, fold bool) error {
	want := LinkDest(home, target)
	if filepath.IsAbs(dest) && filepath.Clean(dest) == dest && (dest == want || fold && strings.EqualFold(dest, want)) {
		return nil
	}
	return &LinkDestError{Target: target, Dest: dest, Want: want}
}
