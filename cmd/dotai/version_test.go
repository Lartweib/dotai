package main

import (
	"encoding/json"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
)

// TestVersionStampedAtBuild builds dotai with the linker flag that the
// release script sets and checks that `dotai version` prints the tag.
func TestVersionStampedAtBuild(t *testing.T) {
	if testing.Short() {
		t.Skip("builds the binary")
	}
	const tag = "v9.8.7-rc.1"
	bin := filepath.Join(t.TempDir(), "dotai")
	if runtime.GOOS == "windows" {
		bin += ".exe"
	}
	// The go of the toolchain that runs the test: go test puts its own
	// GOROOT/bin first on the PATH of the test binary.
	goTool, err := exec.LookPath("go")
	if err != nil {
		t.Fatalf("the go command is not on PATH: %v", err)
	}
	build := exec.Command(goTool, "build", "-trimpath",
		"-ldflags", "-X github.com/lartweib/dotai/internal/app.version="+tag, "-o", bin, ".")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("go build: %v\n%s", err, out)
	}
	out, err := exec.Command(bin, "version", "--json").Output()
	if err != nil {
		t.Fatalf("dotai version --json: %v", err)
	}
	var got struct {
		Data struct {
			Version string `json:"version"`
			OS      string `json:"os"`
			Arch    string `json:"arch"`
		} `json:"data"`
	}
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("stdout = %q: %v", out, err)
	}
	if got.Data.Version != tag || got.Data.OS != runtime.GOOS || got.Data.Arch != runtime.GOARCH {
		t.Errorf("version data = %+v, want %s on %s/%s", got.Data, tag, runtime.GOOS, runtime.GOARCH)
	}
}
