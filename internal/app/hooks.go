package app

import (
	"errors"
	"fmt"
	"slices"

	"github.com/lartweib/dotai/internal/harness"
	"github.com/lartweib/dotai/internal/merge"
	"github.com/lartweib/dotai/internal/pack"
)

// CodeHookPathUnquotable is the diagnostic of a HookPathError.
const CodeHookPathUnquotable = "hook-path-unquotable"

// HookPathError is a hook that runs a tool of decision 0037 for a harness
// that takes the command's path bare, from a tools directory that holds a
// character a bare path cannot: Layer is the hook's layer, Hook names it,
// as in hook hooks.sessionStart, and Char is the first such character of
// Dir.
type HookPathError struct {
	Layer, Hook, Harness, Tool, Dir string
	Char                            rune
}

func (e *HookPathError) Error() string {
	return fmt.Sprintf("%s runs tool %s, but %s cannot run it from the tools directory %s, which holds %q: %s runs a hook through cmd /c and passes quotes on, so the path is bare, and a bare path cannot hold whitespace, quotes or shell operators",
		e.Hook, e.Tool, e.Harness, e.Dir, e.Char, e.Harness)
}

// diagnostic reports the error at the hooks.jsonc of the hook's layer.
func (e *HookPathError) diagnostic() Diagnostic {
	return pack.NewDiagnostic(e.Layer+"/"+merge.HooksFile, 0, 0, CodeHookPathUnquotable, "%s", e.Error()).
		WithHint("install from a home or project directory whose path has no whitespace and none of %s, or move the hook into the blocks of the harnesses that run it through a shell", bareUnsafe)
}

// hookPathStyle is the separators of a hook command's path read as
// quoting says: a bare path may reach bash, through a shared hook, and
// bash drops the backslashes of a bare path, while bash, Bun Shell and
// cmd.exe all run it with forward slashes (HARNESS-NOTES).
func hookPathStyle(quoting harness.Quoting) pathStyle {
	if quoting == harness.Unquoted {
		return forwardSlashes
	}
	return nativeSeparators
}

// expandHooks expands the tool command variables of decision 0037 in the
// hook commands of a scope's merged hooks file, data, whose origins give
// the layer of each hook, for the harnesses a stage renders. A hook of a
// tool-scoped block is written as its harness reads a command. A shared
// hook reaches each rendered harness whose own block does not declare its
// event (rulesync 43), so its path is bare as soon as one of those takes
// it bare. A hook that no rendered harness gets is left as it is, and a
// path that cannot be bare is a HookPathError.
func expandHooks(data []byte, origins map[string]string, tools ToolPaths, scope string, harnesses []string) ([]byte, error) {
	tools = tools.inScope(scope)
	dir, derr := tools.dir()
	return merge.ExpandHookTools(data, func(h merge.Hook, name string) (string, bool, error) {
		ids := readers(h, harnesses)
		if len(ids) == 0 {
			return "", false, nil
		}
		if derr != nil {
			return "", true, fmt.Errorf("%s: ${DOTAI_TOOL:%s} cannot be expanded: %w", h, name, derr)
		}
		quoting, by := harness.ShellQuoted, ids[0]
		if i := slices.IndexFunc(ids, func(id string) bool { return quotingOf(id) == harness.Unquoted }); i >= 0 {
			quoting, by = harness.Unquoted, ids[i]
		}
		c, err := toolCommand(dir, name, tools.goos(), by, quoting, hookPathStyle(quoting))
		if u, ok := errors.AsType[*UnquotableError](err); ok {
			return "", true, &HookPathError{Layer: origins[h.Pointer()], Hook: h.String(), Harness: by, Tool: name, Dir: dir, Char: u.Char}
		}
		if err != nil {
			return "", true, fmt.Errorf("%s: %w", h, err)
		}
		return c, true, nil
	})
}

// readers are the harnesses of harnesses that run hook h: the one whose
// rulesync target is h's block, or for a shared hook every one whose own
// block does not declare h's event.
func readers(h merge.Hook, harnesses []string) []string {
	var out []string
	for _, id := range harnesses {
		a, err := harness.Lookup([]string{id})
		if err != nil {
			continue
		}
		if h.Block == "" && !slices.Contains(h.Shadowed, a[0].Target) || h.Block != "" && a[0].Target == h.Block {
			out = append(out, id)
		}
	}
	return out
}

// hookPathDiagnostic is the diagnostic of err when it is a HookPathError.
func hookPathDiagnostic(err error) (Diagnostic, bool) {
	if e, ok := errors.AsType[*HookPathError](err); ok {
		return e.diagnostic(), true
	}
	return Diagnostic{}, false
}
