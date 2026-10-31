package merge

import (
	"cmp"
	"maps"
	"regexp"
	"slices"
	"strings"

	"go.yaml.in/yaml/v3"

	"github.com/lartweib/dotai/internal/pack"
)

// The tool command variable of decision 0037: ${DOTAI_TOOL:<name>} in a
// settings value stands for the command that runs the tool <name> on the
// OS of the install, from <name>.sh or <name>.ps1 in the scope's tools.
var toolRef = regexp.MustCompile(`\$\{DOTAI_TOOL:([^}]*)\}`)

// toolName is a tool's name in the variable: one file name, without its
// extension.
var toolName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)

// ToolScripts are the extensions of the two scripts a tool command runs:
// the POSIX shell script and the PowerShell one.
var ToolScripts = []string{".sh", ".ps1"}

// toolPrefix opens a tool command variable.
const toolPrefix = "${DOTAI_TOOL:"

// unclosedTool is what follows a tool command variable in s that has no
// closing brace, and whether there is one. Only the last can be: an
// earlier one without a brace after it leaves none after the last.
func unclosedTool(s string) (string, bool) {
	i := strings.LastIndex(s, toolPrefix)
	if i < 0 || strings.Contains(s[i:], "}") {
		return "", false
	}
	return s[i+len(toolPrefix):], true
}

// ExpandTools replaces each tool command variable in s with what command
// gives for its name, and stops at the first error. A name that is not a
// valid tool name, or a variable without its closing brace, is never
// passed to command: validation reports it.
func ExpandTools(s string, command func(name string) (string, error)) (string, error) {
	if rest, ok := unclosedTool(s); ok {
		return "", &ToolNameError{Name: rest, Unclosed: true}
	}
	var err error
	out := toolRef.ReplaceAllStringFunc(s, func(m string) string {
		if err != nil {
			return m
		}
		name := toolRef.FindStringSubmatch(m)[1]
		if !toolName.MatchString(name) {
			err = &ToolNameError{Name: name}
			return m
		}
		var c string
		c, err = command(name)
		return c
	})
	if err != nil {
		return "", err
	}
	return out, nil
}

// ToolNameError is a tool command variable whose name is not a tool name,
// or, with Unclosed, one without its closing brace, Name being what
// follows it.
type ToolNameError struct {
	Name     string
	Unclosed bool
}

func (e *ToolNameError) Error() string {
	if e.Unclosed {
		return toolPrefix + e.Name + " has no closing brace: write ${DOTAI_TOOL:<name>}"
	}
	return toolPrefix + e.Name + "} does not name a tool: a name is letters, digits, '.', '_' and '-', starting with a letter or a digit, without an extension"
}

// checkTools reports each tool command variable in the scope's settings
// whose name is not a tool name or that has no closing brace, or whose
// tool lacks one of its scripts in the scope's tools: the pack ships both,
// so that it installs on every OS.
func (s *scope) checkTools() []pack.Diagnostic {
	var diags []pack.Diagnostic
	settings := slices.SortedFunc(maps.Values(s.settings), func(a, b setting) int { return cmp.Compare(a.seq, b.seq) })
	for _, st := range settings {
		walkStrings(st.v, func(n *yaml.Node) {
			if rest, ok := unclosedTool(n.Value); ok {
				diags = append(diags, pack.NewDiagnostic(st.file, n.Line, n.Column, CodeToolInvalid, "setting %s uses %s%s, which has no closing brace", st.id(), toolPrefix, rest).
					WithHint("write ${DOTAI_TOOL:<name>}, with the name between the colon and the brace"))
			}
			for _, m := range toolRef.FindAllStringSubmatch(n.Value, -1) {
				name := m[1]
				if !toolName.MatchString(name) {
					diags = append(diags, pack.NewDiagnostic(st.file, n.Line, n.Column, CodeToolInvalid, "setting %s uses ${DOTAI_TOOL:%s}, which does not name a tool", st.id(), name).
						WithHint("name a tool by its file name without the extension: letters, digits, '.', '_' and '-'"))
					continue
				}
				for _, ext := range ToolScripts {
					if _, ok := s.files[ToolsDir+"/"+name+ext]; !ok {
						diags = append(diags, pack.NewDiagnostic(st.file, n.Line, n.Column, CodeToolMissing, "setting %s runs tool %s, but no layer of its scope provides %s%s%s", st.id(), name, toolsSource, name, ext).
							WithHint("ship both %s%s.sh and %s%s.ps1: the first runs on Linux and macOS, the second on Windows", toolsSource, name, toolsSource, name))
					}
				}
			}
		})
	}
	return diags
}

// walkStrings calls f with each string scalar of n, keys of mappings left
// out.
func walkStrings(n *yaml.Node, f func(*yaml.Node)) {
	switch n.Kind {
	case yaml.MappingNode:
		for i := 1; i < len(n.Content); i += 2 {
			walkStrings(n.Content[i], f)
		}
	case yaml.SequenceNode:
		for _, c := range n.Content {
			walkStrings(c, f)
		}
	case yaml.ScalarNode:
		if n.Tag == "!!str" && strings.Contains(n.Value, toolPrefix) {
			f(n)
		}
	}
}
