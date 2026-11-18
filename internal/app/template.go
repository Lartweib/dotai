package app

import (
	"fmt"
	"slices"
	"strings"

	"github.com/lartweib/dotai/internal/pack"
	"github.com/lartweib/dotai/internal/packtemplate"
	"github.com/lartweib/dotai/internal/plan"
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

// templatePlanWarnings are the warnings of a plan, of the operation op,
// for the files of the template left unchanged that lie in the layers or
// mods at paths, by their path in the pack: each one needs consent.
func templatePlanWarnings(files, paths []string, op string) []plan.Warning {
	out := []plan.Warning{}
	for _, f := range files {
		if slices.ContainsFunc(paths, func(p string) bool { return strings.HasPrefix(f, p+"/") }) {
			out = append(out, plan.Warning{Code: CodeTemplateUnchanged, Item: f,
				Message: fmt.Sprintf("%s is an example file of dotai's pack template, unchanged: the %s installs it as it is only with your consent; edit or delete it in the pack", f, op)})
		}
	}
	return out
}

// userLayers are the paths of the layers of the user scope: the base and
// the variant.
func userLayers(s *PackSummary, variant string) []string {
	var out []string
	for _, l := range s.Layers {
		if l.Kind == string(pack.LayerBase) || l.Kind == string(pack.LayerVariant) && l.Name == variant {
			out = append(out, l.Path)
		}
	}
	return out
}

// projectLayers are the paths of the profiles and of the mods they name.
func projectLayers(s *PackSummary, profiles []string) []string {
	var out []string
	for _, pr := range s.Profiles {
		if !slices.Contains(profiles, pr.Name) {
			continue
		}
		out = append(out, pr.Path)
		for _, m := range pr.Mods {
			out = append(out, "mods/"+m)
		}
	}
	return out
}

// templateItems are the items of the template-unchanged warnings ws.
func templateItems(ws []plan.Warning) []string {
	var out []string
	for _, w := range ws {
		if w.Code == CodeTemplateUnchanged {
			out = append(out, w.Item)
		}
	}
	return out
}

// TemplateFiles are the files of the pack's template, unchanged, that the
// install writes, for which it asks consent (decision 0046).
func (p InstallPlan) TemplateFiles() []string { return templateItems(p.Warnings) }

// TemplateFiles are the files of the pack's template, unchanged, that the
// apply writes, for which it asks consent (decision 0046).
func (p ProjectPlan) TemplateFiles() []string { return templateItems(p.Warnings) }

// ConflictConsent says that the apply asks consent for its conflicts.
func (p ProjectPlan) ConflictConsent() bool {
	files, keys := conflicts(p)
	return files+keys > 0
}

// TemplateFilesPhrase names n files of the pack's template left
// unchanged, such as "2 example files of dotai's pack template,
// unchanged": the words of every consent question and refusal of an
// install or an apply for them, the CLI's included.
func TemplateFilesPhrase(n int) string {
	return plural(n, "example file", "example files") + " of dotai's pack template, unchanged"
}

// templateConsent is the part of a consent refusal of the operation op
// for the template files files, "" for none.
func templateConsent(op string, files []string) string {
	if len(files) == 0 {
		return ""
	}
	return fmt.Sprintf("the %s would install %s (%s)", op, TemplateFilesPhrase(len(files)), strings.Join(files, ", "))
}
