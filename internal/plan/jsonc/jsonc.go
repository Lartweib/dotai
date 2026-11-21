// Package jsonc edits JSON and JSONC files key by key, on top of the hujson
// syntax tree, so that every byte the edit does not touch stays as it was:
// comments, key order, indentation, line endings and trailing commas. dotai
// edits only the keys it owns in files other tools also write, such as
// harness settings, so it must never reformat or drop anything else. Paths
// are object keys from the top-level object down; they never index into
// arrays, whose elements are read and edited by their index in the array at
// a path (Elements, RemoveElement, ReplaceElement).
//
// Known limits: the own-line comments directly above a deleted key go with
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

// bom is the UTF-8 byte order mark. Parse sets it aside and Bytes puts it
// back.
var bom = []byte("\xef\xbb\xbf")

// Document is a parsed JSON or JSONC object being edited.
type Document struct {
	src    []byte
	bom    bool          // the input starts with a UTF-8 byte order mark
	root   *hujson.Value // nil while the input is empty
	eol    string        // "\n" or "\r\n"
	indent string        // one indentation level: spaces or a tab
}

// Parse parses src, which must be empty or hold a single object that
// encoding/json can read too, so not nested deeper than it allows, and
// detects the style that new lines follow. The first line break decides the
// line ending, so a file with mixed endings gets the first one's. The indent
// unit is the indent of the first top-level key, or two spaces when there is
// none.
func Parse(src []byte) (*Document, error) {
	d := &Document{src: slices.Clone(src), eol: "\n", indent: "  "}
	if i := bytes.IndexByte(src, '\n'); i > 0 && src[i-1] == '\r' {
		d.eol = "\r\n"
	}
	src, d.bom = bytes.CutPrefix(src, bom)
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
	if _, err := decodeValue(v); err != nil {
		return nil, fmt.Errorf("not readable as JSON, such as nested too deeply: %w", err)
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
	if d.bom {
		return append(slices.Clone(bom), d.root.Pack()...)
	}
	return d.root.Pack()
}

// Get returns the value at path as compact standard JSON, without comments
// or trailing commas, and whether it exists. With duplicate keys the last one
// wins, here and in every edit.
func (d *Document) Get(path []string) ([]byte, bool) {
	v := d.lookup(path)
	if v == nil {
		return nil, false
	}
	b, err := compactJSON(*v)
	if err != nil { // Parse checked the document
		return nil, false
	}
	return b, true
}

// Keys returns the names of the members of the object at path, in the order
// they first appear, each once, and whether there is an object there. An
// empty document is an empty object.
func (d *Document) Keys(path []string) ([]string, bool) {
	if d.root == nil && len(path) == 0 {
		return nil, true
	}
	v := d.lookup(path)
	if v == nil {
		return nil, false
	}
	obj, ok := v.Value.(*hujson.Object)
	if !ok {
		return nil, false
	}
	var names []string
	for _, m := range obj.Members {
		if name := m.Name.Value.(hujson.Literal).String(); !slices.Contains(names, name) {
			names = append(names, name)
		}
	}
	return names, true
}

// Canonical returns valid JSON, such as a value Get returns, in a form where
// equal values are equal bytes; see equalityKey.
func Canonical(value []byte) ([]byte, error) {
	k, err := equalityKey(value)
	return []byte(k), err
}

// lookup returns the value at path, or nil when a key is missing or a value
// on the way is not an object. With duplicate keys the last one wins.
func (d *Document) lookup(path []string) *hujson.Value {
	v := d.root
	for _, key := range path {
		if v == nil {
			return nil
		}
		_, v = findMember(v, key)
	}
	return v
}

// findMember returns the index and value of the last member named key in the
// object v, or -1 and nil.
func findMember(v *hujson.Value, key string) (int, *hujson.Value) {
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

// compactJSON returns v as compact JSON without comments or trailing
// commas. A standardized value is valid JSON, unless it nests deeper than
// encoding/json reads.
func compactJSON(v hujson.Value) ([]byte, error) {
	c := v.Clone()
	c.BeforeExtra, c.AfterExtra = nil, nil
	c.Standardize()
	var b bytes.Buffer
	if err := json.Compact(&b, c.Pack()); err != nil {
		return nil, err
	}
	return b.Bytes(), nil
}

// decodeValue decodes v as decodeJSON does.
func decodeValue(v hujson.Value) (any, error) {
	b, err := compactJSON(v)
	if err != nil {
		return nil, err
	}
	return decodeJSON(b)
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
// object, and missing objects on the way are created. An object replacing
// an object is applied key by key, so comments inside it stay; any other
// value, an array included, replaces the old one as a whole. Setting a value
// equal to the current one as canonical JSON (see equalityKey) changes
// nothing.
func (d *Document) Set(path []string, value []byte) error {
	return d.verified(func() error { return d.set(path, value) }, func(top map[string]any) bool {
		parent := modelObject(top, path, true)
		if parent == nil {
			return false
		}
		x, err := decodeJSON(value)
		parent[path[len(path)-1]] = x
		return err == nil
	})
}

// Delete removes the key at path, every duplicate included, with its
// same-line comment and the own-line comments right above it, up to a blank
// line. A missing key is not an error. The parent object stays, even when it
// is left empty.
func (d *Document) Delete(path []string) error {
	return d.verified(func() error {
		if len(path) == 0 {
			return errors.New("empty path")
		}
		for d.lookup(path) != nil { // every duplicate of the key
			d.delete(path)
		}
		return nil
	}, func(top map[string]any) bool {
		if parent := modelObject(top, path, false); parent != nil {
			delete(parent, path[len(path)-1])
		}
		return true
	})
}

// Union appends to the array at path each element, which must be valid
// JSON, that the array does not hold yet, keeping the order of both. A
// missing array is created. Elements are compared as canonical JSON (see
// equalityKey), so numbers compare as written: 1.0 and 1 are different.
func (d *Document) Union(path []string, elems ...[]byte) error {
	return d.verified(func() error { return d.union(path, elems...) }, func(top map[string]any) bool {
		parent := modelObject(top, path, true)
		if parent == nil {
			return false
		}
		arr := []any{}
		if v, present := parent[path[len(path)-1]]; present {
			var ok bool
			if arr, ok = v.([]any); !ok {
				return false
			}
		}
		for _, e := range elems {
			x, err := decodeJSON(e)
			if err != nil {
				return false
			}
			if !slices.ContainsFunc(arr, func(y any) bool { return keyOf(y) == keyOf(x) }) {
				arr = append(arr, x)
			}
		}
		parent[path[len(path)-1]] = arr
		return true
	})
}

// Elements returns the elements of the array at path as compact standard
// JSON, in order, and whether there is an array there.
func (d *Document) Elements(path []string) ([][]byte, bool) {
	v := d.lookup(path)
	if v == nil {
		return nil, false
	}
	arr, ok := v.Value.(*hujson.Array)
	if !ok {
		return nil, false
	}
	out := make([][]byte, len(arr.Elements))
	for i, e := range arr.Elements {
		b, err := compactJSON(e) // Parse checked the document
		if err != nil {
			return nil, false
		}
		out[i] = b
	}
	return out, true
}

// element returns the array at path and checks that it has an element i.
func (d *Document) element(path []string, i int) (*hujson.Array, error) {
	v := d.lookup(path)
	if v == nil {
		return nil, fmt.Errorf("there is no array at %q", path)
	}
	arr, ok := v.Value.(*hujson.Array)
	if !ok {
		return nil, fmt.Errorf("%q is not an array", path)
	}
	if i < 0 || i >= len(arr.Elements) {
		return nil, fmt.Errorf("%q has no element %d", path, i)
	}
	return arr, nil
}

// modelArray returns the array in the model top at path, or nil.
func modelArray(top map[string]any, path []string) []any {
	parent := modelObject(top, path, false)
	if parent == nil {
		return nil
	}
	arr, _ := parent[path[len(path)-1]].([]any)
	return arr
}

// RemoveElement removes element i of the array at path, with its same-line
// comment and the own-line comments right above it, up to a blank line.
// The array stays, even when it is left empty.
func (d *Document) RemoveElement(path []string, i int) error {
	return d.verified(func() error {
		arr, err := d.element(path, i)
		if err != nil {
			return err
		}
		d.deleteElement(arr, i, len(path))
		return nil
	}, func(top map[string]any) bool {
		arr := modelArray(top, path)
		if i < 0 || i >= len(arr) {
			return false
		}
		modelParent := modelObject(top, path, false)
		modelParent[path[len(path)-1]] = slices.Delete(slices.Clone(arr), i, i+1)
		return true
	})
}

// ReplaceElement replaces element i of the array at path by value, which
// must be valid JSON, keeping its position and the comments around it.
func (d *Document) ReplaceElement(path []string, i int, value []byte) error {
	return d.verified(func() error {
		if !json.Valid(value) {
			return fmt.Errorf("value for %q is not valid JSON", path)
		}
		arr, err := d.element(path, i)
		if err != nil {
			return err
		}
		e := &arr.Elements[i]
		if same, err := equalJSON(*e, value); err != nil || same {
			return err
		}
		ind, own := lineIndent(e.BeforeExtra)
		e.Value = d.render(value, ind, own && !writtenInline(e.Value))
		return nil
	}, func(top map[string]any) bool {
		arr := modelArray(top, path)
		x, err := decodeJSON(value)
		if i < 0 || i >= len(arr) || err != nil {
			return false
		}
		arr[i] = x
		return true
	})
}

// verified runs edit and keeps its result only when the output parses again
// and equals, as canonical JSON, the document that model computes on its
// own: model applies the same edit to the decoded top-level object. Any
// difference, such as a sibling key lost in a comment, undoes the edit and
// returns an error, so a bug in the edit layer can never emit a wrong file.
// The model is a plain map: canonical JSON sorts keys, and the golden tests
// guard key order.
func (d *Document) verified(edit func() error, model func(top map[string]any) bool) error {
	// Snapshot the packed bytes: Clone loses the empty marker of a trailing
	// comma, and the bytes parse back to the same tree.
	var saved []byte
	top := map[string]any{}
	if d.root != nil {
		saved = d.root.Pack()
		x, err := decodeValue(*d.root)
		var ok bool
		if top, ok = x.(map[string]any); err != nil || !ok { // Parse checked it
			return errors.New("the document cannot be read as a JSON object")
		}
	}
	err := edit()
	if err == nil && !(model(top) && d.matches(top)) {
		err = errors.New("the edit failed its check and was undone")
	}
	if err != nil {
		d.root = nil
		if saved != nil {
			v, _ := hujson.Parse(saved) // it parsed before the edit
			d.root = &v
		}
	}
	return err
}

// matches reports whether the document's bytes parse and equal want as
// canonical JSON.
func (d *Document) matches(want map[string]any) bool {
	after, err := Parse(d.Bytes())
	if err != nil {
		return false
	}
	got := keyOf(map[string]any{})
	if after.root != nil {
		x, err := decodeValue(*after.root)
		if err != nil {
			return false
		}
		got = keyOf(x)
	}
	return got == keyOf(want)
}

// modelObject returns the object in the model top that holds the last key of
// path, or nil when a value on the way is not an object. With create, it
// adds the missing objects on the way, as Set does.
func modelObject(top map[string]any, path []string, create bool) map[string]any {
	if len(path) == 0 {
		return nil
	}
	m := top
	for _, key := range path[:len(path)-1] {
		v, present := m[key]
		if !present && create {
			v = map[string]any{}
			m[key] = v
		}
		next, ok := v.(map[string]any)
		if !ok {
			return nil
		}
		m = next
	}
	return m
}

// set does the work of Set; verified checks it.
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
		j, v := findMember(parent, key)
		if v == nil {
			d.appendMember(obj, i+1, key, nest(path[i+1:], value))
			return nil
		}
		if i < len(path)-1 {
			parent = v
			continue
		}
		if same, err := equalJSON(*v, value); err != nil || same {
			return err
		}
		next, _ := hujson.Parse(slices.Clone(value))
		oldObj, wasObj := v.Value.(*hujson.Object)
		if newObj, isObj := next.Value.(*hujson.Object); wasObj && isObj {
			return d.setObject(path, oldObj, newObj)
		}
		// A key on its own line gets the new value spread over lines, unless
		// the old value was written inline.
		ind, own := lineIndent(obj.Members[j].Name.BeforeExtra)
		v.Value = d.render(value, ind, own && !writtenInline(v.Value))
	}
	return nil
}

// setObject turns the object old at path into value key by key: it sets the
// keys of value in their order and deletes the keys value does not have, so
// the comments inside old stay.
func (d *Document) setObject(path []string, old, value *hujson.Object) error {
	var gone []string
	for _, m := range old.Members {
		gone = append(gone, m.Name.Value.(hujson.Literal).String())
	}
	for _, m := range value.Members {
		key := m.Name.Value.(hujson.Literal).String()
		gone = slices.DeleteFunc(gone, func(k string) bool { return k == key })
		b, err := compactJSON(m.Value)
		if err != nil {
			return err
		}
		if err := d.set(append(slices.Clip(path), key), b); err != nil {
			return err
		}
	}
	for _, key := range gone {
		for p := append(slices.Clip(path), key); d.lookup(p) != nil; {
			d.delete(p)
		}
	}
	return nil
}

// delete removes the last member at path, which must exist.
func (d *Document) delete(path []string) {
	parent := d.lookup(path[:len(path)-1])
	i, _ := findMember(parent, path[len(path)-1])
	obj := parent.Value.(*hujson.Object)
	n, gone := len(obj.Members), obj.Members[i]
	// next is the text after the key: before the next key, or before the
	// closing brace. It receives what stays around the deleted key.
	next := &obj.AfterExtra
	if i+1 < n {
		next = &obj.Members[i+1].Name.BeforeExtra
	}
	// What stays of the text before the key, then what follows the key's
	// own line, without the key's same-line comment.
	keep := keptBefore(gone.Name.BeforeExtra)
	_, rest := cutLine(*next)
	if endsInLineComment(keep) && len(lineBreaks(*next)) == 0 {
		// The kept line comment would swallow what follows: break the line,
		// indented for the next key or for the closing brace.
		ind, own := lineIndent(gone.Name.BeforeExtra)
		if !own || i == n-1 {
			ind = strings.Repeat(d.indent, len(path)-1)
		}
		rest = append(hujson.Extra(d.eol+ind), bytes.TrimLeft(rest, " \t")...)
	}
	merged := append(keep, rest...)
	if i == 0 && len(lineBreaks(*next)) == 0 && !hasComment(*next) {
		// A first key on one line: keep the spacing after the brace.
		merged = slices.Clone(gone.Name.BeforeExtra)
	}
	if i == n-1 && i > 0 {
		// The previous member becomes the last one: it takes the deleted
		// member's trailing comma, or none.
		prev := &obj.Members[i-1].Value
		if gone.Value.AfterExtra == nil {
			// No trailing comma: move the text before prev's comma out
			// and drop the comma.
			merged = append(slices.Clone(prev.AfterExtra), merged...)
			prev.AfterExtra = nil
		} else if prev.AfterExtra == nil {
			prev.AfterExtra = hujson.Extra{} // a non-nil AfterExtra emits it
		}
	}
	if n == 1 && !hasComment(merged) {
		merged = nil // the object is now empty: write it as {}
	}
	*next = merged
	obj.Members = slices.Delete(obj.Members, i, i+1)
}

// deleteElement removes element i of arr, which sits in the array at a path
// of depth keys, as delete removes a member.
func (d *Document) deleteElement(arr *hujson.Array, i, depth int) {
	n, gone := len(arr.Elements), arr.Elements[i]
	// next is the text after the element: before the next one, or before the
	// closing bracket. It receives what stays around the deleted element.
	next := &arr.AfterExtra
	if i+1 < n {
		next = &arr.Elements[i+1].BeforeExtra
	}
	keep := keptBefore(gone.BeforeExtra)
	_, rest := cutLine(*next)
	if endsInLineComment(keep) && len(lineBreaks(*next)) == 0 {
		ind, own := lineIndent(gone.BeforeExtra)
		if !own || i == n-1 {
			ind = strings.Repeat(d.indent, depth)
		}
		rest = append(hujson.Extra(d.eol+ind), bytes.TrimLeft(rest, " 	")...)
	}
	merged := append(keep, rest...)
	if i == 0 && len(lineBreaks(*next)) == 0 && !hasComment(*next) {
		// A first element on one line: keep the spacing after the bracket.
		merged = slices.Clone(gone.BeforeExtra)
	}
	if i == n-1 && i > 0 {
		// The previous element becomes the last one: it takes the deleted
		// element's trailing comma, or none.
		prev := &arr.Elements[i-1]
		if gone.AfterExtra == nil {
			merged = append(slices.Clone(prev.AfterExtra), merged...)
			prev.AfterExtra = nil
		} else if prev.AfterExtra == nil {
			prev.AfterExtra = hujson.Extra{}
		}
	}
	if n == 1 && !hasComment(merged) {
		merged = nil // the array is now empty: write it as []
	}
	*next = merged
	arr.Elements = slices.Delete(arr.Elements, i, i+1)
}

// union does the work of Union; verified checks it.
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
		held := false
		for _, x := range arr.Elements {
			same, err := equalJSON(x, e)
			if err != nil {
				return err
			}
			held = held || same
		}
		if !held {
			d.appendElement(arr, len(path)+1, e)
		}
	}
	return nil
}

// appendMember appends the member key: value to obj, whose members sit at
// depth.
func (d *Document) appendMember(obj *hujson.Object, depth int, key string, value []byte) {
	var last *hujson.Value
	var lastBefore hujson.Extra
	if n := len(obj.Members); n > 0 {
		last, lastBefore = &obj.Members[n-1].Value, obj.Members[n-1].Name.BeforeExtra
	}
	before, item := d.newItem(&obj.AfterExtra, last, lastBefore, depth, value)
	item.BeforeExtra = hujson.Extra(" ")
	obj.Members = append(obj.Members, hujson.ObjectMember{
		Name:  hujson.Value{BeforeExtra: before, Value: hujson.String(key)},
		Value: item,
	})
}

// appendElement appends value to arr, whose elements sit at depth.
func (d *Document) appendElement(arr *hujson.Array, depth int, value []byte) {
	var last *hujson.Value
	var lastBefore hujson.Extra
	if n := len(arr.Elements); n > 0 {
		last = &arr.Elements[n-1]
		lastBefore = last.BeforeExtra
	}
	before, item := d.newItem(&arr.AfterExtra, last, lastBefore, depth, value)
	item.BeforeExtra = before
	arr.Elements = append(arr.Elements, item)
}

// newItem lays out value as a new last item of a container: tail is the
// text before the closing bracket, last the current last item or nil, and
// lastBefore the text before it. The new item follows the last one's style,
// on its own line with the same indent or inline; in an empty container it
// gets its own line. newItem moves tail and fixes the commas, and returns
// the text to put before the item and the item itself.
func (d *Document) newItem(tail *hujson.Extra, last *hujson.Value, lastBefore hujson.Extra, depth int, value []byte) (hujson.Extra, hujson.Value) {
	ind, own := lineIndent(lastBefore)
	// A comment on the last item's line stays on that line.
	head, rest := cutLine(*tail)
	if last == nil {
		ind, own = strings.Repeat(d.indent, depth), true
		if !hasComment(rest) {
			rest = hujson.Extra(d.eol + strings.Repeat(d.indent, depth-1))
		}
	}
	if !own && endsInLineComment(head) {
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
	*tail = rest
	return before, item
}

// writtenInline reports whether v is a non-empty array or object written on
// one line.
func writtenInline(v hujson.ValueTrimmed) bool {
	n := 0
	switch x := v.(type) {
	case *hujson.Object:
		n = len(x.Members)
	case *hujson.Array:
		n = len(x.Elements)
	}
	return n > 0 && !bytes.ContainsRune(hujson.Value{Value: v}.Pack(), '\n')
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

// equalityKey returns valid JSON in a form where equal values are equal
// strings: compact, with object keys sorted. Numbers stay as written, so
// 1.0 and 1 are different.
func equalityKey(b []byte) (string, error) {
	x, err := decodeJSON(b)
	if err != nil {
		return "", err
	}
	return keyOf(x), nil
}

// equalJSON reports whether v and the valid JSON b are equal as canonical
// JSON.
func equalJSON(v hujson.Value, b []byte) (bool, error) {
	x, err := decodeValue(v)
	if err != nil {
		return false, err
	}
	k, err := equalityKey(b)
	return err == nil && keyOf(x) == k, err
}

// keyOf returns the equality key of a decoded value; see equalityKey.
func keyOf(x any) string {
	out, _ := json.Marshal(x)
	return string(out)
}

// decodeJSON decodes JSON into maps, slices and json.Number values, so
// numbers keep the text they were written with. Valid JSON fails only when
// it nests deeper than encoding/json reads.
func decodeJSON(b []byte) (any, error) {
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.UseNumber()
	var x any
	err := dec.Decode(&x)
	return x, err
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

// keptBefore returns what stays of x, the text before a deleted key: the
// comment that ends the previous line, and every line up to the last blank
// one. The own-line comments right above the key go with it.
func keptBefore(x hujson.Extra) hujson.Extra {
	breaks := lineBreaks(x)
	for j := len(breaks) - 1; j > 0; j-- {
		if len(bytes.TrimSpace(x[breaks[j-1]+1:breaks[j]])) == 0 {
			return slices.Clone(bytes.TrimSuffix(x[:breaks[j]], []byte("\r")))
		}
	}
	head, _ := cutLine(x)
	return head
}

// endsInLineComment reports whether x ends inside a line comment, so that
// anything written after it needs a line break first.
func endsInLineComment(x []byte) bool {
	for i := 0; i < len(x); i++ {
		switch {
		case bytes.HasPrefix(x[i:], []byte("//")):
			end := bytes.IndexByte(x[i:], '\n')
			if end < 0 {
				return true
			}
			i += end
		case bytes.HasPrefix(x[i:], []byte("/*")):
			end := bytes.Index(x[i+2:], []byte("*/"))
			if end < 0 {
				return false
			}
			i += end + 3
		}
	}
	return false
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
