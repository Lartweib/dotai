package jsonc_test

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/lartweib/dotai/internal/plan/jsonc"
)

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
