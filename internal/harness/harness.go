// Package harness describes each supported harness as data (decision
// 0028): its rulesync target, how far it can run isolated from the user's
// own configuration (decision 0018), and where its user scope installs in
// isolated mode (decision 0031). Detecting a harness and launching it
// isolated are logic, and come with the commands that need them; Command
// names the executable that dotai run launches (decision 0032).
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
// argument, Name=<dir>.
type Pointer struct {
	Mechanism Mechanism
	Name      string
	Path      string
	Make      bool // dotai run creates the directory, empty, when it is missing
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
// empty.
type Leak struct {
	Text string
	GOOS string
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
	Note   string // why Name is added, text for people
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

// LeaksOn is the text of each leak on goos, in order; never nil.
func (i Isolation) LeaksOn(goos string) []string {
	out := []string{}
	for _, l := range i.Leaks {
		if l.GOOS == "" || l.GOOS == goos {
			out = append(out, l.Text)
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
	// Settings is the harness's settings file in the user scope, relative to
	// Staging: dotai owns the keys it writes there, not the whole file,
	// since the harness writes into it too (decision 0035).
	Settings string
	// Command is the executable dotai run looks up on PATH and launches
	// isolated; "" while dotai run does not support the harness.
	Command string
	// BatchTarget is the executable, relative to the directory of a Windows
	// batch file found for Command, that the batch file runs, as npm
	// installs it; dotai run launches it instead when it is there.
	BatchTarget string
	Isolation   Isolation
}

var adapters = []Adapter{
	{
		ID: "claudecode", Target: "claudecode", Dir: "claude", Staging: ".claude", Settings: "settings.json", Command: "claude",
		Isolation: Isolation{
			Level: Partial,
			// Decision 0018 verified the new login on Windows only, where the
			// credentials live in the configuration directory.
			Relogin: map[string]Relogin{"windows": ReloginYes},
			Leaks: []Leak{
				{Text: "~/.claude/CLAUDE.md still loads when the working directory is under the home directory"},
				{Text: "credentials live in the configuration directory, so you must log in again", GOOS: "windows"},
			},
			Pointers: []Pointer{{Mechanism: Env, Name: "CLAUDE_CONFIG_DIR"}},
		},
	},
	{
		ID: "antigravity-cli", Target: "antigravity-cli", Dir: "gemini", Staging: ".gemini", Settings: "antigravity-cli/settings.json", Command: "agy",
		// agy moves antigravity-cli/skills to config/skills on its first run
		// and then fails to link it back on Windows, so the skills install
		// where it reads them after the move.
		Moves: []Move{{From: "antigravity-cli/skills", To: "config/skills"}},
		Isolation: Isolation{
			Level:    Partial,
			Relogin:  map[string]Relogin{"": ReloginNo},
			Leaks:    []Leak{{Text: "authentication is shared through the OS keyring"}},
			Pointers: []Pointer{{Mechanism: Flag, Name: "--gemini_dir"}},
		},
	},
	{
		ID: "opencode", Target: "opencode", Dir: "opencode", Staging: ".config/opencode", Install: "config/opencode", Settings: "opencode.jsonc",
		Command: "opencode", BatchTarget: "node_modules/@opencode/cli/bin/opencode.exe",
		Isolation: Isolation{
			Level:   Partial,
			Relogin: map[string]Relogin{"": ReloginYes},
			Leaks: []Leak{
				{Text: "provider credentials do not carry over"},
				{Text: "AGENTS.md, .agents and .opencode still load from the working directory and the directories above it, the home directory included"},
				{Text: "~/.agents/skills is hidden through OPENCODE_TEST_HOME, which OpenCode does not document, so a later OpenCode may load it again"},
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
				// else is set; an empty directory hides it.
				// OpenCode's background service starts in it, so it must exist.
				{Mechanism: Env, Name: "OPENCODE_TEST_HOME", Path: "home", Make: true},
			},
			// Drops ~/.claude and the .claude directories above the working
			// directory.
			Vars: []Var{{Name: "OPENCODE_DISABLE_CLAUDE_CODE", Value: "1"}},
			// Checked on 2.0.30: the commands that take --standalone, and the
			// subcommands of its help.
			Flags: []AddedFlag{{
				Name: "--standalone",
				Commands: []string{"", "api", "auth list", "auth login", "mini", "models", "reload", "run",
					"session delete", "session export", "session import", "session list", "stats"},
				Subcommands: []string{"acp", "api", "auth", "auth export", "auth import", "auth list", "auth login", "auth logout", "auth switch",
					"debug", "mcp", "mini", "models", "pair", "plugin", "reload", "run", "serve", "service",
					"session", "session delete", "session export", "session import", "session list", "stats", "uninstall", "update", "upgrade"},
				Unless: []string{"--server"},
				Note:   "OpenCode runs a private server instead of attaching to a background service, which may be your own with your own configuration",
			}},
		},
	},
}

// SettingsSource is the slash path of the harness's settings file in the
// user-scope staging tree.
func (a Adapter) SettingsSource() string { return path.Join(a.Staging, a.Settings) }

// SettingsTarget is the slash path, relative to the home directory, of the
// harness's settings file in its isolated install.
func (a Adapter) SettingsTarget() string { return path.Join(IsolatedDir, a.Dir, a.Install, a.Settings) }

// All returns every adapter, in a copy the caller may change.
func All() []Adapter {
	out := slices.Clone(adapters)
	for i := range out {
		out[i].Moves = slices.Clone(out[i].Moves)
		out[i].Isolation.Relogin = maps.Clone(out[i].Isolation.Relogin)
		out[i].Isolation.Leaks = slices.Clone(out[i].Isolation.Leaks)
		out[i].Isolation.Pointers = slices.Clone(out[i].Isolation.Pointers)
		out[i].Isolation.Vars = slices.Clone(out[i].Isolation.Vars)
		out[i].Isolation.Flags = slices.Clone(out[i].Isolation.Flags)
		for j := range out[i].Isolation.Flags {
			f := &out[i].Isolation.Flags[j]
			f.Commands, f.Subcommands, f.Unless = slices.Clone(f.Commands), slices.Clone(f.Subcommands), slices.Clone(f.Unless)
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
