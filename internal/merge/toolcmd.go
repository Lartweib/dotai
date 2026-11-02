package merge

import (
	"cmp"
	"encoding/json"
	"errors"
	"maps"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/tailscale/hujson"
	"go.yaml.in/yaml/v3"

	"github.com/lartweib/dotai/internal/pack"
)

// The tool command variable of decision 0037: ${DOTAI_TOOL:<name>} in a
// settings value, or in the command of a hook, stands for the command that
// runs the tool <name> on the OS of the install, from <name>.sh or
// <name>.ps1 in the scope's tools.
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
// and hook commands whose name is not a tool name or that has no closing
// brace, or whose tool lacks one of its scripts in the scope's tools: the
// pack ships both, so that it installs on every OS.
func (s *scope) checkTools() []pack.Diagnostic {
	var diags []pack.Diagnostic
	settings := slices.SortedFunc(maps.Values(s.settings), func(a, b setting) int { return cmp.Compare(a.seq, b.seq) })
	for _, st := range settings {
		walkStrings(st.v, func(n *yaml.Node) {
			diags = append(diags, s.checkToolRefs(st.file, n.Line, n.Column, "setting "+st.id(), n.Value)...)
		})
	}
	if n, ok := s.lists[HooksFile]; ok {
		eachHook(n, func(h Hook, it *node, command string) {
			diags = append(diags, s.checkToolRefs(it.file, it.line, 0, h.String(), command)...)
		})
	}
	return diags
}

// checkToolRefs reports the problems of the tool command variables in v,
// the value of what, at line and column of file.
func (s *scope) checkToolRefs(file string, line, column int, what, v string) []pack.Diagnostic {
	var diags []pack.Diagnostic
	if rest, ok := unclosedTool(v); ok {
		diags = append(diags, pack.NewDiagnostic(file, line, column, CodeToolInvalid, "%s uses %s%s, which has no closing brace", what, toolPrefix, rest).
			WithHint("write ${DOTAI_TOOL:<name>}, with the name between the colon and the brace"))
	}
	for _, m := range toolRef.FindAllStringSubmatch(v, -1) {
		name := m[1]
		if !toolName.MatchString(name) {
			diags = append(diags, pack.NewDiagnostic(file, line, column, CodeToolInvalid, "%s uses ${DOTAI_TOOL:%s}, which does not name a tool", what, name).
				WithHint("name a tool by its file name without the extension: letters, digits, '.', '_' and '-'"))
			continue
		}
		for _, ext := range ToolScripts {
			if _, ok := s.files[ToolsDir+"/"+name+ext]; !ok {
				diags = append(diags, pack.NewDiagnostic(file, line, column, CodeToolMissing, "%s runs tool %s, but no layer of its scope provides %s%s%s", what, name, toolsSource, name, ext).
					WithHint("ship both %s%s.sh and %s%s.ps1: the first runs on Linux and macOS, the second on Windows", toolsSource, name, toolsSource, name))
			}
		}
	}
	return diags
}

// Hook is an entry of a hooks file: the tool-scoped block that holds it,
// such as claudecode, "" for the shared hooks, its event and its place
// among the event's entries.
type Hook struct {
	Block, Event string
	Index        int
}

// Pointer is the JSON pointer of h in the merged hooks file, the key of its
// layer in the file's origins.
func (h Hook) Pointer() string {
	p := "/" + sharedHooks + "/" + escape(h.Event) + "/" + strconv.Itoa(h.Index)
	if h.Block != "" {
		p = "/" + escape(h.Block) + p
	}
	return p
}

// String names h by its event in its block, as in hook claudecode.hooks.stop.
func (h Hook) String() string {
	if h.Block == "" {
		return "hook " + sharedHooks + "." + h.Event
	}
	return "hook " + h.Block + "." + sharedHooks + "." + h.Event
}

// eachHook calls f with each entry of the hooks file n, shared or in a
// tool-scoped block, whose command is a string, and that command.
func eachHook(n *node, f func(h Hook, it *node, command string)) {
	if n.kind != '{' {
		return
	}
	for _, block := range n.keys {
		events := blockHooks(n, block)
		if block == sharedHooks {
			events, block = n.vals[block], ""
		}
		if events == nil || events.kind != '{' {
			continue
		}
		for _, event := range events.keys {
			for i, it := range events.vals[event].items {
				var entry struct {
					Command *string `json:"command"`
				}
				if json.Unmarshal(it.raw, &entry) == nil && entry.Command != nil {
					f(Hook{Block: block, Event: event, Index: i}, it, *entry.Command)
				}
			}
		}
	}
}

// ExpandHookTools replaces each tool command variable in the commands of
// the hooks of data, a merged hooks file, with what command gives for the
// hook and the tool's name, and stops at the first error, which it returns
// as command gave it. The file keeps its layout and every other value.
// Validation reports a variable that is not a valid tool name.
func ExpandHookTools(data []byte, command func(h Hook, name string) (string, error)) ([]byte, error) {
	n, err := readList(pack.Layer{}, pack.File{Path: HooksFile, Data: data})
	if err != nil {
		return nil, err
	}
	var failed error
	eachHook(n, func(h Hook, it *node, c string) {
		if failed != nil || !strings.Contains(c, toolPrefix) {
			return
		}
		c, err := ExpandTools(c, func(name string) (string, error) { return command(h, name) })
		if err == nil {
			it.raw, err = withCommand(it.raw, c)
		}
		failed = err
	})
	if failed != nil {
		return nil, failed
	}
	return listFile(HooksFile, n).Data, nil
}

// withCommand is the hook entry raw, standard JSON, with c as its command.
func withCommand(raw []byte, c string) ([]byte, error) {
	v, err := hujson.Parse(slices.Clone(raw))
	if err != nil {
		return nil, err
	}
	obj, ok := v.Value.(*hujson.Object)
	if !ok {
		return nil, errors.New("a hook entry is not an object")
	}
	lit, err := marshal(c)
	if err != nil {
		return nil, err
	}
	for i, m := range obj.Members {
		if m.Name.Value.(hujson.Literal).String() == "command" {
			obj.Members[i].Value.Value = hujson.Literal(lit)
		}
	}
	return v.Pack(), nil
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
