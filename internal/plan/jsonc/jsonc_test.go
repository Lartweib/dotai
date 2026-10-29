package jsonc_test

import (
	"bytes"
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"

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
// they are idempotent.
func TestGolden(t *testing.T) {
	scripts, _ := filepath.Glob("testdata/*.edits")
	for _, script := range scripts {
		name := strings.TrimSuffix(script, ".edits")
		t.Run(filepath.Base(name), func(t *testing.T) {
			befores, _ := filepath.Glob(name + ".before.json*")
			if len(befores) != 1 {
				t.Fatalf("want one before file for %s, got %v", name, befores)
			}
			before, after := befores[0], strings.Replace(befores[0], ".before.", ".after.", 1)
			got := applyEdits(t, read(t, before), read(t, script))
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

func read(t *testing.T, path string) []byte {
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

func fixtures(t *testing.T) []string {
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
