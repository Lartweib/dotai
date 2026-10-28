// Package merge applies the layer merge rules of decision 0023 to a loaded
// pack and builds one staging tree per scope. It does no I/O. See the merge
// rules in docs/PACK-FORMAT.md.
//
// Not covered yet:
//   - A variant's detect rule. The user scope uses the variant in the
//     Selection, or the pack's default.
//   - MCP server names inside mcp.jsonc. Only the names of rules, commands,
//     subagents and skills are checked.
//   - Merging file contents. Every file that is not part of an item,
//     dotai/settings.yaml included, has one owner by path: the same path in
//     two layers of a scope is an error. Merging settings across layers
//     (lists as a union, deny wins, declared single-value overrides) comes
//     with the render step (roadmap PR 5).
package merge

import (
	"fmt"
	"path"
	"slices"
	"strings"

	"github.com/lartweib/dotai/internal/pack"
)

// Diagnostic codes.
const (
	CodeLayerSelection     = "layer-selection"
	CodeNameDuplicate      = "name-duplicate"
	CodeOverrideUndeclared = "override-undeclared"
	CodeProfileConflict    = "profile-conflict"
	CodeScopeConflict      = "scope-conflict"
	CodeRootRule           = "root-rule-outside-base"
	CodePathConflict       = "path-conflict"
)

// Selection names the layers to merge.
type Selection struct {
	Variant  string   // "": every variant for Check, the default for Stage
	Profiles []string // nil: every profile for Check, none for Stage
}

// File is one file of a staging tree.
type File struct {
	Path  string // relative to the scope root, for example .rulesync/rules/web.md
	Layer string // path of the layer that provides it
	Data  []byte
}

// Staging holds the merged tree of each scope, in path order.
type Staging struct {
	User    []File // base and variant
	Project []File // profiles
}

// Check runs the merge rules over the layers sel names, both scopes
// included. Variants are alternatives, so the user scope is checked as the
// base with each selected variant in turn; the selected profiles are peers
// that may be combined, so they are checked together.
func Check(p *pack.Pack, sel Selection) []pack.Diagnostic {
	variants, profiles, diags := resolve(p, sel, true)
	if len(diags) > 0 {
		return diags
	}
	base := layersOf(p, pack.LayerBase, nil)
	userSets := [][]pack.Layer{base}
	if len(variants) > 0 {
		userSets = nil
		for _, v := range variants {
			userSets = append(userSets, append(slices.Clip(base), v))
		}
	}
	for _, user := range userSets {
		_, d := mergeScope(user)
		diags = append(diags, d...)
	}
	_, d := mergeScope(profiles)
	diags = append(diags, d...)
	diags = append(diags, crossScope(append(slices.Clip(base), variants...), profiles)...)
	seen := map[pack.Diagnostic]bool{}
	return slices.DeleteFunc(diags, func(d pack.Diagnostic) bool {
		dup := seen[d]
		seen[d] = true
		return dup
	})
}

// Stage merges the base and the selected or default variant into the user
// tree, and the selected profiles into the project tree. With diagnostics,
// the staging is empty.
func Stage(p *pack.Pack, sel Selection) (Staging, []pack.Diagnostic) {
	variants, profiles, diags := resolve(p, sel, false)
	if len(diags) > 0 {
		return Staging{}, diags
	}
	user := append(layersOf(p, pack.LayerBase, nil), variants...)
	var s Staging
	s.User, diags = mergeScope(user)
	project, d := mergeScope(profiles)
	diags = append(append(diags, d...), crossScope(user, profiles)...)
	if len(diags) > 0 {
		return Staging{}, diags
	}
	s.Project = project
	return s, nil
}

// resolve returns the variant and profile layers sel names. With all, an
// empty choice means every layer of that kind; without it, the default
// variant and no profile.
func resolve(p *pack.Pack, sel Selection, all bool) (variants, profiles []pack.Layer, diags []pack.Diagnostic) {
	name := sel.Variant
	if name == "" && !all {
		for _, v := range p.Manifest.Variants {
			if v.Default {
				name = v.Name
			}
		}
	}
	switch {
	case name != "":
		variants, diags = pick(p, pack.LayerVariant, []string{name})
	case all:
		variants = layersOf(p, pack.LayerVariant, nil)
	}
	switch {
	case sel.Profiles != nil:
		var d []pack.Diagnostic
		profiles, d = pick(p, pack.LayerProfile, sel.Profiles)
		diags = append(diags, d...)
	case all:
		profiles = layersOf(p, pack.LayerProfile, nil)
	}
	return variants, profiles, diags
}

// pick returns the layers of kind named in names, in that order.
func pick(p *pack.Pack, kind pack.LayerKind, names []string) ([]pack.Layer, []pack.Diagnostic) {
	declared := layersOf(p, kind, nil)
	var out []pack.Layer
	var diags []pack.Diagnostic
	for i, n := range names {
		found := layersOf(p, kind, &n)
		switch {
		case len(found) == 0:
			var want []string
			for _, l := range declared {
				want = append(want, l.Name)
			}
			diags = append(diags, pack.NewDiagnostic(pack.ManifestFile, 0, 0, CodeLayerSelection, "unknown %s %q, want one of %v", kind, n, want))
		case slices.Contains(names[:i], n):
			diags = append(diags, pack.NewDiagnostic(pack.ManifestFile, 0, 0, CodeLayerSelection, "%s %q is selected twice", kind, n))
		default:
			out = append(out, found...)
		}
	}
	return out, diags
}

// layersOf returns the layers of kind, only the one called *name when name
// is set.
func layersOf(p *pack.Pack, kind pack.LayerKind, name *string) []pack.Layer {
	var out []pack.Layer
	for _, l := range p.Layers {
		if l.Kind == kind && (name == nil || l.Name == *name) {
			out = append(out, l)
		}
	}
	return out
}

// owned is an item and the layer it comes from.
type owned struct {
	layer pack.Layer
	item  pack.Item
}

func key(it pack.Item) string { return string(it.Kind) + "/" + it.Name }

// at locates a diagnostic on a whole file: line 1, the start of the file
// that defines the item or provides the path.
func at(file, code, format string, args ...any) pack.Diagnostic {
	return pack.NewDiagnostic(file, 1, 0, code, format, args...)
}

// mergeScope merges the layers of one scope, in order, into its tree.
func mergeScope(layers []pack.Layer) ([]File, []pack.Diagnostic) {
	var diags []pack.Diagnostic
	owners := map[string]owned{}
	for _, l := range layers {
		for _, it := range l.Items {
			if it.Kind == pack.ItemRule && it.Root && l.Kind != pack.LayerBase {
				diags = append(diags, at(it.Path, CodeRootRule, "rule %q is a root rule; only the base may provide the root rule", it.Name))
			}
			prev, seen := owners[key(it)]
			if !seen {
				owners[key(it)] = owned{l, it}
			} else if d, bad := conflict(prev, owned{l, it}); bad {
				diags = append(diags, d)
			} else {
				owners[key(it)] = owned{l, it}
			}
		}
	}
	files, d := collect(layers, owners)
	return files, append(diags, d...)
}

// conflict reports whether next may not replace prev, which has the same
// name in an earlier layer of the same scope.
func conflict(prev, next owned) (pack.Diagnostic, bool) {
	what := fmt.Sprintf("%s %q", next.item.Kind, next.item.Name)
	switch {
	case prev.layer.Path == next.layer.Path:
		return at(next.item.Path, CodeNameDuplicate, "%s is defined twice in layer %s, first at %s:1", what, next.layer.Path, prev.item.Path), true
	case prev.layer.Kind == pack.LayerProfile && next.layer.Kind == pack.LayerProfile:
		return at(next.item.Path, CodeProfileConflict, "%s is also defined by profile %s at %s:1; profiles cannot override each other", what, prev.layer.Name, prev.item.Path), true
	case !next.item.Dotai.Overrides:
		return at(next.item.Path, CodeOverrideUndeclared, "%s is already defined at %s:1; declare dotai: {overrides: true} to replace it", what, prev.item.Path), true
	}
	return pack.Diagnostic{}, false
}

// crossScope reports every project item with the name of a user item.
// Harnesses resolve that clash differently, so overrides do not apply.
func crossScope(user, project []pack.Layer) []pack.Diagnostic {
	owners := map[string]pack.Item{}
	for _, l := range slices.Backward(user) {
		for _, it := range l.Items {
			owners[key(it)] = it
		}
	}
	var diags []pack.Diagnostic
	for _, l := range project {
		for _, it := range l.Items {
			if prev, ok := owners[key(it)]; ok {
				diags = append(diags, at(it.Path, CodeScopeConflict, "%s %q is also defined at user level at %s:1; overrides do not apply across scopes", it.Kind, it.Name, prev.Path))
			}
		}
	}
	return diags
}

// collect builds the tree from the files of layers. A file of an item, which
// for a skill is its whole directory, is kept only when that item owns the
// name. Any other file has one owner by path.
func collect(layers []pack.Layer, owners map[string]owned) ([]File, []pack.Diagnostic) {
	var files []File
	var diags []pack.Diagnostic
	byPath := map[string]string{}
	for _, l := range layers {
		for _, f := range l.Files {
			rel := strings.TrimPrefix(f.Path, l.Path+"/")
			if it, ok := itemOf(l, f.Path); ok {
				if owners[key(it)].item.Path == it.Path {
					files = append(files, File{Path: rel, Layer: l.Path, Data: f.Data})
				}
				continue
			}
			if prev, ok := byPath[rel]; ok {
				diags = append(diags, at(f.Path, CodePathConflict, "%s is also provided at %s:1; a file has one owner", rel, prev))
				continue
			}
			byPath[rel] = f.Path
			files = append(files, File{Path: rel, Layer: l.Path, Data: f.Data})
		}
	}
	slices.SortFunc(files, func(a, b File) int { return strings.Compare(a.Path, b.Path) })
	return files, diags
}

// itemOf returns the item of l that file belongs to.
func itemOf(l pack.Layer, file string) (pack.Item, bool) {
	for _, it := range l.Items {
		if file == it.Path || it.Kind == pack.ItemSkill && strings.HasPrefix(file, path.Dir(it.Path)+"/") {
			return it, true
		}
	}
	return pack.Item{}, false
}
