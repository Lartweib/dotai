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
	// Vendor are the directories, slash paths relative to the home
	// directory, whose contents a package manager or a plugin marketplace
	// put there: a replace wipes them with their entry without looking for
	// credentials inside. Any node_modules is one too.
	Vendor []string
	// Leaks are the paths outside Areas that the harness reads and that
	// are wiped whole.
	Leaks []LeakEntry
	// Split are the files that mix configuration and state, wiped by key.
	Split []SplitFile
	// Relocation are the variables that point the harness at another
	// configuration, which make a replace refuse; a trailing * is a
	// prefix.
	Relocation []string
	// Processes are the executable names that show the harness running,
	// without an extension such as .exe; a trailing * is a prefix. A
	// replace install and its restore refuse while one runs (decision
	// 0038).
	Processes []string
	// NodePackages are the npm packages whose path in a node process's
	// command line shows the harness running as node, as an npm install
	// runs it.
	NodePackages []string
	// LiveWritten are the files, slash paths relative to the home
	// directory, that the harness or a program beside it rewrites while it
	// runs, from its own copy in memory, so that a change dotai makes there
	// under a running harness may be lost, or the harness's write lost to
	// dotai's. A replace install that does not wipe the harness, and the
	// restore of one, refuse while it runs only when they write one of
	// these (decision 0053); everything else the harness reads when it
	// starts.
	LiveWritten []string
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

// sensitive are the parts of a name that look like a login, a secret or a
// history; sensitiveSuffixes the endings that do. A bare "key" is not one,
// since key bindings are configuration.
var (
	sensitive = []string{"cred", "auth", "token", "oauth", "secret", "password", "passwd", "history",
		"session", "conversation", "transcript", "cookie", "keyring"}
	sensitiveSuffixes = []string{".db", ".pem", ".key"}
	// The credential signals, the only ones that count inside a wiped
	// directory, whose files, such as a plugin's hooks and sources, often
	// have names like session-start.sh or auth.ts; and the names of
	// credential files that count there whole.
	credential      = []string{"cred", "oauth", "token", "secret", "password", "passwd", "cookie", "keyring"}
	credentialFiles = []string{"auth.json", "credentials.json", ".credentials.json"}
)

// LooksSensitive reports whether name, without case, looks like a login,
// a secret or a history: an entry of an area that no keep-list classifies,
// or a leak entry, with such a name makes a replace refuse until its
// adapter classifies it (decision 0038). Inside such an entry,
// LooksSensitiveInside applies instead.
func LooksSensitive(name string) bool {
	lower := strings.ToLower(name)
	return slices.ContainsFunc(sensitiveSuffixes, func(s string) bool { return strings.HasSuffix(lower, s) }) ||
		slices.ContainsFunc(sensitive, func(s string) bool { return strings.Contains(lower, s) })
}

// Relocated is the name of each variable of environ, sorted, that is set,
// not empty, and is one of r's relocation variables, by name compared
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
	slices.Sort(out)
	return out
}

// RunsAs reports whether name, the executable name of a running process,
// is one of the harness's Processes: compared without case and without a
// trailing .exe where fold is set, as on Windows.
func (r Real) RunsAs(name string, fold bool) bool {
	if fold {
		name = strings.ToLower(name)
		name = strings.TrimSuffix(name, ".exe")
	}
	return slices.ContainsFunc(r.Processes, func(p string) bool {
		if fold {
			p = strings.ToLower(p)
		}
		if prefix, ok := strings.CutSuffix(p, "*"); ok {
			return strings.HasPrefix(name, prefix)
		}
		return name == p
	})
}

// WritesLive reports whether rel, a slash path relative to the home
// directory, is one of the harness's LiveWritten files, compared without
// case where fold is set, as on Windows and macOS.
func (r Real) WritesLive(rel string, fold bool) bool {
	return slices.ContainsFunc(r.LiveWritten, func(p string) bool {
		if fold {
			return strings.EqualFold(p, rel)
		}
		return p == rel
	})
}

// RunsUnderNode reports whether args, the command line of a node process,
// runs one of the harness's NodePackages: it names the package's
// directory, with either separator.
func (r Real) RunsUnderNode(args string) bool {
	args = strings.ReplaceAll(args, `\`, "/")
	return slices.ContainsFunc(r.NodePackages, func(p string) bool { return strings.Contains(args, "/"+p+"/") })
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

// LooksSensitiveInside reports whether name, without case, the name of a
// file or directory inside an entry a replace wipes, looks like a
// credential: it holds a credential signal, ends in .db, .pem or .key, or
// is a credential file such as auth.json. Such a path makes a replace
// refuse, while a name that only mentions a session, a conversation, a
// history or auth, as plugin sources do, does not.
func LooksSensitiveInside(name string) bool {
	lower := strings.ToLower(name)
	return slices.Contains(credentialFiles, lower) ||
		slices.ContainsFunc(sensitiveSuffixes, func(s string) bool { return strings.HasSuffix(lower, s) }) ||
		slices.ContainsFunc(credential, func(s string) bool { return strings.Contains(lower, s) })
}

// Vendored reports whether rel, a slash path relative to the home
// directory, is a vendor tree of the harness: one of its Vendor
// directories or any node_modules, compared without case where fold is
// set, as on Windows and macOS, and exactly otherwise. Inside a vendor
// tree the names are those of published code, such as Tokenizer.js or
// secret.py, and a package manager or a marketplace puts them back, so a
// replace wipes the tree with its entry without looking inside it for
// credentials (decision 0038).
func (r Real) Vendored(rel string, fold bool) bool {
	same := func(a, b string) bool { return a == b }
	if fold {
		same = strings.EqualFold
	}
	rel = path.Clean(rel)
	return same(path.Base(rel), "node_modules") ||
		slices.ContainsFunc(r.Vendor, func(v string) bool { return same(v, rel) })
}

// ReplaceRelogin is whether the user must log in again after a replace
// install: no, since it keeps each harness's login (decision 0038).
const ReplaceRelogin = ReloginNo

// SettingsTarget is the slash path, relative to the home directory, of the
// harness's settings file in mode m.
func (a Adapter) SettingsTarget(m Mode) string {
	return path.Join(a.InstallDir(m), a.Settings)
}

// HooksTarget is the slash path, relative to the home directory, of the
// harness's Hooks file in mode m, "" for a harness without one.
func (a Adapter) HooksTarget(m Mode) string {
	if a.Hooks == "" {
		return ""
	}
	return path.Join(a.InstallDir(m), a.Hooks)
}

// HomeConfigTarget is the slash path, relative to the home directory, of
// the harness's HomeConfig in mode m: inside its isolated directory, or at
// the root of the home directory in the replace mode; "" for a harness
// without one.
func (a Adapter) HomeConfigTarget(m Mode) string {
	switch {
	case a.HomeConfig == "":
		return ""
	case m == Replace:
		return a.HomeConfig
	}
	return path.Join(IsolatedDir, a.Dir, a.HomeConfig)
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
			// Found on 2.1.330 (HARNESS-NOTES): the files of the running
			// sessions, the background sessions' supervisor, its jobs and
			// its log, which the docs list as state to keep, and two
			// entries the docs do not name.
			{Name: "sessions", Kind: KeepState},
			{Name: "daemon", Kind: KeepState},
			{Name: "jobs", Kind: KeepState},
			{Name: "daemon.log", Kind: KeepHistory},
			{Name: "transcripts", Kind: KeepHistory},
			{Name: "mcp-needs-auth-cache.json", Kind: KeepCache},
			// The rest of the application data the docs list.
			{Name: "agent-memory", Kind: KeepHistory},
			{Name: "stats-cache.json", Kind: KeepHistory},
			{Name: "usage-data", Kind: KeepHistory},
			{Name: "uploads", Kind: KeepHistory},
			{Name: "feedback", Kind: KeepHistory},
			{Name: "feedback-bundles", Kind: KeepHistory},
			{Name: "logs", Kind: KeepHistory},
			{Name: "tasks", Kind: KeepState},
			{Name: "seed-admin", Kind: KeepState},
			{Name: "image-cache", Kind: KeepCache},
			{Name: "remote-settings.json", Kind: KeepCache},
			{Name: "policy-limits.json", Kind: KeepCache},
			{Name: "policy-limits.json.stamp.json", Kind: KeepCache},
			// The quota snapshot a statusline writes: a runtime cache, not config.
			{Name: "rate_limits.json", Kind: KeepCache},
		}}},
		// Cloned marketplaces and the installed copies of plugins; a
		// plugin's own data, in plugins/data, is not one.
		Vendor: []string{".claude/plugins/marketplaces", ".claude/plugins/cache"},
		// Loaded as a project CLAUDE.md under the home directory.
		Leaks: []LeakEntry{{Path: "CLAUDE.md", Collateral: "other tools that read ~/CLAUDE.md lose it until a restore"}},
		Split: []SplitFile{{
			Path: ".claude.json", Keys: []string{"mcpServers"}, Projects: "projects",
			ProjectKeys: []string{"mcpServers", "allowedTools", "enabledMcpjsonServers", "disabledMcpjsonServers", "mcpContextUris"},
		}},
		Relocation: []string{"CLAUDE_CONFIG_DIR"},
		// settings.json gets the keys Claude Code's own commands set, such as
		// theme and model (decision 0035); .claude.json is rewritten by every
		// session and by claude mcp (decision 0049, HARNESS-NOTES).
		LiveWritten: []string{".claude/settings.json", ".claude.json"},
		// The native install's executable; an npm install runs as node.
		Processes:    []string{"claude"},
		NodePackages: []string{"@anthropic-ai/claude-code"},
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
				// Found on 1.5 (HARNESS-NOTES).
				{Name: "history.jsonl", Kind: KeepHistory},
				// The quota snapshot a statusline writes (HARNESS-NOTES).
				{Name: "rate_limits.json", Kind: KeepCache},
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
		// The settings file the harness writes into (decision 0035), the hooks
		// file that programs register their own hooks in at each of their
		// starts (HARNESS-NOTES), and the config.json that agy's changelog
		// documents as holding the plugin enablement and the always-allow
		// rules, which it writes at runtime: not written by dotai.
		LiveWritten: []string{".gemini/antigravity-cli/settings.json", ".gemini/config/hooks.json", ".gemini/config/config.json"},
		Processes:   []string{"agy"},
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
		// The settings file the harness writes into (decision 0035); its
		// plugins, agents and skills are read when it starts.
		LiveWritten: []string{".config/opencode/opencode.jsonc"},
		// The background service runs from a copy of the binary in the
		// cache, opencode-service-<pid>-<hash> (HARNESS-NOTES).
		Processes:    []string{"opencode", "opencode-service-*"},
		NodePackages: []string{"opencode-ai"},
	}
)

// clone is r in a copy the caller may change.
func (r Real) clone() Real {
	r.Moves, r.Leaks, r.Relocation = slices.Clone(r.Moves), slices.Clone(r.Leaks), slices.Clone(r.Relocation)
	r.Vendor = slices.Clone(r.Vendor)
	r.Processes, r.NodePackages = slices.Clone(r.Processes), slices.Clone(r.NodePackages)
	r.LiveWritten = slices.Clone(r.LiveWritten)
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
