package merge

import (
	"encoding/json"
	"path"
	"slices"
	"strings"

	"github.com/lartweib/dotai/internal/pack"
)

// CodeModConflict is a mod whose name a skill of the project scope has:
// both would install to .claude/skills/<name>/.
const CodeModConflict = "mod-conflict"

// ModsDir is where the project tree holds each mod, mods/<mod>/, which a
// project apply installs to Claude Code's .claude/skills/<mod>/ (decision
// 0027).
const ModsDir = pack.ModsDir

// modsOf are the mods the profiles name, each once, in name order.
func modsOf(p *pack.Pack, profiles []pack.Layer) []pack.Mod {
	var out []pack.Mod
	for _, pr := range p.Manifest.Profiles {
		if !slices.ContainsFunc(profiles, func(l pack.Layer) bool { return l.Name == pr.Name }) {
			continue
		}
		for _, name := range pr.Mods {
			i := slices.IndexFunc(p.Mods, func(m pack.Mod) bool { return m.Name == name })
			if i >= 0 && !slices.ContainsFunc(out, func(m pack.Mod) bool { return m.Name == name }) {
				out = append(out, p.Mods[i])
			}
		}
	}
	slices.SortFunc(out, func(a, b pack.Mod) int { return strings.Compare(a.Name, b.Name) })
	return out
}

// modConflicts reports each mod whose name a skill of the project scope s
// has.
func modConflicts(s *scope, mods []pack.Mod) []pack.Diagnostic {
	var out []pack.Diagnostic
	for _, m := range mods {
		if o, ok := s.names[key(string(pack.ItemSkill), m.Name)]; ok {
			out = append(out, pack.NewDiagnostic(pack.ManifestFile, fileLine, 0, CodeModConflict, "mod %q installs to the same .claude/skills/%s/ as %s of profile %s", m.Name, m.Name, o.what, o.layer.Name).
				WithRelated(o.file, fileLine).WithHint("rename the mod or the skill"))
		}
	}
	return out
}

// modFiles are the files of mods in the project tree, under mods/<mod>/,
// as the pack ships them, with .claude-plugin/plugin.json, the mod's name
// and the pack's version, for a mod that ships none.
func modFiles(mods []pack.Mod, version string) []File {
	var out []File
	for _, m := range mods {
		for _, f := range m.Files {
			out = append(out, File{Path: path.Join(ModsDir, m.Name, f.Rel), Layer: m.Path, Data: f.Data, Exec: f.Exec})
		}
		if _, ok := m.File(pack.ModManifest); !ok {
			// The names are plain strings, so this cannot fail.
			data, _ := json.MarshalIndent(struct {
				Name    string `json:"name"`
				Version string `json:"version"`
			}{m.Name, version}, "", "  ")
			out = append(out, File{Path: path.Join(ModsDir, m.Name, pack.ModManifest), Layer: m.Path, Data: append(data, '\n')})
		}
	}
	slices.SortFunc(out, func(a, b File) int { return strings.Compare(a.Path, b.Path) })
	return out
}

// modWarnings are the warnings of mods.
func modWarnings(mods []pack.Mod) []pack.Diagnostic {
	var out []pack.Diagnostic
	for _, m := range mods {
		out = append(out, m.Warnings()...)
	}
	return out
}
