package merge

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
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
	".rulesync/hooks.jsonc":       {CodeHooksInvalid, CodeHooksConflict},
	".rulesync/permissions.jsonc": {CodePermissionsInvalid, CodePermissionsConflict},
}

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
		line := fileLine
		if d, ok := errors.AsType[*duplicateKey](err); ok {
			line = d.line
		}
		s.report(pack.NewDiagnostic(f.Path, line, 0, codes.invalid, "%v", err))
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

// duplicateKey is a key that appears twice in one object of a file.
type duplicateKey struct {
	ptr  string
	line int
}

func (d *duplicateKey) Error() string { return "duplicate key " + d.ptr }

// readList reads a list file, which must hold a JSONC object.
func readList(l pack.Layer, f pack.File) (*node, error) {
	v, err := hujson.Parse(slices.Clone(f.Data)) // Parse aliases its input
	if err != nil {
		return nil, err
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
					return nil, &duplicateKey{p, line(m.Name.StartOffset)}
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
				n.items = append(n.items, it)
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
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	var v any
	if err := d.Decode(&v); err != nil {
		return nil, err
	}
	canon, err := marshal(v)
	if err != nil {
		return nil, err
	}
	return &node{raw: raw, canon: string(canon), layer: l, file: file, line: line}, nil
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
