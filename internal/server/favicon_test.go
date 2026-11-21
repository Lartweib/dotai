package server_test

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lartweib/dotai/internal/server"
)

// TestTheFaviconIsTheLogoMark checks that the page's icon, which index.html
// links, is the mark of docs/images/logo-mark.svg and not a placeholder:
// the same shapes, served as an SVG image.
func TestTheFaviconIsTheLogoMark(t *testing.T) {
	h := server.New(testConfig(t), fakeDeps(), server.Web())
	page := serve(h, request("GET", "/", "")).Body.String()
	if !strings.Contains(page, `rel="icon" href="favicon.svg"`) {
		t.Fatalf("index.html does not link favicon.svg")
	}
	w := serve(h, request("GET", "/favicon.svg", ""))
	if w.Code != http.StatusOK || !strings.HasPrefix(w.Header().Get("Content-Type"), "image/svg+xml") {
		t.Fatalf("GET /favicon.svg = %d %q, want 200 and image/svg+xml", w.Code, w.Header().Get("Content-Type"))
	}
	mark, err := os.ReadFile(filepath.Join("..", "..", "docs", "images", "logo-mark.svg"))
	if err != nil {
		t.Fatal(err)
	}
	for _, tag := range []string{"<rect ", "<path ", "<circle "} {
		shape := string(mark)[strings.Index(string(mark), tag):]
		shape = shape[:strings.Index(shape, "/>")+2]
		if !strings.Contains(w.Body.String(), shape) {
			t.Errorf("favicon.svg lacks the logo mark's %.40s", shape)
		}
	}
}
