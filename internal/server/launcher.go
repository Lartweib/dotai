package server

import (
	"encoding/json"
	"fmt"
	"html"
	"os"
	"sync"
)

// writeLauncher writes a page that sends the browser to url, the address
// with the one-time code, so that the program that opens the browser gets
// the page's path on its command line, which other users of the machine
// may read, and never the code. The page is in a new directory of the
// temporary directory, 0700 and the page 0600, so that only the user reads
// it; on Windows the temporary directory is the user's own. remove deletes
// the directory, at once or later, once.
func writeLauncher(url string) (path string, remove func(), err error) {
	dir, err := os.MkdirTemp("", "dotai-ui-*")
	if err != nil {
		return "", nil, fmt.Errorf("writing the page that opens dotai ui: %w", err)
	}
	remove = sync.OnceFunc(func() { _ = os.RemoveAll(dir) })
	f, err := os.CreateTemp(dir, "open-*.html")
	if err != nil {
		remove()
		return "", nil, fmt.Errorf("writing the page that opens dotai ui: %w", err)
	}
	target, _ := json.Marshal(url)
	page := fmt.Sprintf("<!doctype html>\n<meta charset=\"utf-8\">\n<meta http-equiv=\"refresh\" content=\"0;url=%s\">\n<title>dotai</title>\n"+
		"<script>location.replace(%s)</script>\n<p><a href=\"%s\">Open dotai ui</a></p>\n",
		html.EscapeString(url), target, html.EscapeString(url))
	_ = f.Chmod(0o600)
	_, werr := f.WriteString(page)
	if cerr := f.Close(); werr == nil {
		werr = cerr
	}
	if werr != nil {
		remove()
		return "", nil, fmt.Errorf("writing the page that opens dotai ui: %w", werr)
	}
	return f.Name(), remove, nil
}
