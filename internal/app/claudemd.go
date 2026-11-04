package app

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"slices"
	"strings"

	"github.com/lartweib/dotai/internal/harness"
	"github.com/lartweib/dotai/internal/plan/jsonc"
)

// globMeta are the characters that make a glob pattern of a path, those
// of extended globs included.
const globMeta = `*?[]{}()+@!\`

// homeForms are the forms of the home directory that Claude Code may see
// walking up, with forward slashes: as it is on disk, its links followed
// and, on Windows, in its on-disk case, and as given when that differs.
// Claude Code matches claudeMdExcludes with case (HARNESS-NOTES).
func homeForms(home string) []string {
	return slices.Compact([]string{filepath.ToSlash(realPath(home)), filepath.ToSlash(home)})
}

// homePatterns are the absolute patterns of paths under each of forms, in
// order; ok is false, with none, when a form holds a glob character, since
// its patterns would match other files.
func homePatterns(forms, paths []string) (patterns []string, ok bool) {
	for _, f := range forms {
		if strings.ContainsAny(f, globMeta) {
			return nil, false
		}
		for _, p := range paths {
			patterns = append(patterns, f+"/"+p)
		}
	}
	return patterns, true
}

// excludeHomeInstructions adds the patterns of a's Instructions under
// home to their key in a's settings in the user staging tree, as a union
// after any list the pack sets (decision 0040), so that an isolated
// install owns the key like the pack's own (decision 0035); a harness
// without Instructions is left alone. The settings file is created when
// the pack stages none. A home whose path holds glob characters gets no
// patterns, which would match other files, and a notice says so.
func excludeHomeInstructions(d stageFS, tree, home string, a harness.Adapter) (notice string, err error) {
	hi := a.Isolation.Instructions
	if hi == nil {
		return "", nil
	}
	patterns, ok := homePatterns(homeForms(home), hi.Paths)
	if !ok {
		return fmt.Sprintf("the home directory %s holds a glob character, so %s's %s cannot name its instruction files: they still load when the working directory is under it", home, a.ID, hi.Key), nil
	}
	name := a.SettingsSource()
	data, err := d.ReadFile(tree, name)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		data = []byte("{}\n")
	case err != nil:
		return "", err
	}
	doc, err := jsonc.Parse(data)
	if err != nil {
		return "", fmt.Errorf("%s/%s: %w", ScopeUser, name, err)
	}
	var elems [][]byte
	for _, p := range patterns {
		e, err := json.Marshal(p)
		if err != nil {
			return "", err
		}
		elems = append(elems, e)
	}
	if err := doc.Union([]string{hi.Key}, elems...); err != nil {
		return "", fmt.Errorf("%s/%s: %s must be a list of patterns: %w", ScopeUser, name, hi.Key, err)
	}
	return "", d.WriteFile(tree, name, doc.Bytes(), 0o644)
}

// homeExcluded reports whether a's isolated settings, in its isolated
// directory dir, list under the key of its Instructions every pattern an
// install writes for home, so that dotai run can say whether the home
// directory's instruction files still load: an install from before the
// key, or for a home with a glob character, has none. A harness without
// Instructions has nothing to exclude.
func homeExcluded(d stageFS, home, dir string, a harness.Adapter) bool {
	hi := a.Isolation.Instructions
	if hi == nil {
		return true
	}
	patterns, ok := homePatterns(homeForms(home), hi.Paths)
	if !ok {
		return false
	}
	data, err := d.ReadFile(filepath.Join(dir, filepath.FromSlash(a.Install)), a.Settings)
	if err != nil {
		return false
	}
	doc, err := jsonc.Parse(data)
	if err != nil {
		return false
	}
	raw, ok := doc.Get([]string{hi.Key})
	var list []string
	if !ok || json.Unmarshal(raw, &list) != nil {
		return false
	}
	for _, p := range patterns {
		if !slices.Contains(list, p) {
			return false
		}
	}
	return true
}
