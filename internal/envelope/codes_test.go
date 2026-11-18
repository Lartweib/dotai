package envelope_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/lartweib/dotai/internal/envelope"
)

// TestCodesListsEveryCode checks that Codes holds every Code constant of
// the package, so that an adapter that maps each code, such as the web
// UI's HTTP status, can check it maps them all.
func TestCodesListsEveryCode(t *testing.T) {
	sources, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	var names []string
	for _, src := range sources {
		if strings.HasSuffix(src, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, src, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, d := range f.Decls {
			g, ok := d.(*ast.GenDecl)
			if !ok || g.Tok != token.CONST {
				continue
			}
			for _, spec := range g.Specs {
				for _, n := range spec.(*ast.ValueSpec).Names {
					if strings.HasPrefix(n.Name, "Code") {
						names = append(names, n.Name)
					}
				}
			}
		}
	}
	if len(names) != len(envelope.Codes) {
		t.Errorf("the package declares %d codes %v, Codes lists %d %v", len(names), names, len(envelope.Codes), envelope.Codes)
	}
	if slices.Contains(envelope.Codes, "") {
		t.Error("Codes holds an empty code")
	}
}
