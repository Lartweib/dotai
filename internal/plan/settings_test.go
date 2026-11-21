package plan_test

import (
	"crypto/sha256"
	"encoding/hex"
	"reflect"
	"strings"
	"testing"

	"github.com/lartweib/dotai/internal/plan"
)

// valueHash is the hash of a key's value, given as canonical JSON.
func valueHash(canonical string) string {
	h := sha256.Sum256([]byte(canonical))
	return hex.EncodeToString(h[:])
}

func key(value string, path ...string) plan.Key {
	return plan.Key{Path: path, SHA256: valueHash(value)}
}

// TestSettingsKeys checks the identity of the keys of a settings file:
// the top-level keys, and each variable of Claude Code's env, as the
// settings merge identifies them (decisions 0023 and 0030).
func TestSettingsKeys(t *testing.T) {
	const doc = `{
  // the model
  "model": "opus",
  "env": {"B": "2", "A": "1"},
  "permissions": {"allow": ["Bash(ls)"], "deny": []},
}`
	tests := []struct {
		name, harness, data string
		want                []plan.Key
	}{
		{name: "claude code env per variable", harness: "claudecode", data: doc, want: []plan.Key{
			key(`"opus"`, "model"), key(`"2"`, "env", "B"), key(`"1"`, "env", "A"), key(`{"allow":["Bash(ls)"],"deny":[]}`, "permissions"),
		}},
		{name: "env whole elsewhere", harness: "opencode", data: doc, want: []plan.Key{
			key(`"opus"`, "model"), key(`{"A":"1","B":"2"}`, "env"), key(`{"allow":["Bash(ls)"],"deny":[]}`, "permissions"),
		}},
		{name: "env that is not an object", harness: "claudecode", data: `{"env": "x"}`, want: []plan.Key{key(`"x"`, "env")}},
		{name: "empty env", harness: "claudecode", data: `{"env": {}}`, want: nil},
		// Claude Code's MCP servers in its .claude.json (decision 0049).
		{name: "claude code mcp servers per server", harness: "claudecode", data: `{"userID": "u", "mcpServers": {"b": {"command": "b"}, "a": {"command": "a"}}}`, want: []plan.Key{
			key(`"u"`, "userID"), key(`{"command":"b"}`, "mcpServers", "b"), key(`{"command":"a"}`, "mcpServers", "a"),
		}},
		{name: "mcp servers whole elsewhere", harness: "opencode", data: `{"mcpServers": {"a": 1}}`, want: []plan.Key{key(`{"a":1}`, "mcpServers")}},
		{name: "empty mcp servers", harness: "claudecode", data: `{"mcpServers": {}}`, want: nil},
		{name: "empty file", harness: "claudecode", data: "", want: nil},
		{name: "empty object", harness: "claudecode", data: "{}\n", want: nil},
		{name: "duplicate key, the last wins", harness: "opencode", data: `{"a": 1, "a": 2}`, want: []plan.Key{key(`2`, "a")}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := plan.SettingsKeys(tt.harness, []byte(tt.data))
			if err != nil || !reflect.DeepEqual(got, tt.want) {
				t.Errorf("SettingsKeys() = %+v, %v, want %+v", got, err, tt.want)
			}
		})
	}
	if _, err := plan.SettingsKeys("claudecode", []byte(`[1]`)); err == nil {
		t.Error("SettingsKeys() of an array succeeded, want an error")
	}
}

// TestWriteKeys checks that writing keys changes only those keys, keeps
// everything else byte for byte, and drops an env that it emptied.
func TestWriteKeys(t *testing.T) {
	const current = `{
  // set by the harness
  "theme": "dark",
  "model": "sonnet",
  "env": {
    "OLD": "1"
  }
}
`
	tests := []struct {
		name    string
		current string
		from    string
		paths   [][]string
		want    string
	}{
		{
			name: "set, add and delete", current: current, from: `{"model": "opus", "env": {"NEW": "tmux"}}`,
			paths: [][]string{{"model"}, {"env", "NEW"}, {"env", "OLD"}},
			want: `{
  // set by the harness
  "theme": "dark",
  "model": "opus",
  "env": {
    "NEW": "tmux"
  }
}
`,
		},
		{
			name: "an emptied env goes", current: current, from: `{}`,
			paths: [][]string{{"model"}, {"env", "OLD"}},
			want: `{
  // set by the harness
  "theme": "dark"
}
`,
		},
		{
			name: "an emptied mcpServers goes", current: "{\n  \"userID\": \"u\",\n  \"mcpServers\": {\n    \"a\": {}\n  }\n}\n", from: `{}`,
			paths: [][]string{{"mcpServers", "a"}},
			want:  "{\n  \"userID\": \"u\"\n}\n",
		},
		{
			name: "into a missing file", current: "", from: `{"model": "opus", "env": {"A": "1"}}`,
			paths: [][]string{{"model"}, {"env", "A"}},
			want:  "{\n  \"model\": \"opus\",\n  \"env\": {\n    \"A\": \"1\"\n  }\n}\n",
		},
		{
			name: "nothing to change", current: current, from: `{"model": "sonnet"}`,
			paths: [][]string{{"model"}, {"missing"}},
			want:  current,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := plan.WriteKeys("claudecode", []byte(tt.current), []byte(tt.from), tt.paths)
			if err != nil || string(got) != tt.want {
				t.Errorf("WriteKeys() = %s, %v\nwant %s", got, err, tt.want)
			}
		})
	}
	if _, err := plan.WriteKeys("claudecode", []byte(`{"env": "x"}`), []byte(`{"env": {"A": "1"}}`), [][]string{{"env", "A"}}); err == nil {
		t.Error("WriteKeys() into an env that is not an object succeeded, want an error")
	}
	if _, err := plan.WriteKeys("claudecode", []byte(`{"a": `), []byte(`{}`), [][]string{{"a"}}); err == nil || !strings.Contains(err.Error(), "JSON") {
		t.Errorf("WriteKeys() of a broken file error = %v, want one that says it is not JSON", err)
	}
}

func TestDiffKeys(t *testing.T) {
	before := `{"theme": "dark", "model": "sonnet", "env": {"A": "1", "B": "2"}}`
	after := `{"theme": "dark", "model": "opus", "env": {"B": "2", "C": "3"}}`
	got, err := plan.DiffKeys("claudecode", []byte(before), []byte(after))
	want := []plan.KeyChange{
		{Key: "env.A", Action: plan.Remove},
		{Key: "env.C", Action: plan.Create},
		{Key: "model", Action: plan.Replace},
	}
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Errorf("DiffKeys() = %+v, %v, want %+v", got, err, want)
	}
	if got, err := plan.DiffKeys("claudecode", nil, []byte(`{"a": 1}`)); err != nil || !reflect.DeepEqual(got, []plan.KeyChange{{Key: "a", Action: plan.Create}}) {
		t.Errorf("DiffKeys() of a new file = %+v, %v", got, err)
	}
}

// hookDoc is Claude Code's settings with two groups of SessionStart, one
// with a matcher, a group of another program in Stop, and a foreign key.
const hookDoc = `{
  "theme": "dark",
  "hooks": {
    "SessionStart": [
      {"matcher": "startup", "hooks": [{"type": "command", "command": "a"}]},
      {"hooks": [{"type": "command", "command": "b", "timeout": 5}]}
    ],
    "Stop": [{"hooks": [{"type": "command", "command": "tracker"}]}]
  }
}
`

// TestSettingsKeysOwnEachHookGroup checks the identity of hook entries
// (decision 0050): event, matcher and first command, with the group as the
// value, and a group without a command named by a hash of its content.
func TestSettingsKeysOwnEachHookGroup(t *testing.T) {
	got, err := plan.SettingsKeys("claudecode", []byte(hookDoc))
	want := []plan.Key{
		key(`"dark"`, "theme"),
		key(`{"hooks":[{"command":"a","type":"command"}],"matcher":"startup"}`, "hooks", "SessionStart", "startup", "a"),
		key(`{"hooks":[{"command":"b","timeout":5,"type":"command"}]}`, "hooks", "SessionStart", "", "b"),
		key(`{"hooks":[{"command":"tracker","type":"command"}]}`, "hooks", "Stop", "", "tracker"),
	}
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Errorf("SettingsKeys() = %+v, %v, want %+v", got, err, want)
	}
	if name := got[1].Name(); name != "hooks.SessionStart[startup]: a" {
		t.Errorf("Name() = %q, want the event, the matcher and the command", name)
	}
	for _, tt := range []struct{ name, data string }{
		{"opencode keeps hooks whole", `{"hooks": {"E": [{"hooks": []}]}}`},
	} {
		keys, err := plan.SettingsKeys("opencode", []byte(tt.data))
		if err != nil || len(keys) != 1 || len(keys[0].Path) != 1 {
			t.Errorf("%s: SettingsKeys() = %+v, %v, want the hooks key whole", tt.name, keys, err)
		}
	}
	// A group with no command, one that repeats an identity, an event that
	// is not a list and an empty hooks object.
	odd, err := plan.SettingsKeys("claudecode", []byte(`{"hooks": {"E": [{"hooks": [{"type": "prompt"}]}, {"hooks": [{"command": "x"}]}, {"hooks": [{"command": "x", "timeout": 1}]}], "F": "x"}}`))
	if err != nil || len(odd) != 4 {
		t.Fatalf("SettingsKeys() = %+v, %v, want four keys", odd, err)
	}
	if !strings.HasPrefix(odd[0].Path[3], "#") || odd[2].Path[3] != "x#2" || !reflect.DeepEqual(odd[3].Path, []string{"hooks", "F"}) {
		t.Errorf("paths = %v, want a hash id, a numbered repeat and the event that is not a list as a key", []any{odd[0].Path, odd[1].Path, odd[2].Path, odd[3].Path})
	}
	if empty, err := plan.SettingsKeys("claudecode", []byte(`{"hooks": {}}`)); err != nil || empty != nil {
		t.Errorf("SettingsKeys() of an empty hooks = %+v, %v, want none", empty, err)
	}
}

// TestWriteKeysEditsOnlyDotaisHookGroups checks that setting and deleting a
// hook entry leave every other group, key and byte of the file as they
// were, and that an event or hooks object that a deletion empties goes.
func TestWriteKeysEditsOnlyDotaisHookGroups(t *testing.T) {
	tests := []struct {
		name, current, from string
		paths               [][]string
		want                string
	}{
		{
			name: "delete one group", current: hookDoc, from: `{}`,
			paths: [][]string{{"hooks", "SessionStart", "startup", "a"}},
			want: `{
  "theme": "dark",
  "hooks": {
    "SessionStart": [
      {"hooks": [{"type": "command", "command": "b", "timeout": 5}]}
    ],
    "Stop": [{"hooks": [{"type": "command", "command": "tracker"}]}]
  }
}
`,
		},
		{
			name: "delete the groups of an event", current: hookDoc, from: `{}`,
			paths: [][]string{{"hooks", "SessionStart", "startup", "a"}, {"hooks", "SessionStart", "", "b"}},
			want: `{
  "theme": "dark",
  "hooks": {
    "Stop": [{"hooks": [{"type": "command", "command": "tracker"}]}]
  }
}
`,
		},
		{
			name: "delete the last group of the file", current: `{"hooks": {"Stop": [{"hooks": [{"command": "x"}]}]}, "a": 1}`, from: `{}`,
			paths: [][]string{{"hooks", "Stop", "", "x"}},
			want:  `{"a": 1}`,
		},
		{
			name: "add to an event, a new event and a missing hooks", current: hookDoc,
			from:  `{"hooks": {"Stop": [{"hooks": [{"command": "mine"}]}], "PreToolUse": [{"matcher": "*", "hooks": [{"command": "p"}]}]}}`,
			paths: [][]string{{"hooks", "Stop", "", "mine"}, {"hooks", "PreToolUse", "*", "p"}},
			want: `{
  "theme": "dark",
  "hooks": {
    "SessionStart": [
      {"matcher": "startup", "hooks": [{"type": "command", "command": "a"}]},
      {"hooks": [{"type": "command", "command": "b", "timeout": 5}]}
    ],
    "Stop": [{"hooks": [{"type": "command", "command": "tracker"}]}, {"hooks":[{"command":"mine"}]}],
    "PreToolUse": [
      {
        "matcher": "*",
        "hooks": [
          {
            "command": "p"
          }
        ]
      }
    ]
  }
}
`,
		},
		{
			name: "replace a changed group in place", current: hookDoc,
			from:  `{"hooks": {"SessionStart": [{"hooks": [{"command": "b", "timeout": 9}]}]}}`,
			paths: [][]string{{"hooks", "SessionStart", "", "b"}},
			want: `{
  "theme": "dark",
  "hooks": {
    "SessionStart": [
      {"matcher": "startup", "hooks": [{"type": "command", "command": "a"}]},
      {"hooks":[{"command":"b","timeout":9}]}
    ],
    "Stop": [{"hooks": [{"type": "command", "command": "tracker"}]}]
  }
}
`,
		},
		{
			name: "into a missing file", current: "",
			from:  `{"hooks": {"Stop": [{"hooks": [{"command": "mine"}]}]}}`,
			paths: [][]string{{"hooks", "Stop", "", "mine"}},
			want: `{
  "hooks": {
    "Stop": [
      {
        "hooks": [
          {
            "command": "mine"
          }
        ]
      }
    ]
  }
}
`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := plan.WriteKeys("claudecode", []byte(tt.current), []byte(tt.from), tt.paths)
			if err != nil {
				t.Fatalf("WriteKeys() error = %v", err)
			}
			if string(got) != tt.want {
				t.Errorf("WriteKeys() = %s\nwant %s", got, tt.want)
			}
		})
	}
}
