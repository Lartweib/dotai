package harness_test

import (
	"reflect"
	"slices"
	"testing"

	"github.com/lartweib/dotai/internal/harness"
)

func adapter(t *testing.T, id string) harness.Adapter {
	t.Helper()
	a, err := harness.Lookup([]string{id})
	if err != nil {
		t.Fatal(err)
	}
	return a[0]
}

// TestLayouts checks where each harness's user scope installs, and the
// directory its writes are confined to, in each mode: under the isolated
// root (decision 0031), or in the harness's own configuration directory
// (decision 0038), where agy's rules file moves into config/.
func TestLayouts(t *testing.T) {
	want := map[string]map[harness.Mode][3]string{ // install, root, settings
		"claudecode": {
			harness.Isolated: {".dotai/isolated/claude", ".dotai/isolated/claude", ".dotai/isolated/claude/settings.json"},
			harness.Replace:  {".claude", ".claude", ".claude/settings.json"},
		},
		"antigravity-cli": {
			harness.Isolated: {".dotai/isolated/gemini", ".dotai/isolated/gemini", ".dotai/isolated/gemini/antigravity-cli/settings.json"},
			harness.Replace:  {".gemini", ".gemini", ".gemini/antigravity-cli/settings.json"},
		},
		"opencode": {
			harness.Isolated: {".dotai/isolated/opencode/config/opencode", ".dotai/isolated/opencode", ".dotai/isolated/opencode/config/opencode/opencode.jsonc"},
			harness.Replace:  {".config/opencode", ".config/opencode", ".config/opencode/opencode.jsonc"},
		},
	}
	for _, a := range harness.All() {
		for _, m := range []harness.Mode{harness.Isolated, harness.Replace} {
			if got := [3]string{a.InstallDir(m), a.Root(m), a.SettingsTarget(m)}; got != want[a.ID][m] {
				t.Errorf("%s in %s: install, root, settings = %q, want %q", a.ID, m, got, want[a.ID][m])
			}
		}
	}
	// The settings file, whose keys dotai owns rather than the whole file
	// (decision 0035), in the staging tree.
	sources := map[string]string{
		"claudecode":      ".claude/settings.json",
		"antigravity-cli": ".gemini/antigravity-cli/settings.json",
		"opencode":        ".config/opencode/opencode.jsonc",
	}
	for _, a := range harness.All() {
		if got := a.SettingsSource(); got != sources[a.ID] {
			t.Errorf("%s: staged settings = %q, want %q", a.ID, got, sources[a.ID])
		}
	}
	agy := adapter(t, "antigravity-cli")
	if got := agy.MovesIn(harness.Isolated); !reflect.DeepEqual(got, []harness.Move{{From: "antigravity-cli/skills", To: "config/skills"}}) {
		t.Errorf("agy's isolated moves = %+v, want only the skills", got)
	}
	want2 := []harness.Move{{From: "antigravity-cli/skills", To: "config/skills"}, {From: "GEMINI.md", To: "config/GEMINI.md"}}
	if got := agy.MovesIn(harness.Replace); !reflect.DeepEqual(got, want2) {
		t.Errorf("agy's replace moves = %+v, want the skills and the rules file in config/, so that Gemini CLI gets nothing new", got)
	}
	for _, id := range []string{"claudecode", "opencode"} {
		if got := adapter(t, id).MovesIn(harness.Replace); len(got) != 0 {
			t.Errorf("%s's replace moves = %+v, want none", id, got)
		}
	}
}

// TestDetectionData checks the configuration directory detection looks
// for, and that only agy needs its binary: Gemini CLI creates ~/.gemini
// too (decision 0038).
func TestDetectionData(t *testing.T) {
	want := map[string]struct {
		command, config string
		needsBinary     bool
	}{
		"claudecode":      {"claude", ".claude", false},
		"antigravity-cli": {"agy", ".gemini", true},
		"opencode":        {"opencode", ".config/opencode", false},
	}
	for _, a := range harness.All() {
		w := want[a.ID]
		if a.Command != w.command || a.Real.Dir != w.config || a.Real.NeedsBinary != w.needsBinary {
			t.Errorf("%s: command %q, config %q, needs binary %v, want %+v", a.ID, a.Command, a.Real.Dir, a.Real.NeedsBinary, w)
		}
	}
}

// TestKeepLists classifies the entries of each harness directory, as
// decision 0038 and the replace inventory of HARNESS-NOTES record them:
// login, history, caches and the state the harness needs are kept,
// everything else is wiped, unknown entries included.
func TestKeepLists(t *testing.T) {
	tests := []struct {
		id, area, name string
		shared         bool
		want           harness.KeepKind // "" when it is wiped
	}{
		{"claudecode", ".claude", ".credentials.json", false, harness.KeepLogin},
		{"claudecode", ".claude", "history.jsonl", false, harness.KeepHistory},
		{"claudecode", ".claude", "projects", false, harness.KeepHistory},
		{"claudecode", ".claude", "file-history", false, harness.KeepHistory},
		{"claudecode", ".claude", "backups", false, harness.KeepState},
		{"claudecode", ".claude", "cache", false, harness.KeepCache},
		{"claudecode", ".claude", "statsig", false, harness.KeepCache},
		{"claudecode", ".claude", "todos", false, harness.KeepState},
		{"claudecode", ".claude", "shell-snapshots", false, harness.KeepState},
		// Found on Claude Code 2.1.330: the running sessions, the background
		// sessions' supervisor and its log, an MCP cache, and transcripts.
		{"claudecode", ".claude", "sessions", false, harness.KeepState},
		{"claudecode", ".claude", "daemon", false, harness.KeepState},
		{"claudecode", ".claude", "daemon.log", false, harness.KeepHistory},
		{"claudecode", ".claude", "jobs", false, harness.KeepState},
		{"claudecode", ".claude", "transcripts", false, harness.KeepHistory},
		{"claudecode", ".claude", "mcp-needs-auth-cache.json", false, harness.KeepCache},
		// The application data the Claude Code docs list.
		{"claudecode", ".claude", "agent-memory", false, harness.KeepHistory},
		{"claudecode", ".claude", "stats-cache.json", false, harness.KeepHistory},
		{"claudecode", ".claude", "tasks", false, harness.KeepState},
		{"claudecode", ".claude", "uploads", false, harness.KeepHistory},
		{"claudecode", ".claude", "usage-data", false, harness.KeepHistory},
		{"claudecode", ".claude", "feedback", false, harness.KeepHistory},
		{"claudecode", ".claude", "feedback-bundles", false, harness.KeepHistory},
		{"claudecode", ".claude", "logs", false, harness.KeepHistory},
		{"claudecode", ".claude", "image-cache", false, harness.KeepCache},
		{"claudecode", ".claude", "remote-settings.json", false, harness.KeepCache},
		{"claudecode", ".claude", "policy-limits.json", false, harness.KeepCache},
		{"claudecode", ".claude", "policy-limits.json.stamp.json", false, harness.KeepCache},
		{"claudecode", ".claude", "seed-admin", false, harness.KeepState},
		{"claudecode", ".claude", "dev-mods", false, ""}, // mods, which a fresh install has none of
		{"claudecode", ".claude", "themes", false, ""},
		{"claudecode", ".claude", "settings.json", false, ""},
		{"claudecode", ".claude", "settings.local.json", false, ""},
		{"claudecode", ".claude", "CLAUDE.md", false, ""},
		{"claudecode", ".claude", "agents", false, ""},
		{"claudecode", ".claude", "commands", false, ""},
		{"claudecode", ".claude", "skills", false, ""},
		{"claudecode", ".claude", "hooks", false, ""},
		{"claudecode", ".claude", "output-styles", false, ""},
		{"claudecode", ".claude", "keybindings.json", false, ""},
		// plugins/synced/ goes with plugins/: a fresh install syncs it again.
		{"claudecode", ".claude", "plugins", false, ""},
		{"claudecode", ".claude", "something-new", false, ""},
		// Kept whatever its case, since a Windows file system ignores it.
		{"claudecode", ".claude", "Projects", false, harness.KeepHistory},

		{"antigravity-cli", ".gemini/config", ".migrated", false, harness.KeepState},
		{"antigravity-cli", ".gemini/config", "mcp_config.json", false, ""},
		{"antigravity-cli", ".gemini/config", "projects", false, ""}, // permission grants
		{"antigravity-cli", ".gemini/config", "skills", false, ""},
		{"antigravity-cli", ".gemini/config", "GEMINI.md", false, ""},
		{"antigravity-cli", ".gemini/antigravity-cli", "conversations", false, harness.KeepHistory},
		{"antigravity-cli", ".gemini/antigravity-cli", "conversation_summaries.db", false, harness.KeepHistory},
		{"antigravity-cli", ".gemini/antigravity-cli", "conversation_summaries.db-wal", false, harness.KeepHistory},
		{"antigravity-cli", ".gemini/antigravity-cli", "brain", false, harness.KeepHistory},
		{"antigravity-cli", ".gemini/antigravity-cli", "log", false, harness.KeepHistory},
		{"antigravity-cli", ".gemini/antigravity-cli", "history.jsonl", false, harness.KeepHistory},
		{"antigravity-cli", ".gemini/antigravity-cli", "cache", false, harness.KeepCache},
		{"antigravity-cli", ".gemini/antigravity-cli", "builtin", false, harness.KeepState},
		{"antigravity-cli", ".gemini/antigravity-cli", "installation_id", false, harness.KeepState},
		{"antigravity-cli", ".gemini/antigravity-cli", "updater", false, harness.KeepState},
		{"antigravity-cli", ".gemini/antigravity-cli", "settings.json", false, ""},
		{"antigravity-cli", ".gemini/antigravity-cli", "skills", false, ""},

		{"opencode", ".config/opencode", "service.json", false, harness.KeepState},
		{"opencode", ".config/opencode", "opencode.jsonc", false, ""},
		{"opencode", ".config/opencode", "AGENTS.md", false, ""},
		{"opencode", ".config/opencode", "node_modules", false, ""},
		// ~/.agents/skills stays only for a variant that shares it (decision 0036).
		{"opencode", ".agents", "skills", true, harness.KeepShared},
		{"opencode", ".agents", "skills", false, ""},
		{"opencode", ".agents", "rules", true, ""},
	}
	for _, tt := range tests {
		a := adapter(t, tt.id)
		i := slices.IndexFunc(a.Real.Areas, func(ar harness.Area) bool { return ar.Dir == tt.area })
		if i < 0 {
			t.Errorf("%s has no area %s", tt.id, tt.area)
			continue
		}
		got, kept := a.Real.Areas[i].Kept(tt.name, tt.shared)
		if kept != (tt.want != "") || got != tt.want {
			t.Errorf("%s: %s/%s (shared %v) = %q, %v, want %q", tt.id, tt.area, tt.name, tt.shared, got, kept, tt.want)
		}
	}
}

// TestAreasAndLeaks checks the directories each adapter wipes by
// keep-list, the paths outside them it wipes as explicit leak entries,
// with the warning for other tools that read them, and that the files
// only Gemini CLI or the Antigravity IDE read are in neither.
func TestAreasAndLeaks(t *testing.T) {
	want := map[string]struct{ areas, leaks []string }{
		"claudecode":      {[]string{".claude"}, []string{"CLAUDE.md"}},
		"antigravity-cli": {[]string{".gemini/config", ".gemini/antigravity-cli"}, []string{".gemini/GEMINI.md", ".gemini/AGENTS.md"}},
		"opencode":        {[]string{".config/opencode", ".agents"}, []string{"AGENTS.md", ".opencode"}},
	}
	geminiOnly := []string{".gemini", ".gemini/settings.json", ".gemini/oauth_creds.json", ".gemini/google_accounts.json", ".gemini/commands", ".gemini/extensions", ".gemini/antigravity"}
	for _, a := range harness.All() {
		var areas, leaks []string
		for _, ar := range a.Real.Areas {
			areas = append(areas, ar.Dir)
		}
		for _, l := range a.Real.Leaks {
			leaks = append(leaks, l.Path)
			if l.Collateral == "" {
				t.Errorf("%s: the leak entry %s names no tool it affects", a.ID, l.Path)
			}
		}
		if !reflect.DeepEqual(areas, want[a.ID].areas) || !reflect.DeepEqual(leaks, want[a.ID].leaks) {
			t.Errorf("%s: areas %v, leaks %v, want %+v", a.ID, areas, leaks, want[a.ID])
		}
		for _, p := range append(areas, leaks...) {
			if slices.Contains(geminiOnly, p) {
				t.Errorf("%s wipes %s, which only Gemini CLI or the IDE reads", a.ID, p)
			}
		}
	}
	if agents := adapter(t, "opencode").Real.Areas[1]; agents.Collateral == "" {
		t.Error("~/.agents names no tool it affects")
	}
}

// TestSplitFiles checks the keys of ~/.claude.json a replace wipes: its
// configuration, never its account, onboarding, stats or trust.
func TestSplitFiles(t *testing.T) {
	want := []harness.SplitFile{{
		Path: ".claude.json", Keys: []string{"mcpServers"}, Projects: "projects",
		ProjectKeys: []string{"mcpServers", "allowedTools", "enabledMcpjsonServers", "disabledMcpjsonServers", "mcpContextUris"},
	}}
	if got := adapter(t, "claudecode").Real.Split; !reflect.DeepEqual(got, want) {
		t.Errorf("Claude Code's split files = %+v, want %+v", got, want)
	}
	for _, id := range []string{"antigravity-cli", "opencode"} {
		if got := adapter(t, id).Real.Split; len(got) != 0 {
			t.Errorf("%s splits %+v, want nothing", id, got)
		}
	}
}

// TestHomeConfig checks the file Claude Code keeps at the root of the home
// directory, which rulesync stages for its user-scope MCP servers: it
// installs inside the isolated directory, where CLAUDE_CONFIG_DIR moves
// it, and at the root of the home directory in the replace mode (decision
// 0049). No other harness has one.
func TestHomeConfig(t *testing.T) {
	claude := adapter(t, "claudecode")
	if claude.HomeConfig != ".claude.json" {
		t.Errorf("Claude Code's HomeConfig = %q, want .claude.json", claude.HomeConfig)
	}
	for m, want := range map[harness.Mode]string{harness.Isolated: ".dotai/isolated/claude/.claude.json", harness.Replace: ".claude.json"} {
		if got := claude.HomeConfigTarget(m); got != want {
			t.Errorf("HomeConfigTarget(%s) = %q, want %q", m, got, want)
		}
	}
	// The replace mode splits the same file, so both must name it.
	if claude.HomeConfig != claude.Real.Split[0].Path {
		t.Errorf("HomeConfig %q is not the split file %q", claude.HomeConfig, claude.Real.Split[0].Path)
	}
	for _, id := range []string{"antigravity-cli", "opencode"} {
		if a := adapter(t, id); a.HomeConfig != "" || a.HomeConfigTarget(harness.Replace) != "" || a.HomeConfigTarget(harness.Isolated) != "" {
			t.Errorf("%s has the home file %q, want none", id, a.HomeConfig)
		}
	}
}

// TestLooksSensitive checks the safety net: an unknown entry whose name
// looks like a login or history is never wiped unclassified.
func TestLooksSensitive(t *testing.T) {
	for name, want := range map[string]bool{
		"credentials.json": true, "my-creds": true, "auth.json": true, "OAuth_backup": true, "token": true,
		"API_TOKEN.txt": true, "sessions.db": true, "History": true, "shell_history.txt": true,
		"client_secret.json": true, "password.txt": true, "passwd": true, "session-1.json": true, "conversation.log": true,
		"transcript.md": true, "Cookies": true, "keyring": true, "server.pem": true, "id.key": true,
		"settings.json": false, "agents": false, "dbtools": false, "notes.md": false,
		// A bare "key" is not one: key bindings are configuration.
		"keybindings.json": false, "keys.txt": false, "monkey": false,
	} {
		if got := harness.LooksSensitive(name); got != want {
			t.Errorf("LooksSensitive(%q) = %v, want %v", name, got, want)
		}
	}
}

// TestRelocated checks the variables that point a harness at another
// configuration, which make a replace refuse (decision 0038).
func TestRelocated(t *testing.T) {
	tests := []struct {
		id      string
		environ []string
		fold    bool
		want    []string
	}{
		{"claudecode", []string{"CLAUDE_CONFIG_DIR=/x", "PATH=/bin"}, false, []string{"CLAUDE_CONFIG_DIR"}},
		{"claudecode", []string{"CLAUDE_CONFIG_DIR="}, false, nil}, // empty is unset
		{"claudecode", []string{"claude_config_dir=/x"}, false, nil},
		{"claudecode", []string{"claude_config_dir=/x"}, true, []string{"claude_config_dir"}},
		{"claudecode", []string{"XDG_CONFIG_HOME=/x"}, false, nil},
		{"opencode", []string{"XDG_CONFIG_HOME=/x", "XDG_DATA_HOME=/y", "XDG_STATE_HOME=/z", "XDG_CACHE_HOME=/w"}, false,
			[]string{"XDG_CACHE_HOME", "XDG_CONFIG_HOME", "XDG_DATA_HOME", "XDG_STATE_HOME"}},
		// Desktop sessions set these; they move no configuration.
		{"opencode", []string{"XDG_RUNTIME_DIR=/run/user/1000", "XDG_SESSION_TYPE=x11", "XDG_DATA_DIRS=/usr/share"}, false, nil},
		{"opencode", []string{"OPENCODE_DB=/c", "OPENCODE_CONFIG_DIR=/b", "OPENCODE_CONFIG=/a", "OPENCODE_CONFIG_CONTENT={}", "OPENCODE_DISABLE_CLAUDE_CODE=1"}, false,
			[]string{"OPENCODE_CONFIG", "OPENCODE_CONFIG_CONTENT", "OPENCODE_CONFIG_DIR", "OPENCODE_DB"}}, // sorted, whatever the environment's order
		{"antigravity-cli", []string{"CLAUDE_CONFIG_DIR=/x", "XDG_CONFIG_HOME=/y", "ANTIGRAVITY_GEMINI_DIR=/z"}, false, nil},
	}
	for _, tt := range tests {
		if got := adapter(t, tt.id).Real.Relocated(tt.environ, tt.fold); !reflect.DeepEqual(got, tt.want) {
			t.Errorf("%s: Relocated(%v, fold %v) = %v, want %v", tt.id, tt.environ, tt.fold, got, tt.want)
		}
	}
}

// TestLooksSensitiveInside checks the narrower safety net for the files
// inside a directory a replace wipes, such as a plugin's sources and
// hooks: only credential signals count there.
func TestLooksSensitiveInside(t *testing.T) {
	for name, want := range map[string]bool{
		"credentials.json": true, ".Credentials.json": true, "AUTH.JSON": true, "my-creds": true, "oauth.ts": true,
		"api_token.txt": true, "client_secret.json": true, "password.txt": true, "passwd": true, "Cookies": true,
		"keyring": true, "server.pem": true, "id.key": true, "cache.db": true,
		"auth.ts": false, "session-start.sh": false, "conversation.md": false, "transcript.md": false,
		"history.ts": false, "keybindings.json": false, "notes.md": false,
	} {
		if got := harness.LooksSensitiveInside(name); got != want {
			t.Errorf("LooksSensitiveInside(%q) = %v, want %v", name, got, want)
		}
	}
}

// TestVendored checks the vendor trees of each harness, whose contents a
// package manager or a plugin marketplace put there and which a replace
// wipes without looking for credentials inside: any node_modules, and
// Claude Code's plugin marketplaces and cache, compared without case and
// with either separator, as on Windows. A plugin's data directory, which
// the plugin itself writes, is not one.
func TestVendored(t *testing.T) {
	tests := []struct {
		id, rel string
		want    bool
	}{
		{"claudecode", ".claude/plugins/marketplaces", true},
		{"claudecode", ".claude/plugins/cache", true},
		{"claudecode", `.claude\Plugins\Marketplaces`, true},
		{"claudecode", `.claude\plugins\cache`, true},
		{"claudecode", ".claude/plugins/marketplaces/m/node_modules", true},
		{"claudecode", ".claude/skills/s/node_modules", true},
		{"claudecode", ".claude/plugins", false},
		{"claudecode", ".claude/plugins/data", false},
		{"claudecode", ".claude/plugins/data/p/cache", false},
		{"claudecode", ".claude/plugins/marketplaces-old", false},
		{"claudecode", ".claude/cache", false},
		{"opencode", ".config/opencode/node_modules", true},
		{"opencode", `.config\opencode\Node_Modules`, true},
		{"opencode", ".config/opencode/plugins", false},
		{"opencode", ".config/opencode/my_node_modules", false},
		{"opencode", ".claude/plugins/cache", false}, // Claude Code's, not OpenCode's
		{"antigravity-cli", ".gemini/config/plugins", false},
		{"antigravity-cli", ".gemini/config/plugins/p/node_modules", true},
	}
	for _, tt := range tests {
		if got := adapter(t, tt.id).Real.Vendored(tt.rel); got != tt.want {
			t.Errorf("%s: Vendored(%q) = %v, want %v", tt.id, tt.rel, got, tt.want)
		}
	}
}

// TestRunsAs checks the executable names that show each harness running:
// its command, and for OpenCode the copy of the binary its background
// service runs from, compared without case and .exe on Windows only.
func TestRunsAs(t *testing.T) {
	tests := []struct {
		id, name string
		fold     bool
		want     bool
	}{
		{"claudecode", "claude", false, true},
		{"claudecode", "claude.exe", true, true},
		{"claudecode", "Claude.EXE", true, true},
		{"claudecode", "Claude", false, false},
		{"claudecode", "claude.exe", false, false},
		{"claudecode", "claude-helper", false, false},
		{"claudecode", "node", false, false},
		{"antigravity-cli", "agy", false, true},
		{"antigravity-cli", "agy.exe", true, true},
		{"antigravity-cli", "gemini", false, false},
		{"opencode", "opencode", false, true},
		{"opencode", "opencode-service-1234-abcd", false, true},
		{"opencode", "opencode-service-1234-abcd.exe", true, true},
		{"opencode", "opencoder", false, false},
	}
	for _, tt := range tests {
		if got := adapter(t, tt.id).Real.RunsAs(tt.name, tt.fold); got != tt.want {
			t.Errorf("%s RunsAs(%q, fold %v) = %v, want %v", tt.id, tt.name, tt.fold, got, tt.want)
		}
	}
}

// TestRunsUnderNode checks the npm packages that show a harness running
// as node: Claude Code's and OpenCode's, found in node's command line.
func TestRunsUnderNode(t *testing.T) {
	tests := []struct {
		id, args string
		want     bool
	}{
		{"claudecode", "node /usr/lib/node_modules/@anthropic-ai/claude-code/cli.js --resume", true},
		{"claudecode", `C:\Program Files\nodejs\node.exe C:\Users\u\AppData\Roaming\npm\node_modules\@anthropic-ai\claude-code\cli.js`, true},
		{"claudecode", "node /srv/app/server.js", false},
		{"opencode", "node /usr/lib/node_modules/opencode-ai/bin/opencode", true},
		{"opencode", "node /usr/lib/node_modules/@anthropic-ai/claude-code/cli.js", false},
		{"antigravity-cli", "node /usr/lib/node_modules/@anthropic-ai/claude-code/cli.js", false},
	}
	for _, tt := range tests {
		if got := adapter(t, tt.id).Real.RunsUnderNode(tt.args); got != tt.want {
			t.Errorf("%s RunsUnderNode(%q) = %v, want %v", tt.id, tt.args, got, tt.want)
		}
	}
}
