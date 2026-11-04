package app

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
)

// rulesync names the OpenCode plugin it renders from hooks the same at
// both scopes: the file name and, for OpenCode 2, the plugin id. OpenCode
// keeps one plugin per name, so the project's would hide the user's; the
// project scope's is renamed and given an id of its own. The id is the one
// rulesync 41.0.0 writes, matched exactly, so that a rulesync that writes
// another form fails the stage rather than installing a clashing plugin.
const (
	openCodePlugin          = ".opencode/plugins/rulesync-hooks.js"
	openCodeProjectPlugin   = ".opencode/plugins/rulesync-hooks-project.js"
	openCodePluginID        = `id: "rulesync-hooks",`
	openCodeProjectPluginID = `id: "rulesync-hooks-project",`
)

// renameProjectPlugin moves the OpenCode plugin rulesync rendered into the
// project staging tree dir, if any, to its project name with the project
// id. version is the rulesync that rendered it, for the error.
func renameProjectPlugin(w stageFS, dir, version string) error {
	data, err := w.ReadFile(dir, openCodePlugin)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if n := bytes.Count(data, []byte(openCodePluginID)); n != 1 {
		return fmt.Errorf("project/%s, rendered by rulesync %s, holds %s %d times, not once: dotai cannot give the project plugin an id of its own, "+
			"and OpenCode would load only one of the user and project plugins; this rulesync renders a form dotai does not know", openCodePlugin, version, openCodePluginID, n)
	}
	data = bytes.Replace(data, []byte(openCodePluginID), []byte(openCodeProjectPluginID), 1)
	if err := w.WriteFile(dir, openCodeProjectPlugin, data, 0o644); err != nil {
		return err
	}
	return w.RemoveAll(filepath.Join(dir, filepath.FromSlash(openCodePlugin)))
}
