package app

import (
	"errors"
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/lartweib/dotai/internal/harness"
	"github.com/lartweib/dotai/internal/merge"
	"github.com/lartweib/dotai/internal/pack"
)

// CodeHookPathUnquotable is the diagnostic of a HookPathError.
const CodeHookPathUnquotable = "hook-path-unquotable"

// bareUnsafe are the characters, besides whitespace, that a bare path in a
// hook command cannot hold: quotes, which agy's cmd /c gets as \" and
// passes on, and what cmd.exe or a POSIX shell reads as an operator or a
// pattern. A character that a shell would expand inside double quotes too
// is refused for every hook, quoted or not.
const bareUnsafe = `"'&|<>^();*?[]{}`

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
		WithHint("install from a home or project directory whose path has no whitespace and none of %s, or run the tool from a hook of the blocks of the other harnesses only", bareUnsafe)
}

// expandHooks expands the tool command variables of decision 0037 in the
// hook commands of a scope's merged hooks file, data, whose origins give
// the layer of each hook, for the harnesses a stage renders. A hook of a
// tool-scoped block is written as its harness reads a command, and one of
// a block whose harness is not rendered is left as it is. The shared hooks
// reach every rendered harness, so their path is bare, with forward
// slashes, as soon as one of them takes it bare: bash, Bun Shell and
// cmd.exe all run that form (HARNESS-NOTES). A path that cannot be bare is
// a HookPathError.
func expandHooks(data []byte, origins map[string]string, tools ToolPaths, scope string, harnesses []string) ([]byte, error) {
	tools = tools.inScope(scope)
	dir, derr := tools.dir()
	return merge.ExpandHookTools(data, func(h merge.Hook, name string) (string, error) {
		ids := harnesses
		if h.Block != "" {
			id, ok := renderedBlock(h.Block, harnesses)
			if !ok {
				return "${DOTAI_TOOL:" + name + "}", nil
			}
			ids = []string{id}
		}
		if derr != nil {
			return "", fmt.Errorf("%s: ${DOTAI_TOOL:%s} cannot be expanded: %w", h, name, derr)
		}
		quoting, by := harness.ShellQuoted, ids[0]
		for _, id := range ids {
			if quotingOf(id) == harness.Unquoted {
				quoting, by = harness.Unquoted, id
				break
			}
		}
		if quoting == harness.Unquoted {
			if i := strings.IndexFunc(dir, func(r rune) bool { return unicode.IsSpace(r) || strings.ContainsRune(bareUnsafe, r) }); i >= 0 {
				r, _ := utf8.DecodeRuneInString(dir[i:])
				return "", &HookPathError{Layer: origins[h.Pointer()], Hook: h.String(), Harness: by, Tool: name, Dir: dir, Char: r}
			}
		}
		c, err := toolCommand(dir, name, tools.goos(), by, quoting, quoting == harness.Unquoted)
		if err != nil {
			return "", fmt.Errorf("%s: %w", h, err)
		}
		return c, nil
	})
}

// renderedBlock is the harness of harnesses whose tool-scoped hooks block
// is block, its rulesync target.
func renderedBlock(block string, harnesses []string) (string, bool) {
	for _, id := range harnesses {
		if a, err := harness.Lookup([]string{id}); err == nil && a[0].Target == block {
			return id, true
		}
	}
	return "", false
}

// hookPathDiagnostic is the diagnostic of err when it is a HookPathError.
func hookPathDiagnostic(err error) (Diagnostic, bool) {
	if e, ok := errors.AsType[*HookPathError](err); ok {
		return e.diagnostic(), true
	}
	return Diagnostic{}, false
}
