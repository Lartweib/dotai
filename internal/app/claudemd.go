package app

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"strings"

	"github.com/lartweib/dotai/internal/harness"
	"github.com/lartweib/dotai/internal/plan/jsonc"
)

// globMeta are the characters that make a glob pattern of a path.
const globMeta = `*?[]{}\`

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
	h := filepath.ToSlash(home)
	if strings.ContainsAny(h, globMeta) {
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
	for _, rel := range hi.Paths {
		e, err := json.Marshal(h + "/" + rel)
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
