package jsonc_test

import (
	"bytes"
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/lartweib/dotai/internal/plan/jsonc"
)

var update = flag.Bool("update", false, "rewrite golden files")

// edit is one line of a testdata/*.edits file.
type edit struct {
	Op    string
	Path  []string
	Value json.RawMessage
}

// TestGolden applies each testdata/NAME.edits to NAME.before.* and compares
// the result with NAME.after.*, then applies the edits again to check that
// they are idempotent. An .edits file holds one JSON object per edit, in
// order: {"op": "set", "path": [...], "value": <JSON>}, {"op": "delete",
// "path": [...]} or {"op": "union", "path": [...], "value": [<elements>]}.
// Run go test -update to rewrite the .after files, then review their diff.
func TestGolden(t *testing.T) {
	scripts, err := filepath.Glob("testdata/*.edits")
	if err != nil || len(scripts) == 0 {
		t.Fatalf("no edit scripts: %v", err)
	}
	for _, script := range scripts {
		name := strings.TrimSuffix(script, ".edits")
		t.Run(filepath.Base(name), func(t *testing.T) {
			befores, _ := filepath.Glob(name + ".before.json*")
			if len(befores) != 1 {
				t.Fatalf("want one before file for %s, got %v", name, befores)
			}
			before, after := befores[0], strings.Replace(befores[0], ".before.", ".after.", 1)
			got := applyEdits(t, read(t, before), read(t, script))
			if _, err := jsonc.Parse(got); err != nil {
				t.Fatalf("output does not parse: %v\n%s", err, got)
			}
			if *update {
				if err := os.WriteFile(after, got, 0o644); err != nil {
					t.Fatal(err)
				}
			}
			if want := read(t, after); !bytes.Equal(got, want) {
				t.Errorf("mismatch with %s\ngot:\n%s\nwant:\n%s", after, got, want)
			}
			if again := applyEdits(t, got, read(t, script)); !bytes.Equal(again, got) {
				t.Errorf("edits are not idempotent\nfirst:\n%s\nsecond:\n%s", got, again)
			}
		})
	}
}

func applyEdits(t *testing.T, src, script []byte) []byte {
	t.Helper()
	doc, err := jsonc.Parse(src)
	if err != nil {
		t.Fatal(err)
	}
	dec := json.NewDecoder(bytes.NewReader(script))
	for dec.More() {
		var e edit
		if err := dec.Decode(&e); err != nil {
			t.Fatal(err)
		}
		switch e.Op {
		case "set":
			err = doc.Set(e.Path, e.Value)
		case "delete":
			err = doc.Delete(e.Path)
		case "union":
			var raw []json.RawMessage
			if err := json.Unmarshal(e.Value, &raw); err != nil {
				t.Fatal(err)
			}
			elems := make([][]byte, len(raw))
			for i, r := range raw {
				elems[i] = r
			}
			err = doc.Union(e.Path, elems...)
		default:
			t.Fatalf("unknown op %q", e.Op)
		}
		if err != nil {
			t.Fatalf("%s %q: %v", e.Op, e.Path, err)
		}
	}
	return doc.Bytes()
}

func read(t testing.TB, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestEditErrors(t *testing.T) {
	doc, err := jsonc.Parse([]byte(`{"a": 1}`))
	if err != nil {
		t.Fatal(err)
	}
	if err := doc.Set([]string{"b"}, []byte(`{nope`)); err == nil {
		t.Error("Set with invalid JSON succeeded")
	}
	if err := doc.Set([]string{"a", "b"}, []byte(`1`)); err == nil {
		t.Error("Set through a number succeeded")
	}
	if err := doc.Set([]string{"\x9a"}, []byte(`1`)); err == nil {
		t.Error("Set of a key Get cannot read back succeeded")
	}
	if err := doc.Set(nil, []byte(`{}`)); err == nil {
		t.Error("Set with an empty path succeeded")
	}
	if err := doc.Union([]string{"a"}, []byte(`2`)); err == nil {
		t.Error("Union on a number succeeded")
	}
	if err := doc.Union([]string{"b"}, []byte(`{nope`)); err == nil {
		t.Error("Union with invalid JSON succeeded")
	}
	if got := string(doc.Bytes()); got != `{"a": 1}` {
		t.Errorf("failed edits changed the document: %s", got)
	}
}

func fixtures(t testing.TB) []string {
	t.Helper()
	paths, err := filepath.Glob("testdata/*.before.json*")
	if err != nil || len(paths) == 0 {
		t.Fatalf("no fixtures: %v", err)
	}
	return paths
}

func TestRoundTripIsByteIdentical(t *testing.T) {
	for _, path := range fixtures(t) {
		src, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		doc, err := jsonc.Parse(src)
		if err != nil {
			t.Fatalf("%s: %v", path, err)
		}
		if got := doc.Bytes(); !bytes.Equal(got, src) {
			t.Errorf("%s: round trip changed the bytes\ngot:\n%q\nwant:\n%q", path, got, src)
		}
	}
}

func TestParseRejectsNonObjects(t *testing.T) {
	for _, src := range []string{`[1]`, `"text"`, `{"a": }`, `// only a comment`} {
		if _, err := jsonc.Parse([]byte(src)); err == nil {
			t.Errorf("Parse(%q) succeeded, want an error", src)
		}
	}
}

func TestGet(t *testing.T) {
	doc, err := jsonc.Parse([]byte(`{
  "a": 1, // one
  "b": {"c": [1, /* two */ 2,], "d": null},
  "e": "x"
}`))
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		path []string
		want string
		ok   bool
	}{
		{[]string{"a"}, `1`, true},
		{[]string{"b", "c"}, `[1,2]`, true},
		{[]string{"b", "d"}, `null`, true},
		{[]string{"b"}, `{"c":[1,2],"d":null}`, true},
		{[]string{"missing"}, ``, false},
		{[]string{"e", "x"}, ``, false},
		{[]string{"b", "c", "0"}, ``, false},
	}
	for _, tt := range tests {
		got, ok := doc.Get(tt.path)
		if string(got) != tt.want || ok != tt.ok {
			t.Errorf("Get(%q) = %s, %v; want %s, %v", tt.path, got, ok, tt.want, tt.ok)
		}
	}
}

func set(path []string, value string) func(*jsonc.Document) error {
	return func(d *jsonc.Document) error { return d.Set(path, []byte(value)) }
}

func del(path []string) func(*jsonc.Document) error {
	return func(d *jsonc.Document) error { return d.Delete(path) }
}

func union(path []string, elems ...string) func(*jsonc.Document) error {
	return func(d *jsonc.Document) error {
		raw := make([][]byte, len(elems))
		for i, e := range elems {
			raw[i] = []byte(e)
		}
		return d.Union(path, raw...)
	}
}

// TestEdits covers layouts too small or odd for a golden file.
func TestEdits(t *testing.T) {
	tests := []struct {
		name string
		src  string
		edit func(*jsonc.Document) error
		want string
	}{
		{"append after a line comment on an inline object",
			"{\"a\": 1 // c\n}", set([]string{"b"}, `2`),
			"{\"a\": 1, // c\n  \"b\": 2\n}"},
		{"append after a line comment on an inline array",
			"{\"allow\": [\"x\", \"y\" // c\n]}", union([]string{"allow"}, `"z"`),
			"{\"allow\": [\"x\", \"y\", // c\n    \"z\"\n]}"},
		{"delete before a multi-line block comment",
			"{\n  \"a\": 1, /* x\n y */\n  \"b\": 2\n}\n", del([]string{"a"}),
			"{\n  \"b\": 2\n}\n"},
		{"delete after a multi-line header comment",
			"{ /* hdr\n x */\n  \"a\": 1,\n  \"b\": 2\n}", del([]string{"a"}),
			"{ /* hdr\n x */\n  \"b\": 2\n}"},
		{"append after a multi-line block comment",
			"{\n  \"a\": 1 /* x\n y */\n}\n", set([]string{"b"}, `2`),
			"{\n  \"a\": 1, /* x\n y */\n  \"b\": 2\n}\n"},
		{"replace an object keeps the comments inside it",
			"{\"a\": {// keep\n \"x\": 1}}", set([]string{"a"}, `{"x": 2}`),
			"{\"a\": {// keep\n \"x\": 2}}"},
		{"replace an object sets, adds and deletes only what differs",
			"{\n  \"a\": {\n    // keep\n    \"x\": 1,\n    \"y\": 2\n  }\n}\n",
			set([]string{"a"}, `{"z": {"n": 3}, "x": 1}`),
			"{\n  \"a\": {\n    // keep\n    \"x\": 1,\n    \"z\": {\n      \"n\": 3\n    }\n  }\n}\n"},
		{"delete keeps a section header behind a blank line",
			"{\n  \"a\": 1,\n\n  // Section\n\n  // Lead of b.\n  \"b\": 2,\n  \"c\": 3\n}\n", del([]string{"b"}),
			"{\n  \"a\": 1,\n\n  // Section\n\n  \"c\": 3\n}\n"},
		{"delete keeps the next key's inline comment",
			`{"a": 1, /* note on b */ "b": 2}`, del([]string{"a"}),
			`{ /* note on b */ "b": 2}`},
		{"delete keeps the line break after a kept line comment",
			"{\n  \"a\": 1, // about a\n  \"b\": 2, \"c\": 3\n}\n", del([]string{"b"}),
			"{\n  \"a\": 1, // about a\n  \"c\": 3\n}\n"},
		{"delete of the last key keeps the line break after a line comment",
			"{\"a\": 1, // note\n \"b\": 2}", del([]string{"b"}),
			"{\"a\": 1 // note\n}"},
		{"delete of a nested last key indents the closing brace",
			"{\n  \"o\": {\n    \"a\": 1, // note\n    \"b\": 2}\n}\n", del([]string{"o", "b"}),
			"{\n  \"o\": {\n    \"a\": 1 // note\n  }\n}\n"},
		{"delete of a missing key changes nothing",
			"{\"a\": 1 /* c */}", del([]string{"b"}),
			"{\"a\": 1 /* c */}"},
		{"delete on an empty file changes nothing",
			"\n", del([]string{"a"}),
			"\n"},
		{"replace an array as a whole",
			"{\n  \"a\": [1, /* gone */ 2]\n}\n", set([]string{"a"}, `[3]`),
			"{\n  \"a\": [3]\n}\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			doc, err := jsonc.Parse([]byte(tt.src))
			if err != nil {
				t.Fatal(err)
			}
			if err := tt.edit(doc); err != nil {
				t.Fatal(err)
			}
			if got := string(doc.Bytes()); got != tt.want {
				t.Errorf("got:\n%q\nwant:\n%q", got, tt.want)
			}
		})
	}
}

// reviewerCases are layouts that once broke an edit. They seed the fuzz
// corpus next to the golden fixtures.
var reviewerCases = []string{
	"{\"a\": 1 // c\n}",
	"{\"allow\": [\"x\", \"y\" // c\n]}",
	"{\n  \"a\": 1, /* x\n y */\n  \"b\": 2\n}\n",
	"{ /* hdr\n x */\n  \"a\": 1,\n  \"b\": 2\n}",
	"{\n  \"a\": 1 /* x\n y */\n}\n",
	"{\"a\": {// keep\n \"x\": 1}}",
	"{\n  \"a\": 1, // about a\n  \"b\": 2, \"c\": 3\n}\n",
	"{\"a\": 1, // note\n \"b\": 2}",
	"{\"a\": 1,\n}",
	`{"a": 1, /* note on b */ "b": 2}`,
	"{\n  \"a\": 1,\n\n  // Section\n\n  // Lead of b.\n  \"b\": 2,\n  \"c\": 3\n}\n",
}

// fuzzEdit fuzzes one edit of a top-level key. model applies the same edit
// to the decoded document and reports whether the edit should succeed. A
// failed edit must leave the bytes as they were; a successful one must give
// a document that parses and equals the model as JSON.
func fuzzEdit(f *testing.F, edit func(d *jsonc.Document, key, text string) error, model func(top map[string]any, key, text string) bool) {
	var seeds [][]byte
	for _, path := range fixtures(f) {
		seeds = append(seeds, read(f, path))
	}
	for _, src := range reviewerCases {
		seeds = append(seeds, []byte(src))
	}
	for _, src := range seeds {
		for _, key := range []string{"a", "b", "c", "allow", "new"} {
			f.Add(src, key, "x")
		}
	}
	f.Fuzz(func(t *testing.T, src []byte, key, text string) {
		doc, err := jsonc.Parse(src)
		if err != nil || !utf8.ValidString(key) || !utf8.ValidString(text) {
			return
		}
		top := wholeDocument(doc)
		ok := model(top, key, text)
		if err := edit(doc, key, text); err != nil {
			if !bytes.Equal(doc.Bytes(), src) {
				t.Fatalf("failed edit changed the bytes\n%q", doc.Bytes())
			}
			if ok {
				t.Fatalf("edit failed: %v\n%q", err, src)
			}
			return
		}
		if !ok {
			t.Fatalf("edit succeeded, want an error\n%q", src)
		}
		again, err := jsonc.Parse(doc.Bytes())
		if err != nil {
			t.Fatalf("output does not parse: %v\n%q", err, doc.Bytes())
		}
		if got := wholeDocument(again); !reflect.DeepEqual(got, top) {
			t.Fatalf("got %v, want %v\nsrc: %q\nout: %q", got, top, src, doc.Bytes())
		}
	})
}

// wholeDocument decodes the document's top-level object.
func wholeDocument(d *jsonc.Document) map[string]any {
	whole, ok := d.Get(nil)
	if !ok {
		return map[string]any{}
	}
	return decode(whole).(map[string]any)
}

func decode(b []byte) any {
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.UseNumber()
	var x any
	_ = dec.Decode(&x)
	return x
}

func setValue(text string) []byte {
	value, _ := json.Marshal(map[string]any{"list": []string{text}})
	return value
}

func FuzzSet(f *testing.F) {
	fuzzEdit(f, func(d *jsonc.Document, key, text string) error {
		return d.Set([]string{key}, setValue(text))
	}, func(top map[string]any, key, text string) bool {
		top[key] = decode(setValue(text))
		return true
	})
}

func FuzzDelete(f *testing.F) {
	fuzzEdit(f, func(d *jsonc.Document, key, _ string) error {
		return d.Delete([]string{key})
	}, func(top map[string]any, key, _ string) bool {
		delete(top, key)
		return true
	})
}

func FuzzUnion(f *testing.F) {
	fuzzEdit(f, func(d *jsonc.Document, key, text string) error {
		elem, _ := json.Marshal(text)
		return d.Union([]string{key}, elem, []byte(`1`))
	}, func(top map[string]any, key, text string) bool {
		arr := []any{}
		if v, present := top[key]; present {
			var ok bool
			if arr, ok = v.([]any); !ok {
				return false
			}
		}
		for _, e := range []any{text, json.Number("1")} {
			if !slices.ContainsFunc(arr, func(x any) bool { return reflect.DeepEqual(x, e) }) {
				arr = append(arr, e)
			}
		}
		top[key] = arr
		return true
	})
}

// TestFailedEditKeepsBytes checks that an edit that fails leaves the
// document exactly as it was.
func TestFailedEditKeepsBytes(t *testing.T) {
	for _, src := range []string{"{\"a\": 1,\n}", "{\n  \"a\": [1,],\n  \"o\": {\"x\": 1,},\n}\n"} {
		doc, err := jsonc.Parse([]byte(src))
		if err != nil {
			t.Fatal(err)
		}
		for _, edit := range []func(*jsonc.Document) error{
			set([]string{"a", "b"}, `1`),
			set([]string{"\x9a"}, `1`),
			union([]string{"a"}, `{nope`),
			union([]string{"a", "x"}, `2`),
		} {
			if err := edit(doc); err == nil {
				t.Fatalf("edit on %q succeeded, want an error", src)
			}
			if got := string(doc.Bytes()); got != src {
				t.Errorf("failed edit changed the bytes\ngot:  %q\nwant: %q", got, src)
			}
		}
	}
}
