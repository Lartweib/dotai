package harness_test

import (
	"path"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/lartweib/dotai/internal/harness"
	"github.com/lartweib/dotai/internal/pack"
)

func TestLookup(t *testing.T) {
	tests := []struct {
		ids     []string
		want    []string // ids of the adapters returned
		wantErr string
	}{
		{ids: pack.Harnesses, want: pack.Harnesses},
		{ids: []string{"opencode", "claudecode"}, want: []string{"opencode", "claudecode"}},
		{ids: nil, want: nil},
		{ids: []string{"claudecode", "codex", "cursor"}, wantErr: `no adapter for harness "codex", "cursor"`},
	}
	for _, tt := range tests {
		t.Run(strings.Join(tt.ids, ","), func(t *testing.T) {
			got, err := harness.Lookup(tt.ids)
			if tt.wantErr != "" {
				if err == nil || err.Error() != tt.wantErr {
					t.Fatalf("Lookup() error = %v, want %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("Lookup() error = %v", err)
			}
			var ids []string
			for _, a := range got {
				ids = append(ids, a.ID)
			}
			if !reflect.DeepEqual(ids, tt.want) {
				t.Errorf("Lookup() = %v, want %v", ids, tt.want)
			}
		})
	}
}

// TestAdapters checks the data of every adapter against decisions 0018 and
// 0031.
func TestAdapters(t *testing.T) {
	want := map[string]struct {
		dir, staging, install string
		level                 harness.Level
		pointers              []harness.Pointer
	}{
		"claudecode":      {"claude", ".claude", "", harness.Partial, []harness.Pointer{{Mechanism: harness.Env, Name: "CLAUDE_CONFIG_DIR"}}},
		"antigravity-cli": {"gemini", ".gemini", "", harness.Partial, []harness.Pointer{{Mechanism: harness.Flag, Name: "--gemini_dir"}}},
		"opencode": {"opencode", ".config/opencode", "config/opencode", harness.Partial, []harness.Pointer{
			{Mechanism: harness.Env, Name: "XDG_CONFIG_HOME", Path: "config"},
			{Mechanism: harness.Env, Name: "XDG_DATA_HOME", Path: "data"},
			{Mechanism: harness.Env, Name: "XDG_STATE_HOME", Path: "state"},
			{Mechanism: harness.Env, Name: "XDG_CACHE_HOME", Path: "cache"},
			// An empty home, so that ~/.agents/skills does not load,
			// unless the variant shares skills (decision 0036).
			{Mechanism: harness.Env, Name: "OPENCODE_TEST_HOME", Path: "home", Make: true, Skills: harness.SkillsClosed},
		}},
	}
	all := harness.All()
	if len(all) != len(want) {
		t.Fatalf("All() has %d adapters, want %d", len(all), len(want))
	}
	for _, a := range all {
		t.Run(a.ID, func(t *testing.T) {
			w, ok := want[a.ID]
			if !ok {
				t.Fatalf("unexpected adapter %q", a.ID)
			}
			got := struct {
				dir, staging, install string
				level                 harness.Level
				pointers              []harness.Pointer
			}{a.Dir, a.Staging, a.Install, a.Isolation.Level, a.Isolation.Pointers}
			if !reflect.DeepEqual(got, w) {
				t.Errorf("adapter = %+v, want %+v", got, w)
			}
			if a.Target != a.ID {
				t.Errorf("Target = %q, want the harness id %q", a.Target, a.ID)
			}
			for _, goos := range []string{"windows", "linux", "darwin"} {
				for _, shared := range []bool{false, true} {
					if len(a.Isolation.LeaksOn(goos, shared)) == 0 {
						t.Errorf("a partial isolation names what leaks on %s, shared skills %t", goos, shared)
					}
				}
			}
			for _, p := range []string{a.Dir, a.Staging} {
				if !path.IsAbs("/"+p) || path.Clean(p) != p {
					t.Errorf("%q is not a clean relative slash path", p)
				}
			}
		})
	}
}

// TestAllIsACopy checks that a caller cannot change the adapters.
func TestAllIsACopy(t *testing.T) {
	a := harness.All()[0]
	a.Isolation.Leaks[0].Text = "changed"
	a.Isolation.Relogin["windows"] = harness.ReloginNo
	if b := harness.All()[0]; b.Isolation.Leaks[0].Text == "changed" || b.Isolation.Relogin["windows"] == harness.ReloginNo {
		t.Error("All() shares its data with the caller")
	}
}

// TestMoves checks the staged directories that install elsewhere in a
// harness's isolated directory: agy moves antigravity-cli/skills to
// config/skills on its first run, and cannot link it back on Windows.
func TestMoves(t *testing.T) {
	want := map[string][]harness.Move{"antigravity-cli": {{From: "antigravity-cli/skills", To: "config/skills"}}}
	for _, a := range harness.All() {
		if got := a.Moves; !reflect.DeepEqual(got, want[a.ID]) {
			t.Errorf("%s: Moves = %+v, want %+v", a.ID, got, want[a.ID])
		}
	}
}

// TestOpenCodeLaunch checks what dotai run adds to OpenCode's launch
// besides its pointers: the variable that drops ~/.claude and the .claude
// directories, and --standalone, so that OpenCode runs a private server
// instead of attaching to a background service that may be the user's own,
// with the user's configuration, for every command of 2.0.30 that takes it.
func TestOpenCodeLaunch(t *testing.T) {
	a, err := harness.Lookup([]string{"opencode"})
	if err != nil {
		t.Fatal(err)
	}
	if want := []harness.Var{{Name: "OPENCODE_DISABLE_CLAUDE_CODE", Value: "1"}}; !reflect.DeepEqual(a[0].Isolation.Vars, want) {
		t.Errorf("Vars = %+v, want %+v", a[0].Isolation.Vars, want)
	}
	if len(a[0].Isolation.Flags) != 1 {
		t.Fatalf("Flags = %+v, want --standalone only", a[0].Isolation.Flags)
	}
	f := a[0].Isolation.Flags[0]
	commands := []string{"", "api", "auth export", "auth import", "auth list", "auth login", "auth logout", "auth switch", "mini", "models", "reload", "run",
		"session delete", "session export", "session import", "session list", "stats"}
	if f.Name != "--standalone" || !reflect.DeepEqual(f.Commands, commands) || !reflect.DeepEqual(f.Unless, []string{"--server"}) || f.Note == "" || f.Warning == "" {
		t.Errorf("flag = %+v, want --standalone for %q unless --server, with a note and a warning", f, commands)
	}
	// The global flags of 2.0.30's help, and whether each takes a value.
	globals := map[string]bool{"--help": false, "-h": false, "--version": false, "-v": false, "--wizard": false,
		"--completions": true, "--log-level": true, "--print-logs": false}
	if !reflect.DeepEqual(f.Globals, globals) {
		t.Errorf("Globals = %v, want %v", f.Globals, globals)
	}
	for _, c := range []string{"debug paths", "mcp list", "plugin add", "service status", "auth switch"} {
		if !slices.Contains(f.Subcommands, c) {
			t.Errorf("subcommand %q is missing", c)
		}
	}
	// Variables that would point the isolated OpenCode at another
	// configuration or database.
	if want := []string{"OPENCODE_CONFIG", "OPENCODE_CONFIG_DIR", "OPENCODE_CONFIG_CONTENT", "OPENCODE_CLI_CONFIG_CONTENT", "OPENCODE_DB"}; !reflect.DeepEqual(a[0].Isolation.Drop, want) {
		t.Errorf("Drop = %q, want %q", a[0].Isolation.Drop, want)
	}
	for _, c := range f.Commands {
		if c != "" && !slices.Contains(f.Subcommands, c) {
			t.Errorf("command %q is not one of the subcommands", c)
		}
	}
	for _, c := range []string{"debug", "debug paths", "mcp", "serve", "upgrade", "update", "session", "auth"} {
		if slices.Contains(f.Commands, c) {
			t.Errorf("command %q does not take --standalone", c)
		}
	}
	if got := a[0].BatchTarget; got != "node_modules/@opencode/cli/bin/opencode.exe" {
		t.Errorf("BatchTarget = %q, want the executable npm's opencode.cmd runs", got)
	}
}

// TestCommands checks the executable dotai run launches for each harness:
// all three since roadmap PR 9.
func TestCommands(t *testing.T) {
	want := map[string]string{"claudecode": "claude", "antigravity-cli": "agy", "opencode": "opencode"}
	for _, a := range harness.All() {
		if got := a.Command; got != want[a.ID] {
			t.Errorf("%s: Command = %q, want %q", a.ID, got, want[a.ID])
		}
	}
}

// TestSettings checks where each harness keeps its settings file, whose
// keys dotai owns rather than the whole file (decision 0035): in the
// staging tree and in its isolated install, relative to the home
// directory.
func TestSettings(t *testing.T) {
	want := map[string][2]string{
		"claudecode":      {".claude/settings.json", ".dotai/isolated/claude/settings.json"},
		"antigravity-cli": {".gemini/antigravity-cli/settings.json", ".dotai/isolated/gemini/antigravity-cli/settings.json"},
		"opencode":        {".config/opencode/opencode.jsonc", ".dotai/isolated/opencode/config/opencode/opencode.jsonc"},
	}
	for _, a := range harness.All() {
		if got := [2]string{a.SettingsSource(), a.SettingsTarget(harness.Isolated)}; got != want[a.ID] {
			t.Errorf("%s: settings = %q, want %q", a.ID, got, want[a.ID])
		}
	}
}

// TestIsolationPerOS checks the login and the leaks of each harness by OS:
// decision 0018 verified Claude Code's new login on Windows only, where
// its credentials live in the configuration directory, and the logins of
// agy and OpenCode were checked on Windows only too.
func TestIsolationPerOS(t *testing.T) {
	openCodeLeaks := []string{
		"provider credentials do not carry over",
		"AGENTS.md, .agents and .opencode load from the working directory and every directory above it, so under your home directory these still load: ~/.agents/skills, ~/.opencode and ~/AGENTS.md",
		"OPENCODE_TEST_HOME, which OpenCode does not document, hides ~/.agents/skills only outside your home directory, and a later OpenCode may drop it",
		"with --server, OpenCode reaches a server that runs with its own configuration",
		"a command that does not take --standalone, such as debug config, starts a background service on the port of your own OpenCode service and leaves it running",
	}
	claudeMD := "the CLAUDE.md, .claude/CLAUDE.md and CLAUDE.local.md of each directory above the working directory still load, but for your ~/CLAUDE.md, ~/CLAUDE.local.md, ~/.claude/CLAUDE.md and ~/.claude/rules, which the isolated settings exclude with claudeMdExcludes"
	credentials := "credentials live in the configuration directory, so you must log in again"
	tests := []struct {
		id, goos string
		relogin  harness.Relogin
		leaks    []string
	}{
		{"claudecode", "windows", harness.ReloginYes, []string{claudeMD, credentials}},
		{"claudecode", "linux", harness.ReloginUnknown, []string{claudeMD}},
		{"claudecode", "darwin", harness.ReloginUnknown, []string{claudeMD}},
		{"antigravity-cli", "windows", harness.ReloginNo, []string{"authentication is shared through the OS keyring"}},
		{"antigravity-cli", "linux", harness.ReloginUnknown, []string{"authentication is shared through the OS keyring"}},
		{"opencode", "darwin", harness.ReloginUnknown, openCodeLeaks},
		{"opencode", "linux", harness.ReloginUnknown, openCodeLeaks},
		{"opencode", "windows", harness.ReloginYes, append(slices.Clone(openCodeLeaks), `the temporary directory %TEMP%\opencode is shared`)},
	}
	for _, tt := range tests {
		t.Run(tt.id+"/"+tt.goos, func(t *testing.T) {
			a, err := harness.Lookup([]string{tt.id})
			if err != nil {
				t.Fatal(err)
			}
			if got := a[0].Isolation.ReloginOn(tt.goos); got != tt.relogin {
				t.Errorf("ReloginOn(%s) = %q, want %q", tt.goos, got, tt.relogin)
			}
			if got := a[0].Isolation.LeaksOn(tt.goos, false); !reflect.DeepEqual(got, tt.leaks) {
				t.Errorf("LeaksOn(%s) = %q, want %q", tt.goos, got, tt.leaks)
			}
		})
	}
}

// TestIsolationWithSharedSkills checks what a variant that shares skills
// changes (decision 0036): Claude Code gets a link to each shared skill,
// agy a copy, and OpenCode keeps the real home directory, where it reads
// ~/.agents/skills itself.
func TestIsolationWithSharedSkills(t *testing.T) {
	linked := "your skills in ~/.agents/skills are linked into the isolated install, as the variant shares them; one added there shows up after the next install"
	copied := "your skills in ~/.agents/skills are copied into the isolated install, as the variant shares them; a skill added or changed there shows up after the next install"
	want := map[string]struct {
		dir   string
		copy  bool
		leaks []string
	}{
		"claudecode":      {"skills", false, []string{"the CLAUDE.md, .claude/CLAUDE.md and CLAUDE.local.md of each directory above the working directory still load, but for your ~/CLAUDE.md, ~/CLAUDE.local.md, ~/.claude/CLAUDE.md and ~/.claude/rules, which the isolated settings exclude with claudeMdExcludes", linked}},
		"antigravity-cli": {"antigravity-cli/skills", true, []string{"authentication is shared through the OS keyring", copied}},
		"opencode": {"", false, []string{
			"provider credentials do not carry over",
			"OpenCode runs with your home directory, as the variant shares skills: your ~/.agents/skills loads, and so does anything else OpenCode reads there",
			"AGENTS.md, .agents and .opencode load from the working directory and every directory above it, so under your home directory these still load: ~/.opencode and ~/AGENTS.md",
			"with --server, OpenCode reaches a server that runs with its own configuration",
			"a command that does not take --standalone, such as debug config, starts a background service on the port of your own OpenCode service and leaves it running",
		}},
	}
	for _, a := range harness.All() {
		w := want[a.ID]
		if a.SharedSkills != w.dir || a.CopySharedSkills != w.copy {
			t.Errorf("%s: shared skills directory = %q, copied %t, want %q, %t", a.ID, a.SharedSkills, a.CopySharedSkills, w.dir, w.copy)
		}
		if got := a.Isolation.LeaksOn("linux", true); !reflect.DeepEqual(got, w.leaks) {
			t.Errorf("%s: leaks with shared skills = %q, want %q", a.ID, got, w.leaks)
		}
		var names []string
		for _, ptr := range a.Isolation.PointersFor(true) {
			names = append(names, ptr.Name)
		}
		if slices.Contains(names, "OPENCODE_TEST_HOME") {
			t.Errorf("%s: pointers with shared skills = %v, want no OPENCODE_TEST_HOME", a.ID, names)
		}
		if closed := a.Isolation.PointersFor(false); !reflect.DeepEqual(closed, a.Isolation.Pointers) {
			t.Errorf("%s: pointers of a closed variant = %+v, want every pointer", a.ID, closed)
		}
	}
}

func TestIsolationWithoutDataIsUnknown(t *testing.T) {
	var i harness.Isolation
	if got := i.ReloginOn("linux"); got != harness.ReloginUnknown {
		t.Errorf("ReloginOn() = %q, want unknown", got)
	}
	if got := i.LeaksOn("linux", false); got == nil || len(got) != 0 {
		t.Errorf("LeaksOn() = %#v, want an empty list", got)
	}
}

// TestCommandQuoting checks how each harness reads a command in its
// settings (decision 0037): agy splits it itself and passes quotes on,
// the others run it through a shell.
func TestCommandQuoting(t *testing.T) {
	want := map[string]harness.Quoting{"claudecode": harness.ShellQuoted, "antigravity-cli": harness.Unquoted, "opencode": harness.ShellQuoted}
	for _, a := range harness.All() {
		if a.Commands != want[a.ID] {
			t.Errorf("%s reads commands as %q, want %q", a.ID, a.Commands, want[a.ID])
		}
	}
}

// TestLeaksAt checks the leaks of a launch: the walk-up leak only under
// the home directory and only with the paths that exist there, and the
// home instructions excluded or not.
func TestLeaksAt(t *testing.T) {
	all, err := harness.Lookup([]string{"claudecode", "opencode"})
	if err != nil {
		t.Fatal(err)
	}
	claude, oc := all[0].Isolation, all[1].Isolation
	walkUp := func(leaks []string) []string {
		var out []string
		for _, l := range leaks {
			if strings.Contains(l, "every directory above it") {
				out = append(out, l)
			}
		}
		return out
	}
	only := func(paths ...string) func(string) bool {
		return func(rel string) bool { return slices.Contains(paths, rel) }
	}
	for _, tt := range []struct {
		name   string
		launch harness.Launch
		want   []string
	}{
		{"outside the home directory", harness.Launch{GOOS: "linux", Exists: only(".agents/skills", "AGENTS.md")}, nil},
		{"under it, nothing there", harness.Launch{GOOS: "linux", UnderHome: true, Exists: only()}, nil},
		{"under it, one path", harness.Launch{GOOS: "linux", UnderHome: true, Exists: only(".opencode")},
			[]string{"AGENTS.md, .agents and .opencode load from the working directory and every directory above it, so under your home directory these still load: ~/.opencode"}},
		{"under it, two paths", harness.Launch{GOOS: "linux", UnderHome: true, Exists: only(".agents/skills", "AGENTS.md")},
			[]string{"AGENTS.md, .agents and .opencode load from the working directory and every directory above it, so under your home directory these still load: ~/.agents/skills and ~/AGENTS.md"}},
		{"shared skills leave ~/.agents/skills out", harness.Launch{GOOS: "linux", Shared: true, UnderHome: true, Exists: only(".agents/skills", "AGENTS.md")},
			[]string{"AGENTS.md, .agents and .opencode load from the working directory and every directory above it, so under your home directory these still load: ~/AGENTS.md"}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := walkUp(oc.LeaksAt(tt.launch)); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("walk-up leaks = %q, want %q", got, tt.want)
			}
		})
	}
	excluded := claude.LeaksAt(harness.Launch{GOOS: "linux", Excluded: true})
	if len(excluded) == 0 || !strings.Contains(excluded[0], "but for your ~/CLAUDE.md, ~/CLAUDE.local.md, ~/.claude/CLAUDE.md and ~/.claude/rules, which the isolated settings exclude with claudeMdExcludes") {
		t.Errorf("leaks with the instructions excluded = %q", excluded)
	}
	missing := claude.LeaksAt(harness.Launch{GOOS: "linux"})
	if len(missing) == 0 || !strings.Contains(missing[0], "do not exclude them with claudeMdExcludes, so run dotai install again") {
		t.Errorf("leaks with the instructions not excluded = %q", missing)
	}
	if got := oc.LeaksAt(harness.Launch{GOOS: "linux", Exists: only()}); slices.ContainsFunc(got, func(l string) bool { return strings.Contains(l, "%") }) {
		t.Errorf("leaks = %q, want every template filled", got)
	}
}

// TestAdapterData checks rules every adapter's data must keep: a harness
// that drops variables says why, a walk-up leak has one %s for its paths,
// and each rename names both files and both ids.
func TestAdapterData(t *testing.T) {
	claude, err := harness.Lookup([]string{"claudecode"})
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"CLAUDE_CODE_OAUTH_TOKEN", "ANTHROPIC_API_KEY", "ANTHROPIC_AUTH_TOKEN", "CLAUDE_CODE_USE_BEDROCK", "CLAUDE_CODE_USE_VERTEX",
		"CLAUDE_CODE_USE_FOUNDRY", "AWS_BEARER_TOKEN_BEDROCK", "ANTHROPIC_BASE_URL", "ANTHROPIC_CUSTOM_HEADERS"} {
		if !slices.Contains(claude[0].Isolation.Credentials, name) {
			t.Errorf("Claude Code's Credentials lack %s", name)
		}
	}
	for _, a := range harness.All() {
		if len(a.Isolation.Drop) > 0 && a.Isolation.DropNote == "" {
			t.Errorf("%s drops %q without a DropNote", a.ID, a.Isolation.Drop)
		}
		for _, l := range a.Isolation.Leaks {
			if n := strings.Count(l.Text, "%"); len(l.WalkUp) > 0 && (n != 1 || !strings.Contains(l.Text, "%s")) || len(l.WalkUp) == 0 && n != 0 && !strings.Contains(l.Text, "%TEMP%") {
				t.Errorf("%s leak %q: want one %%s exactly when it has WalkUp", a.ID, l.Text)
			}
		}
		for _, r := range a.Project.Renames {
			if r.From == "" || r.To == "" || r.From == r.To || r.Old == "" || r.New == "" || r.Old == r.New {
				t.Errorf("%s rename %+v: want two files and two ids", a.ID, r)
			}
		}
		if h := a.Isolation.Instructions; h != nil && (h.Key == "" || len(h.Paths) == 0 || h.Others == "") {
			t.Errorf("%s instructions %+v: want a key, paths and the files that still load", a.ID, h)
		}
	}
	oc, err := harness.Lookup([]string{"opencode"})
	if err != nil {
		t.Fatal(err)
	}
	want := []harness.Rename{{From: ".opencode/plugins/rulesync-hooks.js", To: ".opencode/plugins/rulesync-hooks-project.js", Old: `id: "rulesync-hooks",`, New: `id: "rulesync-hooks-project",`}}
	if !reflect.DeepEqual(oc[0].Project.Renames, want) {
		t.Errorf("OpenCode renames = %+v, want %+v", oc[0].Project.Renames, want)
	}
}
