package proc_test

import (
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/lartweib/dotai/internal/proc"
)

// TestListFindsItself checks that the listing of this OS sees the test
// process, by its id and its executable's name.
func TestListFindsItself(t *testing.T) {
	procs, err := proc.List()
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Base(exe)
	i := slices.IndexFunc(procs, func(p proc.Process) bool { return p.PID == os.Getpid() })
	if i < 0 {
		t.Fatalf("List() = %d processes, want this one (%d) among them", len(procs), os.Getpid())
	}
	got := procs[i].Name
	if runtime.GOOS == "windows" {
		got, want = strings.ToLower(got), strings.ToLower(want)
	}
	if got != want {
		t.Errorf("name of this process = %q, want %q", procs[i].Name, filepath.Base(exe))
	}
}
