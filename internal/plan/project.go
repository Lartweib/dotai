package plan

import (
	"cmp"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"maps"
	"path/filepath"
	"slices"
	"strings"

	"github.com/lartweib/dotai/internal/harness"
	"github.com/lartweib/dotai/internal/merge"
	"github.com/lartweib/dotai/internal/plan/jsonc"
)

// The project scope (decision 0041) stays local: each file a profile
// renders installs to its harness's local target, a target that git tracks
// is never written, and every file dotai creates in the project is listed
// in .git/info/exclude. BuildProject plans it from what the caller read of
// the disk and of git; it does no I/O either.

// RecordFile is the project record, relative to the project directory: the
// engine's record of what it applied there, as ~/.dotai/state.json is at
// user level.
const RecordFile = ".dotai/state.json"

// Actions of a project plan besides Create, Replace and Unchanged: an
// existing file merged by key, and an existing file that is not dotai's
// and holds something else, which dotai leaves as it is.
const (
	Merge    Action = "merge"
	Conflict Action = "conflict"
)

// The kinds of the ops no harness owns: the project's tools, under
// .dotai/tools/, and the project record.
const (
	KindTools  = "tools"
	KindRecord = "record"
)

// Warning codes of a project plan.
const (
	// CodeExcludePending: the project is not in a git repository, so the
	// exclude entries wait until it is.
	CodeExcludePending = "exclude-pending"
	// CodeNoLocalSlot: a harness has no local place for something the
	// profiles hold, which it does not get.
	CodeNoLocalSlot = "no-local-slot"
	// CodeScopeClash: an item the plan installs has the name of a user-level
	// item of the same harness, and one of them hides the other.
	CodeScopeClash = "scope-clash"
	// CodeReadTwice: a harness reads an item the plan installs from more
	// than one directory of the project.
	CodeReadTwice = "read-twice"
)

const (
	dotaiDir            = ".dotai/"
	hooksSource         = ".rulesync/hooks.jsonc"
	importItem          = "import of AGENTS.md"
	recordItem          = "project record"
	projectSourcesMatch = ".rulesync/"
)

// SkipTracked is the reason of an item skipped because git tracks its
// target.
const SkipTracked = "tracked"

// kindSources are the staged rulesync sources that hold items of each
// kind, a directory when it ends in /, which a harness without a local slot
// for the kind does not get; kindWords how a kind reads in a warning.
var (
	kindSources = map[harness.ProjectKind][]string{
		harness.ProjectSettings: {".rulesync/permissions.jsonc"},
		harness.ProjectHooks:    {hooksSource},
		harness.ProjectMCP:      {".rulesync/mcp.jsonc"},
		harness.ProjectRules:    {".rulesync/rules/"},
		harness.ProjectAgents:   {".rulesync/subagents/"},
		harness.ProjectSkills:   {".rulesync/skills/"},
		harness.ProjectCommands: {".rulesync/commands/"},
	}
	kindWords = map[harness.ProjectKind]string{harness.ProjectSettings: "settings and permissions", harness.ProjectMCP: "MCP servers"}
)

// ProjectInput is what BuildProject plans from. Files is the project
// staging tree, each with its Hash and its Data. Disk holds the state of
// each target that exists, in the project or, for Claude Code's MCP
// servers, under the home directory, and Content the content of each such
// target that is merged by key. Tracked holds the targets git tracks. Repo
// is set when the project, the top of a worktree, is in a git repository.
// MCPKey is the entry of the projects object of a harness's home file that
// holds its MCP servers, the project with forward slashes when empty.
// Import holds the harnesses whose layout's Imports the project has and no
// file of its ImportFiles imports. Isolated holds the harnesses the user
// installed isolated, whose home file is in their isolated directory.
// Exists holds each path of ProjectProbes that exists.
type ProjectInput struct {
	Project  string // the absolute project directory
	Home     string // the absolute home directory
	Pack     string // the pack's name
	Adapters []harness.Adapter
	Files    []File
	Disk     map[string]Current
	Content  map[string][]byte
	Tracked  map[string]bool
	Repo     bool
	MCPKey   string
	Import   map[string]bool
	Isolated map[string]bool
	Exists   map[string]bool
}

// Placement is where a project apply puts each file. Ops are in target
// order; Skipped are the items left uninstalled because git tracks their
// target, by harness and target; Exclude are the entries of the exclude
// file, as patterns relative to the project, the top of its worktree.
type Placement struct {
	Ops      []ProjectOp
	Skipped  []Skipped
	Exclude  []string
	Warnings []Warning
}

// ProjectOp is one target of a project plan. Harness is "" for the tools
// and the record. Source is the path in the project staging tree, "" for a
// file dotai makes. Before is the SHA-256 of the target, "" when it is
// missing, and After that of what it gets when it is written whole, "" for
// a file merged by key, whose Keys are the keys the profiles write there,
// each with what it does to the key the target holds.
type ProjectOp struct {
	Action  Action       `json:"action"`
	Harness string       `json:"harness"`
	Kind    string       `json:"kind"`
	Source  string       `json:"source"`
	Target  string       `json:"target"`
	Before  string       `json:"before"`
	After   string       `json:"after"`
	Keys    []ProjectKey `json:"keys"`
}

// ProjectKey is a key of a file merged by key, by its path from the top of
// the file: Create when the target lacks it, Unchanged when it holds the
// same value, Merge when it holds another.
type ProjectKey struct {
	Path   []string `json:"key"`
	Action Action   `json:"action"`
}

// Skipped is an item left uninstalled because git tracks Target, the file
// it goes to: a skill, a rule or another item named by kind and name, or a
// key, a hook or an MCP server of a file merged by key. Reason is why,
// SkipTracked so far.
type Skipped struct {
	Harness string `json:"harness"`
	Kind    string `json:"kind"`
	Item    string `json:"item"`
	Target  string `json:"target"`
	Reason  string `json:"reason"`
}

// Warning is what the plan warns about, by Code: the Harness, Item and
// Path it concerns, each "" when it concerns none, and Message, text for
// people.
type Warning struct {
	Code    string `json:"code"`
	Harness string `json:"harness"`
	Item    string `json:"item"`
	Path    string `json:"path"`
	Message string `json:"message"`
}

// ImportHash is the SHA-256 in hex of the rules file that holds line.
func ImportHash(line string) string {
	h := sha256.Sum256([]byte(line + "\n"))
	return hex.EncodeToString(h[:])
}

// located is a target of the plan: the staged file, or what dotai makes,
// the harness, "" for the tools and the record, and where it goes. name is
// the item's name, such as lint for a skill, and dir its directory for an
// item that is one; home marks the home file of the harness's MCP servers.
type located struct {
	file    File
	harness string
	kind    string
	item    string
	name    string
	dir     string
	target  string
	byKey   bool
	each    []harness.Members
	home    *harness.HomeKeys
}

// Target is a target of a project plan: its absolute path and whether the
// plan reads its content, as it does for a file merged by key, not only
// its hash.
type Target struct {
	Path string
	Read bool
}

// ProjectTargets are the targets BuildProject plans for in, from in.Files
// and in.Import alone, in its order; the caller reads them and asks git
// about them before it builds the plan.
func ProjectTargets(in ProjectInput) ([]Target, error) {
	targets, err := in.locate()
	if err != nil {
		return nil, err
	}
	out := make([]Target, len(targets))
	for i, t := range targets {
		out[i] = Target{Path: t.target, Read: t.byKey || t.home != nil}
	}
	return out, nil
}

// BuildProject plans the project tree of in into in.Project, as decision
// 0041 places it. A staged file that no harness of in.Adapters installs, or
// one of the files dotai never writes there such as CLAUDE.md, is an
// UnmappedError, and so are two files with one target.
//
// An item that git tracks the target of is skipped whole: each of its files
// is left as it is, and it is named in Skipped; a file merged by key that
// git tracks names each of its keys, hooks or MCP servers. A file merged by
// key that exists is merged into, key by key; a file written whole that
// exists with the same content is unchanged and otherwise a conflict, left
// as it is. A target that exists, or that the plan does not write, is not
// excluded.
func BuildProject(in ProjectInput) (Placement, error) {
	if !filepath.IsAbs(in.Project) || !filepath.IsAbs(in.Home) {
		return Placement{}, fmt.Errorf("project %q and home %q must be absolute", in.Project, in.Home)
	}
	targets, err := in.locate()
	if err != nil {
		return Placement{}, err
	}
	p := Placement{Exclude: []string{in.pattern(dotaiDir)}}
	tracked := map[string]string{} // the first tracked target of each skipped item, by harness and item
	for _, t := range targets {
		if t.item != "" && in.Tracked[t.target] {
			if _, ok := tracked[t.harness+"\x00"+t.item]; !ok {
				tracked[t.harness+"\x00"+t.item] = t.target
			}
		}
	}
	installed := map[string][]located{} // the items installed, by harness
	created := map[string]bool{}        // the items whose every file is created
	for _, t := range targets {
		if first, ok := tracked[t.harness+"\x00"+t.item]; ok && t.item != "" {
			if first == t.target {
				p.Skipped = append(p.Skipped, Skipped{Harness: t.harness, Kind: t.kind, Item: t.item, Target: t.target, Reason: SkipTracked})
			}
			continue
		}
		if in.Tracked[t.target] {
			items, err := in.itemsOf(t)
			if err != nil {
				return Placement{}, err
			}
			for _, item := range items {
				p.Skipped = append(p.Skipped, Skipped{Harness: t.harness, Kind: t.kind, Item: item, Target: t.target, Reason: SkipTracked})
			}
			continue
		}
		op, err := in.opOf(t)
		if err != nil {
			return Placement{}, err
		}
		p.Ops = append(p.Ops, op)
		if t.item != "" {
			key := t.harness + "\x00" + t.item
			if _, ok := created[key]; !ok {
				created[key] = true
				installed[t.harness] = append(installed[t.harness], t)
			}
			created[key] = created[key] && op.Action == Create
		}
	}
	for _, t := range targets {
		if rel, ok := in.rel(t.dir); ok && created[t.harness+"\x00"+t.item] && !in.Exists[t.dir] {
			if e := in.pattern(rel + "/"); !slices.Contains(p.Exclude, e) {
				p.Exclude = append(p.Exclude, e)
			}
		}
	}
	for _, op := range p.Ops {
		rel, ok := in.rel(op.Target)
		if !ok || op.Action != Create || strings.HasPrefix(rel, dotaiDir) {
			continue
		}
		if slices.ContainsFunc(p.Exclude[1:], func(e string) bool {
			return strings.HasSuffix(e, "/") && strings.HasPrefix(in.pattern(rel), e)
		}) {
			continue
		}
		p.Exclude = append(p.Exclude, in.pattern(rel))
	}
	slices.Sort(p.Exclude[1:])
	slices.SortFunc(p.Ops, func(a, b ProjectOp) int { return cmp.Compare(a.Target, b.Target) })
	p.Warnings = in.warnings(installed)
	return p, nil
}

// ProjectProbes are the paths whose existence BuildProject reads from
// in.Exists: where a user-level item of the name of each item the plan may
// install lives, the skills directories of other harnesses a harness reads
// too, and the directory of each item that is one, which is excluded whole
// when the plan creates it. They are sorted.
func ProjectProbes(in ProjectInput) ([]string, error) {
	targets, err := in.locate()
	if err != nil {
		return nil, err
	}
	var out []string
	for _, t := range targets {
		if t.dir != "" {
			out = append(out, t.dir)
		}
		a, ok := in.adapter(t.harness)
		if !ok || t.name == "" {
			continue
		}
		for _, u := range a.Project.UserItems {
			if string(u.Kind) == t.kind {
				for _, dir := range u.Dirs {
					out = append(out, t.userItem(in.Home, dir))
				}
			}
		}
		if t.dir != "" {
			for _, dir := range a.Project.AlsoReads {
				out = append(out, filepath.Join(in.Project, filepath.FromSlash(dir+t.name)))
			}
		}
	}
	slices.Sort(out)
	return slices.Compact(out), nil
}

// locate gives every target of the plan, by harness in the order of
// in.Adapters, the tools and the record last, each in target order.
func (in ProjectInput) locate() ([]located, error) {
	var out []located
	var unmapped []string
	for _, f := range in.Files {
		if err := checkPath(f.Path); err != nil {
			return nil, err
		}
		if strings.HasPrefix(f.Path, projectSourcesMatch) {
			continue
		}
		if rest, ok := strings.CutPrefix(f.Path, merge.ToolsDir+"/"); ok {
			out = append(out, located{file: f, kind: KindTools, item: "tool " + rest, target: in.at(f.Path)})
			continue
		}
		found := false
		for _, a := range in.Adapters {
			if m := a.Project.MCP; m != nil && f.Path == m.From {
				file := m.Real
				if in.Isolated[a.ID] {
					file = m.Isolated
				}
				out = append(out, located{file: f, harness: a.ID, kind: string(harness.ProjectMCP), target: filepath.Join(in.Home, filepath.FromSlash(file)), home: m})
				found = true
				break
			}
			if t, ok := a.Project.Locate(f.Path); ok {
				l := located{file: f, harness: a.ID, kind: string(t.Kind), item: t.Item, name: t.Name, target: in.at(t.Target), byKey: t.ByKey, each: t.Each}
				if t.Dir != "" {
					l.dir = in.at(strings.TrimSuffix(t.Dir, "/"))
				}
				out = append(out, l)
				found = true
				break
			}
		}
		if !found {
			unmapped = append(unmapped, f.Path)
		}
	}
	if len(unmapped) > 0 {
		slices.Sort(unmapped)
		return nil, &UnmappedError{Paths: unmapped}
	}
	for _, a := range in.Adapters {
		if rel := a.Project.ImportFile(in.Pack); rel != "" && in.Import[a.ID] {
			line := a.Project.ImportLine
			out = append(out, located{file: File{Hash: ImportHash(line), Data: []byte(line + "\n")}, harness: a.ID, kind: string(harness.ProjectRules), item: importItem, target: in.at(rel)})
		}
	}
	out = append(out, located{kind: KindRecord, item: recordItem, target: in.at(RecordFile)})
	seen := map[string]located{}
	for _, t := range out {
		if prev, ok := seen[t.target]; ok {
			return nil, fmt.Errorf("%s and %s install to the same target %s", cmp.Or(prev.file.Path, prev.item), cmp.Or(t.file.Path, t.item), t.target)
		}
		seen[t.target] = t
	}
	order := func(h string) int {
		if i := slices.IndexFunc(in.Adapters, func(a harness.Adapter) bool { return a.ID == h }); i >= 0 {
			return i
		}
		return len(in.Adapters)
	}
	slices.SortStableFunc(out, func(a, b located) int {
		return cmp.Or(cmp.Compare(order(a.harness), order(b.harness)), cmp.Compare(a.target, b.target))
	})
	return out, nil
}

// opOf plans the target t.
func (in ProjectInput) opOf(t located) (ProjectOp, error) {
	op := ProjectOp{Action: Create, Harness: t.harness, Kind: t.kind, Source: t.file.Path, Target: t.target, Keys: []ProjectKey{}}
	cur, exists := in.Disk[t.target]
	if exists {
		op.Before = cur.Hash
	}
	switch {
	case t.kind == KindRecord:
		if exists {
			op.Action = Replace
		}
		return op, nil
	case t.home != nil || t.byKey:
		paths, values, err := t.stagedKeys(in.mcpKey())
		if err != nil {
			return ProjectOp{}, fmt.Errorf("staged %s: %w", t.file.Path, err)
		}
		var doc *jsonc.Document
		if exists {
			if doc, err = jsonc.Parse(in.Content[t.target]); err != nil {
				op.Action = Conflict // not a JSON object dotai can edit key by key
				return op, nil
			}
			op.Action = Unchanged
		}
		for i, p := range paths {
			k := ProjectKey{Path: p, Action: Create}
			if doc != nil {
				if have, ok := doc.Get(p); ok {
					k.Action = Unchanged
					if same, err := sameJSON(have, values[i]); err != nil || !same {
						k.Action = Merge
					}
				}
				if k.Action != Unchanged {
					op.Action = Merge
				}
			}
			op.Keys = append(op.Keys, k)
		}
		return op, nil
	}
	op.After = t.file.Hash
	if exists {
		op.Action = Conflict
		if cur.Hash == t.file.Hash {
			op.Action = Unchanged
		}
	}
	return op, nil
}

// stagedKeys are the keys t writes, with their values: the members of each
// object of t.each one by one and every other key whole, in the order of
// the staged file, or for the home file of a harness's MCP servers, each
// server under the project's entry.
func (t located) stagedKeys(mcpKey string) ([][]string, [][]byte, error) {
	doc, err := jsonc.Parse(t.file.Data)
	if err != nil {
		return nil, nil, err
	}
	if t.home != nil {
		names, _ := doc.Keys([]string{t.home.Servers})
		var paths [][]string
		var values [][]byte
		for _, name := range names {
			v, _ := doc.Get([]string{t.home.Servers, name})
			paths = append(paths, []string{t.home.Projects, mcpKey, t.home.Servers, name})
			values = append(values, v)
		}
		return paths, values, nil
	}
	paths := keysOf(doc, nil, t.each)
	values := make([][]byte, len(paths))
	for i, p := range paths {
		values[i], _ = doc.Get(p)
	}
	return paths, values, nil
}

// keysOf are the keys of the object at prefix in doc, in order: an object
// of each has each member as a key, an object above one is looked into,
// and any other value is a key.
func keysOf(doc *jsonc.Document, prefix []string, each []harness.Members) [][]string {
	names, _ := doc.Keys(prefix)
	var out [][]string
	for _, name := range names {
		p := append(slices.Clone(prefix), name)
		switch {
		case slices.ContainsFunc(each, func(m harness.Members) bool { return slices.Equal(m.Path, p) }):
			if members, ok := doc.Keys(p); ok {
				for _, m := range members {
					out = append(out, append(slices.Clone(p), m))
				}
				continue
			}
		case slices.ContainsFunc(each, func(m harness.Members) bool { return len(m.Path) > len(p) && slices.Equal(m.Path[:len(p)], p) }):
			if _, ok := doc.Keys(p); ok {
				out = append(out, keysOf(doc, p, each)...)
				continue
			}
		}
		out = append(out, p)
	}
	return out
}

// itemsOf names what t holds, for a tracked target that is not part of a
// named item: each key, hook or MCP server of a file merged by key, each
// hook of a hooks file written whole, or the file.
func (in ProjectInput) itemsOf(t located) ([]string, error) {
	switch {
	case t.byKey:
		paths, values, err := t.stagedKeys(in.mcpKey())
		if err != nil {
			return nil, fmt.Errorf("staged %s: %w", t.file.Path, err)
		}
		var out []string
		for i, p := range paths {
			parent := p[:len(p)-1]
			kind := ""
			if i := slices.IndexFunc(t.each, func(m harness.Members) bool { return slices.Equal(m.Path, parent) }); i >= 0 {
				kind = t.each[i].Item
			}
			switch kind {
			case harness.ItemHook:
				out = append(out, hookItems(p[len(p)-1], values[i])...)
			case harness.ItemServer:
				out = append(out, harness.ItemServer+" "+p[len(p)-1])
			default:
				out = append(out, "key "+strings.Join(p, "."))
			}
		}
		return out, nil
	case t.kind == string(harness.ProjectHooks):
		a, _ := in.adapter(t.harness)
		if items := in.sourceHooks(a.Target); len(items) > 0 {
			return items, nil
		}
	}
	return []string{"file " + t.file.Path}, nil
}

// sourceHooks names each hook that the staged .rulesync/hooks.jsonc gives
// the rulesync target: the shared hooks of each event, or the hooks of the
// target's own block for an event it declares, which replace them.
func (in ProjectInput) sourceHooks(target string) []string {
	i := slices.IndexFunc(in.Files, func(f File) bool { return f.Path == hooksSource })
	if i < 0 {
		return nil
	}
	doc, err := jsonc.Parse(in.Files[i].Data)
	if err != nil {
		return nil
	}
	shared, _ := doc.Keys([]string{"hooks"})
	own, _ := doc.Keys([]string{target, "hooks"})
	var out []string
	for _, event := range shared {
		at := []string{"hooks", event}
		if slices.Contains(own, event) {
			at = []string{target, "hooks", event}
		}
		v, _ := doc.Get(at)
		out = append(out, hookItems(event, v)...)
	}
	for _, event := range own {
		if !slices.Contains(shared, event) {
			v, _ := doc.Get([]string{target, "hooks", event})
			out = append(out, hookItems(event, v)...)
		}
	}
	return out
}

// hookItems names each hook of value, the hooks of event: each command in
// it, found at any depth.
func hookItems(event string, value []byte) []string {
	var commands []string
	var walk func(v any)
	walk = func(v any) {
		switch v := v.(type) {
		case map[string]any:
			if c, ok := v["command"].(string); ok {
				commands = append(commands, c)
			}
			for _, k := range slices.Sorted(maps.Keys(v)) {
				if k != "command" {
					walk(v[k])
				}
			}
		case []any:
			for _, e := range v {
				walk(e)
			}
		}
	}
	if v, err := decodeJSON(value); err == nil {
		walk(v)
	}
	if len(commands) == 0 {
		return []string{harness.ItemHook + " " + event}
	}
	out := make([]string, len(commands))
	for i, c := range commands {
		out[i] = harness.ItemHook + " " + event + ": " + c
	}
	return out
}

// warnings are the scope clashes and the items read twice of the items
// installed, by harness, the slots a harness lacks, and the exclude entries
// that wait for a repository, in that order.
func (in ProjectInput) warnings(installed map[string][]located) []Warning {
	out := []Warning{}
	for _, a := range in.Adapters {
		for _, u := range a.Project.UserItems {
			for _, t := range installed[a.ID] {
				if t.kind != string(u.Kind) || t.name == "" || t.item == importItem {
					continue
				}
				for _, dir := range u.Dirs {
					p := t.userItem(in.Home, dir)
					if !in.Exists[p] {
						continue
					}
					msg := fmt.Sprintf("%s loads the user-level %s from %s instead of the project's", a.ID, t.item, p)
					if u.ProjectWins {
						msg = fmt.Sprintf("%s loads the project's %s instead of the user-level one in %s", a.ID, t.item, p)
					}
					out = append(out, Warning{Code: CodeScopeClash, Harness: a.ID, Item: t.item, Path: p, Message: msg})
				}
			}
		}
	}
	for _, a := range in.Adapters {
		for _, t := range installed[a.ID] {
			if t.dir == "" {
				continue
			}
			for _, dir := range a.Project.AlsoReads {
				p := filepath.Join(in.Project, filepath.FromSlash(dir+t.name))
				other := slices.ContainsFunc(in.Adapters, func(b harness.Adapter) bool {
					return slices.ContainsFunc(installed[b.ID], func(o located) bool { return o.kind == t.kind && o.dir == p })
				})
				if in.Exists[p] || other {
					out = append(out, Warning{Code: CodeReadTwice, Harness: a.ID, Item: t.item, Path: p,
						Message: fmt.Sprintf("%s also reads the %s from %s, so it lists it more than once", a.ID, t.item, p)})
				}
			}
		}
	}
	for _, a := range in.Adapters {
		for _, kind := range a.Project.NoSlot {
			if !slices.ContainsFunc(in.Files, func(f File) bool {
				return slices.ContainsFunc(kindSources[kind], func(src string) bool {
					return f.Path == src || strings.HasSuffix(src, "/") && strings.HasPrefix(f.Path, src)
				})
			}) {
				continue
			}
			word := cmp.Or(kindWords[kind], string(kind))
			out = append(out, Warning{Code: CodeNoLocalSlot, Harness: a.ID, Item: string(kind),
				Message: fmt.Sprintf("%s has no local place for project %s, so those of the profiles are not installed for it", a.ID, word)})
		}
	}
	if !in.Repo {
		out = append(out, Warning{Code: CodeExcludePending, Path: in.Project,
			Message: fmt.Sprintf("%s is not in a git repository, so the exclude entries are pending: the next apply adds them once it is one", in.Project)})
	}
	return out
}

// adapter is the adapter of id among in.Adapters.
func (in ProjectInput) adapter(id string) (harness.Adapter, bool) {
	i := slices.IndexFunc(in.Adapters, func(a harness.Adapter) bool { return a.ID == id })
	if i < 0 {
		return harness.Adapter{}, false
	}
	return in.Adapters[i], true
}

// at is the absolute path of rel, a slash path in the project.
func (in ProjectInput) at(rel string) string {
	return filepath.Join(in.Project, filepath.FromSlash(rel))
}

// rel is target as a slash path relative to the project, and whether it is
// inside it.
func (in ProjectInput) rel(target string) (string, bool) {
	rel, err := filepath.Rel(in.Project, target)
	if err != nil || !filepath.IsLocal(rel) {
		return "", false
	}
	return filepath.ToSlash(rel), true
}

// mcpKey is the entry of a home file's projects object for the project.
func (in ProjectInput) mcpKey() string { return cmp.Or(in.MCPKey, filepath.ToSlash(in.Project)) }

// pattern is the exclude entry of rel, a slash path in the project, a
// directory when it ends in /: anchored at the project, the top of its
// worktree, with the characters a pattern reads escaped.
func (in ProjectInput) pattern(rel string) string {
	var b strings.Builder
	b.WriteByte('/')
	trimmed := strings.TrimRight(rel, " ")
	for _, r := range trimmed {
		if strings.ContainsRune(`\*?[`, r) {
			b.WriteByte('\\')
		}
		b.WriteRune(r)
	}
	for range len(rel) - len(trimmed) {
		b.WriteString(`\ `)
	}
	return b.String()
}

// userItem is where a user-level item of the name of t lives in dir, a
// slash path relative to home: the directory of an item that is one, or an
// agent's file.
func (t located) userItem(home, dir string) string {
	name := t.name
	if t.dir == "" {
		name += ".md"
	}
	return filepath.Join(home, filepath.FromSlash(dir), name)
}

// sameJSON reports whether a and b are the same value by canonical JSON.
func sameJSON(a, b []byte) (bool, error) {
	ca, err := jsonc.Canonical(a)
	if err != nil {
		return false, err
	}
	cb, err := jsonc.Canonical(b)
	if err != nil {
		return false, err
	}
	return string(ca) == string(cb), nil
}

// decodeJSON decodes value, JSON or JSONC.
func decodeJSON(value []byte) (any, error) {
	c, err := jsonc.Canonical(value)
	if err != nil {
		return nil, err
	}
	var v any
	return v, json.Unmarshal(c, &v)
}
