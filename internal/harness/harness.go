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
// or Adapter.Dir itself when Path is empty.
type Pointer struct {
	Mechanism Mechanism
	Name      string
	Path      string
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

// Adapter is one harness. Staging and Install map the harness's files in
// the user-scope staging tree to its isolated install: Staging/<rest>
// installs to <home>/IsolatedDir/Dir/Install/<rest>. All paths are slash
// paths.
type Adapter struct {
	ID      string // the name in the manifest's harnesses
	Target  string // the rulesync target
	Dir     string // the harness's directory under IsolatedDir
	Staging string // where rulesync writes the harness's files, relative to the home directory
	Install string // where Staging installs, relative to Dir; "" is Dir itself
	// Command is the executable dotai run looks up on PATH and launches
	// isolated; "" until dotai run supports the harness (roadmap PR 9).
	Command   string
	Isolation Isolation
}

var adapters = []Adapter{
	{
		ID: "claudecode", Target: "claudecode", Dir: "claude", Staging: ".claude", Command: "claude",
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
		ID: "antigravity-cli", Target: "antigravity-cli", Dir: "gemini", Staging: ".gemini",
		Isolation: Isolation{
			Level:    Partial,
			Relogin:  map[string]Relogin{"": ReloginNo},
			Leaks:    []Leak{{Text: "authentication is shared through the OS keyring"}},
			Pointers: []Pointer{{Mechanism: Flag, Name: "--gemini_dir"}},
		},
	},
	{
		ID: "opencode", Target: "opencode", Dir: "opencode", Staging: ".config/opencode", Install: "config/opencode",
		Isolation: Isolation{
			Level:   Partial,
			Relogin: map[string]Relogin{"": ReloginYes},
			Leaks: []Leak{
				{Text: "~/.agents/skills still loads"},
				{Text: "it needs its own service port while your own OpenCode service runs"},
				{Text: "provider credentials do not carry over"},
			},
			Pointers: []Pointer{
				{Mechanism: Env, Name: "XDG_CONFIG_HOME", Path: "config"},
				{Mechanism: Env, Name: "XDG_DATA_HOME", Path: "data"},
				{Mechanism: Env, Name: "XDG_STATE_HOME", Path: "state"},
				{Mechanism: Env, Name: "XDG_CACHE_HOME", Path: "cache"},
			},
		},
	},
}

// All returns every adapter, in a copy the caller may change.
func All() []Adapter {
	out := slices.Clone(adapters)
	for i := range out {
		out[i].Isolation.Relogin = maps.Clone(out[i].Isolation.Relogin)
		out[i].Isolation.Leaks = slices.Clone(out[i].Isolation.Leaks)
		out[i].Isolation.Pointers = slices.Clone(out[i].Isolation.Pointers)
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
