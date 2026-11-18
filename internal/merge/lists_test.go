package merge_test

import (
	"reflect"
	"strings"
	"testing"

	"github.com/lartweib/dotai/internal/merge"
	"github.com/lartweib/dotai/internal/pack"
)

func hooks(data string) src       { return src{rel: ".rulesync/hooks.jsonc", data: data} }
func permissions(data string) src { return src{rel: ".rulesync/permissions.jsonc", data: data} }

// staged returns the file of files at path.
func staged(t *testing.T, files []merge.File, path string) merge.File {
	t.Helper()
	for _, f := range files {
		if f.Path == path {
			return f
		}
	}
	t.Fatalf("no %s in %v", path, files)
	return merge.File{}
}

func TestStageMergesListFiles(t *testing.T) {
	tests := []struct {
		name    string
		pack    *pack.Pack
		sel     merge.Selection
		project bool // the file is in the project scope
		path    string
		want    string
		origins map[string]string
	}{
		{
			name: "permissions in layer order, duplicates once",
			pack: newPack(
				base(permissions("{\n  // base rules\n  \"permission\": {\n    \"bash\": {\"git *\": \"allow\", \"rm -rf *\": \"deny\",},\n  },\n}\n")),
				variant("v", permissions(`{"permission": {"bash": {"npm *": "allow", "git *": "allow"}, "read": {".env": "deny"}}}`)),
			),
			path: ".rulesync/permissions.jsonc",
			want: "{\n  \"permission\": {\n    \"bash\": {\n      \"git *\": \"allow\",\n      \"rm -rf *\": \"deny\",\n      \"npm *\": \"allow\"\n    },\n" +
				"    \"read\": {\n      \".env\": \"deny\"\n    }\n  }\n}\n",
			origins: map[string]string{
				"/permission/bash/git *":    "base",
				"/permission/bash/rm -rf *": "base",
				"/permission/bash/npm *":    "variants/v",
				"/permission/read/.env":     "variants/v",
			},
		},
		{
			name: "hooks per event, identical entries once by canonical JSON",
			pack: newPack(
				base(hooks(`{"version": 1, "hooks": {"preToolUse": [{"matcher": "Bash", "command": "a && b"}]}}`)),
				variant("v", hooks(`{"version": 1, "hooks": {"preToolUse": [{"command": "a && b", "matcher": "Bash"}, {"command": "c"}, {"command": "c"}], "stop": [{"command": "d"}]}}`)),
			),
			path: ".rulesync/hooks.jsonc",
			want: "{\n  \"version\": 1,\n  \"hooks\": {\n    \"preToolUse\": [\n      {\n        \"matcher\": \"Bash\",\n        \"command\": \"a && b\"\n      },\n" +
				"      {\n        \"command\": \"c\"\n      }\n    ],\n    \"stop\": [\n      {\n        \"command\": \"d\"\n      }\n    ]\n  }\n}\n",
			origins: map[string]string{
				"/version":            "base",
				"/hooks/preToolUse/0": "base",
				"/hooks/preToolUse/1": "variants/v",
				"/hooks/stop/0":       "variants/v",
			},
		},
		{
			name: "tool-scoped blocks merge like the shared one",
			pack: newPack(
				base(hooks(`{"claudecode": {"hooks": {"notification": [{"command": "n"}]}}}`)),
				variant("v", hooks(`{"claudecode": {"hooks": {"notification": [{"command": "m"}]}}, "opencode": {"hooks": {"stop": [{"command": "s"}]}}}`)),
			),
			path: ".rulesync/hooks.jsonc",
			want: "{\n  \"claudecode\": {\n    \"hooks\": {\n      \"notification\": [\n        {\n          \"command\": \"n\"\n        },\n        {\n          \"command\": \"m\"\n        }\n      ]\n    }\n  },\n" +
				"  \"opencode\": {\n    \"hooks\": {\n      \"stop\": [\n        {\n          \"command\": \"s\"\n        }\n      ]\n    }\n  }\n}\n",
			origins: map[string]string{
				"/claudecode/hooks/notification/0": "base",
				"/claudecode/hooks/notification/1": "variants/v",
				"/opencode/hooks/stop/0":           "variants/v",
			},
		},
		{
			name:    "profiles in name order, whatever the selection's order",
			pack:    newPack(profile("b", permissions(`{"permission": {"bash": {"b": "allow"}}}`)), profile("a", permissions(`{"permission": {"bash": {"a": "allow", "b": "allow"}}}`))),
			sel:     merge.Selection{Profiles: []string{"b", "a"}},
			project: true,
			path:    ".rulesync/permissions.jsonc",
			want:    "{\n  \"permission\": {\n    \"bash\": {\n      \"a\": \"allow\",\n      \"b\": \"allow\"\n    }\n  }\n}\n",
			origins: map[string]string{"/permission/bash/a": "profiles/a", "/permission/bash/b": "profiles/a"},
		},
		{
			name:    "a key with a slash or a tilde in its origin",
			pack:    newPack(base(permissions(`{"permission": {"edit": {"src/**": "allow", "~/x": "deny"}}}`))),
			path:    ".rulesync/permissions.jsonc",
			want:    "{\n  \"permission\": {\n    \"edit\": {\n      \"src/**\": \"allow\",\n      \"~/x\": \"deny\"\n    }\n  }\n}\n",
			origins: map[string]string{"/permission/edit/src~1**": "base", "/permission/edit/~0~1x": "base"},
		},
		{
			name:    "identical items in the first layer, once",
			pack:    newPack(base(hooks(`{"hooks": {"stop": [{"command": "a"}, {"command": "a"}, 1, 1.0, 1e0]}}`))),
			path:    ".rulesync/hooks.jsonc",
			want:    "{\n  \"hooks\": {\n    \"stop\": [\n      {\n        \"command\": \"a\"\n      },\n      1\n    ]\n  }\n}\n",
			origins: map[string]string{"/hooks/stop/0": "base", "/hooks/stop/1": "base"},
		},
		{
			name: "numbers that are equal, whatever their spelling",
			pack: newPack(
				base(hooks(`{"version": 1, "hooks": {"stop": [{"command": "a", "timeout": 10}]}}`)),
				variant("v", hooks(`{"version": 1.0, "hooks": {"stop": [{"timeout": 1e1, "command": "a"}, {"command": "a", "timeout": 10.5}]}}`)),
			),
			path: ".rulesync/hooks.jsonc",
			want: "{\n  \"version\": 1,\n  \"hooks\": {\n    \"stop\": [\n      {\n        \"command\": \"a\",\n        \"timeout\": 10\n      },\n" +
				"      {\n        \"command\": \"a\",\n        \"timeout\": 10.5\n      }\n    ]\n  }\n}\n",
			origins: map[string]string{"/version": "base", "/hooks/stop/0": "base", "/hooks/stop/1": "variants/v"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s, diags := merge.Stage(tt.pack, tt.sel)
			if len(diags) != 0 {
				t.Fatalf("Stage() diagnostics = %v", format(diags))
			}
			files := s.User
			if tt.project {
				files = s.Project
			}
			f := staged(t, files, tt.path)
			if string(f.Data) != tt.want {
				t.Errorf("data =\n%s\nwant\n%s", f.Data, tt.want)
			}
			if f.Layer != "" || !reflect.DeepEqual(f.Origins, tt.origins) {
				t.Errorf("layer, origins = %q, %v, want \"\", %v", f.Layer, f.Origins, tt.origins)
			}
		})
	}
}

func TestCheckListFiles(t *testing.T) {
	tests := []struct {
		name string
		pack *pack.Pack
		want []string
	}{
		{
			name: "the same entry in two layers",
			pack: newPack(base(permissions(`{"permission": {"bash": {"git *": "allow"}}}`), hooks(`{"version": 1}`)),
				variant("v", permissions(`{"permission": {"bash": {"git *": "allow"}}}`), hooks(`{"version": 1}`))),
		},
		{
			name: "variants are alternatives, so they never clash",
			pack: newPack(base(permissions(`{"permission": {}}`)),
				variant("a", permissions(`{"permission": {"bash": {"x": "allow"}}}`)),
				variant("b", permissions(`{"permission": {"bash": {"x": "deny"}}}`))),
		},
		{
			name: "a later layer changes a value",
			pack: newPack(
				base(permissions("{\"permission\": {\n  \"bash\": {\n    \"git *\": \"allow\"\n  }\n}}"), hooks(`{"version": 1, "hooks": {"stop": []}}`)),
				variant("v", permissions("{\"permission\": {\"bash\": {\n\"git *\": \"deny\"}}}"), hooks("{\n\"version\": 2,\n\"hooks\": {\"stop\": {}}}")),
			),
			want: []string{
				`variants/v/.rulesync/permissions.jsonc:2: permissions-conflict: /permission/bash/git * already has a different value in base (first: base/.rulesync/permissions.jsonc:3) (fix: keep the same value; a layer cannot change or remove what an earlier layer of its scope sets)`,
				`variants/v/.rulesync/hooks.jsonc:2: hooks-conflict: /version already has a different value in base (first: base/.rulesync/hooks.jsonc:1) (fix: keep the same value; a layer cannot change or remove what an earlier layer of its scope sets)`,
				`variants/v/.rulesync/hooks.jsonc:3: hooks-conflict: /hooks/stop already has a different value in base (first: base/.rulesync/hooks.jsonc:1) (fix: keep the same value; a layer cannot change or remove what an earlier layer of its scope sets)`,
			},
		},
		{
			name: "two profiles change a value",
			pack: newPack(profile("a", permissions(`{"permission": {"bash": {"x": "allow"}}}`)), profile("b", permissions(`{"permission": {"bash": {"x": "ask"}}}`))),
			want: []string{
				`profiles/b/.rulesync/permissions.jsonc:1: permissions-conflict: /permission/bash/x already has a different value in profiles/a (first: profiles/a/.rulesync/permissions.jsonc:1) (fix: keep the same value; a layer cannot change or remove what an earlier layer of its scope sets)`,
			},
		},
		{
			name: "files that are not a JSONC object",
			pack: newPack(base(permissions(`{"permission": [`), hooks(`[]`)),
				variant("v", permissions(`{"permission": {"bash": {"x": "allow", "x": "deny"}}}`))),
			want: []string{
				`base/.rulesync/permissions.jsonc:1:17: permissions-invalid: parsing value: unexpected EOF`,
				`base/.rulesync/hooks.jsonc: hooks-invalid: want an object`,
				`variants/v/.rulesync/permissions.jsonc:1: permissions-invalid: duplicate key /permission/bash/x`,
			},
		},
		{
			name: "a parse error on a later line",
			pack: newPack(base(hooks("{\n  \"version\": 1,\n  \"hooks\": {]\n}\n"))),
			want: []string{`base/.rulesync/hooks.jsonc:3:13: hooks-invalid: invalid character ']' at start of value`},
		},
		{
			name: "a duplicate key in a file of one layer",
			pack: newPack(base(hooks("{\n  \"version\": 1,\n  \"version\": 1\n}\n"))),
			want: []string{`base/.rulesync/hooks.jsonc:3: hooks-invalid: duplicate key /version`},
		},
		{
			name: "numbers too large to normalize compare by their literal",
			pack: newPack(base(hooks(`{"a": 1e999999999, "b": 1e600000000}`)), variant("v", hooks(`{"a": 1e999999999, "b": 1e600000000}`))),
		},
		{
			name: "numbers that differ in value conflict",
			pack: newPack(base(hooks(`{"version": 1}`)), variant("v", hooks(`{"version": 1.5e0}`))),
			want: []string{
				`variants/v/.rulesync/hooks.jsonc:1: hooks-conflict: /version already has a different value in base (first: base/.rulesync/hooks.jsonc:1) (fix: keep the same value; a layer cannot change or remove what an earlier layer of its scope sets)`,
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := format(merge.Check(tt.pack, merge.Selection{}))
			want := tt.want
			if want == nil {
				want = []string{}
			}
			if !reflect.DeepEqual(got, want) {
				t.Errorf("Check() =\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
			}
		})
	}
}

// TestWarningsForShadowedHooks checks the warning for a hook event that
// both the shared hooks and a tool-scoped block of hooks.jsonc declare:
// rulesync 43 renders only the block's hooks of that event for that
// harness. It names the event, the block and both files, once per problem
// across variants, and it is no error.
func TestWarningsForShadowedHooks(t *testing.T) {
	shared := `{"hooks": {"sessionStart": [{"command": "a"}], "stop": [{"command": "s"}]}}`
	tests := []struct {
		name string
		pack *pack.Pack
		want []string
	}{
		{"no tool-scoped block", newPack(base(hooks(shared))), nil},
		{"another event in the block", newPack(base(hooks(`{"hooks": {"stop": [{"command": "s"}]}, "claudecode": {"hooks": {"sessionStart": [{"command": "c"}]}}}`))), nil},
		{
			"the same event in one file",
			newPack(base(hooks("{\n  \"hooks\": {\"sessionStart\": [{\"command\": \"a\"}]},\n  \"claudecode\": {\n    \"hooks\": {\"sessionStart\": [{\"command\": \"c\"}]}\n  }\n}\n"))),
			[]string{"base/.rulesync/hooks.jsonc:4: hooks-shadowed: claudecode.hooks.sessionStart replaces the shared hooks.sessionStart for claudecode (first: base/.rulesync/hooks.jsonc:2)"},
		},
		{
			"the block in a variant, two harnesses, with two variants",
			newPack(base(hooks(shared)),
				variant("a", hooks(`{"antigravity-cli": {"hooks": {"sessionStart": [{"command": "g"}]}}, "opencode": {"hooks": {"stop": [{"command": "o"}]}}}`)),
				variant("b")),
			[]string{
				"variants/a/.rulesync/hooks.jsonc:1: hooks-shadowed: antigravity-cli.hooks.sessionStart replaces the shared hooks.sessionStart for antigravity-cli (first: base/.rulesync/hooks.jsonc:1)",
				"variants/a/.rulesync/hooks.jsonc:1: hooks-shadowed: opencode.hooks.stop replaces the shared hooks.stop for opencode (first: base/.rulesync/hooks.jsonc:1)",
			},
		},
		{
			"in the project scope, the shared hooks from another profile",
			newPack(profile("p", hooks(shared)), profile("q", hooks(`{"claudecode": {"hooks": {"stop": [{"command": "c"}]}}}`))),
			[]string{"profiles/q/.rulesync/hooks.jsonc:1: hooks-shadowed: claudecode.hooks.stop replaces the shared hooks.stop for claudecode (first: profiles/p/.rulesync/hooks.jsonc:1)"},
		},
		{"an empty shared event, which loses nothing", newPack(base(hooks(`{"hooks": {"stop": []}, "claudecode": {"hooks": {"stop": [{"command": "c"}]}}}`))), nil},
		{
			"an empty event in the block, which drops the shared hooks",
			newPack(base(hooks(`{"hooks": {"stop": [{"command": "s"}]}, "claudecode": {"hooks": {"stop": []}}}`))),
			[]string{"base/.rulesync/hooks.jsonc:1: hooks-shadowed: claudecode.hooks.stop replaces the shared hooks.stop for claudecode (first: base/.rulesync/hooks.jsonc:1)"},
		},
		{"the user and the project scope apart", newPack(base(hooks(shared)), profile("p", hooks(`{"claudecode": {"hooks": {"stop": [{"command": "c"}]}}}`))), nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if diags := merge.Check(tt.pack, merge.Selection{}); len(diags) != 0 {
				t.Fatalf("Check() = %q, want no error", format(diags))
			}
			ws := merge.Warnings(tt.pack, merge.Selection{})
			for i, w := range ws {
				if !strings.Contains(w.Hint, "rulesync renders only one of them") {
					t.Errorf("warning %d hint = %q, want the reason", i, w.Hint)
				}
				ws[i].Hint = ""
			}
			if got, want := format(ws), append([]string{}, tt.want...); !reflect.DeepEqual(got, want) {
				t.Errorf("Warnings() =\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
			}
		})
	}
}
