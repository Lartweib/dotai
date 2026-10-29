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
