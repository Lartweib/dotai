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
	CodeReadFailed         = "read-failed"
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
// found. The pack is nil only when the manifest cannot be read or parsed.
func Load(fsys fs.FS) (*Pack, []Diagnostic) {
	var diags []Diagnostic
	src, err := fs.ReadFile(fsys, ManifestFile)
	if err != nil {
		code := CodeReadFailed
		if errors.Is(err, fs.ErrNotExist) {
			code = CodeManifestMissing
		}
		return nil, append(diags, Diagnostic{File: ManifestFile, Code: code, Message: "cannot read the pack manifest: " + err.Error()})
	}
	r := reader{file: ManifestFile, diags: &diags}
	root, ok := r.parse(src, CodeYAMLInvalid)
	if !ok {
		return nil, diags
	}

	p := &Pack{Manifest: decodeManifest(fsys, r, root)}
	sortFrom(diags, 0)
	if info, err := fs.Stat(fsys, "base"); err == nil && info.IsDir() {
		p.Layers = append(p.Layers, loadLayer(fsys, &diags, LayerBase, "base", "base"))
	}
	for _, pr := range p.Manifest.Profiles {
		if pr.Path != "" {
			p.Layers = append(p.Layers, loadLayer(fsys, &diags, LayerProfile, pr.Name, pr.Path))
		}
	}
	for _, v := range p.Manifest.Variants {
		if v.Path != "" {
			p.Layers = append(p.Layers, loadLayer(fsys, &diags, LayerVariant, v.Name, v.Path))
		}
	}
	return p, diags
}

func loadLayer(fsys fs.FS, diags *[]Diagnostic, kind LayerKind, name, dir string) Layer {
	l := Layer{Kind: kind, Name: name, Path: dir}
	root := path.Join(dir, ".rulesync")
	_ = fs.WalkDir(fsys, root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			if !errors.Is(err, fs.ErrNotExist) {
				*diags = append(*diags, Diagnostic{File: p, Code: CodeReadFailed, Message: err.Error()})
			}
			return nil
		}
		ik, iname, ok := classify(strings.TrimPrefix(p, root+"/"))
		if d.IsDir() || !ok {
			return nil
		}
		src, err := fs.ReadFile(fsys, p)
		if err != nil {
			*diags = append(*diags, Diagnostic{File: p, Code: CodeReadFailed, Message: err.Error()})
			return nil
		}
		start := len(*diags)
		l.Items = append(l.Items, Item{Kind: ik, Name: iname, Path: p, Dotai: readDotai(reader{file: p, offset: 1, diags: diags}, src)})
		sortFrom(*diags, start)
		return nil
	})
	return l
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
	lines := strings.Split(strings.ReplaceAll(strings.TrimPrefix(string(src), "\uFEFF"), "\r\n", "\n"), "\n")
	if lines[0] != "---" {
		return Dotai{}
	}
	end := slices.IndexFunc(lines[1:], func(l string) bool { return strings.TrimRight(l, " \t") == "---" })
	if end < 0 {
		*r.diags = append(*r.diags, Diagnostic{File: r.file, Line: 1, Column: 1, Code: CodeFrontmatterInvalid, Message: "frontmatter has no closing ---"})
		return Dotai{}
	}
	fm, ok := r.parse([]byte(strings.Join(lines[1:end+1], "\n")), CodeFrontmatterInvalid)
	if !ok || fm == nil {
		return Dotai{}
	}
	o := r.object(nil, fm, "frontmatter")
	if o.vals["dotai"] == nil {
		return Dotai{}
	}
	b := r.object(o.keys["dotai"], o.vals["dotai"], "dotai", "capabilities", "overrides")
	caps, _ := b.list("capabilities", false)
	return Dotai{Capabilities: caps, Overrides: b.boolean("overrides")}
}
