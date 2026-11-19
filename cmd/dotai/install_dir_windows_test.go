package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestInstallDirNormalisation runs install.ps1's ConvertTo-InstallDir on
// paths that no test can install into, such as C:\.
func TestInstallDirNormalisation(t *testing.T) {
	t.Parallel()
	ps, err := exec.LookPath("powershell")
	if err != nil {
		if onCI() {
			t.Fatalf("powershell is required in CI: %v", err)
		}
		t.Skip("no powershell")
	}
	src, err := os.ReadFile(filepath.Join("..", "..", "install.ps1"))
	if err != nil {
		t.Fatal(err)
	}
	text := strings.ReplaceAll(string(src), "\r\n", "\n")
	start := strings.Index(text, "  function ConvertTo-InstallDir")
	end := strings.Index(text, "  # End of ConvertTo-InstallDir.")
	if start < 0 || end < start {
		t.Fatal("install.ps1 has no ConvertTo-InstallDir block")
	}
	fn := text[start:end]
	for in, want := range map[string]string{
		`C:\`:           `C:\`,
		`C:\\`:          `C:\`,
		`C:\x\`:         `C:\x`,
		`C:\x\\`:        `C:\x`,
		`D:\a b\c`:      `D:\a b\c`,
		`\\srv\share`:   `\\srv\share`,
		`\\srv\share\`:  `\\srv\share`,
		`\\srv\share\x`: `\\srv\share\x`,
		`\\`:            "refused",
		`\\srv`:         "refused",
		`\x`:            "refused",
		`x\y`:           "refused",
		`C:x`:           "refused",
	} {
		script := fn + "\ntry { ConvertTo-InstallDir '" + strings.ReplaceAll(in, "'", "''") + "' } catch { 'refused' }"
		out, err := exec.Command(ps, "-NoProfile", "-NonInteractive", "-Command", script).Output()
		if got := strings.TrimSpace(string(out)); err != nil || got != want {
			t.Errorf("ConvertTo-InstallDir %q = %q (%v), want %q", in, got, err, want)
		}
	}
}
