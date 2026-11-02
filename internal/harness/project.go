package harness

import (
	"path"
	"slices"
	"strings"
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
// is a directory, and To is then one too. ByKey marks a settings or
// configuration file, merged by key into its target (decisions 0013 and
// 0035), whose Each are the objects whose members are keys of their own.
type ProjectPath struct {
	From  string
	To    string
	Kind  ProjectKind
	ByKey bool
	Each  []Members
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
// file rulesync renders in a project installs, the pack's own rules file
// that imports AGENTS.md (Import, with <pack> for the pack's name, and
// ImportLine), the MCP servers it keeps outside the project, the kinds it
// has no local slot for, where same-name user items live, and the project
// skills directories of other harnesses it reads too. Paths are slash
// paths.
type ProjectLayout struct {
	Paths      []ProjectPath
	Import     string
	ImportLine string
	MCP        *HomeKeys
	NoSlot     []ProjectKind
	UserItems  []UserItems
	AlsoReads  []string
}

// ProjectTarget is where a staged project file installs: Target, relative
// to the project directory, its Kind, Item, the item it belongs to, such as
// "skill lint", "" for a file merged by key, whose items are its keys, and
// ByKey and Each as in its ProjectPath.
type ProjectTarget struct {
	Target string
	Kind   ProjectKind
	Item   string
	ByKey  bool
	Each   []Members
}

// itemNames are how an item of each kind is named, by the file stem or,
// for a skill, its directory.
var itemNames = map[ProjectKind]string{
	ProjectRules: "rule", ProjectAgents: "agent", ProjectSkills: "skill", ProjectCommands: "command",
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
		name, _, dir := strings.Cut(rest, "/")
		if !dir {
			name = strings.TrimSuffix(name, path.Ext(name))
		}
		item := ""
		if word, ok := itemNames[p.Kind]; ok {
			item = word + " " + name
		}
		return ProjectTarget{Target: p.To + rest, Kind: p.Kind, Item: item}, true
	}
	return ProjectTarget{}, false
}

// ImportFile is the pack's rules file that imports AGENTS.md, relative to
// the project directory, "" for a harness that reads AGENTS.md itself.
func (l ProjectLayout) ImportFile(pack string) string {
	return strings.ReplaceAll(l.Import, "<pack>", pack)
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
	l.UserItems = slices.Clone(l.UserItems)
	for i := range l.UserItems {
		l.UserItems[i].Dirs = slices.Clone(l.UserItems[i].Dirs)
	}
	return l
}

// The project layout of each harness (decision 0041), from rulesync's
// project output and the project scope inventory of HARNESS-NOTES.
var (
	claudeProject = ProjectLayout{
		Paths: []ProjectPath{
			{From: ".claude/rules/", To: ".claude/rules/", Kind: ProjectRules},
			{From: ".claude/agents/", To: ".claude/agents/", Kind: ProjectAgents},
			{From: ".claude/skills/", To: ".claude/skills/", Kind: ProjectSkills},
			{From: ".claude/commands/", To: ".claude/commands/", Kind: ProjectCommands},
			// .claude/settings.json is the team's; the local file wins over it
			// and its allow rules apply without workspace trust.
			{From: ".claude/settings.json", To: ".claude/settings.local.json", Kind: ProjectSettings, ByKey: true,
				Each: []Members{{Path: []string{"env"}}, {Path: []string{"hooks"}, Item: ItemHook}, {Path: []string{"permissions"}}}},
		},
		// A project AGENTS.md alone did not load on 2.1.316; an import in a
		// rules file resolves from that file.
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
			{From: ".agents/skills/", To: ".agents/skills/", Kind: ProjectSkills},
			{From: ".agents/hooks.json", To: ".agents/hooks.json", Kind: ProjectHooks, ByKey: true,
				Each: []Members{{Path: []string{"rulesync"}, Item: ItemHook}}},
			{From: ".agents/mcp_config.json", To: ".agents/mcp_config.json", Kind: ProjectMCP, ByKey: true,
				Each: []Members{{Path: []string{"mcpServers"}, Item: ItemServer}}},
		},
		// agy has no project settings file, and rulesync renders no project
		// permissions for it.
		NoSlot: []ProjectKind{ProjectSettings},
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
			{From: ".opencode/skills/", To: ".opencode/skills/", Kind: ProjectSkills},
			{From: ".opencode/commands/", To: ".opencode/commands/", Kind: ProjectCommands},
			{From: ".opencode/plugins/", To: ".opencode/plugins/", Kind: ProjectHooks},
			// The root opencode.json(c) is the team's; .opencode/opencode.json
			// is read after it, and wins.
			{From: "opencode.jsonc", To: ".opencode/opencode.json", Kind: ProjectSettings, ByKey: true,
				Each: []Members{{Path: []string{"mcp", "servers"}, Item: ItemServer}}},
		},
		UserItems: []UserItems{
			{Kind: ProjectSkills, Dirs: []string{".config/opencode/skills", ".agents/skills", ".claude/skills", IsolatedDir + "/opencode/config/opencode/skills"}, ProjectWins: true},
			{Kind: ProjectAgents, Dirs: []string{".config/opencode/agents", IsolatedDir + "/opencode/config/opencode/agents"}, ProjectWins: true},
		},
		// OpenCode collects .claude and .agents of every directory too.
		AlsoReads: []string{".claude/skills/", ".agents/skills/"},
	}
)
