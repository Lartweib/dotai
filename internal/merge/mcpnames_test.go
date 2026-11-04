package merge_test

import (
	"reflect"
	"testing"

	"github.com/lartweib/dotai/internal/merge"
)

func TestMCPServerNamesListsTheServersInFileOrder(t *testing.T) {
	names, err := merge.MCPServerNames([]byte(`{
  // comments are fine
  "mcpServers": {"web": {"command": "w"}, "db": {"url": "u"}}
}`))
	if err != nil || !reflect.DeepEqual(names, []string{"web", "db"}) {
		t.Errorf("MCPServerNames() = %v, %v, want [web db]", names, err)
	}
	if _, err := merge.MCPServerNames([]byte(`[]`)); err == nil {
		t.Error("MCPServerNames of an array: no error")
	}
}
