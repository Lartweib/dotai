package app

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"unicode/utf8"
)

// The executable content of a pack: what runs on the user's machine, or
// decides what installs, which an update shows before it replaces the
// clone. Decision 0044 names every layer's .rulesync/hooks.jsonc,
// .rulesync/mcp.jsonc, dotai/settings.yaml and dotai/tools/, and mods/;
// dotai also shows .rulesync/permissions.jsonc, every file other than
// Markdown in .rulesync/skills/, commands/ and subagents/, which a harness
// may run, dotai.yaml, which picks the layers and the mods, and any file
// with its executable bit. A path matches in any directory, declared as a
// layer or not, and in any case, so that neither a manifest that declares
// a new layer nor a case-insensitive file system can hide one.

// ExecChange is a file of executable content that an update adds, removes
// or modifies: its slash path from the pack root, Before and After, the
// sha256 of its content ("" when it is absent), its modes before and
// after, as the commits record them ("" when unknown or absent), and Diff,
// a unified diff. Binary says there is no diff, since the file or its path
// is not text a terminal or a page shows as it is, such as a control
// character that could hide a line: compare the hashes, as Note says.
type ExecChange struct {
	Path       string `json:"path"`
	Change     string `json:"change"`
	Before     string `json:"before"`
	After      string `json:"after"`
	ModeBefore string `json:"modeBefore"`
	ModeAfter  string `json:"modeAfter"`
	Binary     bool   `json:"binary"`
	Diff       string `json:"diff"`
	Note       string `json:"note"`
}

// The changes of an ExecChange.
const (
	ChangeAdded    = "added"
	ChangeRemoved  = "removed"
	ChangeModified = "modified"
)

// modeExecutable is the mode git records for an executable file.
const modeExecutable = "100755"

// execPath reports whether rel, a slash path from the pack root, is
// executable content by its name.
func execPath(rel string) bool {
	rel = strings.ToLower(rel)
	if rel == "dotai.yaml" || strings.HasPrefix(rel, "mods/") {
		return true
	}
	seg := "/" + rel
	for _, suffix := range []string{"/.rulesync/hooks.jsonc", "/.rulesync/mcp.jsonc", "/.rulesync/permissions.jsonc", "/dotai/settings.yaml"} {
		if strings.HasSuffix(seg, suffix) {
			return true
		}
	}
	if strings.Contains(seg, "/dotai/tools/") {
		return true
	}
	for _, dir := range []string{"/.rulesync/skills/", "/.rulesync/commands/", "/.rulesync/subagents/"} {
		if strings.Contains(seg, dir) && !strings.HasSuffix(rel, ".md") {
			return true
		}
	}
	return false
}

// execFiles reads the files under dir that are executable content, by
// their name or by their mode in modes, by slash path, skipping .git at
// the root; none when dir is "".
func execFiles(dir string, modes map[string]string) (map[string][]byte, error) {
	out := map[string][]byte{}
	if dir == "" {
		return out, nil
	}
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(dir, p)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if rel == ".git" {
			return filepath.SkipDir
		}
		if !d.Type().IsRegular() || !execPath(rel) && modes[rel] != modeExecutable {
			return nil
		}
		data, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		out[rel] = data
		return nil
	})
	return out, err
}

// execChanges compares the executable content of the clone old ("" for
// none) with that of new, by path, and with their modes, when both are
// known: a file whose mode changes is shown even when its bytes do not.
func execChanges(old, new string, oldModes, newModes map[string]string) ([]ExecChange, error) {
	before, err := execFiles(old, oldModes)
	if err != nil {
		return nil, err
	}
	after, err := execFiles(new, newModes)
	if err != nil {
		return nil, err
	}
	// A file executable on one side only is executable content on both.
	for p := range after {
		if _, ok := before[p]; !ok && old != "" {
			if data, err := os.ReadFile(filepath.Join(old, filepath.FromSlash(p))); err == nil {
				before[p] = data
			}
		}
	}
	for p := range before {
		if _, ok := after[p]; !ok {
			if data, err := os.ReadFile(filepath.Join(new, filepath.FromSlash(p))); err == nil {
				after[p] = data
			}
		}
	}
	var paths []string
	for p := range before {
		paths = append(paths, p)
	}
	for p := range after {
		if _, ok := before[p]; !ok {
			paths = append(paths, p)
		}
	}
	slices.Sort(paths)
	modesKnown := oldModes != nil && newModes != nil
	changes := []ExecChange{}
	for _, p := range paths {
		a, inOld := before[p]
		b, inNew := after[p]
		c := ExecChange{Path: p, Change: ChangeModified, Before: execHash(a, inOld), After: execHash(b, inNew)}
		if inOld {
			c.ModeBefore = oldModes[p]
		}
		if inNew {
			c.ModeAfter = newModes[p]
		}
		modeChanged := modesKnown && inOld && inNew && c.ModeBefore != c.ModeAfter
		if inOld && inNew && bytes.Equal(a, b) && !modeChanged {
			continue
		}
		oldName, newName := "a/"+p, "b/"+p
		switch {
		case !inOld:
			c.Change, oldName = ChangeAdded, ""
		case !inNew:
			c.Change, newName = ChangeRemoved, ""
		}
		var notes []string
		if modeChanged {
			notes = append(notes, fmt.Sprintf("mode %s -> %s", c.ModeBefore, c.ModeAfter))
		}
		if unsafePath(p) || isBinary(a) || isBinary(b) {
			c.Binary = true
			notes = append(notes, "binary, or text with control characters a terminal could hide lines with: compare the hashes")
		} else {
			c.Diff = unifiedDiff(oldName, newName, string(a), string(b))
		}
		c.Note = strings.Join(notes, "; ")
		changes = append(changes, c)
	}
	return changes, nil
}

// execHash is "sha256:" and the SHA-256 of data in hex, "" when it is
// absent.
func execHash(data []byte, present bool) string {
	if !present {
		return ""
	}
	sum := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(sum[:])
}

// isBinary reports whether data is not text a diff can show as it is: a
// NUL, invalid UTF-8, or a control character (unsafeText).
func isBinary(data []byte) bool {
	return bytes.IndexByte(data, 0) >= 0 || !utf8.Valid(data) || unsafeText(string(data))
}

// unsafeText reports whether s holds what a terminal or a page would not
// show as it is: a C0 control other than a tab, a line feed and a carriage
// return before a line feed, DEL, a C1 control, or a bidirectional
// override, which can reorder what a line shows.
func unsafeText(s string) bool {
	for i, r := range s {
		switch {
		case r == '\t' || r == '\n':
		case r == '\r':
			if !strings.HasPrefix(s[i+1:], "\n") {
				return true
			}
		case r < 0x20 || r == 0x7f || r >= 0x80 && r <= 0x9f:
			return true
		case r >= 0x202a && r <= 0x202e || r >= 0x2066 && r <= 0x2069:
			return true
		}
	}
	return false
}

// unsafePath reports whether a path holds a control character of any
// kind, a line feed, a carriage return and a tab included, or a
// bidirectional override: stricter than unsafeText, since a path is one
// line of a diff header, which a line feed in it could forge.
func unsafePath(p string) bool {
	return strings.ContainsFunc(p, func(r rune) bool {
		return r < 0x20 || r == 0x7f || r >= 0x80 && r <= 0x9f || r >= 0x202a && r <= 0x202e || r >= 0x2066 && r <= 0x2069
	})
}
