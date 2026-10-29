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
	"slices"

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
