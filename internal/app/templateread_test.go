package app_test

import (
	"context"
	"fmt"
	"io/fs"
	"slices"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/lartweib/dotai/internal/app"
	"github.com/lartweib/dotai/internal/packtemplate"
)

// openCounter is a file system that records the name of every Open.
type openCounter struct {
	fs.FS
	opened []string
}

func (c *openCounter) Open(name string) (fs.File, error) {
	c.opened = append(c.opened, name)
	return c.FS.Open(name)
}

// TestValidateReadsOnlyWhatThePackInstalls: the template-unchanged check
// hashes the files the loader reads, those of the layers' .rulesync/ and
// dotai/ and of mods/, never a tree the pack does not install, such as a
// node_modules/ at its top or in a layer.
func TestValidateReadsOnlyWhatThePackInstalls(t *testing.T) {
	fsys := filled(t, packtemplate.Answers{Name: "mine", Harnesses: []string{"opencode"}})
	unrelated := []string{"node_modules/left-pad/index.js", "base/node_modules/left-pad/index.js", "base/docs/notes.md"}
	for i := range 200 {
		unrelated = append(unrelated, fmt.Sprintf("node_modules/pkg-%d/index.js", i))
	}
	overview := fsys["base/.rulesync/rules/overview.md"].Data
	for _, p := range unrelated {
		fsys[p] = &fstest.MapFile{Data: overview} // a template file's content, to warn about if read
	}
	c := &openCounter{FS: fsys}
	res, err := app.ValidateFS(c, app.Selection{})
	if err != nil {
		t.Fatal(err)
	}
	var read []string
	for _, name := range c.opened {
		if slices.Contains(unrelated, name) {
			read = append(read, name)
		}
	}
	if len(read) > 0 {
		t.Errorf("validate opened %d files the pack does not install, such as %s", len(read), read[0])
	}
	got := unchangedFiles(res)
	if !slices.Contains(got, "base/.rulesync/rules/overview.md") {
		t.Errorf("template-unchanged files = %v, want the base's overview", got)
	}
	if i := slices.IndexFunc(got, func(f string) bool { return slices.Contains(unrelated, f) }); i >= 0 {
		t.Errorf("template-unchanged warns about %s, which the pack does not install", got[i])
	}
}

// TestAddGitPackWarnsOfTemplateFilesLeftUnchanged: a git pack whose
// fetched tree keeps files of the template unchanged is added, with the
// template-unchanged warning a local pack gets.
func TestAddGitPackWarnsOfTemplateFilesLeftUnchanged(t *testing.T) {
	gitHome(t)
	home := t.TempDir()
	r := newRemote(t, "https://github.com/acme/template-pack")
	files := map[string]string{"dotai.yaml": "", "base": "", "profiles": "", "variants": "", "mods": "", "README.md": ""}
	r.commit(t, files)
	clear(files)
	for p, f := range filled(t, packtemplate.Answers{Name: "mine", Harnesses: []string{"opencode"}}) {
		files[p] = string(f.Data)
	}
	r.commit(t, files)
	runGit(t, r.dir, "tag", "v1")
	res, err := app.AddGitPack(context.Background(), home, r.url, app.PackGitOptions{Ref: "v1"})
	if err != nil {
		t.Fatalf("AddGitPack() error = %v", err)
	}
	i := slices.IndexFunc(res.Warnings, func(w app.PackWarning) bool { return w.Code == app.CodeTemplateUnchanged })
	if res.Outcome != app.PackAdded || i < 0 || !strings.Contains(res.Warnings[i].Message, "base/.rulesync/rules/overview.md") {
		t.Fatalf("AddGitPack() = %+v, want the pack added with template-unchanged naming the files", res)
	}
}
