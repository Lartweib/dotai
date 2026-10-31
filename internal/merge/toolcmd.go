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

// ExpandTools replaces each tool command variable in s with what command
// gives for its name, and stops at the first error. A name that is not a
// valid tool name is never passed to command: validation reports it.
func ExpandTools(s string, command func(name string) (string, error)) (string, error) {
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

// ToolNameError is a tool command variable whose name is not a tool name.
type ToolNameError struct{ Name string }

func (e *ToolNameError) Error() string {
	return "${DOTAI_TOOL:" + e.Name + "} does not name a tool: a name is letters, digits, '.', '_' and '-', starting with a letter or a digit, without an extension"
}

// checkTools reports each tool command variable in the scope's settings
// whose name is not a tool name, or whose tool lacks one of its scripts in
// the scope's tools: the pack ships both, so that it installs on every OS.
func (s *scope) checkTools() []pack.Diagnostic {
	var diags []pack.Diagnostic
	settings := slices.SortedFunc(maps.Values(s.settings), func(a, b setting) int { return cmp.Compare(a.seq, b.seq) })
	for _, st := range settings {
		walkStrings(st.v, func(n *yaml.Node) {
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
		if n.Tag == "!!str" && strings.Contains(n.Value, "${DOTAI_TOOL:") {
			f(n)
		}
	}
}
