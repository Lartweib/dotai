package app

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"

	"github.com/lartweib/dotai/internal/harness"
)

// renameProjectFiles carries out the Renames of the project layout of
// each adapter in the project staging tree dir, where rulesync rendered
// them: a file that is missing is skipped, and one without exactly one Old
// fails the stage, naming version, the rulesync that rendered it.
func renameProjectFiles(w stageFS, dir string, adapters []harness.Adapter, version string) error {
	for _, a := range adapters {
		for _, r := range a.Project.Renames {
			if err := renameStaged(w, dir, r, version); err != nil {
				return err
			}
		}
	}
	return nil
}

// renameStaged carries out r in dir.
func renameStaged(w stageFS, dir string, r harness.Rename, version string) error {
	data, err := w.ReadFile(dir, r.From)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if n := bytes.Count(data, []byte(r.Old)); n != 1 {
		return fmt.Errorf("%s/%s, rendered by rulesync %s, holds %s %d times, not once: dotai cannot give it a name of its own as %s, "+
			"so it would clash with the file of the same name at user scope; this rulesync renders a form dotai does not know", ScopeProject, r.From, version, r.Old, n, r.To)
	}
	data = bytes.Replace(data, []byte(r.Old), []byte(r.New), 1)
	if err := w.WriteFile(dir, r.To, data, 0o644); err != nil {
		return err
	}
	return w.RemoveAll(filepath.Join(dir, filepath.FromSlash(r.From)))
}
