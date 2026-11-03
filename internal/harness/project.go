package harness

import (
	"path"
	"slices"
	"strings"

	"github.com/lartweib/dotai/internal/pack"
)

// ProjectKind is a kind of item a profile installs in a project.
type ProjectKind string

// Project kinds. Settings is a harness's settings file, which holds its
// permissions too, and Claude Code's hooks.
const (
	ProjectRules    ProjectKind = "rules"
	ProjectSettings ProjectKind = "settings"
	ProjectHooks    ProjectKind = "hooks"
	ProjectAgents   ProjectKind = "agents"
	ProjectSkills   ProjectKind = "skills"
	ProjectCommands ProjectKind = "commands"
	ProjectMCP      ProjectKind = "mcp"
	// ProjectMods are mods, Claude Code plugins with a hooks module, which
	// install whole (decision 0027).
	ProjectMods ProjectKind = "mods"
)

// What the members of an object of a file merged by key are, besides keys:
// hook events, whose hooks are named one by one, or MCP servers.
const (
	ItemHook   = "hook"
	ItemServer = "MCP server"
)

// Members is an object of a file merged by key whose members are keys of
// their own, such as Claude Code's env variables or hook events: Path from
// the top of the file, and Item, what each member is, "" for a key.
type Members struct {
	Path []string
	Item string
}

// ProjectPath is where rulesync's project output of a harness installs
// instead (decision 0041): From, a slash path in the project staging tree,
// installs to To, relative to the project directory. A From that ends in /
// is a directory, and To is then one too; DirItems marks one whose items
// are directories, such as skills, rather than files, and WholeDir one
// whose item directories dotai owns whole: git tracking any file in one,
// the item's or another, skips the item. ByKey marks a
// settings or configuration file, merged by key into its target
// (decisions 0013 and 0035), whose Each are the objects whose members are
// keys of their own.
type ProjectPath struct {
	From     string
	To       string
	Kind     ProjectKind
	DirItems bool
	WholeDir bool
	ByKey    bool
	Each     []Members
}

// ImportFile is a file of the project that may already import the rules
// file the harness does not read itself: Path, relative to the project,
// imports it when a word of it is one of Lines, each resolved from Path.
type ImportFile struct {
	Path  string
	Lines []string
}

// HomeKeys is a staged project file whose Servers the harness reads from a
// file of the home directory instead, under Projects["<project path with
// forward slashes>"].Servers: Isolated for an isolated install of the
// harness, Real otherwise, both relative to the home directory.
type HomeKeys struct {
	From     string
	Servers  string
	Projects string
	Isolated string
	Real     string
}

// UserItems is where same-name items of Kind live at user level, each Dirs
// relative to the home directory, and whether the project item wins over
// one there (decision 0023).
type UserItems struct {
	Kind        ProjectKind
	Dirs        []string
	ProjectWins bool
}

// ProjectLayout is a harness's project scope (decision 0041): where each
// file rulesync renders in a project installs; Imports, a rules file of the
// project the harness does not load alone, which the pack's own rules file
// imports (Import, with <pack> for the pack's name, holding ImportLine)
// unless one of ImportFiles already does; the MCP servers it keeps outside
// the project; the kinds it has no local slot for; where same-name user
// items live; and the project skills directories of other harnesses it
// reads too. Paths are slash paths.
type ProjectLayout struct {
	Paths       []ProjectPath
	Imports     string
	ImportFiles []ImportFile
	Import      string
	ImportLine  string
	MCP         *HomeKeys
	NoSlot      []ProjectKind
	UserItems   []UserItems
	AlsoReads   []string
}

// ProjectTarget is where a staged project file installs: Target, relative
// to the project directory, its Kind, Item, the item it belongs to, such as
// "skill lint", "" for a file merged by key, whose items are its keys,
// Name, the item's name, Dir, the item's directory with a trailing slash
// for an item that is one, and WholeDir, ByKey and Each as in its
// ProjectPath.
type ProjectTarget struct {
	Target   string
	Kind     ProjectKind
	Item     string
	Name     string
	Dir      string
	WholeDir bool
	ByKey    bool
	Each     []Members
}

// itemNames are how an item of each kind is named, by the file stem or,
// for a skill, its directory.
var itemNames = map[ProjectKind]string{
	ProjectRules: "rule", ProjectAgents: "agent", ProjectSkills: "skill", ProjectCommands: "command", ProjectMods: "mod",
}

// Locate gives the target of source, a slash path in the project staging
// tree; ok is false when the harness does not install it, such as a file
// of another harness or one of the files decision 0041 never writes.
func (l ProjectLayout) Locate(source string) (ProjectTarget, bool) {
	for _, p := range l.Paths {
		if !strings.HasSuffix(p.From, "/") {
			if source == p.From {
				return ProjectTarget{Target: p.To, Kind: p.Kind, ByKey: p.ByKey, Each: p.Each}, true
			}
			continue
		}
		rest, ok := strings.CutPrefix(source, p.From)
		if !ok {
			continue
		}
		t := ProjectTarget{Target: p.To + rest, Kind: p.Kind, WholeDir: p.WholeDir}
		if p.DirItems {
			name, _, _ := strings.Cut(rest, "/")
			t.Name, t.Dir = name, p.To+name+"/"
		} else {
			t.Name = strings.TrimSuffix(rest, path.Ext(rest))
		}
		if word, ok := itemNames[p.Kind]; ok {
			t.Item = word + " " + t.Name
		} else {
			t.Name = ""
		}
		return t, true
	}
	return ProjectTarget{}, false
}

// Takes reports whether the layout has a place for items of kind.
func (l ProjectLayout) Takes(kind ProjectKind) bool {
	return slices.ContainsFunc(l.Paths, func(p ProjectPath) bool { return p.Kind == kind })
}

// ImportFile is the pack's rules file that imports AGENTS.md, relative to
// the project directory, "" for a harness that reads AGENTS.md itself.
func (l ProjectLayout) ImportFile(pack string) string {
	return strings.ReplaceAll(l.Import, "<pack>", pack)
}

// ImportedBy reports whether data, the content of the project file name,
// already imports Imports: one of its words is one of the import lines of
// that file.
func (l ProjectLayout) ImportedBy(name string, data []byte) bool {
	i := slices.IndexFunc(l.ImportFiles, func(f ImportFile) bool { return f.Path == name })
	if i < 0 {
		return false
	}
	for _, word := range strings.Fields(string(data)) {
		if slices.Contains(l.ImportFiles[i].Lines, word) {
			return true
		}
	}
	return false
}

// clone is l in a copy the caller may change.
func (l ProjectLayout) clone() ProjectLayout {
	l.Paths = slices.Clone(l.Paths)
	for i := range l.Paths {
		l.Paths[i].Each = slices.Clone(l.Paths[i].Each)
		for j := range l.Paths[i].Each {
			l.Paths[i].Each[j].Path = slices.Clone(l.Paths[i].Each[j].Path)
		}
	}
	if l.MCP != nil {
		m := *l.MCP
		l.MCP = &m
	}
	l.NoSlot, l.AlsoReads = slices.Clone(l.NoSlot), slices.Clone(l.AlsoReads)
	l.ImportFiles = slices.Clone(l.ImportFiles)
	for i := range l.ImportFiles {
		l.ImportFiles[i].Lines = slices.Clone(l.ImportFiles[i].Lines)
	}
	l.UserItems = slices.Clone(l.UserItems)
	for i := range l.UserItems {
		l.UserItems[i].Dirs = slices.Clone(l.UserItems[i].Dirs)
	}
	return l
}

// ModsSource is where the project tree holds the mods, one directory each,
// and ModsMinClaude the first Claude Code that loads a mod (HARNESS-NOTES).
const (
	ModsSource    = pack.ModsDir + "/"
	ModsMinClaude = "2.1.287"
)

// The project layout of each harness (decision 0041), from rulesync's
// project output and the project scope inventory of HARNESS-NOTES.
var (
	claudeProject = ProjectLayout{
		Paths: []ProjectPath{
			{From: ".claude/rules/", To: ".claude/rules/", Kind: ProjectRules},
			{From: ".claude/agents/", To: ".claude/agents/", Kind: ProjectAgents},
			{From: ".claude/skills/", To: ".claude/skills/", Kind: ProjectSkills, DirItems: true},
			{From: ".claude/commands/", To: ".claude/commands/", Kind: ProjectCommands},
			// A mod, staged under mods/, is a plugin, which Claude Code loads
			// from the project only under .claude/skills/<name>/ (decision
			// 0027).
			{From: ModsSource, To: ".claude/skills/", Kind: ProjectMods, DirItems: true, WholeDir: true},
			// .claude/settings.json is the team's; the local file wins over it
			// and its allow rules apply without workspace trust.
			{From: ".claude/settings.json", To: ".claude/settings.local.json", Kind: ProjectSettings, ByKey: true,
				Each: []Members{{Path: []string{"env"}}, {Path: []string{"hooks"}, Item: ItemHook}, {Path: []string{"permissions"}}}},
		},
		// A project AGENTS.md alone did not load on 2.1.316; an import in a
		// rules file resolves from that file.
		Imports: "AGENTS.md",
		ImportFiles: []ImportFile{
			{Path: "CLAUDE.md", Lines: []string{"@AGENTS.md", "@./AGENTS.md"}},
			{Path: ".claude/CLAUDE.md", Lines: []string{"@../AGENTS.md"}},
			{Path: "CLAUDE.local.md", Lines: []string{"@AGENTS.md", "@./AGENTS.md"}},
		},
		Import:     ".claude/rules/<pack>.md",
		ImportLine: "@../../AGENTS.md",
		// Claude Code's local scope; CLAUDE_CONFIG_DIR moves the file into
		// the configuration directory, as an isolated install sets it.
		MCP: &HomeKeys{From: ".mcp.json", Servers: "mcpServers", Projects: "projects", Isolated: IsolatedDir + "/claude/.claude.json", Real: ".claude.json"},
		UserItems: []UserItems{
			{Kind: ProjectSkills, Dirs: []string{".claude/skills", IsolatedDir + "/claude/skills"}},
			{Kind: ProjectAgents, Dirs: []string{".claude/agents", IsolatedDir + "/claude/agents"}, ProjectWins: true},
		},
	}
	agyProject = ProjectLayout{
		Paths: []ProjectPath{
			{From: ".agents/rules/", To: ".agents/rules/", Kind: ProjectRules},
			{From: ".agents/agents/", To: ".agents/agents/", Kind: ProjectAgents},
			// rulesync renders commands as skills for agy, which has no commands.
			{From: ".agents/skills/", To: ".agents/skills/", Kind: ProjectSkills, DirItems: true},
			{From: ".agents/hooks.json", To: ".agents/hooks.json", Kind: ProjectHooks, ByKey: true,
				Each: []Members{{Path: []string{"rulesync"}, Item: ItemHook}}},
			{From: ".agents/mcp_config.json", To: ".agents/mcp_config.json", Kind: ProjectMCP, ByKey: true,
				Each: []Members{{Path: []string{"mcpServers"}, Item: ItemServer}}},
		},
		// agy has no project settings file, and rulesync renders no project
		// permissions for it; it has no mods.
		NoSlot: []ProjectKind{ProjectSettings, ProjectMods},
		UserItems: []UserItems{
			{Kind: ProjectSkills, Dirs: []string{".gemini/config/skills", ".gemini/antigravity-cli/skills", IsolatedDir + "/gemini/config/skills"}, ProjectWins: true},
			{Kind: ProjectAgents, Dirs: []string{".gemini/config/agents", IsolatedDir + "/gemini/config/agents"}, ProjectWins: true},
		},
	}
	openCodeProject = ProjectLayout{
		Paths: []ProjectPath{
			// Named in instructions of .opencode/opencode.json, which rulesync
			// writes into opencode.jsonc.
			{From: ".opencode/memories/", To: ".opencode/memories/", Kind: ProjectRules},
			{From: ".opencode/agents/", To: ".opencode/agents/", Kind: ProjectAgents},
			{From: ".opencode/skills/", To: ".opencode/skills/", Kind: ProjectSkills, DirItems: true},
			{From: ".opencode/commands/", To: ".opencode/commands/", Kind: ProjectCommands},
			{From: ".opencode/plugins/", To: ".opencode/plugins/", Kind: ProjectHooks},
			// The root opencode.json(c) is the team's; .opencode/opencode.json
			// is read after it, and wins.
			{From: "opencode.jsonc", To: ".opencode/opencode.json", Kind: ProjectSettings, ByKey: true,
				Each: []Members{{Path: []string{"mcp", "servers"}, Item: ItemServer}}},
		},
		// OpenCode has no mods.
		NoSlot: []ProjectKind{ProjectMods},
		UserItems: []UserItems{
			{Kind: ProjectSkills, Dirs: []string{".config/opencode/skills", ".agents/skills", ".claude/skills", IsolatedDir + "/opencode/config/opencode/skills"}, ProjectWins: true},
			{Kind: ProjectAgents, Dirs: []string{".config/opencode/agents", IsolatedDir + "/opencode/config/opencode/agents"}, ProjectWins: true},
		},
		// OpenCode collects .claude and .agents of every directory too.
		AlsoReads: []string{".claude/skills/", ".agents/skills/"},
	}
)
