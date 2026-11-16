package server_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// declarations reads the custom properties of the rule that opens with
// selector in css, in order, as "name: value".
func declarations(t *testing.T, css, selector string) []string {
	t.Helper()
	start := strings.Index(css, selector+" {")
	if start < 0 {
		t.Fatalf("styles.css has no rule %s", selector)
	}
	body := css[start+len(selector)+2:]
	end := strings.Index(body, "}")
	if end < 0 {
		t.Fatalf("styles.css: the rule %s does not close", selector)
	}
	var out []string
	for _, d := range strings.Split(body[:end], ";") {
		if d = strings.TrimSpace(d); d != "" {
			out = append(out, d)
		}
	}
	return out
}

// The dark theme is written twice, once for the system's preference and
// once for the theme the header button pins; the two must not drift.
func TestTheTwoDarkThemesMatch(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("web", "styles.css"))
	if err != nil {
		t.Fatal(err)
	}
	css := string(data)
	system := declarations(t, css, `:root:not([data-theme="light"])`)
	pinned := declarations(t, css, `:root[data-theme="dark"]`)
	if len(system) < 10 {
		t.Fatalf("the dark theme of the system has %d declarations, want the whole token set", len(system))
	}
	if strings.Join(system, "\n") != strings.Join(pinned, "\n") {
		t.Errorf("the dark themes differ:\nsystem:\n%s\npinned:\n%s", strings.Join(system, "\n"), strings.Join(pinned, "\n"))
	}
}
