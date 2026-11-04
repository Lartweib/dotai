// Package merge applies the layer merge rules of decision 0023 to a loaded
// pack and builds one staging tree per scope. It does no I/O. Each rule of
// the "Merge rules" table in docs/PACK-FORMAT.md maps to one function here:
// claim (Name row), mergeMCP (MCP servers in the Name row), mergeList
// (Lists row, decision 0040), checkRoot (Root rule row), claimPath (every
// other file) and crossScope (the cross-scope paragraph). claimPath also
// places a layer's tools at their install path, .dotai/tools/, and reports
// a tool in two layers of a scope as tool-conflict (decision 0030). mergeSettings applies the Single values
// row to dotai/settings.yaml within a scope; a user setting and a project
// setting never clash, because they go to different files.
//
// Not covered yet:
//   - A variant's detect rule. The user scope uses the variant in the
//     Selection, or the pack's default.
//   - Removing an entry of an earlier layer from a list file (decision
//     0040): a later layer only adds to hooks.jsonc and permissions.jsonc.
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
	CodeToolConflict          = "tool-conflict"
	CodeToolMissing           = "tool-missing"
	CodeToolInvalid           = "tool-invalid"
	CodeHooksInvalid          = "hooks-invalid"
	CodeHooksConflict         = "hooks-conflict"
	CodePermissionsInvalid    = "permissions-invalid"
	CodePermissionsConflict   = "permissions-conflict"
)

// Warning codes: what Warnings reports is no error.
const (
	CodeHooksShadowed = "hooks-shadowed"
)

const (
	mcpFile   = ".rulesync/mcp.jsonc"
	mcpServer = "mcp-server" // the kind of an MCP server in a scope's names
	fileLine  = 0            // a problem with an item or a file concerns the whole file
)

// A layer's tools, and where a scope's tree holds them: their install path
// under the home or the project directory.
const (
	toolsSource = "dotai/tools/"
	ToolsDir    = ".dotai/tools"
)

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
	Exec    bool // the source is executable
	// Generator names what made a file no layer provides, such as a mod's
	// plugin manifest, GeneratorDotai; "" for the others.
	Generator string
}

// GeneratorDotai is the Generator of a file dotai makes while staging.
const GeneratorDotai = "dotai"

// Staging holds the merged tree of each scope, in path order, and the
// merged settings of each scope by harness.
type Staging struct {
	User            []File // base and variant
	Project         []File // profiles
	UserSettings    map[string][]Setting
	ProjectSettings map[string][]Setting
}

// Check runs the merge rules over the layers sel names, both scopes
// included. The base is merged once; each selected variant is then merged
// on top of it, since variants are alternatives. The selected profiles are
// peers that may be combined, so they are merged together.
func Check(p *pack.Pack, sel Selection) []pack.Diagnostic {
	users, project, profiles, diags := checked(p, sel)
	if users == nil {
		return diags
	}
	cross := crossScope(users, project)
	return slices.Concat(diags, withoutShadowed(project.diags, cross), cross, once(append(users, project), (*scope).checkTools), modConflicts(project, modsOf(p, profiles)))
}

// Warnings returns what is no error in the layers sel names, as Check
// merges them, but may not do what the pack means: a hook event that both
// the shared hooks and a tool-scoped block declare, and a mod's subagent
// matcher that names its agent bare. It returns nothing for
// layers that cannot be selected.
func Warnings(p *pack.Pack, sel Selection) []pack.Diagnostic {
	users, project, profiles, _ := checked(p, sel)
	if users == nil {
		return nil
	}
	return append(once(append(users, project), (*scope).shadowedHooks), modWarnings(modsOf(p, profiles))...)
}

// checked merges the layers sel names as Check does: the user scope once
// per selected variant, on one merge of the base, and the project scope.
// It returns the profile layers too, the diagnostics of the user scopes,
// and no scope when sel names layers that do not exist.
func checked(p *pack.Pack, sel Selection) (users []*scope, project *scope, profiles []pack.Layer, diags []pack.Diagnostic) {
	variants, profiles, diags := resolve(p, sel, true)
	if len(diags) > 0 {
		return nil, nil, nil, diags
	}
	base := newScope(layersOf(p, pack.LayerBase)...)
	diags = base.diags
	users = []*scope{base}
	if len(variants) > 0 {
		users = nil
	}
	for _, v := range variants {
		u := base.with(v)
		diags = append(diags, u.diags...)
		users = append(users, u)
	}
	return users, newScope(profiles...), profiles, diags
}

// once is check over scopes, each problem once: what the base has is in
// the user scope of every variant.
func once(scopes []*scope, check func(*scope) []pack.Diagnostic) []pack.Diagnostic {
	var out []pack.Diagnostic
	for _, s := range scopes {
		for _, d := range check(s) {
			if !slices.Contains(out, d) {
				out = append(out, d)
			}
		}
	}
	return out
}

// Stage merges the base and the selected or default variant into the user
// tree, and the selected profiles into the project tree, with the mods
// they name under mods/. With diagnostics,
// the staging is empty.
func Stage(p *pack.Pack, sel Selection) (Staging, []pack.Diagnostic) {
	variants, profiles, diags := resolve(p, sel, false)
	if len(diags) > 0 {
		return Staging{}, diags
	}
	user := newScope(append(layersOf(p, pack.LayerBase), variants...)...)
	project := newScope(profiles...)
	cross := crossScope([]*scope{user}, project)
	mods := modsOf(p, profiles)
	if diags = slices.Concat(user.diags, withoutShadowed(project.diags, cross), cross, once([]*scope{user, project}, (*scope).checkTools), modConflicts(project, mods)); len(diags) > 0 {
		return Staging{}, diags
	}
	project.dropUserServers(user)
	tree := append(project.tree(), modFiles(mods, p.Manifest)...)
	slices.SortFunc(tree, func(a, b File) int { return strings.Compare(a.Path, b.Path) })
	return Staging{User: user.tree(), Project: tree, UserSettings: user.mergedSettings(), ProjectSettings: project.mergedSettings()}, nil
}

// dropUserServers removes from the project scope s each MCP server that
// the user scope defines the same way: it is staged once, at user level,
// so that a harness that lets a project server shadow a user one, as
// Claude Code does, never runs a copy that goes stale when the user scope
// changes.
func (s *scope) dropUserServers(user *scope) {
	for name := range s.servers {
		o := s.names[key(mcpServer, name)]
		if prev, ok := user.names[o.key]; ok && prev.name == o.name && o.same(prev) {
			delete(s.servers, name)
		}
	}
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
			diags = append(diags, pack.NewDiagnostic(pack.ManifestFile, 0, 0, CodeLayerSelection, "unknown %s %q", kind, n).WithHint("use one of %v", want))
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
	def        string    // canonical JSON of an MCP server's definition
}

// clash reports a problem of o with prev, the earlier owner of its name.
func (o owner) clash(prev owner, code, format string, args ...any) pack.Diagnostic {
	return pack.NewDiagnostic(o.file, o.line, 0, code, format, args...).WithRelated(prev.file, prev.line)
}

func key(kind, name string) string { return kind + "/" + strings.ToLower(name) }

// staged is a file of a scope's tree with where it comes from.
type staged struct {
	File
	src  string // path in the pack
	item pack.ItemRef
}

// scope is the merge state of one scope's layers, applied in order.
type scope struct {
	names    map[string]owner           // by key
	root     string                     // file of the root rule
	files    map[string]staged          // by path in the tree
	lower    map[string]string          // lower-case path to path
	servers  map[string]json.RawMessage // MCP server definitions by name
	lists    map[string]*node           // the merged list files by path
	settings map[string]setting         // by identity
	seq      int                        // the next setting's place in the order
	diags    []pack.Diagnostic
}

func newScope(layers ...pack.Layer) *scope {
	s := &scope{names: map[string]owner{}, files: map[string]staged{}, lower: map[string]string{}, servers: map[string]json.RawMessage{}, lists: map[string]*node{}, settings: map[string]setting{}}
	for _, l := range layers {
		s.add(l)
	}
	return s
}

// with returns a copy of s with l merged on top. The copy reports only the
// diagnostics l causes.
func (s *scope) with(l pack.Layer) *scope {
	c := &scope{names: maps.Clone(s.names), root: s.root, files: maps.Clone(s.files), lower: maps.Clone(s.lower), servers: maps.Clone(s.servers),
		lists: map[string]*node{}, settings: maps.Clone(s.settings), seq: s.seq}
	for p, n := range s.lists {
		c.lists[p] = n.clone()
	}
	c.add(l)
	return c
}

func (s *scope) report(d pack.Diagnostic) { s.diags = append(s.diags, d) }

// add merges layer l: its items first, then its files, then its settings.
// A file of an item enters the tree only when its layer owns the item.
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
		case listFiles[f.Rel] != (listCodes{}):
			s.mergeList(l, f)
		case f.Rel == settingsFile: // merged from l.Settings below
		default:
			s.claimPath(l, f)
		}
	}
	if l.Settings != nil {
		s.mergeSettings(l)
	}
}

// claim applies the Name row: one name, one owner. A later layer replaces
// the owner only when it declares the override, and never across profiles;
// an override must have something to replace. It reports whether o now
// owns the name.
func (s *scope) claim(o owner) bool {
	prev, taken := s.names[o.key]
	reject := func(d pack.Diagnostic) bool {
		s.report(d)
		return false
	}
	switch {
	case !taken:
		if o.item.Dotai.Overrides {
			s.report(pack.NewDiagnostic(o.file, o.line, 0, CodeOverrideWithoutTarget, "%s has nothing to override", o.what).
				WithHint("remove dotai: {overrides: true}; no earlier layer of its scope defines it"))
		}
	case prev.name != o.name:
		return reject(o.clash(prev, CodeNameCaseConflict, "%s differs only by case from %s", o.what, prev.what))
	case prev.layer.Path == o.layer.Path:
		return reject(o.clash(prev, CodeNameDuplicate, "%s is defined twice in layer %s", o.what, o.layer.Path))
	case o.same(prev):
		return false // an MCP server defined the same way again: kept once, from the first layer
	case prev.item.Root:
		return reject(o.clash(prev, CodeRootRuleOverride, "%s replaces the root rule", o.what).
			WithHint("rename it; the root rule cannot be overridden"))
	case prev.layer.Kind == pack.LayerProfile && o.layer.Kind == pack.LayerProfile:
		return reject(o.clash(prev, CodeProfileConflict, "%s is also defined by profile %s", o.what, prev.layer.Name).
			WithHint("rename one of them; profiles cannot override each other"))
	case o.item.Kind == "":
		return reject(o.clash(prev, CodeServerConflict, "%s already exists in %s with a different definition", o.what, prev.layer.Path).
			WithHint("rename it, or define it the same way; MCP servers cannot be overridden"))
	case !o.item.Dotai.Overrides:
		return reject(o.clash(prev, CodeOverrideUndeclared, "%s already exists in %s", o.what, prev.layer.Path).
			WithHint("add dotai: {overrides: true} to replace it"))
	default:
		s.drop(prev)
	}
	s.names[o.key] = o
	return true
}

// same reports whether o and prev are MCP servers with the same definition
// by canonical JSON, which is no clash: there is nothing to override.
func (o owner) same(prev owner) bool { return o.def != "" && o.def == prev.def }

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
		s.report(pack.NewDiagnostic(it.Path, fileLine, 0, CodeRootRule, "rule %q is a root rule outside the base", it.Name).
			WithHint("remove root: true; only the base may provide the root rule"))
	case s.root != "":
		s.report(pack.NewDiagnostic(it.Path, fileLine, 0, CodeRootRuleDuplicate, "second root rule").WithRelated(s.root, fileLine))
	default:
		s.root = it.Path
	}
}

// claimPath gives any other file one owner by path. A tool enters the tree
// at its install path.
func (s *scope) claimPath(l pack.Layer, f pack.File) {
	at := f.Rel
	tool, isTool := strings.CutPrefix(f.Rel, toolsSource)
	if isTool {
		at = ToolsDir + "/" + tool
	}
	prev, taken := s.files[at]
	switch {
	case taken && isTool:
		s.report(pack.NewDiagnostic(f.Path, fileLine, 0, CodeToolConflict, "tool %s is also provided by %s", tool, prev.Layer).WithRelated(prev.src, fileLine).
			WithHint("rename it; a tool has one owner, and a layer that adjusts another layer's tool ships a file with a different name"))
	case taken:
		s.report(pack.NewDiagnostic(f.Path, fileLine, 0, CodePathConflict, "%s is also provided by %s", f.Rel, prev.Layer).WithRelated(prev.src, fileLine).
			WithHint("keep it in one layer; a file has one owner"))
	default:
		s.put(staged{File{Path: at, Layer: l.Path, Data: f.Data, Exec: f.Exec}, f.Path, f.Item})
	}
}

// put adds f to the tree unless a path that differs only by case is there.
func (s *scope) put(f staged) {
	if other, ok := s.lower[strings.ToLower(f.Path)]; ok && other != f.Path {
		s.report(pack.NewDiagnostic(f.src, fileLine, 0, CodeNameCaseConflict, "%s differs only by case from %s", f.Path, other).WithRelated(s.files[other].src, fileLine))
		return
	}
	s.files[f.Path] = f
	s.lower[strings.ToLower(f.Path)] = f.Path
}

// mergeMCP merges the servers of a layer's mcp.jsonc by name under the Name
// row. Servers have no override syntax, so a repeated name is an error,
// unless both define the server the same way, by canonical JSON: it is
// then kept once, as the first layer defines it.
func (s *scope) mergeMCP(l pack.Layer, f pack.File) {
	servers, err := readMCP(f.Data)
	if err != nil {
		s.report(pack.NewDiagnostic(f.Path, fileLine, 0, CodeMCPInvalid, "%v", err))
		return
	}
	for _, sv := range servers {
		o := owner{key: key(mcpServer, sv.name), name: sv.name, what: fmt.Sprintf("MCP server %q", sv.name), layer: l, file: f.Path, line: sv.line, def: sv.canon}
		if s.claim(o) {
			s.servers[sv.name] = sv.raw
		}
	}
}

type server struct {
	name  string
	line  int
	raw   json.RawMessage
	canon string // canonical JSON of raw, which identifies the definition
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
		raw := m.Value.Pack()
		canon, err := canonical(raw)
		if err != nil {
			return nil, err
		}
		out = append(out, server{m.Name.Value.(hujson.Literal).String(), line, raw, canon})
	}
	return out, nil
}

// tree returns the scope's files in path order, with one merged mcp.jsonc
// when any layer has MCP servers, and one merged file for each list file
// a layer has.
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
	for p, n := range s.lists {
		out = append(out, listFile(p, n))
	}
	slices.SortFunc(out, func(a, b File) int { return strings.Compare(a.Path, b.Path) })
	return out
}

// crossScope applies the cross-scope paragraph: a project name may not
// repeat a user name, even with overrides, because harnesses resolve that
// clash differently. An MCP server defined the same way in both scopes is
// no clash, and Stage keeps it at user level only (dropUserServers). users
// holds the user scope once per variant; each clash is reported once,
// against the first variant that has the name.
func crossScope(users []*scope, project *scope) []pack.Diagnostic {
	owners := slices.SortedFunc(maps.Values(project.names), func(a, b owner) int {
		return cmp.Or(strings.Compare(a.file, b.file), cmp.Compare(a.line, b.line))
	})
	var diags []pack.Diagnostic
	for _, o := range owners {
		for _, u := range users {
			prev, ok := u.names[o.key]
			if !ok || prev.name == o.name && o.same(prev) {
				continue
			}
			if prev.name != o.name {
				diags = append(diags, o.clash(prev, CodeNameCaseConflict, "%s differs only by case from %s", o.what, prev.what))
			} else {
				diags = append(diags, o.clash(prev, CodeScopeConflict, "%s is also defined at user level", o.what).
					WithHint("rename it; overrides do not apply across scopes"))
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

// MCPServerNames lists the servers of an mcp.jsonc file by name, in file
// order, for a reader such as the web UI's pack detail.
func MCPServerNames(data []byte) ([]string, error) {
	servers, err := readMCP(data)
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(servers))
	for _, s := range servers {
		names = append(names, s.name)
	}
	return names, nil
}
