package harness

import (
	"path"
	"slices"
	"strings"
)

// Mode is an install mode: where a harness's user scope installs.
type Mode string

// Install modes. Isolated installs under IsolatedDir (decision 0031);
// Replace resets the harness's own configuration to a fresh install and
// installs there (decision 0038).
const (
	Isolated Mode = "isolated"
	Replace  Mode = "replace"
)

// Real is a harness's own configuration at user level, which the replace
// mode resets to the state of a fresh install (decision 0038). Paths are
// slash paths relative to the home directory.
type Real struct {
	// Dir is the harness's configuration directory: detection looks for
	// it, and the replace mode installs Staging there.
	Dir string
	// Moves are directories or files of Staging that install elsewhere in
	// Dir in the replace mode, besides Adapter.Moves; a Move whose From is
	// a file moves that file only.
	Moves []Move
	// NeedsBinary is set when Dir alone does not show the harness is
	// installed, since another program creates it too.
	NeedsBinary bool
	// Areas are the directories wiped by keep-list: every entry not on
	// its keep-list goes, unknown entries included.
	Areas []Area
	// Leaks are the paths outside Areas that the harness reads and that
	// are wiped whole.
	Leaks []LeakEntry
	// Split are the files that mix configuration and state, wiped by key.
	Split []SplitFile
	// Relocation are the variables that point the harness at another
	// configuration, which make a replace refuse; a trailing * is a
	// prefix.
	Relocation []string
}

// KeepKind is why an entry of an Area is kept.
type KeepKind string

// The kinds of kept entries: login and credentials, history (sessions,
// conversations, logs), caches, the state the harness needs to work, and
// the user's shared skills folder for a variant that shares it (decision
// 0036).
const (
	KeepLogin   KeepKind = "login"
	KeepHistory KeepKind = "history"
	KeepCache   KeepKind = "cache"
	KeepState   KeepKind = "state"
	KeepShared  KeepKind = "shared"
)

// Keep is an entry of an Area that a replace keeps: Name, compared
// without case, only for the installs Skills names.
type Keep struct {
	Name   string
	Kind   KeepKind
	Skills Sharing
}

// Area is a directory wiped by keep-list. Collateral, when set, is what
// wiping one of its entries does to other tools, text for people.
type Area struct {
	Dir        string
	Keep       []Keep
	Collateral string
}

// Kept is the kind of the entry name of a, and whether a replace keeps it
// for an install whose variant shares the user's skills or not.
func (a Area) Kept(name string, shared bool) (KeepKind, bool) {
	for _, k := range a.Keep {
		if strings.EqualFold(k.Name, name) && k.Skills.applies(shared) {
			return k.Kind, true
		}
	}
	return "", false
}

// LeakEntry is a path outside the harness's Areas that it reads, wiped
// whole; Collateral is what that does to other tools, text for people.
type LeakEntry struct {
	Path       string
	Collateral string
}

// SplitFile is a file that holds configuration and state: a replace wipes
// its configuration Keys, top-level keys, and the ProjectKeys of each
// entry of its Projects object, and keeps everything else. dotai never
// owns such a file.
type SplitFile struct {
	Path        string
	Keys        []string
	Projects    string
	ProjectKeys []string
}

// sensitive are the parts of a name that look like a login or history.
var sensitive = []string{"cred", "auth", "token", "oauth", "history"}

// LooksSensitive reports whether name, an entry no keep-list classifies,
// looks like a login or history: such an entry makes a replace refuse
// until its adapter classifies it (decision 0038).
func LooksSensitive(name string) bool {
	lower := strings.ToLower(name)
	return strings.HasSuffix(lower, ".db") || slices.ContainsFunc(sensitive, func(s string) bool { return strings.Contains(lower, s) })
}

// Relocated is the name of each variable of environ, in order, that is
// set, not empty, and is one of r's relocation variables, by name compared
// without case when fold is set, as on Windows.
func (r Real) Relocated(environ []string, fold bool) []string {
	var out []string
	for _, kv := range environ {
		name, value, ok := strings.Cut(kv, "=")
		if !ok || value == "" {
			continue
		}
		key := name
		if fold {
			key = strings.ToUpper(name)
		}
		if slices.ContainsFunc(r.Relocation, func(v string) bool {
			if prefix, ok := strings.CutSuffix(v, "*"); ok {
				return strings.HasPrefix(key, prefix)
			}
			return key == v
		}) {
			out = append(out, name)
		}
	}
	return out
}

// InstallDir is where the harness's Staging installs in mode m, relative
// to the home directory.
func (a Adapter) InstallDir(m Mode) string {
	if m == Replace {
		return a.Real.Dir
	}
	return path.Join(IsolatedDir, a.Dir, a.Install)
}

// Root is the directory that every write for the harness in mode m goes
// through, relative to the home directory: its isolated directory, or its
// configuration directory.
func (a Adapter) Root(m Mode) string {
	if m == Replace {
		return a.Real.Dir
	}
	return path.Join(IsolatedDir, a.Dir)
}

// MovesIn are the moves of the harness's staging tree in mode m.
func (a Adapter) MovesIn(m Mode) []Move {
	if m == Replace {
		return append(slices.Clone(a.Moves), a.Real.Moves...)
	}
	return slices.Clone(a.Moves)
}

// SettingsTarget is the slash path, relative to the home directory, of the
// harness's settings file in mode m.
func (a Adapter) SettingsTarget(m Mode) string {
	return path.Join(a.InstallDir(m), a.Settings)
}

// The replace layout of each harness (decision 0038), from the replace
// inventory of HARNESS-NOTES. A keep-list errs on the side of keeping: an
// entry kept by mistake leaves the result less clean, one wiped by mistake
// loses what the harness needed.
var (
	claudeReal = Real{
		Dir: ".claude",
		Areas: []Area{{Dir: ".claude", Keep: []Keep{
			// On Windows and Linux the login lives in the configuration
			// directory (decision 0018).
			{Name: ".credentials.json", Kind: KeepLogin},
			// Verified in the inventory: sessions and memory, .claude.json
			// backups, and the model catalog.
			{Name: "projects", Kind: KeepHistory},
			{Name: "backups", Kind: KeepState},
			{Name: "cache", Kind: KeepCache},
			// Not in the inventory: the history file and the runtime state
			// and caches Claude Code 2.x keeps there.
			{Name: "history.jsonl", Kind: KeepHistory},
			{Name: "file-history", Kind: KeepHistory},
			{Name: "plans", Kind: KeepHistory},
			{Name: "debug", Kind: KeepHistory},
			{Name: "todos", Kind: KeepState},
			{Name: "shell-snapshots", Kind: KeepState},
			{Name: "session-env", Kind: KeepState},
			{Name: "ide", Kind: KeepState},
			{Name: "statsig", Kind: KeepCache},
			{Name: "paste-cache", Kind: KeepCache},
		}}},
		// Loaded as a project CLAUDE.md under the home directory.
		Leaks: []LeakEntry{{Path: "CLAUDE.md", Collateral: "other tools that read ~/CLAUDE.md lose it until a restore"}},
		Split: []SplitFile{{
			Path: ".claude.json", Keys: []string{"mcpServers"}, Projects: "projects",
			ProjectKeys: []string{"mcpServers", "allowedTools", "enabledMcpjsonServers", "disabledMcpjsonServers", "mcpContextUris"},
		}},
		Relocation: []string{"CLAUDE_CONFIG_DIR"},
	}
	agyReal = Real{
		Dir: ".gemini",
		// The pack's rules file goes to config/, which agy reads too, so
		// that Gemini CLI, which reads the root one, gets nothing new.
		Moves: []Move{{From: "GEMINI.md", To: "config/GEMINI.md"}},
		// Gemini CLI creates ~/.gemini too.
		NeedsBinary: true,
		Areas: []Area{
			{Dir: ".gemini/config", Keep: []Keep{{Name: ".migrated", Kind: KeepState}}},
			{Dir: ".gemini/antigravity-cli", Keep: []Keep{
				{Name: "annotations", Kind: KeepHistory},
				{Name: "brain", Kind: KeepHistory},
				{Name: "cli.log", Kind: KeepHistory},
				{Name: "conversation_summaries.db", Kind: KeepHistory},
				{Name: "conversation_summaries.db-shm", Kind: KeepHistory},
				{Name: "conversation_summaries.db-wal", Kind: KeepHistory},
				{Name: "conversations", Kind: KeepHistory},
				{Name: "crashes", Kind: KeepHistory},
				{Name: "log", Kind: KeepHistory},
				{Name: "cache", Kind: KeepCache},
				{Name: "bin", Kind: KeepState},
				{Name: "builtin", Kind: KeepState},
				{Name: "installation_id", Kind: KeepState},
				{Name: "jetski_state.pbtxt", Kind: KeepState},
				{Name: "last_check.timestamp", Kind: KeepState},
				{Name: "presence", Kind: KeepState},
				{Name: "updater", Kind: KeepState},
			}},
		},
		// agy reads both; so does Gemini CLI.
		Leaks: []LeakEntry{
			{Path: ".gemini/GEMINI.md", Collateral: "Gemini CLI reads it too, and loses it until a restore"},
			{Path: ".gemini/AGENTS.md", Collateral: "Gemini CLI reads it too, and loses it until a restore"},
		},
	}
	openCodeReal = Real{
		Dir: ".config/opencode",
		Areas: []Area{
			// service.json holds the background service's password.
			{Dir: ".config/opencode", Keep: []Keep{{Name: "service.json", Kind: KeepState}}},
			// Read by the walk-up from a working directory under the home
			// directory; the shared skills stay for a variant that shares
			// them (decision 0036).
			{Dir: ".agents", Keep: []Keep{{Name: "skills", Kind: KeepShared, Skills: SkillsShared}},
				Collateral: "other tools that read ~/.agents, such as those that install skills in ~/.agents/skills, lose it until a restore"},
		},
		Leaks: []LeakEntry{
			{Path: "AGENTS.md", Collateral: "other tools that read ~/AGENTS.md lose it until a restore"},
			{Path: ".opencode", Collateral: "other tools that read ~/.opencode lose it until a restore"},
		},
		// The XDG base directories OpenCode honors, and its own variables.
		Relocation: []string{"XDG_CONFIG_HOME", "XDG_DATA_HOME", "XDG_STATE_HOME", "XDG_CACHE_HOME", "OPENCODE_CONFIG*", "OPENCODE_DB"},
	}
)

// clone is r in a copy the caller may change.
func (r Real) clone() Real {
	r.Moves, r.Leaks, r.Relocation = slices.Clone(r.Moves), slices.Clone(r.Leaks), slices.Clone(r.Relocation)
	r.Areas = slices.Clone(r.Areas)
	for i := range r.Areas {
		r.Areas[i].Keep = slices.Clone(r.Areas[i].Keep)
	}
	r.Split = slices.Clone(r.Split)
	for i := range r.Split {
		r.Split[i].Keys, r.Split[i].ProjectKeys = slices.Clone(r.Split[i].Keys), slices.Clone(r.Split[i].ProjectKeys)
	}
	return r
}
