// Package jsonc edits JSON and JSONC files key by key, on top of the hujson
// syntax tree, so that every byte the edit does not touch stays as it was:
// comments, key order, indentation, line endings and trailing commas
// (decision 0028). Paths are object keys from the top-level object down;
// they never index into arrays.
//
// Known limits: an own-line comment directly above a deleted key goes with
// it, and an inserted array or object is written one item per line when its
// parent spans several lines.
package jsonc

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/tailscale/hujson"
)

// Document is a parsed JSON or JSONC object being edited.
type Document struct {
	src    []byte
	root   *hujson.Value // nil while the input is empty
	eol    string        // "\n" or "\r\n"
	indent string        // one indentation level: spaces or a tab
}

// Parse parses src, which must be empty or hold a single object, and
// detects its line ending and indentation unit.
func Parse(src []byte) (*Document, error) {
	d := &Document{src: slices.Clone(src), eol: "\n", indent: "  "}
	if i := bytes.IndexByte(src, '\n'); i > 0 && src[i-1] == '\r' {
		d.eol = "\r\n"
	}
	if len(bytes.TrimSpace(src)) == 0 {
		return d, nil
	}
	v, err := hujson.Parse(slices.Clone(src)) // Parse aliases its input
	if err != nil {
		return nil, err
	}
	obj, ok := v.Value.(*hujson.Object)
	if !ok {
		return nil, errors.New("top-level value is not an object")
	}
	if len(obj.Members) > 0 {
		if ind, ok := lineIndent(obj.Members[0].Name.BeforeExtra); ok && ind != "" {
			d.indent = ind
		}
	}
	d.root = &v
	return d, nil
}

// Bytes returns the document; without edits it is the input, byte for byte.
func (d *Document) Bytes() []byte {
	if d.root == nil {
		return slices.Clone(d.src)
	}
	return d.root.Pack()
}

// Get returns the value at path as compact standard JSON, without comments,
// and whether it exists.
func (d *Document) Get(path []string) ([]byte, bool) {
	v := d.lookup(path)
	if v == nil {
		return nil, false
	}
	return standard(*v), true
}

// lookup returns the value at path, or nil when a key is missing or a value
// on the way is not an object. With duplicate keys the last one wins.
func (d *Document) lookup(path []string) *hujson.Value {
	v := d.root
	for _, key := range path {
		if v == nil {
			return nil
		}
		_, v = member(v, key)
	}
	return v
}

// member returns the index and value of the last member named key in the
// object v, or -1 and nil.
func member(v *hujson.Value, key string) (int, *hujson.Value) {
	obj, ok := v.Value.(*hujson.Object)
	if !ok {
		return -1, nil
	}
	for i := len(obj.Members) - 1; i >= 0; i-- {
		if name, _ := obj.Members[i].Name.Value.(hujson.Literal); name.String() == key {
			return i, &obj.Members[i].Value
		}
	}
	return -1, nil
}

// standard returns v as compact JSON without comments or trailing commas.
func standard(v hujson.Value) []byte {
	c := v.Clone()
	c.BeforeExtra, c.AfterExtra = nil, nil
	c.Standardize()
	var b bytes.Buffer
	_ = json.Compact(&b, c.Pack()) // a standardized value is valid JSON
	return b.Bytes()
}

// lineIndent returns the whitespace after the last line break of x, and
// false when x has no line break or that whitespace is followed by a comment.
func lineIndent(x hujson.Extra) (string, bool) {
	i := bytes.LastIndexByte(x, '\n')
	if i < 0 || len(bytes.Trim(x[i+1:], " \t")) > 0 {
		return "", false
	}
	return string(x[i+1:]), true
}

// Set sets the value at path to value, which must be valid JSON. An existing
// key keeps its position and comments; a new key goes at the end of its
// object, and missing objects on the way are created.
func (d *Document) Set(path []string, value []byte) error {
	return d.verified(func() error { return d.set(path, value) }, func(after *Document) bool {
		got, ok := after.Get(path)
		return ok && canonical(got) == canonical(value)
	})
}

// Delete removes the key at path with its same-line comment and the own-line
// comments right above it. A missing key is not an error.
func (d *Document) Delete(path []string) error {
	return d.verified(func() error {
		if len(path) == 0 {
			return errors.New("empty path")
		}
		for d.lookup(path) != nil { // every duplicate of the key
			d.delete(path)
		}
		return nil
	}, func(after *Document) bool {
		_, ok := after.Get(path)
		return !ok
	})
}

// Union appends to the array at path each element, which must be valid
// JSON, that the array does not hold yet, keeping the order of both. A
// missing array is created. Elements are compared as canonical JSON (see
// canonical), so numbers compare as written: 1.0 and 1 are different.
func (d *Document) Union(path []string, elems ...[]byte) error {
	return d.verified(func() error { return d.union(path, elems...) }, func(after *Document) bool {
		got, _ := after.Get(path)
		var held []json.RawMessage
		_ = json.Unmarshal(got, &held)
		for _, e := range elems {
			if !slices.ContainsFunc(held, func(h json.RawMessage) bool { return canonical(h) == canonical(e) }) {
				return false
			}
		}
		return true
	})
}

// verified runs edit and keeps its result only when the output parses again
// and check accepts the parsed output. Otherwise it puts the document back
// as it was and returns an error, so a bug in the edit layer can never emit
// a corrupt file.
func (d *Document) verified(edit func() error, check func(after *Document) bool) error {
	var saved *hujson.Value
	if d.root != nil {
		c := d.root.Clone()
		saved = &c
	}
	err := edit()
	if err == nil {
		after, perr := Parse(d.Bytes())
		if perr != nil || !check(after) {
			err = errors.New("the edit failed its check and was undone")
		}
	}
	if err != nil {
		d.root = saved
	}
	return err
}

// set does the work of Set.
func (d *Document) set(path []string, value []byte) error {
	if len(path) == 0 {
		return errors.New("empty path")
	}
	if !json.Valid(value) {
		return fmt.Errorf("value for %q is not valid JSON", path)
	}
	if d.root == nil {
		d.root = &hujson.Value{Value: &hujson.Object{}, AfterExtra: hujson.Extra(d.eol)}
	}
	parent := d.root
	for i, key := range path {
		obj, ok := parent.Value.(*hujson.Object)
		if !ok {
			return fmt.Errorf("%q is not an object", path[:i])
		}
		j, v := member(parent, key)
		if v == nil {
			d.appendTo(parent, i+1, &key, nest(path[i+1:], value))
			return nil
		}
		if i < len(path)-1 {
			parent = v
			continue
		}
		old := standard(*v)
		if canonical(old) == canonical(value) {
			return nil
		}
		// Spread the new value over lines unless the old one was a
		// non-empty array or object written on one line.
		_, literal := v.Value.(hujson.Literal)
		spread := literal || len(old) <= 2 || bytes.ContainsRune(hujson.Value{Value: v.Value}.Pack(), '\n')
		ind, own := lineIndent(obj.Members[j].Name.BeforeExtra)
		v.Value = d.render(value, ind, own && spread)
	}
	return nil
}

// delete removes the last member at path, which must exist.
func (d *Document) delete(path []string) {
	parent := d.lookup(path[:len(path)-1])
	i, _ := member(parent, path[len(path)-1])
	obj := parent.Value.(*hujson.Object)
	n, gone := len(obj.Members), obj.Members[i]
	next := &obj.AfterExtra
	if i+1 < n {
		next = &obj.Members[i+1].Name.BeforeExtra
	}
	// Keep the comment that ends the line before the key, and what follows
	// the key's own line.
	keep, _ := cutLine(gone.Name.BeforeExtra)
	_, rest := cutLine(*next)
	merged := append(keep, rest...)
	if i == 0 && !bytes.ContainsRune(*next, '\n') {
		merged = slices.Clone(gone.Name.BeforeExtra) // one-line object
	}
	if i == n-1 && i > 0 {
		// The previous member becomes the last one: it takes the deleted
		// member's trailing comma, or none.
		prev := &obj.Members[i-1].Value
		if gone.Value.AfterExtra == nil {
			merged = append(slices.Clone(prev.AfterExtra), merged...)
			prev.AfterExtra = nil
		} else if prev.AfterExtra == nil {
			prev.AfterExtra = hujson.Extra{}
		}
	}
	if n == 1 && !hasComment(merged) {
		merged = nil
	}
	*next = merged
	obj.Members = slices.Delete(obj.Members, i, i+1)
}

// union does the work of Union.
func (d *Document) union(path []string, elems ...[]byte) error {
	for _, e := range elems {
		if !json.Valid(e) {
			return fmt.Errorf("element for %q is not valid JSON", path)
		}
	}
	v := d.lookup(path)
	if v == nil {
		if err := d.set(path, []byte("[]")); err != nil {
			return err
		}
		v = d.lookup(path)
	}
	arr, ok := v.Value.(*hujson.Array)
	if !ok {
		return fmt.Errorf("%q is not an array", path)
	}
	for _, e := range elems {
		held := func(x hujson.Value) bool { return canonical(standard(x)) == canonical(e) }
		if !slices.ContainsFunc(arr.Elements, held) {
			d.appendTo(v, len(path)+1, nil, e)
		}
	}
	return nil
}

// appendTo appends value to the object or array c, whose items sit at depth.
// name is the new member's key, or nil for an array element. The new item
// follows the style of the last one: on its own line with the same indent,
// or inline. An empty container is spread over lines.
func (d *Document) appendTo(c *hujson.Value, depth int, name *string, value []byte) {
	var tail *hujson.Extra
	var last *hujson.Value
	var ind string
	var own bool
	switch x := c.Value.(type) {
	case *hujson.Object:
		tail = &x.AfterExtra
		if n := len(x.Members); n > 0 {
			last = &x.Members[n-1].Value
			ind, own = lineIndent(x.Members[n-1].Name.BeforeExtra)
		}
	case *hujson.Array:
		tail = &x.AfterExtra
		if n := len(x.Elements); n > 0 {
			last = &x.Elements[n-1]
			ind, own = lineIndent(last.BeforeExtra)
		}
	}
	// A comment on the last item's line stays on that line.
	head, rest := cutLine(*tail)
	if last == nil {
		ind, own = strings.Repeat(d.indent, depth), true
		if !hasComment(rest) {
			rest = hujson.Extra(d.eol + strings.Repeat(d.indent, depth-1))
		}
	}
	if !own && bytes.Contains(head, []byte("//")) {
		// A line comment runs to the end of the line: break after it.
		ind, own = strings.Repeat(d.indent, depth), true
	}
	before := append(head, ' ')
	if own {
		before = append(head, d.eol+ind...)
	}
	item := hujson.Value{Value: d.render(value, ind, own)}
	if last != nil && last.AfterExtra != nil {
		item.AfterExtra = hujson.Extra{} // keep the trailing comma
	} else if last != nil {
		last.AfterExtra = hujson.Extra{} // add the separating comma
	}
	switch x := c.Value.(type) {
	case *hujson.Object:
		item.BeforeExtra = hujson.Extra(" ")
		x.Members = append(x.Members, hujson.ObjectMember{
			Name:  hujson.Value{BeforeExtra: before, Value: hujson.String(*name)},
			Value: item,
		})
	case *hujson.Array:
		item.BeforeExtra = before
		x.Elements = append(x.Elements, item)
	}
	*tail = rest
}

// render turns valid JSON into a value: compact, or spread over lines with
// prefix as the indent of its first line.
func (d *Document) render(value []byte, prefix string, spread bool) hujson.ValueTrimmed {
	var b bytes.Buffer
	if spread {
		_ = json.Indent(&b, value, prefix, d.indent)
	} else {
		_ = json.Compact(&b, value)
	}
	v, _ := hujson.Parse(bytes.ReplaceAll(b.Bytes(), []byte("\n"), []byte(d.eol)))
	return v.Value
}

// nest wraps value in one object per key, outermost first.
func nest(keys []string, value []byte) []byte {
	for i := len(keys) - 1; i >= 0; i-- {
		k, _ := json.Marshal(keys[i])
		value = slices.Concat([]byte("{"), k, []byte(":"), value, []byte("}"))
	}
	return value
}

// canonical returns valid JSON in a form where equal values are equal
// strings: compact, with object keys sorted. Numbers stay as written, so
// 1.0 and 1 are different.
func canonical(b []byte) string {
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.UseNumber()
	var x any
	_ = dec.Decode(&x)
	out, _ := json.Marshal(x)
	return string(out)
}

// lineBreaks returns the offsets of the line breaks in x, the whitespace and
// comments between two tokens, that are not inside a block comment.
func lineBreaks(x []byte) []int {
	var out []int
	for i := 0; i < len(x); i++ {
		switch {
		case bytes.HasPrefix(x[i:], []byte("//")):
			i += bytes.IndexByte(x[i:], '\n') - 1 // the next turn sees the '\n'
		case bytes.HasPrefix(x[i:], []byte("/*")):
			i += bytes.Index(x[i+2:], []byte("*/")) + 3
		case x[i] == '\n':
			out = append(out, i)
		}
	}
	return out
}

// cutLine splits x at its first line break outside a block comment. head is
// the text before it when that holds a comment, and rest starts at the line
// break. Without a line break, head is nil and rest is x. Both are copies.
func cutLine(x hujson.Extra) (head, rest hujson.Extra) {
	breaks := lineBreaks(x)
	if len(breaks) == 0 {
		return nil, slices.Clone(x)
	}
	i := breaks[0]
	if i > 0 && x[i-1] == '\r' {
		i--
	}
	if hasComment(x[:i]) {
		head = slices.Clone(x[:i])
	}
	return head, slices.Clone(x[i:])
}

// hasComment reports whether the whitespace and comments in x hold a comment.
func hasComment(x []byte) bool { return bytes.IndexByte(x, '/') >= 0 }
