// Package harness describes each supported harness as data (decision
// 0028): its rulesync target, how far it can run isolated from the user's
// own configuration (decision 0018), where its user scope installs in each
// mode (decisions 0031 and 0038), its own configuration, which the
// replace mode resets to a fresh install by keep-list (decision 0038), and
// where its project scope installs (decision 0041). Its
// helpers only read that data: the layout of a mode, whether an entry is
// kept, whether a name looks like a login or a history, and which
// relocation variables an environment sets. Detecting a harness and
// launching it are logic that reads the disk and PATH, and live with the
// commands that need them; Command names the executable that detection
// looks for and dotai run launches (decision 0032).
package harness

import (
	"fmt"
	"maps"
	"path"
	"slices"
	"strings"
)

// IsolatedDir is the directory, under the home directory, that holds one
// directory per harness for isolated installs (decision 0031).
const IsolatedDir = ".dotai/isolated"

// SharedSkillsDir is the user's shared skills folder, under the home
// directory, where other tools install skills (decision 0036).
const SharedSkillsDir = ".agents/skills"

// Level is how fully a harness runs apart from the user's configuration.
type Level string

// Isolation levels of decision 0018. A harness with None does not offer
// the isolated mode.
const (
	Full    Level = "full"
	Partial Level = "partial"
	None    Level = "none"
)

// Mechanism is how a harness is pointed at another directory.
type Mechanism string

// Mechanisms.
const (
	Env  Mechanism = "env"  // an environment variable
	Flag Mechanism = "flag" // a command-line flag
)

// Pointer is an environment variable or flag set to a directory of the
// harness's isolated install: Path, a slash path relative to Adapter.Dir,
// or Adapter.Dir itself when Path is empty. A flag is given in one
// argument, Name=<dir>. Skills limits it to the installs whose variant
// shares the user's skills, or to those whose variant does not.
type Pointer struct {
	Mechanism Mechanism
	Name      string
	Path      string
	Make      bool // dotai run creates the directory, empty, when it is missing
	Skills    Sharing
}

// Sharing is whether an item applies only to an install whose variant
// shares the user's skills folder, ~/.agents/skills (decision 0036), only
// to one whose variant does not, or, empty, to both.
type Sharing string

// Sharing values.
const (
	SkillsShared Sharing = "shared"
	SkillsClosed Sharing = "closed"
)

// applies reports whether an item limited to s applies to an install that
// shares skills or not.
func (s Sharing) applies(shared bool) bool {
	return s == "" || s == SkillsShared && shared || s == SkillsClosed && !shared
}

// Relogin is whether the user must log in again in the isolated mode.
type Relogin string

// Relogin values. Unknown is an OS where it was not verified.
const (
	ReloginYes     Relogin = "yes"
	ReloginNo      Relogin = "no"
	ReloginUnknown Relogin = "unknown"
)

// Leak is something the user's own setup still provides in the isolated
// mode, text for people, on the OS GOOS only, or on every OS when GOOS is
// empty, and for the installs Skills names. A leak with WalkUp comes from
// the directories above the working directory: WalkUp are the slash paths,
// relative to the home directory, that it reaches there, and Text holds
// one %s, which becomes those of them that apply, each as ~/<path>,
// joined with commas and a final "and" (see LeaksAt). Text must read
// whether that is one path or several, as in "these still load: %s".
type Leak struct {
	Text   string
	GOOS   string
	Skills Sharing
	WalkUp []string
}

// HomeInstructions are the instruction files of the real home directory
// that a harness finds by walking up from a working directory under it,
// past its configuration pointer, and that its settings can exclude: Paths
// are glob patterns relative to the home directory, with forward slashes,
// and Key is the key of the isolated settings file that lists absolute
// glob patterns of files to skip. An isolated install owns Key there and
// lists Paths under the home directory. Others names the files the walk-up
// still loads, text for people.
type HomeInstructions struct {
	Key    string
	Paths  []string
	Others string
}

// names are Paths as a leak names them: ~/<path>, without a trailing /**.
func (h HomeInstructions) names() string {
	var out []string
	for _, p := range h.Paths {
		out = append(out, "~/"+strings.TrimSuffix(p, "/**"))
	}
	return joinAnd(out)
}

// Leak is the leak of the walk-up, with the home directory's files
// excluded by Key or not.
func (h HomeInstructions) Leak(excluded bool) string {
	if excluded {
		return fmt.Sprintf("%s, but for your %s, which the isolated settings exclude with %s", h.Others, h.names(), h.Key)
	}
	return fmt.Sprintf("%s, and so do your %s: the isolated settings do not exclude them with %s, so run dotai install again", h.Others, h.names(), h.Key)
}

// Isolation is what decision 0018 records for a harness's isolated mode.
// Relogin is by GOOS, with "" for every OS it does not name; an OS that
// neither covers is unknown. Read them for an OS with ReloginOn and
// LeaksOn.
type Isolation struct {
	Level    Level
	Relogin  map[string]Relogin
	Leaks    []Leak // shown before installing and launching
	Pointers []Pointer
	Vars     []Var       // set by dotai run after the pointers
	Flags    []AddedFlag // added by dotai run to the user's arguments
	// Drop are variables of the user's environment that point the harness
	// at another configuration, or carry another session into it; dotai
	// run removes them and says so, with DropNote, why, text for people.
	Drop     []string
	DropNote string
	// Credentials are variables of the user's environment that log the
	// harness in, which dotai run passes on and names in a warning.
	Credentials []string
	// Instructions are the home directory's instruction files that the
	// isolated mode excludes, nil for a harness that does not need it.
	Instructions *HomeInstructions
}

// Launch is what dotai run knows of a launch, for LeaksAt: the OS, whether
// the install's variant shares skills, whether the working directory is
// the home directory or below it, which WalkUp paths exist under the home
// directory, and whether the isolated settings exclude the home
// directory's Instructions.
type Launch struct {
	GOOS      string
	Shared    bool
	UnderHome bool
	Exists    func(rel string) bool
	Excluded  bool
}

// Var is an environment variable that dotai run sets to a fixed value.
type Var struct {
	Name  string
	Value string
}

// AddedFlag is a flag that dotai run adds to the user's arguments when the
// command they run takes it, right after its subcommands. A command is a
// path of subcommand names separated by spaces, "" for the harness's
// default command.
type AddedFlag struct {
	Name     string
	Commands []string // the commands that take Name
	// Subcommands are the commands of the harness that start with a
	// subcommand, which tell a subcommand from an argument of the default
	// command, such as a directory.
	Subcommands []string
	// Unless are flags of the user's that take Name's place: with one of
	// them, or Name itself, Name is not added.
	Unless []string
	// Globals are the flags every command takes, which may come before a
	// subcommand, and whether each takes a value as the next argument.
	Globals map[string]bool
	Note    string // why Name is added, text for people
	// Warning is what may happen when the command is not one dotai knows,
	// so that Name is not added, text for people.
	Warning string
}

// ReloginOn is whether the user must log in again on goos.
func (i Isolation) ReloginOn(goos string) Relogin {
	if r, ok := i.Relogin[goos]; ok {
		return r
	}
	if r, ok := i.Relogin[""]; ok {
		return r
	}
	return ReloginUnknown
}

// LeaksOn is the text of each leak on goos, in order, for an install
// whose variant shares the user's skills or not, as an install shows them:
// the Instructions excluded, and each walk-up leak with all its paths.
// Never nil.
func (i Isolation) LeaksOn(goos string, shared bool) []string {
	return i.LeaksAt(Launch{GOOS: goos, Shared: shared, UnderHome: true, Exists: func(string) bool { return true }, Excluded: true})
}

// LeaksAt is the text of each leak for the launch l, in order: the leak of
// the Instructions, excluded or not, then each leak of the OS and the
// install, a walk-up leak left out unless the working directory is under
// the home directory and some of its WalkUp paths exist, and then naming
// only those. Never nil.
func (i Isolation) LeaksAt(l Launch) []string {
	out := []string{}
	if i.Instructions != nil {
		out = append(out, i.Instructions.Leak(l.Excluded))
	}
	for _, leak := range i.Leaks {
		if leak.GOOS != "" && leak.GOOS != l.GOOS || !leak.Skills.applies(l.Shared) {
			continue
		}
		if len(leak.WalkUp) == 0 {
			out = append(out, leak.Text)
			continue
		}
		var found []string
		for _, rel := range leak.WalkUp {
			if l.UnderHome && l.Exists(rel) {
				found = append(found, "~/"+rel)
			}
		}
		if len(found) > 0 {
			out = append(out, fmt.Sprintf(leak.Text, joinAnd(found)))
		}
	}
	return out
}

// joinAnd is items joined with commas and a final "and".
func joinAnd(items []string) string {
	if len(items) < 2 {
		return strings.Join(items, "")
	}
	return strings.Join(items[:len(items)-1], ", ") + " and " + items[len(items)-1]
}

// PointersFor is the pointers of an install whose variant shares the
// user's skills or not, in order; never nil.
func (i Isolation) PointersFor(shared bool) []Pointer {
	out := []Pointer{}
	for _, p := range i.Pointers {
		if p.Skills.applies(shared) {
			out = append(out, p)
		}
	}
	return out
}

// Move is a directory of a harness's staging tree that installs to another
// directory of its isolated install: From/<rest>, relative to
// Adapter.Staging, installs to To/<rest>, relative to Adapter.Install.
type Move struct {
	From string
	To   string
}

// Adapter is one harness. Staging and Install map the harness's files in
// the user-scope staging tree to its isolated install: Staging/<rest>
// installs to <home>/IsolatedDir/Dir/Install/<rest>, unless <rest> is in a
// directory of Moves. All paths are slash paths.
type Adapter struct {
	ID      string // the name in the manifest's harnesses
	Target  string // the rulesync target
	Dir     string // the harness's directory under IsolatedDir
	Staging string // where rulesync writes the harness's files, relative to the home directory
	Install string // where Staging installs, relative to Dir; "" is Dir itself
	Moves   []Move // directories of Staging that install elsewhere under Install
	// SharedSkills is the harness's skills directory in the user scope,
	// relative to Staging, where an install whose variant shares the user's
	// skills links each skill of ~/.agents/skills (decision 0036); "" for a
	// harness that reads ~/.agents/skills itself.
	SharedSkills string
	// CopySharedSkills copies each shared skill into SharedSkills instead
	// of linking it, for a harness that skips a link there when it looks
	// for skills.
	CopySharedSkills bool
	// Settings is the harness's settings file in the user scope, relative to
	// Staging: dotai owns the keys it writes there, not the whole file,
	// since the harness writes into it too (decision 0035).
	Settings string
	// Commands is how the harness reads a command in its settings, such as
	// a statusline, which decides how a tool command of decision 0037
	// names its script.
	Commands Quoting
	// Command is the executable dotai run looks up on PATH and launches
	// isolated; "" while dotai run does not support the harness.
	Command string
	// BatchTarget is the executable, relative to the directory of a Windows
	// batch file found for Command, that the batch file runs, as npm
	// installs it; dotai run launches it instead when it is there.
	BatchTarget string
	Isolation   Isolation
	// Real is the harness's own configuration, which the replace mode
	// resets and installs into (decision 0038).
	Real Real
	// Project is where the harness's project scope installs, local and
	// untracked (decision 0041).
	Project ProjectLayout
}

// Quoting is how a harness reads a command in its settings.
type Quoting string

// Quoting values.
const (
	// ShellQuoted is a command a shell parses: a path in double quotes
	// may hold spaces.
	ShellQuoted Quoting = "shell"
	// Unquoted is a command the harness splits at whitespace itself,
	// passing quotes on as part of the arguments: a path is written bare
	// and cannot hold whitespace.
	Unquoted Quoting = "unquoted"
)

// linkedSkills and copiedSkills are the leak of a harness into which an
// install whose variant shares skills links, or copies, the skills of
// ~/.agents/skills.
const (
	linkedSkills = "your skills in ~/.agents/skills are linked into the isolated install, as the variant shares them; one added there shows up after the next install"
	copiedSkills = "your skills in ~/.agents/skills are copied into the isolated install, as the variant shares them; a skill added or changed there shows up after the next install"
)

// walkUp is the Text of OpenCode's walk-up leaks.
const walkUp = "AGENTS.md, .agents and .opencode load from the working directory and every directory above it, so under your home directory these still load: %s"

var adapters = []Adapter{
	{
		ID: "claudecode", Target: "claudecode", Dir: "claude", Staging: ".claude", Settings: "settings.json", Command: "claude",
		SharedSkills: "skills",
		Commands:     ShellQuoted,
		Real:         claudeReal,
		Project:      claudeProject,
		Isolation: Isolation{
			Level: Partial,
			// Decision 0018 verified the new login on Windows only, where the
			// credentials live in the configuration directory.
			Relogin: map[string]Relogin{"windows": ReloginYes},
			// Claude Code walks up from the working directory for its
			// instructions, past CLAUDE_CONFIG_DIR, and claudeMdExcludes
			// skips them by absolute path (HARNESS-NOTES).
			Instructions: &HomeInstructions{Key: "claudeMdExcludes", Paths: []string{"CLAUDE.md", "CLAUDE.local.md", ".claude/CLAUDE.md", ".claude/rules/**"},
				Others: "the CLAUDE.md, .claude/CLAUDE.md and CLAUDE.local.md of each directory above the working directory still load"},
			Leaks: []Leak{
				{Text: "credentials live in the configuration directory, so you must log in again", GOOS: "windows"},
				{Text: linkedSkills, Skills: SkillsShared},
			},
			Pointers: []Pointer{{Mechanism: Env, Name: "CLAUDE_CONFIG_DIR"}},
			// From the environment variables reference
			// (https://code.claude.com/docs/en/env-vars, read 2026-11-04):
			// plugin directories, a shell setup sourced before each Bash
			// command, and the markers Claude Code sets in the processes it
			// spawns, which a session started from one would inherit.
			Drop: []string{"CLAUDE_CODE_PLUGIN_DIRS", "CLAUDE_CODE_PLUGIN_SEED_DIR", "CLAUDE_CODE_PLUGIN_CACHE_DIR", "CLAUDE_ENV_FILE",
				"CLAUDE_CODE_SESSION_ID", "CLAUDECODE", "CLAUDE_CODE_CHILD_SESSION"},
			DropNote: "each loads your own plugins or shell setup, or carries the Claude Code session dotai runs in, into the isolated one",
			// Each logs Claude Code in instead of the isolated login.
			Credentials: []string{"CLAUDE_CODE_OAUTH_TOKEN", "ANTHROPIC_API_KEY", "ANTHROPIC_AUTH_TOKEN"},
		},
	},
	{
		ID: "antigravity-cli", Target: "antigravity-cli", Dir: "gemini", Staging: ".gemini", Settings: "antigravity-cli/settings.json", Command: "agy",
		// agy moves antigravity-cli/skills to config/skills on its first run
		// and then fails to link it back on Windows, so the skills install
		// where it reads them after the move.
		Moves:        []Move{{From: "antigravity-cli/skills", To: "config/skills"}},
		SharedSkills: "antigravity-cli/skills",
		// agy skips a junction in its skills directory (HARNESS-NOTES).
		CopySharedSkills: true,
		// agy splits a statusline command itself and passes quotes on
		// (HARNESS-NOTES).
		Commands: Unquoted,
		Real:     agyReal,
		Project:  agyProject,
		Isolation: Isolation{
			Level: Partial,
			// Verified on Windows only.
			Relogin:  map[string]Relogin{"windows": ReloginNo},
			Leaks:    []Leak{{Text: "authentication is shared through the OS keyring"}, {Text: copiedSkills, Skills: SkillsShared}},
			Pointers: []Pointer{{Mechanism: Flag, Name: "--gemini_dir"}},
		},
	},
	{
		ID: "opencode", Target: "opencode", Dir: "opencode", Staging: ".config/opencode", Install: "config/opencode", Settings: "opencode.jsonc",
		Command: "opencode", BatchTarget: "node_modules/@opencode/cli/bin/opencode.exe",
		// Not checked: OpenCode has no statusline setting.
		Commands: ShellQuoted,
		Real:     openCodeReal,
		Project:  openCodeProject,
		Isolation: Isolation{
			Level: Partial,
			// Verified on Windows only.
			Relogin: map[string]Relogin{"windows": ReloginYes},
			Leaks: []Leak{
				{Text: "provider credentials do not carry over"},
				{Text: "OpenCode runs with your home directory, as the variant shares skills: your ~/.agents/skills loads, and so does anything else OpenCode reads there", Skills: SkillsShared},
				{Text: walkUp, Skills: SkillsClosed, WalkUp: []string{".agents/skills", ".opencode", "AGENTS.md"}},
				{Text: walkUp, Skills: SkillsShared, WalkUp: []string{".opencode", "AGENTS.md"}},
				{Text: "OPENCODE_TEST_HOME, which OpenCode does not document, hides ~/.agents/skills only outside your home directory, and a later OpenCode may drop it", Skills: SkillsClosed},
				{Text: "with --server, OpenCode reaches a server that runs with its own configuration"},
				{Text: "a command that does not take --standalone, such as debug config, starts a background service on the port of your own OpenCode service and leaves it running"},
				{Text: `the temporary directory %TEMP%\opencode is shared`, GOOS: "windows"},
			},
			Pointers: []Pointer{
				{Mechanism: Env, Name: "XDG_CONFIG_HOME", Path: "config"},
				{Mechanism: Env, Name: "XDG_DATA_HOME", Path: "data"},
				{Mechanism: Env, Name: "XDG_STATE_HOME", Path: "state"},
				{Mechanism: Env, Name: "XDG_CACHE_HOME", Path: "cache"},
				// os.homedir() for ~/.agents/skills, which loads whatever
				// else is set; an empty directory hides it. A variant that
				// shares skills keeps the real one (decision 0036).
				// OpenCode's background service starts in it, so it must exist.
				{Mechanism: Env, Name: "OPENCODE_TEST_HOME", Path: "home", Make: true, Skills: SkillsClosed},
			},
			// Drops ~/.claude and the .claude directories above the working
			// directory.
			Vars: []Var{{Name: "OPENCODE_DISABLE_CLAUDE_CODE", Value: "1"}},
			// Checked on 2.0.30: the commands that take --standalone, and the
			// subcommands of its help.
			Flags: []AddedFlag{{
				Name: "--standalone",
				Commands: []string{"", "api", "auth export", "auth import", "auth list", "auth login", "auth logout", "auth switch",
					"mini", "models", "reload", "run", "session delete", "session export", "session import", "session list", "stats"},
				Subcommands: []string{"acp", "api", "auth", "auth export", "auth import", "auth list", "auth login", "auth logout", "auth switch",
					"debug", "debug agents", "debug config", "debug paths", "mcp", "mcp add", "mcp auth", "mcp list", "mcp logout",
					"mini", "models", "pair", "plugin", "plugin add", "plugin check", "plugin list", "plugin remove", "plugin update",
					"reload", "run", "serve", "service", "service get", "service restart", "service set", "service start", "service status",
					"service stop", "service unset", "session", "session delete", "session export", "session import", "session list",
					"stats", "uninstall", "update", "upgrade"},
				Unless: []string{"--server"},
				Globals: map[string]bool{"--help": false, "-h": false, "--version": false, "-v": false, "--wizard": false,
					"--completions": true, "--log-level": true, "--print-logs": false},
				Note:    "OpenCode runs a private server instead of attaching to a background service, which may be your own with your own configuration",
				Warning: "OpenCode may attach to a background service that is already running with another configuration, such as your own",
			}},
			// Checked on 2.0.30: each points OpenCode at a configuration or a
			// database of its own.
			Drop:     []string{"OPENCODE_CONFIG", "OPENCODE_CONFIG_DIR", "OPENCODE_CONFIG_CONTENT", "OPENCODE_CLI_CONFIG_CONTENT", "OPENCODE_DB"},
			DropNote: "each points opencode at another configuration",
		},
	},
}

// SettingsSource is the slash path of the harness's settings file in the
// user-scope staging tree.
func (a Adapter) SettingsSource() string { return path.Join(a.Staging, a.Settings) }

// All returns every adapter, in a copy the caller may change.
func All() []Adapter {
	out := slices.Clone(adapters)
	for i := range out {
		out[i].Moves = slices.Clone(out[i].Moves)
		out[i].Real = out[i].Real.clone()
		out[i].Project = out[i].Project.clone()
		out[i].Isolation.Relogin = maps.Clone(out[i].Isolation.Relogin)
		out[i].Isolation.Leaks = slices.Clone(out[i].Isolation.Leaks)
		for j := range out[i].Isolation.Leaks {
			out[i].Isolation.Leaks[j].WalkUp = slices.Clone(out[i].Isolation.Leaks[j].WalkUp)
		}
		if h := out[i].Isolation.Instructions; h != nil {
			c := *h
			c.Paths = slices.Clone(h.Paths)
			out[i].Isolation.Instructions = &c
		}
		out[i].Isolation.Pointers = slices.Clone(out[i].Isolation.Pointers)
		out[i].Isolation.Vars = slices.Clone(out[i].Isolation.Vars)
		out[i].Isolation.Flags = slices.Clone(out[i].Isolation.Flags)
		out[i].Isolation.Drop = slices.Clone(out[i].Isolation.Drop)
		out[i].Isolation.Credentials = slices.Clone(out[i].Isolation.Credentials)
		for j := range out[i].Isolation.Flags {
			f := &out[i].Isolation.Flags[j]
			f.Commands, f.Subcommands, f.Unless = slices.Clone(f.Commands), slices.Clone(f.Subcommands), slices.Clone(f.Unless)
			f.Globals = maps.Clone(f.Globals)
		}
	}
	return out
}

// Lookup returns the adapters of ids, in their order. An id without an
// adapter is an error that names every such id.
func Lookup(ids []string) ([]Adapter, error) {
	all := All()
	var out []Adapter
	var missing []string
	for _, id := range ids {
		i := slices.IndexFunc(all, func(a Adapter) bool { return a.ID == id })
		if i < 0 {
			missing = append(missing, fmt.Sprintf("%q", id))
			continue
		}
		out = append(out, all[i])
	}
	if len(missing) > 0 {
		return nil, fmt.Errorf("no adapter for harness %s", strings.Join(missing, ", "))
	}
	return out, nil
}
