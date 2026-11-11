package pack_test

import (
	"os"
	"path/filepath"
	"testing"
	"testing/fstest"

	"github.com/lartweib/dotai/internal/pack"
)

func TestIdentityReadsOnlyTheNameAndVersionOfTheManifest(t *testing.T) {
	name, version, err := pack.Identity(os.DirFS(filepath.Join("..", "..", "examples", "minimal-pack")))
	if err != nil || name != "minimal-pack" || version != "0.1.0" {
		t.Errorf("Identity() = %q, %q, %v", name, version, err)
	}
	if _, _, err := pack.Identity(fstest.MapFS{}); err == nil {
		t.Error("Identity() of a directory without a manifest: no error")
	}
	if _, _, err := pack.Identity(fstest.MapFS{"dotai.yaml": {Data: []byte("name: [\n")}}); err == nil {
		t.Error("Identity() of a manifest that is not YAML: no error")
	}
}
