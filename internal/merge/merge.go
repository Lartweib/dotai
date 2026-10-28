// Package merge applies the layer merge rules of decision 0023 to a loaded
// pack and builds one staging tree per scope. It does no I/O. Each rule of
// the "Merge rules" table in docs/PACK-FORMAT.md maps to one function here:
// claim (Name row), mergeMCP (MCP servers in the Name row), checkRoot (Root
// rule row), claimPath (every other file) and crossScope (the cross-scope
// paragraph).
//
// Not covered yet:
//   - A variant's detect rule. The user scope uses the variant in the
//     Selection, or the pack's default.
//   - Merging lists and values: dotai/settings.yaml, hooks.jsonc and
//     permissions.jsonc in two layers of a scope are reported as
//     merge-pending until their content merge lands (roadmap PR 5); any
//     other repeated path is an error.
package merge

import (
	"bytes"
	"cmp"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/tailscale/hujson"

	"github.com/lartweib/dotai/internal/pack"
)

// Diagnostic codes.
const (
	CodeLayerSelection        = "layer-selection"
	CodeNameDuplicate         = "name-duplicate"
	CodeNameCaseConflict      = "name-case-conflict"
	CodeOverrideUndeclared    = "override-undeclared"
	CodeOverrideWithoutTarget = "override-without-target"
	CodeProfileConflict       = "profile-conflict"
	CodeScopeConflict         = "scope-conflict"
	CodeRootRule              = "root-rule-outside-base"
	CodeRootRuleDuplicate     = "root-rule-duplicate"
	CodeRootRuleOverride      = "root-rule-override"
	CodeServerConflict        = "mcp-server-conflict"
	CodeMCPInvalid            = "mcp-invalid"
	CodePathConflict          = "path-conflict"
	CodeMergePending          = "merge-pending"
)

const (
	mcpFile   = ".rulesync/mcp.jsonc"
	mcpServer = "mcp-server" // the kind of an MCP server in a scope's names
	fileLine  = 1            // an item or a file is located at the start of its file
)

// pendingMerge holds the files whose lists and values will be merged
// across layers; until then one owner per scope is allowed.
var pendingMerge = []string{"dotai/settings.yaml", ".rulesync/hooks.jsonc", ".rulesync/permissions.jsonc"}

// Selection names the layers to merge.
type Selection struct {
	Variant  string   // "": every variant for Check, the default for Stage
	Profiles []string // nil: every profile for Check, none for Stage
}

// File is one file of a staging tree.
type File struct {
	Path    string            // relative to the scope root, for example .rulesync/rules/web.md
	Layer   string            // path of the layer that provides it; "" for a merged file
	Origins map[string]string // for a merged file, the layer path of each entry by name
	Data    []byte
}

// Staging holds the merged tree of each scope, in path order.
type Staging struct {
	User    []File // base and variant
	Project []File // profiles
}

// Check runs the merge rules over the layers sel names, both scopes
// included. The base is merged once; each selected variant is then merged
// on top of it, since variants are alternatives. The selected profiles are
// peers that may be combined, so they are merged together.
func Check(p *pack.Pack, sel Selection) []pack.Diagnostic {
	variants, profiles, diags := resolve(p, sel, true)
	if len(diags) > 0 {
		return diags
	}
	base := newScope(layersOf(p, pack.LayerBase)...)
	diags = base.diags
	users := []*scope{base}
	if len(variants) > 0 {
		users = nil
	}
	for _, v := range variants {
		u := base.with(v)
		diags = append(diags, u.diags...)
		users = append(users, u)
	}
	project := newScope(profiles...)
	cross := crossScope(users, project)
	return slices.Concat(diags, withoutShadowed(project.diags, cross), cross)
}

// Stage merges the base and the selected or default variant into the user
// tree, and the selected profiles into the project tree. With diagnostics,
// the staging is empty.
func Stage(p *pack.Pack, sel Selection) (Staging, []pack.Diagnostic) {
	variants, profiles, diags := resolve(p, sel, false)
	if len(diags) > 0 {
		return Staging{}, diags
	}
	user := newScope(append(layersOf(p, pack.LayerBase), variants...)...)
	project := newScope(profiles...)
	cross := crossScope([]*scope{user}, project)
	if diags = slices.Concat(user.diags, withoutShadowed(project.diags, cross), cross); len(diags) > 0 {
		return Staging{}, diags
	}
	return Staging{User: user.tree(), Project: project.tree()}, nil
}

// resolve returns the variant and profile layers sel names. With all, an
// empty choice means every layer of that kind; without it, the default
// variant and no profile. Profiles are peers, so they come back sorted by
// name and no diagnostic depends on the order they were given in.
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
		variants = layersOf(p, pack.LayerVariant)
	}
	switch {
	case sel.Profiles != nil:
		var d []pack.Diagnostic
		profiles, d = pick(p, pack.LayerProfile, sel.Profiles)
		diags = append(diags, d...)
	case all:
		profiles = layersOf(p, pack.LayerProfile)
	}
	slices.SortFunc(profiles, func(a, b pack.Layer) int { return strings.Compare(a.Name, b.Name) })
	return variants, profiles, diags
}

// pick returns the layers of kind named in names.
func pick(p *pack.Pack, kind pack.LayerKind, names []string) ([]pack.Layer, []pack.Diagnostic) {
	declared := layersOf(p, kind)
	var out []pack.Layer
	var diags []pack.Diagnostic
	for i, n := range names {
		j := slices.IndexFunc(declared, func(l pack.Layer) bool { return l.Name == n })
		switch {
		case j < 0:
			var want []string
			for _, l := range declared {
				want = append(want, l.Name)
			}
			diags = append(diags, pack.NewDiagnostic(pack.ManifestFile, 0, 0, CodeLayerSelection, "unknown %s %q, want one of %v", kind, n, want))
		case slices.Contains(names[:i], n):
			diags = append(diags, pack.NewDiagnostic(pack.ManifestFile, 0, 0, CodeLayerSelection, "%s %q is selected twice", kind, n))
		default:
			out = append(out, declared[j])
		}
	}
	return out, diags
}

func layersOf(p *pack.Pack, kind pack.LayerKind) []pack.Layer {
	var out []pack.Layer
	for _, l := range p.Layers {
		if l.Kind == kind {
			out = append(out, l)
		}
	}
	return out
}

// owner is what owns a name in a scope: an item or an MCP server.
type owner struct {
	key        string // kind and lower-case name: names that differ only by case clash
	name, what string // the exact name, and a description such as `rule "style"`
	layer      pack.Layer
	file       string
	line       int
	item       pack.Item // zero for an MCP server
}

func (o owner) at() string { return where(o.file, o.line) }

func where(file string, line int) string { return fmt.Sprintf("%s:%d", file, line) }

func key(kind, name string) string { return kind + "/" + strings.ToLower(name) }

// staged is a file of a scope's tree with where it comes from.
type staged struct {
	File
	src  string // path in the pack
	item pack.ItemRef
}

// scope is the merge state of one scope's layers, applied in order.
type scope struct {
	names   map[string]owner           // by key
	root    string                     // location of the root rule
	files   map[string]staged          // by path in the tree
	lower   map[string]string          // lower-case path to path
	servers map[string]json.RawMessage // MCP server definitions by name
	diags   []pack.Diagnostic
}

func newScope(layers ...pack.Layer) *scope {
	s := &scope{names: map[string]owner{}, files: map[string]staged{}, lower: map[string]string{}, servers: map[string]json.RawMessage{}}
	for _, l := range layers {
		s.add(l)
	}
	return s
}

// with returns a copy of s with l merged on top. The copy reports only the
// diagnostics l causes.
func (s *scope) with(l pack.Layer) *scope {
	c := &scope{names: maps.Clone(s.names), root: s.root, files: maps.Clone(s.files), lower: maps.Clone(s.lower), servers: maps.Clone(s.servers)}
	c.add(l)
	return c
}

func (s *scope) report(file string, line int, code, format string, args ...any) {
	s.diags = append(s.diags, pack.NewDiagnostic(file, line, 0, code, format, args...))
}

// add merges layer l: its items first, then its files. A file of an item
// enters the tree only when its layer owns the item.
func (s *scope) add(l pack.Layer) {
	for _, it := range l.Items {
		s.checkRoot(l, it)
		s.claim(owner{key: key(string(it.Kind), it.Name), name: it.Name, what: fmt.Sprintf("%s %q", it.Kind, it.Name), layer: l, file: it.Path, line: fileLine, item: it})
	}
	for _, f := range l.Files {
		switch {
		case f.Item != (pack.ItemRef{}):
			if s.names[key(string(f.Item.Kind), f.Item.Name)].layer.Path == l.Path {
				s.put(staged{File{Path: f.Rel, Layer: l.Path, Data: f.Data}, f.Path, f.Item})
			}
		case f.Rel == mcpFile:
			s.mergeMCP(l, f)
		default:
			s.claimPath(l, f)
		}
	}
}

// claim applies the Name row: one name, one owner. A later layer replaces
// the owner only when it declares the override, and never across profiles;
// an override must have something to replace. It reports whether o now
// owns the name.
func (s *scope) claim(o owner) bool {
	prev, taken := s.names[o.key]
	reject := func(code, format string, args ...any) bool {
		s.report(o.file, o.line, code, format, args...)
		return false
	}
	switch {
	case !taken:
		if o.item.Dotai.Overrides {
			s.report(o.file, o.line, CodeOverrideWithoutTarget, "%s declares dotai: {overrides: true} but no earlier layer of its scope defines it", o.what)
		}
	case prev.name != o.name:
		return reject(CodeNameCaseConflict, "%s differs only by case from %s at %s", o.what, prev.what, prev.at())
	case prev.layer.Path == o.layer.Path:
		return reject(CodeNameDuplicate, "%s is defined twice in layer %s, first at %s", o.what, o.layer.Path, prev.at())
	case prev.item.Root:
		return reject(CodeRootRuleOverride, "%s replaces the root rule at %s; the root rule cannot be overridden", o.what, prev.at())
	case prev.layer.Kind == pack.LayerProfile && o.layer.Kind == pack.LayerProfile:
		return reject(CodeProfileConflict, "%s is also defined by profile %s at %s; profiles cannot override each other", o.what, prev.layer.Name, prev.at())
	case o.item.Kind == "":
		return reject(CodeServerConflict, "%s is already defined at %s; MCP servers cannot be overridden", o.what, prev.at())
	case !o.item.Dotai.Overrides:
		return reject(CodeOverrideUndeclared, "%s is already defined at %s; declare dotai: {overrides: true} to replace it", o.what, prev.at())
	default:
		s.drop(prev)
	}
	s.names[o.key] = o
	return true
}

// drop removes the files of the item that prev owned.
func (s *scope) drop(prev owner) {
	for p, f := range s.files {
		if f.item == prev.item.Ref() && f.Layer == prev.layer.Path {
			delete(s.files, p)
			delete(s.lower, strings.ToLower(p))
		}
	}
}

// checkRoot applies the Root rule row: only the base provides the root
// rule, and only one.
func (s *scope) checkRoot(l pack.Layer, it pack.Item) {
	switch {
	case it.Kind != pack.ItemRule || !it.Root:
	case l.Kind != pack.LayerBase:
		s.report(it.Path, fileLine, CodeRootRule, "rule %q is a root rule; only the base may provide the root rule", it.Name)
	case s.root != "":
		s.report(it.Path, fileLine, CodeRootRuleDuplicate, "rule %q is a second root rule; the first is at %s", it.Name, s.root)
	default:
		s.root = where(it.Path, fileLine)
	}
}

// claimPath gives any other file one owner by path. The files of
// pendingMerge follow the same rule until their content merge lands, with
// their own code.
func (s *scope) claimPath(l pack.Layer, f pack.File) {
	prev, taken := s.files[f.Rel]
	switch {
	case taken && slices.Contains(pendingMerge, f.Rel):
		s.report(f.Path, fileLine, CodeMergePending, "%s is also provided at %s; merging its lists and values across layers lands in a later version of dotai", f.Rel, where(prev.src, fileLine))
	case taken:
		s.report(f.Path, fileLine, CodePathConflict, "%s is also provided at %s; a file has one owner", f.Rel, where(prev.src, fileLine))
	default:
		s.put(staged{File{Path: f.Rel, Layer: l.Path, Data: f.Data}, f.Path, f.Item})
	}
}

// put adds f to the tree unless a path that differs only by case is there.
func (s *scope) put(f staged) {
	if other, ok := s.lower[strings.ToLower(f.Path)]; ok && other != f.Path {
		s.report(f.src, fileLine, CodeNameCaseConflict, "%s differs only by case from %s at %s", f.Path, other, where(s.files[other].src, fileLine))
		return
	}
	s.files[f.Path] = f
	s.lower[strings.ToLower(f.Path)] = f.Path
}

// mergeMCP merges the servers of a layer's mcp.jsonc by name under the Name
// row. Servers have no override syntax, so a repeated name is an error.
func (s *scope) mergeMCP(l pack.Layer, f pack.File) {
	servers, err := readMCP(f.Data)
	if err != nil {
		s.report(f.Path, 0, CodeMCPInvalid, "%v", err)
		return
	}
	for _, sv := range servers {
		o := owner{key: key(mcpServer, sv.name), name: sv.name, what: fmt.Sprintf("MCP server %q", sv.name), layer: l, file: f.Path, line: sv.line}
		if s.claim(o) {
			s.servers[sv.name] = sv.raw
		}
	}
}

type server struct {
	name string
	line int
	raw  json.RawMessage
}

// readMCP reads the servers of an mcp.jsonc file, in file order.
func readMCP(data []byte) ([]server, error) {
	v, err := hujson.Parse(slices.Clone(data)) // Parse aliases its input
	if err != nil {
		return nil, err
	}
	v.Standardize()
	errShape := errors.New(`want an object with only an "mcpServers" object`)
	top, ok := v.Value.(*hujson.Object)
	if !ok || len(top.Members) != 1 || top.Members[0].Name.Value.(hujson.Literal).String() != "mcpServers" {
		return nil, errShape
	}
	list, ok := top.Members[0].Value.Value.(*hujson.Object)
	if !ok {
		return nil, errShape
	}
	var out []server
	for _, m := range list.Members {
		line := 1 + bytes.Count(data[:m.Name.StartOffset], []byte("\n"))
		out = append(out, server{m.Name.Value.(hujson.Literal).String(), line, m.Value.Pack()})
	}
	return out, nil
}

// tree returns the scope's files in path order, with one merged mcp.jsonc
// when any layer has MCP servers.
func (s *scope) tree() []File {
	var out []File
	for _, f := range s.files {
		out = append(out, f.File)
	}
	if len(s.servers) > 0 {
		f := File{Path: mcpFile, Origins: map[string]string{}}
		for name := range s.servers {
			f.Origins[name] = s.names[key(mcpServer, name)].layer.Path
		}
		// The servers are standard JSON after readMCP, so this cannot fail.
		f.Data, _ = json.MarshalIndent(map[string]any{"mcpServers": s.servers}, "", "  ")
		f.Data = append(f.Data, '\n')
		out = append(out, f)
	}
	slices.SortFunc(out, func(a, b File) int { return strings.Compare(a.Path, b.Path) })
	return out
}

// crossScope applies the cross-scope paragraph: a project name may not
// repeat a user name, even with overrides, because harnesses resolve that
// clash differently. users holds the user scope once per variant; each
// clash is reported once, against the first variant that has the name.
func crossScope(users []*scope, project *scope) []pack.Diagnostic {
	owners := slices.SortedFunc(maps.Values(project.names), func(a, b owner) int {
		return cmp.Or(strings.Compare(a.file, b.file), cmp.Compare(a.line, b.line))
	})
	var diags []pack.Diagnostic
	for _, o := range owners {
		for _, u := range users {
			prev, ok := u.names[o.key]
			if !ok {
				continue
			}
			if prev.name != o.name {
				diags = append(diags, pack.NewDiagnostic(o.file, o.line, 0, CodeNameCaseConflict, "%s differs only by case from %s at %s", o.what, prev.what, prev.at()))
			} else {
				diags = append(diags, pack.NewDiagnostic(o.file, o.line, 0, CodeScopeConflict, "%s is also defined at user level at %s; overrides do not apply across scopes", o.what, prev.at()))
			}
			break
		}
	}
	return diags
}

// withoutShadowed drops an override-without-target of a project item that
// cross already reports for clashing with a user item: one diagnostic per
// problem.
func withoutShadowed(project, cross []pack.Diagnostic) []pack.Diagnostic {
	return slices.DeleteFunc(slices.Clone(project), func(d pack.Diagnostic) bool {
		return d.Code == CodeOverrideWithoutTarget && slices.ContainsFunc(cross, func(c pack.Diagnostic) bool { return c.File == d.File })
	})
}
