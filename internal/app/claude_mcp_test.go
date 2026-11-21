package app_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/lartweib/dotai/internal/app"
	"github.com/lartweib/dotai/internal/pack"
	"github.com/lartweib/dotai/internal/plan"
	"github.com/lartweib/dotai/internal/rulesync"
	"github.com/lartweib/dotai/internal/state"
)

// Claude Code keeps its user-scope MCP servers in .claude.json at the root
// of the home directory, which rulesync stages as user/.claude.json. dotai
// owns only the servers it writes there, server by server (decision 0049).

// threeServers and twoServers are rulesync's .claude.json for a pack with
// three MCP servers, and for the same pack without codegraph.
const (
	threeServers = `{
  "mcpServers": {
    "engram": {"type": "stdio", "command": "engram", "args": ["mcp"]},
    "otea": {"type": "stdio", "command": "otea", "args": ["mcp"]},
    "codegraph": {"type": "stdio", "command": "codegraph", "args": ["serve"]}
  }
}
`
	twoServers = `{
  "mcpServers": {
    "engram": {"type": "stdio", "command": "engram", "args": ["mcp"]},
    "otea": {"type": "stdio", "command": "otea", "args": ["mcp"]}
  }
}
`
)

// isolatedClaudeJSON is Claude Code's .claude.json in the isolated install.
const isolatedClaudeJSON = ".dotai/isolated/claude/.claude.json"

// mcpRulesync is a fake rulesync that writes, for a user scope that
// includes Claude Code, each harness's rules file and *servers as its
// .claude.json, unless it is empty.
func mcpRulesync(t *testing.T, servers *string) func(context.Context, rulesync.Result, []string, ...rulesync.Job) error {
	rules := replaceRun(t)
	return func(ctx context.Context, bin rulesync.Result, targets []string, jobs ...rulesync.Job) error {
		if err := rules(ctx, bin, targets, jobs...); err != nil {
			return err
		}
		for _, j := range jobs {
			if j.Global && slices.Contains(targets, "claudecode") && *servers != "" {
				writeFile(t, filepath.Join(j.Dir, ".claude.json"), *servers)
			}
		}
		return nil
	}
}

func installMCP(t *testing.T, d app.ExecuteFS, home string, servers *string) (app.InstallResult, error) {
	t.Helper()
	f := quiet(t)
	return app.InstallWith(context.Background(), d, f.fetch, mcpRulesync(t, servers), examplePack, "", home)
}

// servers are the names of the MCP servers in the .claude.json name, nil
// when it is missing.
func servers(t *testing.T, name string) []string {
	t.Helper()
	if _, err := os.Lstat(name); os.IsNotExist(err) {
		return nil
	}
	out := []string{}
	m, _ := settingsOf(t, name)["mcpServers"].(map[string]any)
	for k := range m {
		out = append(out, k)
	}
	slices.Sort(out)
	return out
}

func opAt(ops []app.InstallOp, target string) (app.InstallOp, bool) {
	for _, op := range ops {
		if op.Target == target {
			return op, true
		}
	}
	return app.InstallOp{}, false
}

// TestInstallIsolatedWritesClaudeMCPServers is the bug the decision starts
// from: a pack with MCP servers failed with path-unmapped. Its servers go
// into the isolated .claude.json, which CLAUDE_CONFIG_DIR makes Claude Code
// read, each one an owned key.
func TestInstallIsolatedWritesClaudeMCPServers(t *testing.T) {
	home, _ := installHome(t)
	s := threeServers
	res, err := installMCP(t, newClockFS(t), home, &s)
	if err != nil {
		t.Fatalf("Install() error = %v, want the MCP servers installed", err)
	}
	target := at(home, isolatedClaudeJSON)
	if got := servers(t, target); !reflect.DeepEqual(got, []string{"codegraph", "engram", "otea"}) {
		t.Errorf("servers = %v, want the pack's three", got)
	}
	op, ok := opAt(res.Ops, target)
	want := []plan.KeyChange{{Key: "mcpServers.codegraph", Action: plan.Create}, {Key: "mcpServers.engram", Action: plan.Create}, {Key: "mcpServers.otea", Action: plan.Create}}
	if !ok || op.Action != plan.Create || op.Harness != "claudecode" || op.Source != ".claude.json" || op.Kind != "mcp" || !reflect.DeepEqual(op.Keys, want) {
		t.Errorf("op = %+v, %v, want a create of %+v", op, ok, want)
	}
	st := readJSON(t, res.State, state.Decode)
	f, ok := ownedFile(t, st, target)
	if !ok || !reflect.DeepEqual(keyNames(f.Keys), []string{"mcpServers.engram", "mcpServers.otea", "mcpServers.codegraph"}) {
		t.Errorf("state record = %+v, want the three servers owned", f)
	}
	if _, err := os.Lstat(at(home, ".claude.json")); !os.IsNotExist(err) {
		t.Errorf("~/.claude.json = %v, want the isolated install to leave it alone", err)
	}
}

// TestIsolatedClaudeMCPRoundTrip follows the isolated .claude.json through
// a launch, an install that drops a server, the doctor and restores: what
// Claude Code writes there stays, only dotai's servers change, and the
// restore of the first install removes the file only when nothing else is
// left in it.
func TestIsolatedClaudeMCPRoundTrip(t *testing.T) {
	home, _ := installHome(t)
	d := newClockFS(t)
	s := threeServers
	if _, err := installMCP(t, d, home, &s); err != nil {
		t.Fatal(err)
	}
	target := at(home, isolatedClaudeJSON)
	// Claude Code's first launch writes its state, and the user adds a
	// server of their own.
	v := settingsOf(t, target)
	v["mcpServers"].(map[string]any)["mine"] = map[string]any{"command": "mine"}
	harnessWrites(t, target, map[string]any{"numStartups": 1.0, "userID": "u", "mcpServers": v["mcpServers"]})

	if res := runDoctor(t, &fakeGit{}, home, false); len(res.Findings) != 0 {
		t.Errorf("Doctor() after a launch = %q, want nothing found", codes(res))
	}

	s = twoServers
	res, err := installMCP(t, d, home, &s)
	if err != nil {
		t.Fatalf("Install() without codegraph error = %v", err)
	}
	if op, _ := opAt(res.Ops, target); op.Action != plan.Replace || !reflect.DeepEqual(op.Keys, []plan.KeyChange{{Key: "mcpServers.codegraph", Action: plan.Remove}}) {
		t.Errorf("op = %+v, want codegraph removed only", op)
	}
	got := settingsOf(t, target)
	if got["numStartups"] != 1.0 || got["userID"] != "u" || !reflect.DeepEqual(servers(t, target), []string{"engram", "mine", "otea"}) {
		t.Errorf(".claude.json = %v, want Claude Code's keys, the user's server and the pack's two", got)
	}

	// The doctor reports a server dotai owns that the user changed.
	m := got["mcpServers"].(map[string]any)
	m["otea"] = map[string]any{"command": "changed"}
	harnessWrites(t, target, map[string]any{"mcpServers": m})
	if want := "key-changed " + target + " mcpServers.otea"; !slices.Contains(codes(runDoctor(t, &fakeGit{}, home, false)), want) {
		t.Errorf("Doctor() = %q, want %q", codes(runDoctor(t, &fakeGit{}, home, false)), want)
	}
	m["otea"] = map[string]any{"type": "stdio", "command": "otea", "args": []any{"mcp"}}
	harnessWrites(t, target, map[string]any{"mcpServers": m})

	// Undoing both installs, newest first, leaves what Claude Code and the
	// user wrote.
	for range 2 {
		if _, err := restore(d, home, "", nil); err != nil {
			t.Fatalf("Restore() error = %v", err)
		}
	}
	got = settingsOf(t, target)
	if got["numStartups"] != 1.0 || !reflect.DeepEqual(servers(t, target), []string{"mine"}) {
		t.Errorf(".claude.json after the restore = %v, want only what was not dotai's", got)
	}
}

// TestRestoreRemovesTheIsolatedClaudeJSONItCreated checks that a restore
// takes away the .claude.json an install created, when nothing else was
// written into it.
func TestRestoreRemovesTheIsolatedClaudeJSONItCreated(t *testing.T) {
	home, _ := installHome(t)
	d := newClockFS(t)
	before := snapshot(t, home)
	s := threeServers
	if _, err := installMCP(t, d, home, &s); err != nil {
		t.Fatal(err)
	}
	res, err := restore(d, home, "", nil)
	if err != nil || !res.Uninstalled {
		t.Fatalf("Restore() = %+v, %v, want the install undone", res, err)
	}
	if _, err := os.Lstat(at(home, isolatedClaudeJSON)); !os.IsNotExist(err) {
		t.Errorf(".claude.json = %v, want it gone", err)
	}
	if after := snapshot(t, home); !reflect.DeepEqual(after, before) {
		t.Errorf("home after the restore = %v, want %v", after, before)
	}
}

// TestInstallStopsOnABrokenIsolatedClaudeJSON checks that a .claude.json
// that is not a JSON object is never overwritten: the install names it and
// writes nothing.
func TestInstallStopsOnABrokenIsolatedClaudeJSON(t *testing.T) {
	home, _ := installHome(t)
	writeFile(t, at(home, isolatedClaudeJSON), `{"numStartups": `)
	s := threeServers
	_, err := installMCP(t, newClockFS(t), home, &s)
	if err == nil || !strings.Contains(err.Error(), at(home, isolatedClaudeJSON)) || !strings.Contains(err.Error(), "JSON") {
		t.Fatalf("Install() error = %v, want the broken file named", err)
	}
	if data, _ := os.ReadFile(at(home, isolatedClaudeJSON)); string(data) != `{"numStartups": ` {
		t.Errorf(".claude.json = %s, want it untouched", data)
	}
}

func replaceMCP(t *testing.T, d app.ExecuteFS, h app.Host, home string, servers *string, again bool) (app.InstallResult, error) {
	t.Helper()
	f := quiet(t)
	opts := app.InstallOptions{Harnesses: pack.Harnesses}
	if again {
		return app.InstallAgainReplace(context.Background(), d, h, f.fetch, mcpRulesync(t, servers), examplePack, home, opts)
	}
	return app.InstallReplace(context.Background(), d, h, f.fetch, mcpRulesync(t, servers), examplePack, home, opts)
}

// TestReplaceMergesClaudeMCPServersIntoTheRealClaudeJSON checks the
// replace mode (decisions 0038 and 0049): the reset wipes the user's own
// MCP servers and keeps every other key of ~/.claude.json, the pack's
// servers are set one by one in the same install, a second install leaves
// them as they are, and a restore puts the file back as it was.
func TestReplaceMergesClaudeMCPServersIntoTheRealClaudeJSON(t *testing.T) {
	home, h := realHome(t)
	target := at(home, ".claude.json")
	original, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	d := newClockFS(t)
	s := threeServers
	res, err := replaceMCP(t, d, h, home, &s, false)
	if err != nil {
		t.Fatalf("Install() error = %v", err)
	}
	got := settingsOf(t, target)
	for _, k := range []string{"firstStartTime", "oauthAccount", "numStartups", "projects"} {
		if _, ok := got[k]; !ok {
			t.Errorf("~/.claude.json lacks %s after the replace: %v", k, got)
		}
	}
	if !reflect.DeepEqual(servers(t, target), []string{"codegraph", "engram", "otea"}) {
		t.Errorf("servers = %v, want the pack's three and not the user's", servers(t, target))
	}
	if !slices.ContainsFunc(res.WipeKeys, func(k app.WipeKey) bool { return k.File == target && slices.Equal(k.Key, []string{"mcpServers"}) }) {
		t.Errorf("wiped keys = %+v, want the user's mcpServers", res.WipeKeys)
	}
	op, ok := opAt(res.Ops, target)
	if want := []plan.KeyChange{{Key: "mcpServers.codegraph", Action: plan.Create}, {Key: "mcpServers.engram", Action: plan.Create}, {Key: "mcpServers.otea", Action: plan.Create}}; !ok || !reflect.DeepEqual(op.Keys, want) {
		t.Errorf("op = %+v, want the three servers created", op)
	}
	if r := runDoctor(t, &fakeGit{}, home, false); slices.ContainsFunc(r.Findings, func(f app.Finding) bool { return f.Path == target }) {
		t.Errorf("Doctor() = %q, want nothing about ~/.claude.json", codes(r))
	}

	// Claude Code writes its state, a changed key and a new one; a second
	// install, without codegraph, keeps the servers dotai owns and removes
	// only codegraph.
	harnessWrites(t, target, map[string]any{"numStartups": 4.0, "tipsHistory": map[string]any{"new-user": 1.0}})
	s = twoServers
	res, err = replaceMCP(t, d, h, home, &s, true)
	if err != nil {
		t.Fatalf("second Install() error = %v", err)
	}
	if op, _ := opAt(res.Ops, target); !reflect.DeepEqual(op.Keys, []plan.KeyChange{{Key: "mcpServers.codegraph", Action: plan.Remove}}) {
		t.Errorf("second op = %+v, want codegraph removed only", op)
	}
	if slices.ContainsFunc(res.WipeKeys, func(k app.WipeKey) bool { return k.File == target && len(k.Key) > 0 && k.Key[0] == "mcpServers" }) {
		t.Errorf("second wiped keys = %+v, want dotai's own servers kept", res.WipeKeys)
	}
	if got := settingsOf(t, target); got["numStartups"] != 4.0 || !reflect.DeepEqual(servers(t, target), []string{"engram", "otea"}) {
		t.Errorf("~/.claude.json = %v, want Claude Code's write kept and two servers", got)
	}

	// Undoing both installs, newest first: the user's servers come back
	// and dotai's go, and what Claude Code wrote after the install stays,
	// so the file is not the backup's copy put back whole.
	for range 2 {
		if _, err := restore(d, home, "", nil); err != nil {
			t.Fatalf("Restore() error = %v", err)
		}
	}
	got = settingsOf(t, target)
	if got["numStartups"] != 4.0 || !reflect.DeepEqual(got["tipsHistory"], map[string]any{"new-user": 1.0}) {
		t.Errorf("~/.claude.json after the restore = %v, want Claude Code's later writes kept", got)
	}
	writeFile(t, at(home, "orig.json"), string(original))
	want := settingsOf(t, at(home, "orig.json"))
	for _, k := range []string{"numStartups", "tipsHistory"} {
		delete(got, k)
		delete(want, k)
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("~/.claude.json after the restore = %v, want the rest as it was: %v", got, want)
	}
}

// TestRestoreReplaceGivesBackClaudeJSONExactly checks that a replace
// install with MCP servers that nothing changed since is undone byte for
// byte.
func TestRestoreReplaceGivesBackClaudeJSONExactly(t *testing.T) {
	home, h := realHome(t)
	target := at(home, ".claude.json")
	original, _ := os.ReadFile(target)
	d := newClockFS(t)
	s := threeServers
	if _, err := replaceMCP(t, d, h, home, &s, false); err != nil {
		t.Fatal(err)
	}
	res, err := restore(d, home, "", nil)
	if err != nil || len(res.Conflicts) != 0 {
		t.Fatalf("Restore() = %+v, %v, want no conflicts", res, err)
	}
	if got, _ := os.ReadFile(target); string(got) != string(original) {
		t.Errorf("~/.claude.json after the restore =\n%s\nwant\n%s", got, original)
	}
}

// TestReplaceCreatesAMissingClaudeJSONAndItsRestoreRemovesIt checks a home
// without ~/.claude.json: the replace install creates it with the pack's
// servers, and the restore removes it.
func TestReplaceCreatesAMissingClaudeJSONAndItsRestoreRemovesIt(t *testing.T) {
	home, h := realHome(t)
	target := at(home, ".claude.json")
	if err := os.Remove(target); err != nil {
		t.Fatal(err)
	}
	d := newClockFS(t)
	s := threeServers
	res, err := replaceMCP(t, d, h, home, &s, false)
	if err != nil {
		t.Fatal(err)
	}
	if op, _ := opAt(res.Ops, target); op.Action != plan.Create {
		t.Errorf("op = %+v, want a create", op)
	}
	if _, err := restore(d, home, "", nil); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(target); !os.IsNotExist(err) {
		t.Errorf("~/.claude.json = %v, want it removed", err)
	}
}

// TestReplaceWithMCPRollsBackAndSettlesAtEveryStep checks the replace
// install that writes the wipe of ~/.claude.json and the pack's servers as
// one write: a failure at any write puts the home back exactly, and an
// install killed at any write is undone exactly by the next restore.
func TestReplaceWithMCPRollsBackAndSettlesAtEveryStep(t *testing.T) {
	s := threeServers
	count := func() int {
		home, h := realHome(t)
		d := &stepFS{clockFS: newClockFS(t)}
		if _, err := replaceMCP(t, d, h, home, &s, false); err != nil {
			t.Fatal(err)
		}
		return d.n
	}
	n := count()
	for k := 1; k <= n; k++ {
		for _, kill := range []bool{false, true} {
			t.Run(fmt.Sprintf("at %d, killed %v", k, kill), func(t *testing.T) {
				heavy(t, k-1)
				home, h := realHome(t)
				before := homeEntries(t, home)
				d := &stepFS{clockFS: newClockFS(t), at: k, kill: kill}
				if kill {
					func() {
						defer func() {
							if r := recover(); r != errCrash {
								t.Fatalf("recovered %v, want the crash", r)
							}
						}()
						_, _ = replaceMCP(t, d, h, home, &s, false)
					}()
					if _, err := restore(newClockFS(t), home, "", nil); err != nil {
						t.Fatalf("Restore() after the kill error = %v", err)
					}
				} else if _, err := replaceMCP(t, d, h, home, &s, false); !errors.Is(err, errInjected) {
					t.Fatalf("Install() error = %v, want the injected failure", err)
				}
				diff(t, "home", homeEntries(t, home), before)
			})
		}
	}
}

// TestUnmappedHintNamesWhatIsMissing checks the hint of path-unmapped: a
// file of a harness the install leaves out asks for that harness, and any
// other file is one dotai has no place for, which adding a harness would
// not fix.
func TestUnmappedHintNamesWhatIsMissing(t *testing.T) {
	home, _ := installHome(t)
	f := quiet(t)
	f.files = map[string]string{"user/notes.md": "x\n"}
	p, err := f.planInstall(examplePack, "", home)
	if err != nil || len(p.Diagnostics) != 1 || p.Diagnostics[0].Code != app.CodePathUnmapped {
		t.Fatalf("PlanInstall() = %+v, %v, want one path-unmapped", p.Diagnostics, err)
	}
	if hint := p.Diagnostics[0].Hint; strings.Contains(hint, "add the harness") || !strings.Contains(hint, "no place") {
		t.Errorf("hint = %q, want one that says dotai has no place for it", hint)
	}
	if got := app.UnmappedHint(".claude.json"); !strings.Contains(got, "add claudecode to harnesses") {
		t.Errorf("hint of a Claude Code file = %q, want it to ask for claudecode", got)
	}
}

// TestReplaceStopsOnABrokenClaudeJSON checks that a ~/.claude.json that is
// not a JSON object stops a replace install with MCP servers, naming it,
// and that nothing is written.
func TestReplaceStopsOnABrokenClaudeJSON(t *testing.T) {
	home, h := realHome(t)
	target := at(home, ".claude.json")
	writeFile(t, target, "{\r\n  \"numStartups\": 3,\r\n")
	before := homeEntries(t, home)
	s := threeServers
	if _, err := replaceMCP(t, newClockFS(t), h, home, &s, false); err == nil || !strings.Contains(err.Error(), target) {
		t.Fatalf("Install() error = %v, want the broken file named", err)
	}
	diff(t, "home", homeEntries(t, home), before)
}

// TestNewClaudeJSONIsPrivate checks that a .claude.json dotai creates, in
// either mode, is readable by its owner only, as Claude Code writes it,
// since MCP servers may carry tokens; one that is there keeps its mode.
func TestNewClaudeJSONIsPrivate(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX permissions")
	}
	mode := func(name string) os.FileMode {
		t.Helper()
		info, err := os.Stat(name)
		if err != nil {
			t.Fatal(err)
		}
		return info.Mode().Perm()
	}
	s := threeServers
	home, _ := installHome(t)
	if _, err := installMCP(t, newClockFS(t), home, &s); err != nil {
		t.Fatal(err)
	}
	if got := mode(at(home, isolatedClaudeJSON)); got != 0o600 {
		t.Errorf("isolated .claude.json mode = %o, want 600", got)
	}

	home, h := realHome(t)
	target := at(home, ".claude.json")
	if err := os.Remove(target); err != nil {
		t.Fatal(err)
	}
	if _, err := replaceMCP(t, newClockFS(t), h, home, &s, false); err != nil {
		t.Fatal(err)
	}
	if got := mode(target); got != 0o600 {
		t.Errorf("new ~/.claude.json mode = %o, want 600", got)
	}

	home, h = realHome(t)
	target = at(home, ".claude.json")
	if err := os.Chmod(target, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := replaceMCP(t, newClockFS(t), h, home, &s, false); err != nil {
		t.Fatal(err)
	}
	if got := mode(target); got != 0o644 {
		t.Errorf("existing ~/.claude.json mode = %o, want it kept at 644", got)
	}
}

// collidingSeeds are ~/.claude.json files whose own servers share a name
// with a server of the pack, engram: alone, and with per-project keys the
// reset wipes too.
var collidingSeeds = map[string]string{
	"servers": "{\n  \"numStartups\": 3,\n  \"mcpServers\": {\"engram\": {\"command\": \"mine-engram\"}, \"mine\": {\"command\": \"mine\"}}\n}\n",
	"projects": "{\n  \"numStartups\": 3,\n  \"mcpServers\": {\"engram\": {\"command\": \"mine-engram\"}, \"mine\": {\"command\": \"mine\"}},\n" +
		"  \"projects\": {\n    \"C:/work\": {\"allowedTools\": [\"Bash\"], \"hasTrustDialogAccepted\": true, \"mcpServers\": {\"p\": {\"command\": \"p\"}}},\n" +
		"    \"C:/other\": {\"hasTrustDialogAccepted\": true}\n  },\n  \"userID\": \"u\"\n}\n",
}

// TestRestoreReplaceIsExactWithAServerOfTheSameName checks that a restore
// of a replace install gives ~/.claude.json back byte for byte when the
// user's own servers include one of the name of a pack's server, which
// the reset wiped and the pack set again (decision 0049).
func TestRestoreReplaceIsExactWithAServerOfTheSameName(t *testing.T) {
	for name, seed := range collidingSeeds {
		t.Run(name, func(t *testing.T) {
			home, h := realHome(t)
			target := at(home, ".claude.json")
			writeFile(t, target, seed)
			d := newClockFS(t)
			s := threeServers
			if _, err := replaceMCP(t, d, h, home, &s, false); err != nil {
				t.Fatal(err)
			}
			if _, err := restore(d, home, "", nil); err != nil {
				t.Fatalf("Restore() error = %v", err)
			}
			if got, _ := os.ReadFile(target); string(got) != seed {
				t.Errorf("~/.claude.json after the restore =\n%s\nwant\n%s", got, seed)
			}
		})
	}
}

// TestReplaceRollsBackExactlyWithAServerOfTheSameName checks the same of a
// replace install that fails at any write, over each colliding seed.
func TestReplaceRollsBackExactlyWithAServerOfTheSameName(t *testing.T) {
	s := threeServers
	for name, seed := range collidingSeeds {
		home, h := realHome(t)
		writeFile(t, at(home, ".claude.json"), seed)
		count := &stepFS{clockFS: newClockFS(t)}
		if _, err := replaceMCP(t, count, h, home, &s, false); err != nil {
			t.Fatal(err)
		}
		for k := 1; k <= count.n; k++ {
			t.Run(fmt.Sprintf("%s at %d", name, k), func(t *testing.T) {
				heavy(t, k-1)
				home, h := realHome(t)
				target := at(home, ".claude.json")
				writeFile(t, target, seed)
				if _, err := replaceMCP(t, &stepFS{clockFS: newClockFS(t), at: k}, h, home, &s, false); !errors.Is(err, errInjected) {
					t.Fatalf("Install() error = %v, want the injected failure", err)
				}
				if got, _ := os.ReadFile(target); string(got) != seed {
					t.Errorf("~/.claude.json after the rollback =\n%s\nwant\n%s", got, seed)
				}
			})
		}
	}
}
