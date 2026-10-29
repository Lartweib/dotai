package pack

import (
	"errors"
	"io"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"go.yaml.in/yaml/v3"
)

// reader decodes the YAML of one file and collects its diagnostics.
type reader struct {
	file   string
	offset int // lines before the YAML document, such as a frontmatter fence
	diags  *[]Diagnostic
}

// report records a problem at node n; a nil n has no position.
func (r reader) report(n *yaml.Node, code, format string, args ...any) {
	line, col := 0, 0
	if n != nil {
		line, col = n.Line+r.offset, n.Column
	}
	*r.diags = append(*r.diags, NewDiagnostic(r.file, line, col, code, format, args...))
}

var yamlErr = regexp.MustCompile(`^yaml: (?:line (\d+): )?`)

// parse decodes src and returns its root node, nil when src is empty. A
// syntax error or a second document is reported with code and its line,
// and ok is false.
func (r reader) parse(src string, code string) (root *yaml.Node, ok bool) {
	var doc, next yaml.Node
	dec := yaml.NewDecoder(strings.NewReader(src))
	err := dec.Decode(&doc)
	if err == nil {
		if err = dec.Decode(&next); err == nil {
			r.report(&next, code, "only one YAML document is allowed")
			return nil, false
		}
	}
	if errors.Is(err, io.EOF) {
		err = nil
	}
	if err != nil {
		line := 0
		if m := yamlErr.FindStringSubmatch(err.Error()); m != nil && m[1] != "" {
			line, _ = strconv.Atoi(m[1])
			line += r.offset
		}
		*r.diags = append(*r.diags, NewDiagnostic(r.file, line, 0, code, "%s", yamlErr.ReplaceAllString(err.Error(), "")))
		return nil, false
	}
	if len(doc.Content) == 0 {
		return nil, true
	}
	return doc.Content[0], true
}

// object is a decoded YAML mapping. Missing required fields are reported at
// owner, the key that holds the mapping (or the mapping itself at the root).
type object struct {
	r     reader
	owner *yaml.Node
	what  string
	keys  map[string]*yaml.Node
	vals  map[string]*yaml.Node
	order []string
	bad   bool // not a mapping; already reported
}

// object decodes mapping n, reporting duplicate keys and, when known is not
// empty, unknown keys. If n is not a mapping, every getter returns zero.
func (r reader) object(owner, n *yaml.Node, what string, known ...string) object {
	o := object{r: r, owner: owner, what: what, keys: map[string]*yaml.Node{}, vals: map[string]*yaml.Node{}}
	if o.owner == nil {
		o.owner = n
	}
	if n == nil || n.Kind != yaml.MappingNode {
		r.report(n, CodeFieldType, "%s must be a mapping", what)
		o.bad = true
		return o
	}
	for i := 0; i+1 < len(n.Content); i += 2 {
		k, v := n.Content[i], n.Content[i+1]
		switch {
		case o.keys[k.Value] != nil:
			r.report(k, CodeFieldDuplicate, "%s: duplicate field %q", what, k.Value)
		case len(known) > 0 && !slices.Contains(known, k.Value):
			r.report(k, CodeFieldUnknown, "%s: unknown field %q", what, k.Value)
		default:
			o.keys[k.Value], o.vals[k.Value] = k, v
			o.order = append(o.order, k.Value)
		}
	}
	return o
}

// has reports whether key is set to a value other than null.
func (o object) has(key string) bool { return o.vals[key] != nil && o.vals[key].Tag != "!!null" }

// missing reports whether key is absent or null. A required key is
// reported: when absent at the owner, when null at its value, like an
// empty one.
func (o object) missing(key string, required bool) bool {
	switch n := o.vals[key]; {
	case n == nil:
		if required && !o.bad {
			o.r.report(o.owner, CodeFieldRequired, "%s: missing required field %q", o.what, key)
		}
		return true
	case n.Tag == "!!null":
		if required {
			o.r.report(n, CodeFieldRequired, "%s: %q must not be empty", o.what, key)
		}
		return true
	}
	return false
}

// str returns the scalar at key and its node.
func (o object) str(key string, required bool) (string, *yaml.Node) {
	if o.missing(key, required) {
		return "", nil
	}
	n := o.vals[key]
	if n.Kind != yaml.ScalarNode {
		o.r.report(n, CodeFieldType, "%s: %q must be a string", o.what, key)
		return "", nil
	}
	if required && n.Value == "" {
		o.r.report(n, CodeFieldRequired, "%s: %q must not be empty", o.what, key)
		return "", nil
	}
	return n.Value, n
}

// list returns the scalars of the sequence at key, with their nodes.
func (o object) list(key string, required bool) ([]string, []*yaml.Node) {
	if o.missing(key, required) {
		return nil, nil
	}
	n := o.vals[key]
	if n.Kind != yaml.SequenceNode {
		o.r.report(n, CodeFieldType, "%s: %q must be a list", o.what, key)
		return nil, nil
	}
	if required && len(n.Content) == 0 {
		o.r.report(n, CodeFieldRequired, "%s: %q must not be empty", o.what, key)
	}
	var vals []string
	var nodes []*yaml.Node
	for _, it := range n.Content {
		if it.Kind != yaml.ScalarNode {
			o.r.report(it, CodeFieldType, "%s: %q items must be strings", o.what, key)
			continue
		}
		vals, nodes = append(vals, it.Value), append(nodes, it)
	}
	return vals, nodes
}

// boolean returns the optional boolean at key. valid is false when key is
// missing or its value is not a boolean, which is reported.
func (o object) boolean(key string) (value, valid bool) {
	if o.missing(key, false) {
		return false, false
	}
	n := o.vals[key]
	if n.Kind != yaml.ScalarNode || n.Tag != "!!bool" {
		o.r.report(n, CodeFieldType, "%s: %q must be true or false", o.what, key)
		return false, false
	}
	b, _ := strconv.ParseBool(n.Value)
	return b, true
}
