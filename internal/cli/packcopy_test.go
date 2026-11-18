package cli_test

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lartweib/dotai/internal/cli"
	"github.com/lartweib/dotai/internal/envelope"
)

// copySource is a copy of the template-pack fixture in a new directory,
// so that a copy next to it lands in a temporary directory too.
func copySource(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "template-pack")
	err := filepath.WalkDir(templatePack, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, _ := filepath.Rel(templatePack, p)
		data, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Join(dir, filepath.Dir(rel)), 0o755); err != nil {
			return err
		}
		return os.WriteFile(filepath.Join(dir, rel), data, 0o644)
	})
	if err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestRunPackCopy(t *testing.T) {
	packHome(t)
	from := copySource(t)
	to := filepath.Join(filepath.Dir(from), "mine")
	var stdout, stderr bytes.Buffer
	if code := cli.Run([]string{"pack", "copy", from, to, "--json"}, newEnv(&stdout, &stderr)); code != cli.ExitOK {
		t.Fatalf("exit = %d; stdout %s; stderr %s", code, &stdout, &stderr)
	}
	assertGolden(t, filepath.Join("testdata", "pack-copy.json.golden"), placeholder(placeholder(stdout.Bytes(), to), from))

	// Without a target, the copy goes to <name>-copy next to the pack, as
	// the web UI offers; text names both.
	stdout.Reset()
	if code := cli.Run([]string{"pack", "copy", from}, newEnv(&stdout, &stderr)); code != cli.ExitOK {
		t.Fatalf("exit = %d; stderr %s", code, &stderr)
	}
	want := filepath.Join(filepath.Dir(from), "template-pack-copy")
	if !strings.Contains(stdout.String(), "copied "+from+" to "+want) {
		t.Errorf("stdout = %q, want the copy to %s", stdout.String(), want)
	}

	// A target that exists is refused, as the web UI's copy is.
	stdout.Reset()
	if code := cli.Run([]string{"pack", "copy", from, to, "--json"}, newEnv(&stdout, &stderr)); code != cli.ExitInternal {
		t.Errorf("exit = %d, want %d", code, cli.ExitInternal)
	}
	if e := envelopeOf(t, &stdout); e["error"].(map[string]any)["code"] != envelope.CodeCopyRefused {
		t.Errorf("envelope = %v, want copy-refused", e)
	}
}
