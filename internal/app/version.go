// Package app holds the engine use cases. The CLI and any future UI call
// only this layer.
package app

import (
	"runtime"
	"runtime/debug"
)

// Set at build time with -ldflags, for example:
//
//	-X github.com/lartweib/dotai/internal/app.version=1.2.3
//	-X github.com/lartweib/dotai/internal/app.commit=<sha>
//	-X github.com/lartweib/dotai/internal/app.buildDate=<RFC 3339>
var (
	version   string
	commit    string
	buildDate string
)

// VersionInfo describes the running binary. Unknown fields are empty.
type VersionInfo struct {
	Version   string `json:"version"`
	Commit    string `json:"commit"`
	BuildDate string `json:"buildDate"`
	GoVersion string `json:"goVersion"`
	OS        string `json:"os"`
	Arch      string `json:"arch"`
}

// Version reports the version of the running binary.
func Version() VersionInfo {
	info, _ := debug.ReadBuildInfo()
	return resolveVersion(buildInputs{
		version: version, commit: commit, date: buildDate,
		info:      info,
		goVersion: runtime.Version(), goos: runtime.GOOS, goarch: runtime.GOARCH,
	})
}

type buildInputs struct {
	version, commit, date   string           // linker values; empty when not set
	info                    *debug.BuildInfo // nil when unavailable
	goVersion, goos, goarch string
}

// resolveVersion prefers linker values and falls back to the build info
// that the Go toolchain embeds.
func resolveVersion(in buildInputs) VersionInfo {
	v := VersionInfo{
		Version: in.version, Commit: in.commit, BuildDate: in.date,
		GoVersion: in.goVersion, OS: in.goos, Arch: in.goarch,
	}
	if in.info != nil {
		if v.Version == "" && in.info.Main.Version != "(devel)" {
			v.Version = in.info.Main.Version
		}
		for _, s := range in.info.Settings {
			switch {
			case s.Key == "vcs.revision" && v.Commit == "":
				v.Commit = s.Value
			case s.Key == "vcs.time" && v.BuildDate == "":
				v.BuildDate = s.Value
			}
		}
	}
	if v.Version == "" {
		v.Version = "dev"
	}
	return v
}
