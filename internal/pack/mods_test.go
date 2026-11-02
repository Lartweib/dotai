package pack_test

import (
	"reflect"
	"slices"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/lartweib/dotai/internal/pack"
)

// modPack is validPack with the mod panel, which the profile web uses.
func modPack() fstest.MapFS {
	f := validPack()
	editManifest("    hints: [astro.config.*]", "    hints: [astro.config.*]\n    mods: [panel]")(f)
	f["mods/panel/hooks/hooks.json"] = file(`{"modules": ["./register.js"]}`)
	f["mods/panel/hooks/register.js"] = file("export function register(on) {}\n")
	return f
}

func TestLoadMods(t *testing.T) {
	f := modPack()
	f["mods/panel/agents/helper.md"] = file("---\nname: helper\n---\n")
	f["mods/unused/hooks/hooks.json"] = file(`{"modules": ["../register.mjs"], "hooks": {}}`)
	f["mods/unused/register.mjs"] = file("export const register = () => {}\n")
	f["mods/unused/.claude-plugin/plugin.json"] = file(`{"name": "unused", "version": "2.0.0"}`)
	p, diags, err := pack.Load(f)
	if err != nil || len(diags) != 0 {
		t.Fatalf("Load() = %v, %v, want no diagnostics", format(diags), err)
	}
	if got := p.Manifest.Profiles[0].Mods; !reflect.DeepEqual(got, []string{"panel"}) {
		t.Errorf("web's mods = %v, want [panel]", got)
	}
	var names []string
	for _, m := range p.Mods {
		var rels []string
		for _, f := range m.Files {
			rels = append(rels, f.Rel)
		}
		names = append(names, m.Name+" "+m.Path+" "+strings.Join(rels, ","))
	}
	want := []string{
		"panel mods/panel agents/helper.md,hooks/hooks.json,hooks/register.js",
		"unused mods/unused .claude-plugin/plugin.json,hooks/hooks.json,register.mjs",
	}
	if !reflect.DeepEqual(names, want) {
		t.Errorf("mods = %q, want %q", names, want)
	}
}

func TestLoadModDiagnostics(t *testing.T) {
	const hooks = "mods/panel/hooks/hooks.json"
	tests := []struct {
		name   string
		mutate func(fstest.MapFS)
		want   []string
	}{
		{
			name:   "unknown mod",
			mutate: editManifest("mods: [panel]", "mods: [panel, nope]"),
			want:   []string{`dotai.yaml:12:19: mod-unknown: profile web: mod "nope" is not a directory under mods/`},
		},
		{
			name:   "mod name not safe for a folder and a plugin name",
			mutate: editManifest("mods: [panel]", "mods: [panel, ../x, Big]"),
			want: []string{
				`dotai.yaml:12:19: mod-name-invalid: profile web: mod name "../x" must be lower-case letters, digits and single hyphens, starting with a letter or a digit`,
				`dotai.yaml:12:25: mod-name-invalid: profile web: mod name "Big" must be lower-case letters, digits and single hyphens, starting with a letter or a digit`,
			},
		},
		{
			name:   "a mod named twice by one profile",
			mutate: editManifest("mods: [panel]", "mods: [panel, panel]"),
			want:   []string{`dotai.yaml:12:19: mod-unknown: profile web: mod "panel" is named twice`},
		},
		{
			name:   "a variant names a mod",
			mutate: editManifest("    default: true", "    default: true\n    mods: [panel]"),
			want:   []string{`dotai.yaml:17:5: mod-scope: variant standalone: mods are project-scoped, so only a profile names them`},
		},
		{
			name: "mod directory with an invalid name",
			mutate: func(f fstest.MapFS) {
				f["mods/My_Mod/hooks/hooks.json"] = file(`{"modules": ["./r.js"]}`)
				f["mods/My_Mod/hooks/r.js"] = file("")
			},
			want: []string{`mods/My_Mod:0:0: mod-name-invalid: mod name "My_Mod" must be lower-case letters, digits and single hyphens, starting with a letter or a digit`},
		},
		{
			name:   "a file in mods",
			mutate: setFile("mods/README.md", "x"),
			want:   []string{`mods/README.md:0:0: layer-not-directory: "mods/README.md" is not a directory`},
		},
		{
			name:   "mods is a link",
			mutate: func(f fstest.MapFS) { dropTree(f, "mods"); symlink("mods", "elsewhere")(f) },
			want: []string{
				`dotai.yaml:12:12: mod-unknown: profile web: mod "panel" is not a directory under mods/`,
				`mods:0:0: symlink-not-allowed: "mods" is a symbolic link; packs must not contain links`,
			},
		},
		{
			name:   "a link in a mod",
			mutate: symlink("mods/panel/hooks/extra.js", "../../x.js"),
			want:   []string{`mods/panel/hooks/extra.js:0:0: symlink-not-allowed: "mods/panel/hooks/extra.js" is a symbolic link; packs must not contain links`},
		},
		{
			name:   "no hooks.json",
			mutate: func(f fstest.MapFS) { delete(f, hooks) },
			want:   []string{`mods/panel:0:0: mod-hooks-invalid: mod panel has no hooks/hooks.json, which names its hooks module`},
		},
		{
			name:   "hooks.json is not a JSON object",
			mutate: setFile(hooks, `["./register.js"]`),
			want:   []string{hooks + `:0:0: mod-hooks-invalid: hooks/hooks.json must be a JSON object with "modules", a list of module paths`},
		},
		{
			name:   "no modules",
			mutate: setFile(hooks, `{"hooks": {}}`),
			want:   []string{hooks + `:0:0: mod-hooks-invalid: hooks/hooks.json must be a JSON object with "modules", a list of module paths`},
		},
		{
			name:   "empty modules",
			mutate: setFile(hooks, `{"modules": []}`),
			want:   []string{hooks + `:0:0: mod-hooks-invalid: hooks/hooks.json must be a JSON object with "modules", a list of module paths`},
		},
		{
			name:   "a module that is not a string",
			mutate: setFile(hooks, `{"modules": [1]}`),
			want:   []string{hooks + `:0:0: mod-hooks-invalid: hooks/hooks.json must be a JSON object with "modules", a list of module paths`},
		},
		{
			name:   "hooks that are not an object",
			mutate: setFile(hooks, `{"modules": ["./register.js"], "hooks": []}`),
			want:   []string{hooks + `:0:0: mod-hooks-invalid: "hooks" of hooks/hooks.json must be an object of hook events`},
		},
		{
			name:   "a module outside the mod",
			mutate: setFile(hooks, `{"modules": ["../../other/register.js", "/abs/r.js", "C:/r.js", ".\\register.js"]}`),
			want: []string{
				hooks + `:0:0: mod-module-invalid: module "../../other/register.js" must be a relative slash path inside the mod`,
				hooks + `:0:0: mod-module-invalid: module "/abs/r.js" must be a relative slash path inside the mod`,
				hooks + `:0:0: mod-module-invalid: module "C:/r.js" must be a relative slash path inside the mod`,
				hooks + `:0:0: mod-module-invalid: module ".\\register.js" must be a relative slash path inside the mod`,
			},
		},
		{
			name: "a module with another extension",
			mutate: func(f fstest.MapFS) {
				f[hooks] = file(`{"modules": ["./register.py"]}`)
				f["mods/panel/hooks/register.py"] = file("")
			},
			want: []string{hooks + `:0:0: mod-module-invalid: module "./register.py" must end in .js, .mjs, .cjs, .jsx, .ts, .mts, .cts or .tsx`},
		},
		{
			name:   "a missing module",
			mutate: setFile(hooks, `{"modules": ["./register.js", "./gone.ts"]}`),
			want:   []string{hooks + `:0:0: mod-module-invalid: module "./gone.ts" is not a file of the mod (mods/panel/hooks/gone.ts)`},
		},
		{
			name:   "a plugin manifest with another name",
			mutate: setFile("mods/panel/.claude-plugin/plugin.json", `{"name": "other"}`),
			want:   []string{`mods/panel/.claude-plugin/plugin.json:0:0: mod-manifest-invalid: .claude-plugin/plugin.json must be a JSON object whose "name", when set, is "panel", the mod's name`},
		},
		{
			name:   "a plugin manifest that is not an object",
			mutate: setFile("mods/panel/.claude-plugin/plugin.json", `[]`),
			want:   []string{`mods/panel/.claude-plugin/plugin.json:0:0: mod-manifest-invalid: .claude-plugin/plugin.json must be a JSON object whose "name", when set, is "panel", the mod's name`},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := modPack()
			tt.mutate(f)
			_, diags, err := pack.Load(f)
			if err != nil {
				t.Fatal(err)
			}
			if got := format(diags); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("diagnostics =\n%q\nwant\n%q", got, tt.want)
			}
		})
	}
}

func TestModWarnings(t *testing.T) {
	tests := []struct {
		name, hooks string
		want        []string
	}{
		{name: "bare agent name", hooks: `{"SubagentStart": [{"matcher": "helper", "hooks": []}]}`,
			want: []string{`mods/panel/hooks/hooks.json:0:0: mod-matcher-bare: SubagentStart matcher "helper" names the mod's agent helper bare; Claude Code names it panel:helper`}},
		{name: "in an alternation", hooks: `{"SubagentStop": [{"matcher": "other|helper"}]}`,
			want: []string{`mods/panel/hooks/hooks.json:0:0: mod-matcher-bare: SubagentStop matcher "other|helper" names the mod's agent helper bare; Claude Code names it panel:helper`}},
		{name: "plugin form", hooks: `{"SubagentStart": [{"matcher": "^panel:helper$"}]}`},
		{name: "another event", hooks: `{"PreToolUse": [{"matcher": "helper"}]}`},
		{name: "not an agent of the mod", hooks: `{"SubagentStart": [{"matcher": "reviewer"}]}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := modPack()
			f["mods/panel/agents/helper.md"] = file("---\nname: helper\n---\n")
			f["mods/panel/hooks/hooks.json"] = file(`{"modules": ["./register.js"], "hooks": ` + tt.hooks + `}`)
			p, diags, err := pack.Load(f)
			if err != nil || len(diags) != 0 {
				t.Fatalf("Load() = %v, %v", format(diags), err)
			}
			i := slices.IndexFunc(p.Mods, func(m pack.Mod) bool { return m.Name == "panel" })
			if got := format(p.Mods[i].Warnings()); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("warnings = %q, want %q", got, tt.want)
			}
		})
	}
}
