package merge_test

import (
	"reflect"
	"strings"
	"testing"

	"github.com/lartweib/dotai/internal/merge"
	"github.com/lartweib/dotai/internal/pack"
)

// src is one file of a test layer; kind is empty for a file that is not an
// item.
type src struct {
	rel             string // path inside the layer
	kind            pack.ItemKind
	name            string
	overrides, root bool
}

func rule(name string) src {
	return src{rel: ".rulesync/rules/" + name + ".md", kind: pack.ItemRule, name: name}
}
func command(name string) src {
	return src{rel: ".rulesync/commands/" + name + ".md", kind: pack.ItemCommand, name: name}
}
func skill(name string) src {
	return src{rel: ".rulesync/skills/" + name + "/SKILL.md", kind: pack.ItemSkill, name: name}
}
func subagent(rel, name string) src {
	return src{rel: ".rulesync/subagents/" + rel, kind: pack.ItemSubagent, name: name}
}
func plain(rel string) src  { return src{rel: rel} }
func (s src) override() src { s.overrides = true; return s }
func (s src) asRoot() src   { s.root = true; return s }

func layer(kind pack.LayerKind, name string, srcs ...src) pack.Layer {
	dir := map[pack.LayerKind]string{pack.LayerBase: "base", pack.LayerProfile: "profiles/" + name, pack.LayerVariant: "variants/" + name}[kind]
	l := pack.Layer{Kind: kind, Name: name, Path: dir}
	for _, s := range srcs {
		p := dir + "/" + s.rel
		l.Files = append(l.Files, pack.File{Path: p, Data: []byte(p)})
		if s.kind != "" {
			l.Items = append(l.Items, pack.Item{Kind: s.kind, Name: s.name, Path: p, Root: s.root, Dotai: pack.Dotai{Overrides: s.overrides}})
		}
	}
	return l
}

func base(srcs ...src) pack.Layer                 { return layer(pack.LayerBase, "base", srcs...) }
func profile(name string, srcs ...src) pack.Layer { return layer(pack.LayerProfile, name, srcs...) }
func variant(name string, srcs ...src) pack.Layer { return layer(pack.LayerVariant, name, srcs...) }

// newPack declares every profile and variant of layers in the manifest. The
// first variant is the default.
func newPack(layers ...pack.Layer) *pack.Pack {
	p := &pack.Pack{Layers: layers}
	for _, l := range layers {
		switch l.Kind {
		case pack.LayerProfile:
			p.Manifest.Profiles = append(p.Manifest.Profiles, pack.Profile{Name: l.Name, Path: l.Path})
		case pack.LayerVariant:
			p.Manifest.Variants = append(p.Manifest.Variants, pack.Variant{Name: l.Name, Path: l.Path, Default: len(p.Manifest.Variants) == 0})
		}
	}
	return p
}

func format(ds []pack.Diagnostic) []string {
	out := []string{}
	for _, d := range ds {
		out = append(out, d.String())
	}
	return out
}

func TestCheck(t *testing.T) {
	tests := []struct {
		name string
		pack *pack.Pack
		sel  merge.Selection
		want []string
	}{
		{
			name: "distinct names",
			pack: newPack(base(rule("overview").asRoot(), skill("commit")), variant("v", rule("v")), profile("web", rule("web"))),
		},
		{
			name: "same name in different kinds",
			pack: newPack(base(rule("x"), command("x"), skill("x"), subagent("x.md", "x"))),
		},
		{
			name: "same name in two variants, which are never applied together",
			pack: newPack(base(), variant("a", rule("x")), variant("b", rule("x"))),
		},
		{
			name: "declared override in a variant",
			pack: newPack(base(rule("style")), variant("v", rule("style").override())),
		},
		{
			name: "undeclared override",
			pack: newPack(base(rule("style")), variant("v", rule("style"))),
			want: []string{`variants/v/.rulesync/rules/style.md:1: override-undeclared: rule "style" is already defined at base/.rulesync/rules/style.md:1; declare dotai: {overrides: true} to replace it`},
		},
		{
			name: "same name twice in one layer",
			pack: newPack(base(subagent("a/helper.md", "helper"), subagent("b/helper.md", "helper").override())),
			want: []string{`base/.rulesync/subagents/b/helper.md:1: name-duplicate: subagent "helper" is defined twice in layer base, first at base/.rulesync/subagents/a/helper.md:1`},
		},
		{
			name: "duplicate in the base is reported once for every variant",
			pack: newPack(base(command("x"), command("x")), variant("a"), variant("b")),
			want: []string{`base/.rulesync/commands/x.md:1: name-duplicate: command "x" is defined twice in layer base, first at base/.rulesync/commands/x.md:1`},
		},
		{
			name: "two profiles, even with overrides",
			pack: newPack(profile("a", skill("lint")), profile("b", skill("lint").override())),
			want: []string{`profiles/b/.rulesync/skills/lint/SKILL.md:1: profile-conflict: skill "lint" is also defined by profile a at profiles/a/.rulesync/skills/lint/SKILL.md:1; profiles cannot override each other`},
		},
		{
			name: "profile and base, even with overrides",
			pack: newPack(base(command("deploy")), profile("a", command("deploy").override())),
			want: []string{`profiles/a/.rulesync/commands/deploy.md:1: scope-conflict: command "deploy" is also defined at user level at base/.rulesync/commands/deploy.md:1; overrides do not apply across scopes`},
		},
		{
			name: "profile and variant",
			pack: newPack(base(), variant("v", skill("seo")), profile("a", skill("seo"))),
			want: []string{`profiles/a/.rulesync/skills/seo/SKILL.md:1: scope-conflict: skill "seo" is also defined at user level at variants/v/.rulesync/skills/seo/SKILL.md:1; overrides do not apply across scopes`},
		},
		{
			name: "root rule outside the base",
			pack: newPack(base(), variant("v", rule("main").asRoot()), profile("a", rule("app").asRoot())),
			want: []string{
				`variants/v/.rulesync/rules/main.md:1: root-rule-outside-base: rule "main" is a root rule; only the base may provide the root rule`,
				`profiles/a/.rulesync/rules/app.md:1: root-rule-outside-base: rule "app" is a root rule; only the base may provide the root rule`,
			},
		},
		{
			name: "same file path in two layers of a scope",
			pack: newPack(base(plain("dotai/settings.yaml")), variant("v", plain("dotai/settings.yaml"))),
			want: []string{`variants/v/dotai/settings.yaml:1: path-conflict: dotai/settings.yaml is also provided at base/dotai/settings.yaml:1; a file has one owner`},
		},
		{
			name: "same file path in two profiles",
			pack: newPack(profile("a", plain(".rulesync/mcp.jsonc")), profile("b", plain(".rulesync/mcp.jsonc"))),
			want: []string{`profiles/b/.rulesync/mcp.jsonc:1: path-conflict: .rulesync/mcp.jsonc is also provided at profiles/a/.rulesync/mcp.jsonc:1; a file has one owner`},
		},
		{
			name: "same file path in both scopes",
			pack: newPack(base(plain("dotai/settings.yaml")), profile("a", plain("dotai/settings.yaml"))),
		},
		{
			name: "chosen variant only",
			pack: newPack(base(rule("x")), variant("a"), variant("b", rule("x"))),
			sel:  merge.Selection{Variant: "a"},
		},
		{
			name: "chosen profiles only",
			pack: newPack(profile("a", rule("x")), profile("b", rule("x")), profile("c")),
			sel:  merge.Selection{Profiles: []string{"c", "a"}},
		},
		{
			name: "unknown variant and profiles",
			pack: newPack(base(), variant("v"), profile("a")),
			sel:  merge.Selection{Variant: "x", Profiles: []string{"a", "b", "a"}},
			want: []string{
				`dotai.yaml: layer-selection: unknown variant "x", want one of [v]`,
				`dotai.yaml: layer-selection: unknown profile "b", want one of [a]`,
				`dotai.yaml: layer-selection: profile "a" is selected twice`,
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := format(merge.Check(tt.pack, tt.sel))
			if tt.want == nil {
				tt.want = []string{}
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("Check() =\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(tt.want, "\n"))
			}
		})
	}
}

func files(fs []merge.File) []string {
	out := []string{}
	for _, f := range fs {
		if string(f.Data) != f.Layer+"/"+f.Path {
			out = append(out, "bad data for "+f.Path)
		}
		out = append(out, f.Path+" "+f.Layer)
	}
	return out
}

func TestStage(t *testing.T) {
	p := newPack(
		base(rule("overview").asRoot(), skill("commit"), plain(".rulesync/skills/commit/notes.md"), subagent("reviewer.md", "reviewer"), plain("dotai/settings.yaml"), plain("README.md")),
		profile("web", rule("web"), plain("dotai/tools/lint.sh")),
		profile("docs", command("publish")),
		variant("standalone", rule("standalone")),
		variant("tmux", rule("tmux"), skill("commit").override(), subagent("team/reviewer.md", "reviewer").override()),
	)
	tests := []struct {
		name          string
		sel           merge.Selection
		user, project []string
	}{
		{
			name: "default variant and no profiles",
			user: []string{
				".rulesync/rules/overview.md base",
				".rulesync/rules/standalone.md variants/standalone",
				".rulesync/skills/commit/SKILL.md base",
				".rulesync/skills/commit/notes.md base",
				".rulesync/subagents/reviewer.md base",
				"README.md base",
				"dotai/settings.yaml base",
			},
			project: []string{},
		},
		{
			name: "overriding variant and two profiles",
			sel:  merge.Selection{Variant: "tmux", Profiles: []string{"web", "docs"}},
			user: []string{
				".rulesync/rules/overview.md base",
				".rulesync/rules/tmux.md variants/tmux",
				".rulesync/skills/commit/SKILL.md variants/tmux",
				".rulesync/subagents/team/reviewer.md variants/tmux",
				"README.md base",
				"dotai/settings.yaml base",
			},
			project: []string{
				".rulesync/commands/publish.md profiles/docs",
				".rulesync/rules/web.md profiles/web",
				"dotai/tools/lint.sh profiles/web",
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s, diags := merge.Stage(p, tt.sel)
			if len(diags) != 0 {
				t.Fatalf("Stage() diagnostics = %v", format(diags))
			}
			if got := files(s.User); !reflect.DeepEqual(got, tt.user) {
				t.Errorf("user =\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(tt.user, "\n"))
			}
			if got := files(s.Project); !reflect.DeepEqual(got, tt.project) {
				t.Errorf("project =\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(tt.project, "\n"))
			}
		})
	}
}

func TestStageRejectsConflicts(t *testing.T) {
	p := newPack(base(rule("x")), variant("v"), profile("a", rule("x")))
	s, diags := merge.Stage(p, merge.Selection{Profiles: []string{"a"}})
	if len(diags) != 1 || diags[0].Code != merge.CodeScopeConflict {
		t.Errorf("diagnostics = %v, want one scope-conflict", format(diags))
	}
	if s.User != nil || s.Project != nil {
		t.Errorf("staging = %+v, want empty", s)
	}
}

func TestStageWithoutVariants(t *testing.T) {
	s, diags := merge.Stage(newPack(base(rule("x"))), merge.Selection{})
	if len(diags) != 0 || len(s.User) != 1 || s.User[0].Layer != "base" {
		t.Errorf("Stage() = %+v, %v, want the base only", s, format(diags))
	}
}
