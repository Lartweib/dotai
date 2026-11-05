package server_test

import (
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
)

// dictionary reads the dictionary name of js/i18n.js: each key with the
// sorted placeholders its words name, in every plural form.
func dictionary(t *testing.T, src, name string) map[string][]string {
	t.Helper()
	start := strings.Index(src, "const "+name+" = {")
	if start < 0 {
		t.Fatalf("js/i18n.js has no dictionary %s", name)
	}
	end := strings.Index(src[start:], "\n};")
	if end < 0 {
		t.Fatalf("the dictionary %s does not end", name)
	}
	entry := regexp.MustCompile(`^\s+"([^"]+)": (.*),$`)
	holder := regexp.MustCompile(`\{(\w+)\}`)
	out := map[string][]string{}
	for line := range strings.SplitSeq(src[start:start+end], "\n") {
		m := entry.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		if _, dup := out[m[1]]; dup {
			t.Errorf("%s holds %q twice", name, m[1])
		}
		var names []string
		for _, h := range holder.FindAllStringSubmatch(m[2], -1) {
			if !slices.Contains(names, h[1]) {
				names = append(names, h[1])
			}
		}
		slices.Sort(names)
		out[m[1]] = names
	}
	return out
}

// TestTheDictionariesHaveTheSameKeysAndPlaceholders checks that the English
// and Spanish words of the web UI name the same keys, each with the same
// placeholders, so that no board shows a key or a raw {name}.
func TestTheDictionariesHaveTheSameKeysAndPlaceholders(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("web", "js", "i18n.js"))
	if err != nil {
		t.Fatal(err)
	}
	en, es := dictionary(t, string(data), "en"), dictionary(t, string(data), "es")
	if len(en) < 100 {
		t.Fatalf("read %d English keys, want the dictionary", len(en))
	}
	for k, want := range en {
		got, ok := es[k]
		switch {
		case !ok:
			t.Errorf("Spanish lacks %q", k)
		case !slices.Equal(got, want):
			t.Errorf("%q: Spanish names %v, English %v", k, got, want)
		}
	}
	for k := range es {
		if _, ok := en[k]; !ok {
			t.Errorf("English lacks %q", k)
		}
	}
}
