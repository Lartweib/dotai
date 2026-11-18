package app

import (
	"fmt"
	"slices"
	"strings"

	"github.com/lartweib/dotai/internal/pack"
	"github.com/lartweib/dotai/internal/packtemplate"
)

// CodeTemplateUnchanged is a file of a pack that is still one of the
// template's example files, as some released version of the template
// wrote it (decision 0046): validate and pack add warn about it, and an
// install or apply plan that installs it needs consent.
const CodeTemplateUnchanged = "template-unchanged"

// templateUnchanged warns about each file of the pack p that the loader
// read, those of its layers' .rulesync/ and dotai/ and of its mods, whose
// content, line endings normalized, is a file of the template, in path
// order. It reads nothing of its own: a file the pack does not install,
// such as one of a node_modules/, is never read, and the files at the
// top, such as the manifest and a README, are not installed.
func templateUnchanged(p *pack.Pack) []pack.Diagnostic {
	var files []pack.File
	for _, l := range p.Layers {
		files = append(files, l.Files...)
	}
	for _, m := range p.Mods {
		files = append(files, m.Files...)
	}
	slices.SortFunc(files, func(a, b pack.File) int { return comparePaths(a.Path, b.Path) })
	out := []pack.Diagnostic{}
	for i, f := range files {
		if i > 0 && files[i-1].Path == f.Path || !packtemplate.Unchanged(f.Data) {
			continue // a path two layers share is warned about once
		}
		out = append(out, pack.NewDiagnostic(f.Path, 0, 0, CodeTemplateUnchanged, "this file is an example file of dotai's pack template, unchanged").
			WithHint("edit it for your pack, or delete it; while it is unchanged, dotai install and dotai apply ask before installing it"))
	}
	return out
}

// comparePaths orders the slash-separated paths a and b as a walk of
// their tree meets them: segment by segment.
func comparePaths(a, b string) int {
	return slices.Compare(strings.Split(a, "/"), strings.Split(b, "/"))
}

// templateFiles are the files of the template-unchanged warnings ws.
func templateFiles(ws []pack.Diagnostic) []string {
	var out []string
	for _, w := range ws {
		if w.Code == CodeTemplateUnchanged {
			out = append(out, w.File)
		}
	}
	return out
}

// templatePackWarnings is the warning of a pack addition for the
// template-unchanged warnings of res, none when it has none.
func templatePackWarnings(res ValidateResult) []PackWarning {
	files := templateFiles(res.Warnings)
	if len(files) == 0 {
		return nil
	}
	return []PackWarning{{Code: CodeTemplateUnchanged, Message: fmt.Sprintf("%s of the pack %s dotai's pack template, unchanged: %s; edit or delete %s, since dotai install and dotai apply ask before installing %s",
		plural(len(files), "file", "files"), pick(len(files), "is an example file of", "are example files of"), strings.Join(files, ", "), pick(len(files), "it", "them"), pick(len(files), "it", "them"))}}
}
