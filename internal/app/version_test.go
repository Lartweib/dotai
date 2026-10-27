package app

import (
	"runtime/debug"
	"testing"
)

func TestResolveVersion(t *testing.T) {
	stamped := &debug.BuildInfo{
		Main: debug.Module{Version: "v0.1.0"},
		Settings: []debug.BuildSetting{
			{Key: "vcs.revision", Value: "abc123"},
			{Key: "vcs.time", Value: "2026-10-27T10:00:00Z"},
		},
	}
	devel := &debug.BuildInfo{Main: debug.Module{Version: "(devel)"}}

	tests := []struct {
		name string
		in   buildInputs
		want VersionInfo
	}{
		{
			name: "linker values win over build info",
			in: buildInputs{
				version: "1.2.3", commit: "deadbeef", date: "2026-10-01T00:00:00Z",
				info: stamped, goVersion: "go1.26.5", goos: "linux", goarch: "amd64",
			},
			want: VersionInfo{
				Version: "1.2.3", Commit: "deadbeef", BuildDate: "2026-10-01T00:00:00Z",
				GoVersion: "go1.26.5", OS: "linux", Arch: "amd64",
			},
		},
		{
			name: "falls back to build info",
			in:   buildInputs{info: stamped, goVersion: "go1.26.5", goos: "darwin", goarch: "arm64"},
			want: VersionInfo{
				Version: "v0.1.0", Commit: "abc123", BuildDate: "2026-10-27T10:00:00Z",
				GoVersion: "go1.26.5", OS: "darwin", Arch: "arm64",
			},
		},
		{
			name: "devel build reports dev",
			in:   buildInputs{info: devel, goVersion: "go1.26.5", goos: "windows", goarch: "amd64"},
			want: VersionInfo{Version: "dev", GoVersion: "go1.26.5", OS: "windows", Arch: "amd64"},
		},
		{
			name: "no build info reports dev",
			in:   buildInputs{goVersion: "go1.26.5", goos: "linux", goarch: "arm64"},
			want: VersionInfo{Version: "dev", GoVersion: "go1.26.5", OS: "linux", Arch: "arm64"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := resolveVersion(tt.in); got != tt.want {
				t.Errorf("resolveVersion() = %+v, want %+v", got, tt.want)
			}
		})
	}
}
