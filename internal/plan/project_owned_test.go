package plan_test

import (
	"encoding/json"
	"path/filepath"
	"reflect"
	"slices"
	"testing"

	"github.com/lartweib/dotai/internal/plan"
	"github.com/lartweib/dotai/internal/plan/jsonc"
)

// ownedHash is the SHA-256 a record keeps of a key's value.
func ownedHash(t *testing.T, value string) string {
	t.Helper()
	h, err := plan.ValueHash([]byte(value))
	if err != nil {
		t.Fatal(err)
	}
	return h
}

func TestBuildProjectReplacesWhatItOwnsAndNotWhatItDoesNot(t *testing.T) {
	in := projectInput(t)
	at := func(rel string) string { return filepath.Join(in.Project, filepath.FromSlash(rel)) }
	in.Disk[at(".claude/rules/web.md")] = plan.Current{Hash: "what dotai wrote"}
	in.Disk[at(".claude/agents/webdev.md")] = plan.Current{Hash: "edited since"}
	in.Disk[at(".agents/rules/web.md")] = plan.Current{Hash: "someone else's"}
	in.Owned = map[string]plan.ProjectOwned{
		at(".claude/rules/web.md"):     {Harness: "claudecode", Hash: "what dotai wrote", Created: true, Exclude: "/.claude/rules/web.md"},
		at(".claude/agents/webdev.md"): {Harness: "claudecode", Hash: "what dotai wrote", Created: true, Exclude: "/.claude/agents/webdev.md"},
	}
	p, err := plan.BuildProject(in)
	if err != nil {
		t.Fatal(err)
	}
	ops := opsByTarget(p, in.Project)
	if op := ops[".claude/rules/web.md"]; op.Action != plan.Replace || op.Owns == nil || op.Owns.Hash != "new:.claude/rules/web.md" || !op.Owns.Created {
		t.Errorf("owned file = %+v, want a replace that dotai still owns as created", op)
	}
	if op := ops[".claude/agents/webdev.md"]; op.Action != plan.Conflict {
		t.Errorf("owned file edited since = %+v, want a conflict", op)
	}
	if op := ops[".agents/rules/web.md"]; op.Action != plan.Conflict || op.Owns == nil || op.Owns.Created {
		t.Errorf("foreign file = %+v, want a conflict that dotai owns, not as created, once overwritten", op)
	}
	// Created files dotai still owns stay excluded, though they exist now.
	for _, e := range []string{"/.claude/rules/web.md", "/.claude/agents/webdev.md"} {
		if !slices.Contains(p.Exclude, e) {
			t.Errorf("exclude = %q, want the owned entry %s kept", p.Exclude, e)
		}
	}
	if slices.Contains(p.Exclude, "/.agents/rules/web.md") {
		t.Errorf("exclude = %q, want no entry for a foreign file", p.Exclude)
	}
}

func TestBuildProjectRemovesWhatTheProfilesNoLongerGive(t *testing.T) {
	in := projectInput(t)
	at := func(rel string) string { return filepath.Join(in.Project, filepath.FromSlash(rel)) }
	in.Disk[at(".claude/rules/old.md")] = plan.Current{Hash: "old"}
	in.Disk[at(".claude/rules/edited.md")] = plan.Current{Hash: "edited"}
	in.Disk[at(".claude/rules/foreign.md")] = plan.Current{Hash: "dotai's over the user's"}
	in.Owned = map[string]plan.ProjectOwned{
		at(".claude/rules/old.md"):     {Harness: "claudecode", Kind: "rules", Hash: "old", Created: true, Exclude: "/.claude/rules/old.md"},
		at(".claude/rules/edited.md"):  {Harness: "claudecode", Kind: "rules", Hash: "dotai's", Created: true},
		at(".claude/rules/foreign.md"): {Harness: "claudecode", Kind: "rules", Hash: "dotai's over the user's", Backup: "20261102T101500.000Z_apply"},
		at(".claude/rules/gone.md"):    {Harness: "claudecode", Kind: "rules", Hash: "gone", Created: true},
	}
	p, err := plan.BuildProject(in)
	if err != nil {
		t.Fatal(err)
	}
	ops := opsByTarget(p, in.Project)
	if op := ops[".claude/rules/old.md"]; op.Action != plan.Remove || op.Owns != nil || op.Before != "old" {
		t.Errorf("dropped file = %+v, want a remove", op)
	}
	if op := ops[".claude/rules/edited.md"]; op.Action != plan.Conflict || op.After != "" || !op.Removal {
		t.Errorf("dropped file edited since = %+v, want a conflict that removes it", op)
	}
	if op := ops[".claude/rules/foreign.md"]; op.Action != plan.Replace || op.PutBack != "20261102T101500.000Z_apply" {
		t.Errorf("dropped file dotai wrote over the user's = %+v, want the user's copy put back", op)
	}
	if _, ok := ops[".claude/rules/gone.md"]; ok {
		t.Errorf("a dropped file that is gone already is planned: %+v", ops[".claude/rules/gone.md"])
	}
	if slices.Contains(p.Exclude, "/.claude/rules/old.md") {
		t.Errorf("exclude = %q, want the removed file's entry dropped", p.Exclude)
	}
}

func TestBuildProjectMergesListsAsAUnionAndKeepsForeignKeys(t *testing.T) {
	in := projectInput(t)
	target := filepath.Join(in.Project, ".claude", "settings.local.json")
	in.Disk[target] = plan.Current{Hash: "x"}
	in.Content[target] = []byte(`{
  // the user's own
  "permissions": {"allow": ["Bash(ls)"]},
  "model": "opus",
  "theme": "dark"
}
`)
	p, err := plan.BuildProject(in)
	if err != nil {
		t.Fatal(err)
	}
	op := opsByTarget(p, in.Project)[".claude/settings.local.json"]
	if op.Action != plan.Merge {
		t.Fatalf("op = %+v, want a merge", op)
	}
	doc, err := jsonc.Parse(op.Data)
	if err != nil {
		t.Fatal(err)
	}
	if v, _ := doc.Get([]string{"permissions", "allow"}); string(v) != `["Bash(ls)","Bash(npm test)"]` {
		t.Errorf("permissions.allow = %s, want the user's entry and the profile's", v)
	}
	if v, _ := doc.Get([]string{"theme"}); string(v) != `"dark"` {
		t.Errorf("theme = %s, want the user's key kept", v)
	}
	if v, _ := doc.Get([]string{"model"}); string(v) != `"sonnet"` {
		t.Errorf("model = %s, want the profile's", v)
	}
	keys := map[string]plan.OwnedKey{}
	for _, k := range op.Owns.Keys {
		keys[k.Name()] = k
	}
	if k := keys["permissions.allow"]; string(k.Before) != `["Bash(ls)"]` || k.SHA256 != ownedHash(t, `["Bash(ls)","Bash(npm test)"]`) {
		t.Errorf("owned permissions.allow = %+v, want what it held before and the union", k)
	}
	if k := keys["model"]; string(k.Before) != `"opus"` {
		t.Errorf("owned model = %+v, want the user's value as before", k)
	}
	if k := keys["hooks.SessionStart"]; k.Before != nil {
		t.Errorf("owned hooks.SessionStart = %+v, want no value before", k)
	}
	if !reflect.DeepEqual(op.Owns.Parents, [][]string{{"hooks"}}) || op.Owns.Created {
		t.Errorf("owned parents = %q, created %v, want hooks only, in a file dotai did not create", op.Owns.Parents, op.Owns.Created)
	}

	// The next apply keeps the user's entry and drops what the profiles no
	// longer give, from the value the record kept.
	in2 := projectInput(t)
	in2.Files = slices.DeleteFunc(in2.Files, func(f plan.File) bool { return f.Path == ".claude/settings.json" })
	in2.Files = append(in2.Files, plan.File{Path: ".claude/settings.json", Hash: "h", Data: []byte(`{"permissions":{"allow":["Bash(go test)"]}}`)})
	in2.Disk[target] = plan.Current{Hash: "y"}
	in2.Content[target] = op.Data
	in2.Owned = map[string]plan.ProjectOwned{target: *op.Owns}
	p2, err := plan.BuildProject(in2)
	if err != nil {
		t.Fatal(err)
	}
	op2 := opsByTarget(p2, in2.Project)[".claude/settings.local.json"]
	doc2, err := jsonc.Parse(op2.Data)
	if err != nil {
		t.Fatal(err)
	}
	if v, _ := doc2.Get([]string{"permissions", "allow"}); string(v) != `["Bash(ls)","Bash(go test)"]` {
		t.Errorf("permissions.allow = %s, want the user's entry and the new profile's only", v)
	}
	if v, _ := doc2.Get([]string{"model"}); string(v) != `"opus"` {
		t.Errorf("model = %s, want the user's value put back", v)
	}
	if _, ok := doc2.Get([]string{"hooks"}); ok {
		t.Errorf("hooks = present, want the object dotai created removed once empty:\n%s", op2.Data)
	}
	actions := map[string]plan.Action{}
	for _, k := range op2.Keys {
		actions[plan.OwnedKey{Path: k.Path}.Name()] = k.Action
	}
	if actions["model"] != plan.Remove || actions["hooks.SessionStart"] != plan.Remove || actions["permissions.allow"] != plan.Merge {
		t.Errorf("key actions = %v, want model and hooks removed, permissions merged", actions)
	}
}

func TestBuildProjectReportsAnOwnedKeyChangedSince(t *testing.T) {
	in := projectInput(t)
	target := filepath.Join(in.Project, ".claude", "settings.local.json")
	in.Disk[target] = plan.Current{Hash: "x"}
	in.Content[target] = []byte(`{"model":"haiku"}`)
	in.Owned = map[string]plan.ProjectOwned{target: {Harness: "claudecode", Created: true, Keys: []plan.OwnedKey{{Path: []string{"model"}, SHA256: ownedHash(t, `"sonnet"`)}}}}
	p, err := plan.BuildProject(in)
	if err != nil {
		t.Fatal(err)
	}
	op := opsByTarget(p, in.Project)[".claude/settings.local.json"]
	i := slices.IndexFunc(op.Keys, func(k plan.ProjectKey) bool { return slices.Equal(k.Path, []string{"model"}) })
	if i < 0 || op.Keys[i].Action != plan.Conflict {
		t.Errorf("keys = %+v, want model a conflict", op.Keys)
	}
}

func TestBuildProjectRefusesAForeignMCPServerOfTheSameName(t *testing.T) {
	in := projectInput(t)
	claudeJSON := filepath.Join(in.Home, ".claude.json")
	in.Disk[claudeJSON] = plan.Current{Hash: "y"}
	key := filepath.ToSlash(in.Project)
	in.Content[claudeJSON] = []byte(`{"projects":{"` + key + `":{"mcpServers":{"browser":{"command":"mine"}}}}}`)
	p, err := plan.BuildProject(in)
	if err != nil {
		t.Fatal(err)
	}
	want := []plan.Warning{{Code: plan.CodeMCPServerForeign, Harness: "claudecode", Item: "MCP server browser", Path: claudeJSON}}
	if len(p.Refusals) != 1 || p.Refusals[0].Code != want[0].Code || p.Refusals[0].Item != want[0].Item || p.Refusals[0].Path != claudeJSON {
		t.Errorf("refusals = %+v, want %+v", p.Refusals, want)
	}

	// Once dotai owns it, it is dotai's to rewrite.
	path := []string{"projects", key, "mcpServers", "browser"}
	in.Owned = map[string]plan.ProjectOwned{claudeJSON: {Harness: "claudecode", Keys: []plan.OwnedKey{{Path: path, SHA256: ownedHash(t, `{"command":"mine"}`)}}}}
	p, err = plan.BuildProject(in)
	if err != nil {
		t.Fatal(err)
	}
	op := opsByTarget(p, in.Project)[claudeJSON]
	if len(p.Refusals) != 0 || op.Action != plan.Merge || len(op.Edits) != 1 || !slices.Equal(op.Edits[0].Path, path) {
		t.Errorf("owned server: refusals %+v, op %+v, want a merge that sets it", p.Refusals, op)
	}
	var v map[string]any
	if err := json.Unmarshal(op.Edits[0].Value, &v); err != nil || v["type"] != "stdio" {
		t.Errorf("edit value = %s, want the staged server", op.Edits[0].Value)
	}
}

func TestBuildProjectCreatesTheMCPEntryAndRecordsItsParents(t *testing.T) {
	in := projectInput(t)
	claudeJSON := filepath.Join(in.Home, ".claude.json")
	in.Disk[claudeJSON] = plan.Current{Hash: "y"}
	in.Content[claudeJSON] = []byte(`{"projects":{}}`)
	p, err := plan.BuildProject(in)
	if err != nil {
		t.Fatal(err)
	}
	op := opsByTarget(p, in.Project)[claudeJSON]
	key := filepath.ToSlash(in.Project)
	if op.Action != plan.Merge || op.Owns == nil || !reflect.DeepEqual(op.Owns.Parents, [][]string{{"projects", key}, {"projects", key, "mcpServers"}}) {
		t.Errorf("op = %+v owns %+v, want the project's entry and its mcpServers recorded as created", op, op.Owns)
	}
}

func TestBuildProjectRefusesATrackedMarker(t *testing.T) {
	in := projectInput(t)
	marker := filepath.Join(in.Project, ".dotai", "state.json")
	in.Tracked[marker] = true
	p, err := plan.BuildProject(in)
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Refusals) != 1 || p.Refusals[0].Code != plan.CodeMarkerTracked || p.Refusals[0].Path != marker {
		t.Errorf("refusals = %+v, want the tracked marker refused", p.Refusals)
	}
}

func TestBuildProjectSharesAnMCPServerWithAnotherWorktree(t *testing.T) {
	in := projectInput(t)
	claudeJSON := filepath.Join(in.Home, ".claude.json")
	key := filepath.ToSlash(in.Project)
	path := []string{"projects", key, "mcpServers", "browser"}
	in.Disk[claudeJSON] = plan.Current{Hash: "y"}
	in.Content[claudeJSON] = []byte(`{"projects":{"` + key + `":{"mcpServers":{"browser":{"command":"other"}}}}}`)
	in.Shared = [][]string{path}
	p, err := plan.BuildProject(in)
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Refusals) != 0 {
		t.Errorf("refusals = %+v, want none for a server another worktree's apply owns", p.Refusals)
	}
	// Dropping it leaves it to the other worktree.
	in.Files = slices.DeleteFunc(in.Files, func(f plan.File) bool { return f.Path == ".mcp.json" })
	in.Owned = map[string]plan.ProjectOwned{claudeJSON: {Harness: "claudecode", Kind: "mcp", Keys: []plan.OwnedKey{{Path: path, SHA256: ownedHash(t, `{"command":"other"}`)}}}}
	p, err = plan.BuildProject(in)
	if err != nil {
		t.Fatal(err)
	}
	if op, ok := opsByTarget(p, in.Project)[claudeJSON]; ok {
		t.Errorf("op = %+v, want the shared server left alone", op)
	}
}

func TestKeyPathQuotesNamesThatAreNotSimple(t *testing.T) {
	for _, tt := range []struct {
		key  []string
		want string
	}{
		{[]string{"model"}, "model"},
		{[]string{"env", "DOTAI_TERMINAL"}, "env.DOTAI_TERMINAL"},
		{[]string{"projects", "C:/work", "mcpServers", "browser"}, `projects["C:/work"].mcpServers.browser`},
		{[]string{"a b"}, `["a b"]`},
	} {
		if got := plan.KeyPath(tt.key); got != tt.want {
			t.Errorf("KeyPath(%q) = %s, want %s", tt.key, got, tt.want)
		}
	}
}
