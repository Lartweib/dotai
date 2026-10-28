// Package pack loads a dotai pack and checks its structure: the dotai.yaml
// manifest, the layers and the dotai: blocks of Markdown sources. It does no
// I/O of its own: everything is read through an fs.FS. See docs/PACK-FORMAT.md.
//
// Load checks each file on its own. Rules that span layers, such as
// duplicate names and declared overrides (decision 0023), are not checked
// here: they belong to the merge step, which consumes Pack.Layers.
package pack

import (
	"errors"
	"io/fs"
	"path"
	"slices"
	"strings"
)

// Diagnostic codes.
const (
	CodeManifestMissing    = "manifest-missing"
	CodeYAMLInvalid        = "yaml-invalid"
	CodeFieldType          = "field-type"
	CodeFieldRequired      = "field-required"
	CodeFieldUnknown       = "field-unknown"
	CodeFieldDuplicate     = "field-duplicate"
	CodeSchemaUnknown      = "schema-unknown"
	CodeVersionInvalid     = "version-invalid"
	CodeEngineInvalid      = "engine-invalid"
	CodeRulesyncInvalid    = "rulesync-invalid"
	CodeHarnessUnknown     = "harness-unknown"
	CodeLayerPathInvalid   = "layer-path-invalid"
	CodeLayerMissing       = "layer-missing"
	CodeVariantDefault     = "variant-default"
	CodeFrontmatterInvalid = "frontmatter-invalid"
	CodeSymlinkNotAllowed  = "symlink-not-allowed"
	CodeNotRegularFile     = "not-regular-file"
	CodeFileTooLarge       = "file-too-large"
	CodeLayerNotDirectory  = "layer-not-directory"
)

// Layer layout (docs/PACK-FORMAT.md).
const (
	baseDir     = "base"      // the implicit base layer
	rulesyncDir = ".rulesync" // a layer's rulesync sources
	fenceLines  = 1           // lines before the YAML of a frontmatter
)

// Pack is a loaded pack.
type Pack struct {
	Manifest Manifest
	Layers   []Layer // base (if present), then profiles, then variants
}

// LayerKind tells where a layer applies.
type LayerKind string

// Layer kinds.
const (
	LayerBase    LayerKind = "base"
	LayerProfile LayerKind = "profile"
	LayerVariant LayerKind = "variant"
)

// Layer is one directory of the pack with the uniform layer shape.
type Layer struct {
	Kind  LayerKind
	Name  string
	Path  string
	Items []Item // in lexical path order
}

// ItemKind is the kind of a named rulesync source.
type ItemKind string

// Item kinds.
const (
	ItemRule     ItemKind = "rule"
	ItemCommand  ItemKind = "command"
	ItemSubagent ItemKind = "subagent"
	ItemSkill    ItemKind = "skill"
)

// Item is a named Markdown source of a layer's .rulesync/ tree.
type Item struct {
	Kind  ItemKind
	Name  string
	Path  string // relative to the pack root
	Dotai Dotai
}

// Dotai is the dotai: frontmatter block of an item.
type Dotai struct {
	Capabilities []string
	Overrides    bool
}

// Load reads the pack in fsys and returns it with every structural problem
// found. The pack is nil when the manifest cannot be used. The error is set
// only when the pack cannot be read at all, for example a permission error
// or a dotai.yaml that is a directory; then there is no pack and no
// diagnostics. Symbolic links are never followed.
func Load(fsys fs.FS) (*Pack, []Diagnostic, error) {
	src, d, err := readRegular(fsys, ManifestFile)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return nil, []Diagnostic{newDiagnostic(ManifestFile, 0, 0, CodeManifestMissing, "the pack has no %s", ManifestFile)}, nil
	case err != nil:
		return nil, nil, err
	case d != nil:
		return nil, []Diagnostic{*d}, nil
	}
	var diags []Diagnostic
	r := reader{file: ManifestFile, diags: &diags}
	root, ok := r.parse(normalize(src), CodeYAMLInvalid)
	if !ok {
		return nil, diags, nil
	}
	p := &Pack{Manifest: decodeManifest(fsys, r, root)}
	sortFrom(diags, 0)

	var layers []Layer
	hasBase, err := checkBase(fsys, &diags)
	if err != nil {
		return nil, nil, err
	}
	if hasBase {
		layers = append(layers, Layer{Kind: LayerBase, Name: baseDir, Path: baseDir})
	}
	for _, pr := range p.Manifest.Profiles {
		if pr.Path != "" {
			layers = append(layers, Layer{Kind: LayerProfile, Name: pr.Name, Path: pr.Path})
		}
	}
	for _, v := range p.Manifest.Variants {
		if v.Path != "" {
			layers = append(layers, Layer{Kind: LayerVariant, Name: v.Name, Path: v.Path})
		}
	}
	for _, l := range layers {
		if l.Items, err = loadItems(fsys, &diags, l.Path); err != nil {
			return nil, nil, err
		}
		p.Layers = append(p.Layers, l)
	}
	return p, diags, nil
}

// checkBase reports whether the optional base layer exists and is usable.
func checkBase(fsys fs.FS, diags *[]Diagnostic) (bool, error) {
	info, link, err := lstat(fsys, baseDir)
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if code, msg := entryProblem(baseDir, link, info.Mode(), true); code != "" {
		*diags = append(*diags, newDiagnostic(baseDir, 0, 0, code, "%s", msg))
		return false, nil
	}
	return true, nil
}

// loadItems walks the layer at dir without following links. It reports
// every link and non-regular file in the layer and reads the items of its
// .rulesync/ tree.
func loadItems(fsys fs.FS, diags *[]Diagnostic, dir string) ([]Item, error) {
	var items []Item
	err := fs.WalkDir(fsys, dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		if code, msg := entryProblem(p, "", d.Type(), false); code != "" {
			*diags = append(*diags, newDiagnostic(p, 0, 0, code, "%s", msg))
			return nil
		}
		rel, inRulesync := strings.CutPrefix(p, path.Join(dir, rulesyncDir)+"/")
		kind, name, isItem := classify(rel)
		if !inRulesync || !isItem {
			return nil
		}
		src, problem, err := readLimited(fsys, p)
		if err != nil {
			return err
		}
		if problem != nil {
			*diags = append(*diags, *problem)
			return nil
		}
		start := len(*diags)
		items = append(items, Item{Kind: kind, Name: name, Path: p, Dotai: readDotai(reader{file: p, offset: fenceLines, diags: diags}, src)})
		sortFrom(*diags, start)
		return nil
	})
	return items, err
}

// classify maps a path inside .rulesync/ to the item it defines.
func classify(rel string) (ItemKind, string, bool) {
	parts := strings.Split(rel, "/")
	base, isMD := strings.CutSuffix(parts[len(parts)-1], ".md")
	switch {
	case len(parts) == 2 && parts[0] == "rules" && isMD:
		return ItemRule, base, true
	case len(parts) == 2 && parts[0] == "commands" && isMD:
		return ItemCommand, base, true
	case len(parts) >= 2 && parts[0] == "subagents" && isMD:
		return ItemSubagent, base, true
	case len(parts) == 3 && parts[0] == "skills" && parts[2] == "SKILL.md":
		return ItemSkill, parts[1], true
	}
	return "", "", false
}

// readDotai reads the dotai: block from the frontmatter of a Markdown
// source. A file without frontmatter has an empty block.
func readDotai(r reader, src []byte) Dotai {
	lines := strings.Split(normalize(src), "\n")
	if !isFence(lines[0]) {
		return Dotai{}
	}
	end := slices.IndexFunc(lines[1:], isFence)
	if end < 0 {
		*r.diags = append(*r.diags, newDiagnostic(r.file, 1, 1, CodeFrontmatterInvalid, "frontmatter has no closing ---"))
		return Dotai{}
	}
	fm, ok := r.parse(strings.Join(lines[1:end+1], "\n"), CodeFrontmatterInvalid)
	if !ok || fm == nil {
		return Dotai{}
	}
	o := r.object(nil, fm, "frontmatter")
	if !o.has("dotai") {
		return Dotai{}
	}
	b := r.object(o.keys["dotai"], o.vals["dotai"], "dotai", "capabilities", "overrides")
	caps, _ := b.list("capabilities", false)
	overrides, _ := b.boolean("overrides")
	return Dotai{Capabilities: caps, Overrides: overrides}
}

// isFence reports whether line opens or closes a frontmatter block.
func isFence(line string) bool { return strings.TrimRight(line, " \t") == "---" }
