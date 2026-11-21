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
