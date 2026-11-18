package app

import (
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/lartweib/dotai/internal/plan"
)

// TestRealHomeInFailsOnALeftoverOfANetworkStagingTree checks that a file
// that names a staging tree on a network share, or under a \\?\ prefix, in
// any form the rewrite leaves, fails the stage: with backslashes, with
// mixed separators, escaped in a JSON string, or under the other prefix.
func TestRealHomeInFailsOnALeftoverOfANetworkStagingTree(t *testing.T) {
	const share, long, drive = "//srv/share/stage", "//?/UNC/srv/share/stage", "//?/C:/stage"
	for _, tt := range []struct{ name, root, file, data string }{
		{"backslash", share, "user/notes.md", `\\srv\share\stage\project\x`},
		{"mixed", share, "user/notes.md", `\\srv/share\stage/project/x`},
		{"mixed prefix", share, "user/notes.md", `/\srv\share\stage\project`},
		{"json", share, "user/settings.json", `{"a":"\\\\srv\\share\\stage\\project"}`},
		{"long prefix", share, "user/notes.md", `\\?\UNC\srv\share\stage\project`},
		{"long prefix json", share, "user/settings.json", `{"a":"\\\\?\\UNC\\srv\\share\\stage\\project"}`},
		{"long root, short form", long, "user/notes.md", `\\srv\share\stage\project`},
		{"long root, forward slashes", long, "user/notes.md", `//srv/share/stage/project`},
		{"long root, long form", long, "user/notes.md", `\\?\unc\srv\share\stage\project`},
		{"long drive root", drive, "user/notes.md", `C:\stage\project`},
	} {
		t.Run(tt.name, func(t *testing.T) {
			tools := ToolsIn(filepath.Join(t.TempDir(), "home"), "project")
			_, _, err := realHomeIn(tt.file, []byte(tt.data), tt.root, []string{tt.root}, "user", tools, plan.FoldsCase(runtime.GOOS))
			if err == nil || !strings.Contains(err.Error(), "staging tree") {
				t.Errorf("realHomeIn(%q) error = %v, want one that names the staging tree", tt.data, err)
			}
		})
	}
}

// TestRealHomeInFailsOnAPercentEncodedLeftover checks that a file that
// names the staging tree percent-encoded, as a file URL does, with hex
// digits in either case, fails the stage rather than being left as it is.
func TestRealHomeInFailsOnAPercentEncodedLeftover(t *testing.T) {
	for _, tt := range []struct{ name, root, data string }{
		{"space", "C:/Users/John Doe/stage", "file:///C:/Users/John%20Doe/stage/user/x"},
		{"colon", "C:/Users/ana/stage", "C%3A/Users/ana/stage/user/x"},
		{"lower hex", "C:/Users/ana/stage", "C%3a%5cUsers%5cana%5cstage"},
		{"separators", "/home/ana/stage", "file://%2Fhome%2Fana%2Fstage"},
		{"multibyte", "/home/Jürgen/stage", "file:///home/J%c3%BCrgen/stage/user"},
		{"network share", "//srv/share/stage", "file:%2F%2Fsrv/share/stage/user"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			tools := ToolsIn(filepath.Join(t.TempDir(), "home"), "project")
			_, _, err := realHomeIn("user/notes.md", []byte(tt.data), tt.root, []string{tt.root}, "user", tools, plan.FoldsCase(runtime.GOOS))
			if err == nil || !strings.Contains(err.Error(), "staging tree") {
				t.Errorf("realHomeIn(%q) error = %v, want one that names the staging tree", tt.data, err)
			}
		})
	}
}

// TestRealHomeInWritesTheRealHomeOverANetworkStagingTree checks that the
// home directory under a staging tree on a network share, written with
// forward slashes, still becomes the real one.
func TestRealHomeInWritesTheRealHomeOverANetworkStagingTree(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip(`a \\server\share path is a network path only on Windows`)
	}
	root := `\\srv\share\stage`
	home := filepath.Join(t.TempDir(), "home")
	got, ok, err := realHomeIn("user/notes.md", []byte("//srv/share/stage/user/.ssh\n"), root, []string{root}, "user", ToolsIn(home, "project"), true)
	if want := filepath.ToSlash(home) + "/.ssh\n"; err != nil || !ok || string(got) != want {
		t.Errorf("realHomeIn() = %q, %v, %v, want %q", got, ok, err, want)
	}
}
