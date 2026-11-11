package server

import (
	"embed"
	"io/fs"
	"mime"
)

// The embedded fonts are WOFF2, which not every system names a type for.
func init() { _ = mime.AddExtensionType(".woff2", "font/woff2") }

// webFiles holds the frontend, plain files with no build step (decision
// 0043), embedded so that go install builds a binary that serves it.
//
//go:embed web
var webFiles embed.FS

// Web returns the embedded frontend, rooted at its index.html.
func Web() fs.FS {
	sub, err := fs.Sub(webFiles, "web")
	if err != nil {
		panic(err) // the directory is embedded above
	}
	return sub
}
