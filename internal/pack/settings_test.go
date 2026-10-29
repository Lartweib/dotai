package pack_test

import (
	"cmp"
	"reflect"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"

	"github.com/lartweib/dotai/internal/pack"
)

const settings = "base/dotai/settings.yaml"

func TestLoadSettingsDiagnostics(t *testing.T) {
	const profile = "profiles/web/dotai/settings.yaml"
	tests := []struct {
		name string
		file string // settings.yaml to write, default the base one
		src  string
		want []string // "file:line:col: code: message", file implied
	}{
		{name: "empty file", src: ""},
		{name: "syntax error", src: "claudecode:\n  model: a: b\n", want: []string{"2:0: yaml-invalid: mapping values are not allowed in this context"}},
		{name: "not a mapping", src: "- claudecode\n", want: []string{"1:1: field-type: settings.yaml must be a mapping"}},
		{name: "comment-only file is an empty layer", src: "# nothing yet\n"},
		{name: "bare document marker is an empty layer", src: "---\n"},
		{name: "top-level scalar", src: "sonnet\n", want: []string{"1:1: field-type: settings.yaml must be a mapping"}},
		{name: "null root", src: "~\n", want: []string{"1:1: settings-value-invalid: settings.yaml: null is not allowed: a layer cannot unset a key"}},
		{name: "two documents", src: "claudecode: {}\n---\nopencode: {}\n", want: []string{"2:1: yaml-invalid: only one YAML document is allowed"}},
		{
			name: "merge keys, anchored keys and tagged collections",
			src:  "claudecode:\n  <<: {a: 1}\n  &k b: 1\n  c: !set {x: 1}\n  d: !!seq [1]\n  e: !!map {}\n  \"<<\": quoted\nopencode:\n  <<: *k\n",
			want: []string{
				"2:3: settings-value-invalid: claudecode: merge keys are not allowed",
				"3:3: settings-value-invalid: claudecode.b: YAML anchors and aliases are not allowed",
				"4:6: settings-value-invalid: claudecode.c: tag !set is not allowed",
				"9:3: settings-value-invalid: opencode: merge keys are not allowed",
			},
		},
		{
			name: "unknown harness and harness outside the manifest",
			src:  "vscode: {}\nantigravity-cli: {}\n",
			want: []string{
				`1:1: harness-unknown: unknown harness "vscode", want one of [claudecode opencode] or dotai`,
				`2:1: harness-unknown: harness "antigravity-cli" is not in the manifest, want one of [claudecode opencode] or dotai`,
			},
		},
		{name: "harness block is not a mapping", src: "claudecode: sonnet\n", want: []string{"1:13: field-type: claudecode must be a mapping"}},
		{
			name: "agy settings in a profile",
			file: profile,
			src:  "antigravity-cli:\n  model: flash\n",
			want: []string{"1:1: settings-scope: profiles cannot carry antigravity-cli settings: agy has no project settings file"},
		},
		{
			name: "anchors and aliases",
			src:  "claudecode:\n  a: &x 1\n  b: *x\n",
			want: []string{
				"2:6: settings-value-invalid: claudecode.a: YAML anchors and aliases are not allowed",
				"3:6: settings-value-invalid: claudecode.b: YAML anchors and aliases are not allowed",
			},
		},
		{
			name: "null values cannot unset a key",
			src:  "claudecode:\n  model: ~\n  list: [a, null]\n",
			want: []string{
				"2:10: settings-value-invalid: claudecode.model: null is not allowed: a layer cannot unset a key",
				"3:13: settings-value-invalid: claudecode.list[1]: null is not allowed: a layer cannot unset a key",
			},
		},
		{
			name: "keys that are not strings",
			src:  "claudecode:\n  1: a\n  env: {true: b}\n",
			want: []string{
				"2:3: settings-value-invalid: claudecode: key 1 must be a string",
				"3:9: settings-value-invalid: claudecode.env: key true must be a string",
			},
		},
		{
			name: "numbers that are not finite and unknown tags",
			src:  "opencode:\n  a: .inf\n  b: [-.Inf, .NaN, !!float 1e400, 1.5, 1e400]\n  c: !secret x\n",
			want: []string{
				"2:6: settings-value-invalid: opencode.a: .inf is not a finite number",
				"3:7: settings-value-invalid: opencode.b[0]: -.Inf is not a finite number",
				"3:14: settings-value-invalid: opencode.b[1]: .NaN is not a finite number",
				"3:20: settings-value-invalid: opencode.b[2]: 1e400 is not a finite number",
				"4:6: settings-value-invalid: opencode.c: tag !secret is not allowed",
			},
		},
		{
			name: "integers that do not fit 64 bits",
			src:  "opencode:\n  a: [18446744073709551615, -9223372036854775808, 0x1F]\n  b: 18446744073709551616\n  c: -9223372036854775809\n",
			want: []string{
				"3:6: settings-value-invalid: opencode.b: 18446744073709551616 does not fit a 64-bit integer",
				"4:6: settings-value-invalid: opencode.c: -9223372036854775809 does not fit a 64-bit integer",
			},
		},
		{
			name: "duplicate keys at any depth",
			src:  "claudecode:\n  env:\n    A: 1\n    A: 2\nclaudecode: {}\n",
			want: []string{
				`4:5: field-duplicate: claudecode.env: duplicate field "A"`,
				`5:1: field-duplicate: settings.yaml: duplicate field "claudecode"`,
			},
		},
		{
			name: "dotai block fields",
			src:  "dotai:\n  model: x\n  overrides: claudecode.model\n",
			want: []string{
				`2:3: field-unknown: dotai: unknown field "model"`,
				`3:14: field-type: dotai: "overrides" must be a list`,
			},
		},
		{name: "dotai block is not a mapping", src: "dotai: [x]\n", want: []string{"1:8: field-type: dotai must be a mapping"}},
		{
			name: "claudecode env merges per variable",
			src:  "claudecode:\n  env: [A]\nopencode:\n  env: x\n",
			want: []string{"2:8: field-type: claudecode.env must be a mapping: it merges per variable"},
		},
		{
			name: "top-level keys with a dot",
			src:  "claudecode:\n  env.A: x\n  env: {B.C: y}\nopencode:\n  a.b: {c.d: z}\n",
			want: []string{
				`2:3: settings-value-invalid: claudecode: key "env.A" must not contain a dot: a settings key is a name`,
				`5:3: settings-value-invalid: opencode: key "a.b" must not contain a dot: a settings key is a name`,
			},
		},
		{
			name: "malformed override paths",
			src: "claudecode:\n  model: a\n  env: {A: b}\nopencode:\n  env: {}\ndotai:\n  overrides:\n" +
				"    - claudecode\n    - .model\n    - claudecode.model.alias\n    - claudecode.env\n    - opencode.env.A\n    - 1\n",
			want: []string{
				`8:7: override-path-invalid: override "claudecode" must be <harness>.<key> or claudecode.env.<VAR>`,
				`9:7: override-path-invalid: override ".model" must be <harness>.<key> or claudecode.env.<VAR>`,
				`10:7: override-path-invalid: override "claudecode.model.alias" must be <harness>.<key> or claudecode.env.<VAR>`,
				`11:7: override-path-invalid: override "claudecode.env" must name one variable, as claudecode.env.<VAR>`,
				`12:7: override-path-invalid: override "opencode.env.A" must be <harness>.<key> or claudecode.env.<VAR>`,
				`13:7: field-type: dotai: "overrides" items must be strings`,
			},
		},
		{
			name: "overrides of keys the layer does not set, and duplicates",
			src:  "claudecode:\n  model: a\n  env: {A: b}\ndotai:\n  overrides: [claudecode.statusLine, claudecode.env.B, opencode.model, claudecode.model, claudecode.model]\n",
			want: []string{
				`5:15: override-key-unset: override "claudecode.statusLine" names a key this layer does not set`,
				`5:38: override-key-unset: override "claudecode.env.B" names a key this layer does not set`,
				`5:56: override-key-unset: override "opencode.model" names a key this layer does not set`,
				`5:90: field-duplicate: dotai: duplicate override "claudecode.model"`,
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fsys := validPack()
			name := cmp.Or(tt.file, settings)
			if tt.file == profile {
				editManifest("[claudecode, opencode]", "[claudecode, antigravity-cli]")(fsys)
			}
			fsys[name] = file(tt.src)
			_, diags, err := pack.Load(fsys)
			if err != nil {
				t.Fatalf("Load() error = %v", err)
			}
			var want []string
			for _, w := range tt.want {
				want = append(want, name+":"+w)
			}
			if got := format(diags); !reflect.DeepEqual(got, want) {
				t.Errorf("diagnostics =\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
			}
		})
	}
}

// keys lists the keys of mapping n, in order.
func keys(n *yaml.Node) string {
	var ks []string
	for i := 0; i < len(n.Content); i += 2 {
		ks = append(ks, n.Content[i].Value)
	}
	return strings.Join(ks, ",")
}

func TestLoadSettings(t *testing.T) {
	fsys := validPack()
	editManifest("[claudecode, opencode]", "[claudecode, antigravity-cli, opencode]")(fsys)
	fsys[settings] = file(`opencode:
  model: anthropic/claude-sonnet-4-5
claudecode:
  statusLine: { type: command, command: x, padding: 0 }
  env: { ZED: "1", ALPHA: "2" }
  model: sonnet
antigravity-cli:
  statusLine: { type: command }
dotai:
  overrides: [claudecode.model, claudecode.env.ZED]
`)
	p, diags, err := pack.Load(fsys)
	if err != nil || len(diags) != 0 {
		t.Fatalf("Load() = %v, %v, want no diagnostics", diags, err)
	}
	s := p.Layers[0].Settings
	if s == nil {
		t.Fatal("base settings = nil")
	}
	var got []string
	for _, b := range s.Blocks {
		got = append(got, b.Harness+":"+keys(b.Value))
	}
	want := []string{"opencode:model", "claudecode:statusLine,env,model", "antigravity-cli:statusLine"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("blocks = %q, want %q", got, want)
	}
	if env := keys(s.Blocks[1].Value.Content[3]); env != "ZED,ALPHA" {
		t.Errorf("claudecode.env keys = %q, want file order", env)
	}
	wantOverrides := []pack.Override{
		{Harness: "claudecode", Key: "model", Line: 10, Column: 15},
		{Harness: "claudecode", Key: "env", Var: "ZED", Line: 10, Column: 33},
	}
	if !reflect.DeepEqual(s.Overrides, wantOverrides) {
		t.Errorf("overrides = %+v, want %+v", s.Overrides, wantOverrides)
	}
	if p.Layers[1].Settings != nil {
		t.Errorf("profile web has no settings.yaml, got %+v", p.Layers[1].Settings)
	}
}
