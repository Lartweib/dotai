package merge

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"slices"
	"strconv"
	"strings"

	"github.com/tailscale/hujson"

	"github.com/lartweib/dotai/internal/pack"
)

// A list file is merged across the layers of a scope as a union (decision
// 0040): objects merge by key, the items of an array are appended in layer
// order with an item identical to an earlier one, by canonical JSON, kept
// once, and a value that is neither an object nor an array must be the same
// in every layer that sets it. Nothing a layer adds can be removed or
// changed by a later one.
var listFiles = map[string]listCodes{
	HooksFile:                     {CodeHooksInvalid, CodeHooksConflict},
	".rulesync/permissions.jsonc": {CodePermissionsInvalid, CodePermissionsConflict},
}

// HooksFile is the hooks of a layer, and the merged hooks of a scope.
const HooksFile = ".rulesync/hooks.jsonc"

// sharedHooks is the member of a hooks file that holds the hooks of every
// harness; any other member with a hooks object of its own is a
// tool-scoped block, such as claudecode.
const sharedHooks = "hooks"

// listCodes are the diagnostic codes of a list file.
type listCodes struct{ invalid, conflict string }

// node is a value of a list file with where it comes from. An array's items
// are leaves: they are compared whole, never merged.
type node struct {
	kind  byte // '{', '[' or 0 for any other value
	keys  []string
	vals  map[string]*node
	items []*node
	raw   []byte // standard JSON of a leaf
	canon string // canonical JSON of a leaf, which identifies it
	layer pack.Layer
	file  string
	line  int // of the member name, or of the item
}

// mergeList merges the list file f of layer l into the scope's tree of it.
func (s *scope) mergeList(l pack.Layer, f pack.File) {
	codes := listFiles[f.Rel]
	src, err := readList(l, f)
	if err != nil {
		line, column := fileLine, 0
		if p, ok := errors.AsType[*positioned](err); ok {
			line, column = p.line, p.column
		}
		s.report(pack.NewDiagnostic(f.Path, line, column, codes.invalid, "%v", err))
		return
	}
	dst, ok := s.lists[f.Rel]
	if !ok {
		s.lists[f.Rel] = src
		return
	}
	s.unite(dst, src, "", codes.conflict)
}

// unite adds src to dst, which comes from earlier layers.
func (s *scope) unite(dst, src *node, ptr, code string) {
	switch {
	case dst.kind == '{' && src.kind == '{':
		for _, k := range src.keys {
			if prev, ok := dst.vals[k]; ok {
				s.unite(prev, src.vals[k], ptr+"/"+escape(k), code)
				continue
			}
			dst.keys = append(dst.keys, k)
			dst.vals[k] = src.vals[k]
		}
	case dst.kind == '[' && src.kind == '[':
		for _, it := range src.items {
			if !slices.ContainsFunc(dst.items, func(d *node) bool { return d.canon == it.canon }) {
				dst.items = append(dst.items, it)
			}
		}
	case dst.kind != src.kind || dst.canon != src.canon:
		s.report(pack.NewDiagnostic(src.file, src.line, 0, code, "%s already has a different value in %s", ptr, dst.layer.Path).WithRelated(dst.file, dst.line).
			WithHint("keep the same value; a layer cannot change or remove what an earlier layer of its scope sets"))
	}
}

// positioned is a problem at a place in a file; column 0 is the whole line.
type positioned struct {
	line, column int
	msg          string
}

func (p *positioned) Error() string { return p.msg }

// parseError moves the position hujson writes into its message, "hujson:
// line L, column C: cause", to a positioned error with the cause alone.
func parseError(err error) error {
	var line, column int
	cause := errors.Unwrap(err)
	if _, scan := fmt.Sscanf(err.Error(), "hujson: line %d, column %d:", &line, &column); scan != nil || cause == nil {
		return err
	}
	return &positioned{line, column, cause.Error()}
}

// readList reads a list file, which must hold a JSONC object.
func readList(l pack.Layer, f pack.File) (*node, error) {
	v, err := hujson.Parse(slices.Clone(f.Data)) // Parse aliases its input
	if err != nil {
		return nil, parseError(err)
	}
	v.Standardize()
	if _, ok := v.Value.(*hujson.Object); !ok {
		return nil, errors.New("want an object")
	}
	line := func(offset int) int { return 1 + bytes.Count(f.Data[:offset], []byte("\n")) }
	var read func(v hujson.Value, ptr string, at int) (*node, error)
	read = func(v hujson.Value, ptr string, at int) (*node, error) {
		n := &node{layer: l, file: f.Path, line: at}
		switch c := v.Value.(type) {
		case *hujson.Object:
			n.kind, n.vals = '{', map[string]*node{}
			for _, m := range c.Members {
				k := m.Name.Value.(hujson.Literal).String()
				p := ptr + "/" + escape(k)
				if _, ok := n.vals[k]; ok {
					return nil, &positioned{line(m.Name.StartOffset), 0, "duplicate key " + p}
				}
				child, err := read(m.Value, p, line(m.Name.StartOffset))
				if err != nil {
					return nil, err
				}
				n.keys = append(n.keys, k)
				n.vals[k] = child
			}
		case *hujson.Array:
			n.kind = '['
			for _, e := range c.Elements {
				it, err := leaf(e.Pack(), l, f.Path, line(e.StartOffset))
				if err != nil {
					return nil, err
				}
				if !slices.ContainsFunc(n.items, func(d *node) bool { return d.canon == it.canon }) {
					n.items = append(n.items, it) // an identical item is kept once, within a layer too
				}
			}
		default:
			return leaf(v.Pack(), l, f.Path, at)
		}
		return n, nil
	}
	return read(v, "", fileLine)
}

// leaf returns the leaf of the standard JSON raw.
func leaf(raw []byte, l pack.Layer, file string, line int) (*node, error) {
	canon, err := canonical(raw)
	if err != nil {
		return nil, err
	}
	return &node{raw: raw, canon: canon, layer: l, file: file, line: line}, nil
}

// canonical is the canonical JSON of the standard JSON raw: keys sorted,
// insignificant whitespace dropped and every number spelled one way.
func canonical(raw []byte) (string, error) {
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	var v any
	if err := d.Decode(&v); err != nil {
		return "", err
	}
	canon, err := marshal(normalize(v))
	return string(canon), err
}

// normalize gives every number of v one spelling, so that 1, 1.0 and 1e0
// are the same value. A number beyond 2^±4096 keeps its literal: printing
// it would cost memory in proportion to its exponent.
func normalize(v any) any {
	switch c := v.(type) {
	case json.Number:
		f, _, err := big.ParseFloat(string(c), 10, 512, big.ToNearestEven)
		if err != nil || f.IsInf() {
			return c
		}
		if exp := f.MantExp(nil); exp > 4096 || exp < -4096 {
			return c
		}
		return json.Number(f.Text('e', -1))
	case []any:
		for i := range c {
			c[i] = normalize(c[i])
		}
	case map[string]any:
		for k := range c {
			c[k] = normalize(c[k])
		}
	}
	return v
}

// marshal is json.Marshal without HTML escaping, so that a command such as
// a && b stays readable. Object keys come out sorted.
func marshal(v any) ([]byte, error) {
	var b bytes.Buffer
	e := json.NewEncoder(&b)
	e.SetEscapeHTML(false)
	if err := e.Encode(v); err != nil {
		return nil, err
	}
	return bytes.TrimSuffix(b.Bytes(), []byte("\n")), nil
}

// escape makes k a reference token of a JSON pointer (RFC 6901).
func escape(k string) string {
	return strings.NewReplacer("~", "~0", "/", "~1").Replace(k)
}

// clone returns a deep copy of n, so that a variant merged on a copy of the
// base leaves the base's tree as it was. Leaves are never changed.
func (n *node) clone() *node {
	if n.kind == 0 {
		return n
	}
	c := *n
	c.keys, c.items = slices.Clone(n.keys), slices.Clone(n.items)
	if n.vals != nil {
		c.vals = make(map[string]*node, len(n.vals))
		for k, v := range n.vals {
			c.vals[k] = v.clone()
		}
	}
	return &c
}

// listFile returns the merged file of a tree: indented JSON, keys in the
// order they first appeared, and the layer of every leaf and array item by
// its JSON pointer.
func listFile(path string, n *node) File {
	f := File{Path: path, Origins: map[string]string{}}
	var b bytes.Buffer
	var write func(n *node, ptr string)
	write = func(n *node, ptr string) {
		switch n.kind {
		case '{':
			b.WriteByte('{')
			for i, k := range n.keys {
				if i > 0 {
					b.WriteByte(',')
				}
				name, _ := marshal(k) // a string always marshals
				b.Write(name)
				b.WriteByte(':')
				write(n.vals[k], ptr+"/"+escape(k))
			}
			b.WriteByte('}')
		case '[':
			b.WriteByte('[')
			for i, it := range n.items {
				if i > 0 {
					b.WriteByte(',')
				}
				b.Write(it.raw)
				f.Origins[ptr+"/"+strconv.Itoa(i)] = it.layer.Path
			}
			b.WriteByte(']')
		default:
			b.Write(n.raw)
			f.Origins[ptr] = n.layer.Path
		}
	}
	write(n, "")
	var out bytes.Buffer
	if err := json.Indent(&out, b.Bytes(), "", "  "); err != nil {
		panic(fmt.Sprintf("merge: %s is not JSON: %v", path, err)) // built from standard JSON
	}
	out.WriteByte('\n')
	f.Data = out.Bytes()
	return f
}

// shadowedHooks warns about each event of a tool-scoped block of the
// scope's hooks that the shared hooks declare too: rulesync 41 renders the
// block's hooks of that event for its harness, and drops the shared ones
// (HARNESS-NOTES). The warning is at the event in the block and points at
// the event in the shared hooks.
func (s *scope) shadowedHooks() []pack.Diagnostic {
	n, ok := s.lists[HooksFile]
	if !ok || n.kind != '{' {
		return nil
	}
	shared := n.vals[sharedHooks]
	if shared == nil || shared.kind != '{' {
		return nil
	}
	var out []pack.Diagnostic
	for _, block := range n.keys {
		hooks := blockHooks(n, block)
		if hooks == nil {
			continue
		}
		for _, event := range hooks.keys {
			prev, ok := shared.vals[event]
			if !ok || prev.kind == '[' && len(prev.items) == 0 {
				continue // no shared hook of the event to lose
			}
			at := hooks.vals[event]
			out = append(out, pack.NewDiagnostic(at.file, at.line, 0, CodeHooksShadowed, "%s.hooks.%s replaces the shared hooks.%s for %s", block, event, event, block).WithRelated(prev.file, prev.line).
				WithHint("rulesync renders only one of them for %s: add the shared %s hooks to %s.hooks.%s too, or move the event out of one of the two", block, event, block, event))
		}
	}
	return out
}

// blockHooks is the hooks object of the tool-scoped block of the hooks
// file n, nil when that member of n is not a block.
func blockHooks(n *node, block string) *node {
	b := n.vals[block]
	if block == sharedHooks || b.kind != '{' {
		return nil
	}
	if h := b.vals[sharedHooks]; h != nil && h.kind == '{' {
		return h
	}
	return nil
}
