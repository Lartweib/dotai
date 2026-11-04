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

// claudeMdExcludes is the key of Claude Code's settings that skips
// instruction files by absolute path or glob; the lists of every settings
// layer merge.
const claudeMdExcludes = "claudeMdExcludes"

// homeInstructions are the instruction files of the real home directory
// that Claude Code finds by walking up from a working directory under it,
// even with CLAUDE_CONFIG_DIR set, as slash paths relative to it.
var homeInstructions = []string{"CLAUDE.md", "CLAUDE.local.md", ".claude/CLAUDE.md", ".claude/rules/**"}

// globMeta are the characters that make a glob pattern of a path.
const globMeta = `*?[]{}\`

// excludeHomeInstructions adds the patterns of homeInstructions under
// home to claudeMdExcludes in Claude Code's settings in the user staging
// tree, as a union after any list the pack sets (decision 0040), so that
// an isolated install owns the key like the pack's own (decision 0035).
// The settings file is created when the pack stages none. A home whose
// path holds glob characters gets no patterns, which would match other
// files, and a notice says so.
func excludeHomeInstructions(d stageFS, tree, home string) (notice string, err error) {
	h := filepath.ToSlash(home)
	if strings.ContainsAny(h, globMeta) {
		return fmt.Sprintf("the home directory %s holds a glob character, so Claude Code's claudeMdExcludes cannot name its CLAUDE.md files: they still load when the working directory is under it", home), nil
	}
	a, err := harness.Lookup([]string{"claudecode"})
	if err != nil {
		return "", err
	}
	name := a[0].SettingsSource()
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
	for _, rel := range homeInstructions {
		e, err := json.Marshal(h + "/" + rel)
		if err != nil {
			return "", err
		}
		elems = append(elems, e)
	}
	if err := doc.Union([]string{claudeMdExcludes}, elems...); err != nil {
		return "", fmt.Errorf("%s/%s: %s must be a list of patterns: %w", ScopeUser, name, claudeMdExcludes, err)
	}
	return "", d.WriteFile(tree, name, doc.Bytes(), 0o644)
}
