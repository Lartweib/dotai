package merge_test

import (
	"reflect"
	"strings"
	"testing"

	"github.com/lartweib/dotai/internal/merge"
	"github.com/lartweib/dotai/internal/pack"
)

// modsPack has the profiles web and docs, which both use the mod panel,
// and docs also the mod notes, which ships its own plugin manifest.
func modsPack() *pack.Pack {
	p := newPack(base(rule("overview").asRoot()), profile("web", rule("web")), profile("docs", skill("guide")), profile("plain", rule("plain")))
	p.Manifest.Name, p.Manifest.Version = "test-pack", "1.2.3"
	p.Manifest.Profiles[0].Mods = []string{"panel"}
	p.Manifest.Profiles[1].Mods = []string{"panel", "notes"}
	p.Mods = []pack.Mod{
		{Name: "notes", Path: "mods/notes", Files: []pack.File{
			{Path: "mods/notes/.claude-plugin/plugin.json", Rel: ".claude-plugin/plugin.json", Data: []byte(`{"name":"notes","version":"9.0.0"}`)},
			{Path: "mods/notes/hooks/hooks.json", Rel: "hooks/hooks.json", Data: []byte(`{"modules":["./r.ts"]}`)},
			{Path: "mods/notes/hooks/r.ts", Rel: "hooks/r.ts", Data: []byte("r"), Exec: true},
		}},
		{Name: "panel", Path: "mods/panel", Files: []pack.File{
			{Path: "mods/panel/agents/helper.md", Rel: "agents/helper.md", Data: []byte("a")},
			{Path: "mods/panel/hooks/hooks.json", Rel: "hooks/hooks.json", Data: []byte(`{"modules":["./register.js"],"hooks":{"SubagentStart":[{"matcher":"helper"}]}}`)},
			{Path: "mods/panel/hooks/register.js", Rel: "hooks/register.js", Data: []byte("x")},
		}},
	}
	return p
}

func TestStagePlacesTheModsOfTheProfiles(t *testing.T) {
	p := modsPack()
	for _, tt := range []struct {
		name     string
		profiles []string
		want     []string
	}{
		{name: "one profile", profiles: []string{"web"}, want: []string{
			".rulesync/rules/web.md profiles/web",
			"mods/panel/.claude-plugin/plugin.json dotai",
			"mods/panel/agents/helper.md mods/panel",
			"mods/panel/hooks/hooks.json mods/panel",
			"mods/panel/hooks/register.js mods/panel",
		}},
		{name: "a mod two profiles name is staged once", profiles: []string{"web", "docs"}, want: []string{
			".rulesync/rules/web.md profiles/web",
			".rulesync/skills/guide/SKILL.md profiles/docs",
			"mods/notes/.claude-plugin/plugin.json mods/notes",
			"mods/notes/hooks/hooks.json mods/notes",
			"mods/notes/hooks/r.ts mods/notes",
			"mods/panel/.claude-plugin/plugin.json dotai",
			"mods/panel/agents/helper.md mods/panel",
			"mods/panel/hooks/hooks.json mods/panel",
			"mods/panel/hooks/register.js mods/panel",
		}},
		{name: "no mods", profiles: []string{"plain"}, want: []string{".rulesync/rules/plain.md profiles/plain"}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			s, diags := merge.Stage(p, merge.Selection{Profiles: tt.profiles})
			if len(diags) != 0 {
				t.Fatalf("Stage() diagnostics = %v", format(diags))
			}
			var got []string
			data := map[string]string{}
			for _, f := range s.Project {
				got = append(got, f.Path+" "+f.Layer+f.Generator)
				data[f.Path] = string(f.Data)
				if f.Path == "mods/notes/hooks/r.ts" && !f.Exec {
					t.Errorf("%s lost its executable bit", f.Path)
				}
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("project =\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(tt.want, "\n"))
			}
			if m, ok := data["mods/panel/.claude-plugin/plugin.json"]; ok && m != "{\n  \"name\": \"panel\",\n  \"version\": \"1.2.3\",\n  \"description\": \"The mod panel of the pack test-pack.\"\n}\n" {
				t.Errorf("panel's plugin.json = %q, want its name, the pack's version and a description", m)
			}
			if m, ok := data["mods/notes/.claude-plugin/plugin.json"]; ok && m != `{"name":"notes","version":"9.0.0"}` {
				t.Errorf("notes' plugin.json = %q, want the one it ships", m)
			}
		})
	}
	if s, _ := merge.Stage(p, merge.Selection{}); len(s.User) == 0 || strings.Contains(strings.Join(files(s.User), "\n"), "mods/") {
		t.Errorf("the user scope = %v, want no mods", files(s.User))
	}
}

func TestStageWritesThePacksDescriptionAndAuthorIntoAModsPluginManifest(t *testing.T) {
	p := modsPack()
	p.Manifest.Description, p.Manifest.Author = "Web tooling.", "Ann Example"
	s, diags := merge.Stage(p, merge.Selection{Profiles: []string{"web"}})
	if len(diags) != 0 {
		t.Fatalf("Stage() diagnostics = %v", format(diags))
	}
	want := "{\n  \"name\": \"panel\",\n  \"version\": \"1.2.3\",\n  \"description\": \"Web tooling.\",\n  \"author\": {\n    \"name\": \"Ann Example\"\n  }\n}\n"
	for _, f := range s.Project {
		if f.Path == "mods/panel/.claude-plugin/plugin.json" && string(f.Data) != want {
			t.Errorf("plugin.json =\n%s\nwant\n%s", f.Data, want)
		}
	}
}

func TestCheckRejectsAModNamedAsAProjectSkill(t *testing.T) {
	p := modsPack()
	p.Mods[1].Name, p.Mods[1].Path = "guide", "mods/guide"
	p.Manifest.Profiles[0].Mods = []string{"guide"}
	p.Manifest.Profiles[1].Mods = nil
	want := []string{`dotai.yaml: mod-conflict: mod "guide" installs to the same .claude/skills/guide/ as skill "guide" of profile docs (first: profiles/docs/.rulesync/skills/guide/SKILL.md) (fix: rename the mod or the skill)`}
	if got := format(merge.Check(p, merge.Selection{})); !reflect.DeepEqual(got, want) {
		t.Errorf("Check() = %q, want %q", got, want)
	}
	if _, diags := merge.Stage(p, merge.Selection{Profiles: []string{"web", "docs"}}); !reflect.DeepEqual(format(diags), want) {
		t.Errorf("Stage() = %q, want %q", format(diags), want)
	}
	if _, diags := merge.Stage(p, merge.Selection{Profiles: []string{"web"}}); len(diags) != 0 {
		t.Errorf("Stage() of web alone = %q, want none: docs is not applied", format(diags))
	}
}

func TestWarningsOfTheModsOfTheProfiles(t *testing.T) {
	p := modsPack()
	want := []string{`mods/panel/hooks/hooks.json: mod-matcher-bare: SubagentStart matcher "helper" names the mod's agent helper bare; Claude Code names it panel:helper (fix: match ^panel:helper$)`}
	if got := format(merge.Warnings(p, merge.Selection{})); !reflect.DeepEqual(got, want) {
		t.Errorf("Warnings() = %q, want %q", got, want)
	}
	if got := format(merge.Warnings(p, merge.Selection{Profiles: []string{"plain"}})); len(got) != 0 {
		t.Errorf("Warnings() of plain = %q, want none", got)
	}
}
