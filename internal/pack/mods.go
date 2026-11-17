package pack

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"path"
	"regexp"
	"slices"
	"strings"
)

// Mod diagnostic and warning codes.
const (
	CodeModUnknown         = "mod-unknown"
	CodeModDuplicate       = "mod-duplicate"
	CodeModPathInvalid     = "mod-path-invalid"
	CodeModPathCase        = "mod-path-case-conflict"
	CodeModNameInvalid     = "mod-name-invalid"
	CodeModScope           = "mod-scope"
	CodeModHooksInvalid    = "mod-hooks-invalid"
	CodeModModuleInvalid   = "mod-module-invalid"
	CodeModManifestInvalid = "mod-manifest-invalid"
	CodeModMatcherBare     = "mod-matcher-bare"
)

// Mod layout (docs/PACK-FORMAT.md): each mod is a directory of ModsDir,
// a Claude Code plugin with a hooks module, which a profile names
// (decisions 0011 and 0027).
const (
	ModsDir      = "mods"
	ModHooks     = "hooks/hooks.json"
	ModManifest  = ".claude-plugin/plugin.json"
	modAgentsDir = "agents/"
)

// ModExtensions are the file extensions a hooks module may have; Claude
// Code loads it with no build step.
var ModExtensions = []string{".js", ".mjs", ".cjs", ".jsx", ".ts", ".mts", ".cts", ".tsx"}

// modName is a name safe for a folder and for a plugin name: lower-case
// letters, digits and single hyphens.
var modName = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)

const modNameRule = "must be lower-case letters, digits and single hyphens, starting with a letter or a digit"

// modNameProblem says why name cannot name a mod, "" when it can.
func modNameProblem(name string) string {
	switch {
	case len(name) > 64 || !modName.MatchString(name):
		return modNameRule
	case BadName(name):
		return "is a device name Windows refuses as a folder"
	}
	return ""
}

// ModNameProblem says why name breaks the rule for mod names, "" when it
// follows it; a new local pack's name follows it too (decision 0044).
func ModNameProblem(name string) string { return modNameProblem(name) }

// Mod is a directory of the pack's mods/: a Claude Code plugin tree that a
// project apply installs whole, as the pack ships it.
type Mod struct {
	Name  string
	Path  string // relative to the pack root, mods/<name>
	Files []File // in lexical path order, Rel relative to the mod
}

// File gives the mod's file at rel, relative to the mod.
func (m Mod) File(rel string) (File, bool) {
	i := slices.IndexFunc(m.Files, func(f File) bool { return f.Rel == rel })
	if i < 0 {
		return File{}, false
	}
	return m.Files[i], true
}

// decodeMods reads the mods a profile names, each a directory of mods/
// with a valid name, once.
func decodeMods(fsys fs.FS, o object) []string {
	names, nodes := o.list("mods", false)
	var out []string
	for i, name := range names {
		switch problem := modNameProblem(name); {
		case problem != "":
			o.r.report(nodes[i], CodeModNameInvalid, "%s: mod name %q %s", o.what, name, problem)
		case slices.Contains(out, name):
			o.r.report(nodes[i], CodeModDuplicate, "%s: mod %q is named twice", o.what, name)
		default:
			if info, link, err := lstat(fsys, path.Join(ModsDir, name)); err != nil || link != "" || !info.IsDir() {
				o.r.report(nodes[i], CodeModUnknown, "%s: mod %q is not a directory under %s/", o.what, name, ModsDir)
				continue
			}
			out = append(out, name)
		}
	}
	return out
}

// loadMods reads every directory of mods/ without following links and
// checks each mod: its name, its files, its hooks/hooks.json and the
// modules it names, and its plugin manifest when it ships one.
func loadMods(fsys fs.FS, diags *[]Diagnostic) ([]Mod, error) {
	info, link, err := lstat(fsys, ModsDir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if code, msg := entryProblem(ModsDir, link, info.Mode(), true); code != "" {
		*diags = append(*diags, NewDiagnostic(ModsDir, 0, 0, code, "%s", msg))
		return nil, nil
	}
	entries, err := fs.ReadDir(fsys, ModsDir)
	if err != nil {
		return nil, err
	}
	var out []Mod
	for _, e := range entries {
		dir := path.Join(ModsDir, e.Name())
		if code, msg := entryProblem(dir, "", e.Type(), true); code != "" {
			*diags = append(*diags, NewDiagnostic(dir, 0, 0, code, "%s", msg))
			continue
		}
		if problem := modNameProblem(e.Name()); problem != "" {
			*diags = append(*diags, NewDiagnostic(dir, 0, 0, CodeModNameInvalid, "mod name %q %s", e.Name(), problem))
			continue
		}
		m := Mod{Name: e.Name(), Path: dir}
		err := fs.WalkDir(fsys, dir, func(p string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return err
			}
			if code, msg := entryProblem(p, "", d.Type(), false); code != "" {
				*diags = append(*diags, NewDiagnostic(p, 0, 0, code, "%s", msg))
				return nil
			}
			data, problem, err := readLimited(fsys, p)
			if err != nil {
				return err
			}
			if problem != nil {
				*diags = append(*diags, *problem)
				return nil
			}
			info, err := d.Info()
			if err != nil {
				return err
			}
			m.Files = append(m.Files, File{Path: p, Rel: strings.TrimPrefix(p, dir+"/"), Data: data, Exec: info.Mode()&0o111 != 0})
			return nil
		})
		if err != nil {
			return nil, err
		}
		*diags = append(*diags, m.checkPaths()...)
		*diags = append(*diags, m.check()...)
		out = append(out, m)
	}
	return out, nil
}

// checkPaths reports each file of the mod whose path some OS dotai
// supports cannot store, as the staged paths of a project are checked, and
// each path that differs only by case from an earlier one, which a file
// system that ignores case holds as one file.
func (m Mod) checkPaths() []Diagnostic {
	var out []Diagnostic
	seen := map[string]string{}
	for _, f := range m.Files {
		for seg := range strings.SplitSeq(f.Rel, "/") {
			if BadName(seg) {
				out = append(out, NewDiagnostic(f.Path, 0, 0, CodeModPathInvalid, "%q is not a file name every OS can store: %q", f.Rel, seg))
				break
			}
		}
		low := strings.ToLower(f.Rel)
		if prev, ok := seen[low]; ok {
			out = append(out, NewDiagnostic(f.Path, 0, 0, CodeModPathCase, "%q differs only by case from %q", f.Rel, prev).WithRelated(path.Join(m.Path, prev), 0))
			continue
		}
		seen[low] = f.Rel
	}
	return out
}

// modHooks is what dotai reads of a mod's hooks/hooks.json.
type modHooks struct {
	Modules []string                   `json:"modules"`
	Hooks   map[string]json.RawMessage `json:"hooks"`
}

// readHooks reads data, a hooks/hooks.json, and reports whether it has the
// shape of a mod's: an object with a list of module paths, and settings
// hooks, when it has them, an object of events.
func readHooks(data []byte) (h modHooks, msg string) {
	var top map[string]json.RawMessage
	shape := `hooks/hooks.json must be a JSON object with "modules", a list of module paths`
	if err := json.Unmarshal(data, &top); err != nil || top == nil {
		return h, shape
	}
	if err := json.Unmarshal(top["modules"], &h.Modules); err != nil || len(h.Modules) == 0 {
		return h, shape
	}
	if raw, ok := top["hooks"]; ok {
		if err := json.Unmarshal(raw, &h.Hooks); err != nil || h.Hooks == nil {
			return h, `"hooks" of hooks/hooks.json must be an object of hook events`
		}
	}
	return h, ""
}

// check reports what keeps the mod from loading as a plugin with a hooks
// module.
func (m Mod) check() []Diagnostic {
	var out []Diagnostic
	at := path.Join(m.Path, ModHooks)
	f, ok := m.File(ModHooks)
	if !ok {
		out = append(out, NewDiagnostic(m.Path, 0, 0, CodeModHooksInvalid, "mod %s has no %s, which names its hooks module", m.Name, ModHooks))
	} else if h, msg := readHooks(f.Data); msg != "" {
		out = append(out, NewDiagnostic(at, 0, 0, CodeModHooksInvalid, "%s", msg))
	} else {
		for _, mod := range h.Modules {
			// A module is relative to hooks.json, and stays inside the mod.
			rel := path.Join(path.Dir(ModHooks), mod)
			switch {
			case strings.Contains(mod, `\`) || strings.HasPrefix(mod, "/") || strings.Contains(mod, ":") || !fs.ValidPath(rel) || rel == ".":
				out = append(out, NewDiagnostic(at, 0, 0, CodeModModuleInvalid, "module %q must be a relative slash path inside the mod", mod))
			case !slices.Contains(ModExtensions, path.Ext(mod)):
				out = append(out, NewDiagnostic(at, 0, 0, CodeModModuleInvalid, "module %q must end in %s or %s", mod,
					strings.Join(ModExtensions[:len(ModExtensions)-1], ", "), ModExtensions[len(ModExtensions)-1]))
			default:
				if _, ok := m.File(rel); !ok {
					out = append(out, NewDiagnostic(at, 0, 0, CodeModModuleInvalid, "module %q is not a file of the mod (%s)", mod, path.Join(m.Path, rel)))
				}
			}
		}
	}
	if f, ok := m.File(ModManifest); ok {
		var manifest map[string]json.RawMessage
		var name string
		err := json.Unmarshal(f.Data, &manifest)
		if raw, set := manifest["name"]; err == nil && set {
			err = json.Unmarshal(raw, &name)
			if err == nil && name != m.Name {
				err = fmt.Errorf("name %q", name)
			}
		}
		if err != nil || manifest == nil {
			out = append(out, NewDiagnostic(f.Path, 0, 0, CodeModManifestInvalid, "%s must be a JSON object whose \"name\", when set, is %q, the mod's name", ModManifest, m.Name))
		}
	}
	return out
}

// subagentEvents are the hook events whose matcher is an agent's name,
// which for a plugin's agent is <plugin>:<agent>.
var subagentEvents = []string{"SubagentStart", "SubagentStop"}

// Warnings are what the mod may not do as its author means: a matcher of
// a SubagentStart or SubagentStop hook that names one of the mod's agents
// bare, which Claude Code names <mod>:<agent>, so it never matches.
func (m Mod) Warnings() []Diagnostic {
	f, ok := m.File(ModHooks)
	if !ok {
		return nil
	}
	h, msg := readHooks(f.Data)
	if msg != "" {
		return nil
	}
	var agents []string
	for _, f := range m.Files {
		if rest, ok := strings.CutPrefix(f.Rel, modAgentsDir); ok && !strings.Contains(rest, "/") && path.Ext(rest) == ".md" {
			agents = append(agents, strings.TrimSuffix(rest, ".md"))
		}
	}
	var out []Diagnostic
	for _, event := range subagentEvents {
		var entries []struct {
			Matcher string `json:"matcher"`
		}
		if json.Unmarshal(bytes.TrimSpace(h.Hooks[event]), &entries) != nil {
			continue
		}
		for _, e := range entries {
			for alt := range strings.SplitSeq(e.Matcher, "|") {
				if slices.Contains(agents, alt) {
					out = append(out, NewDiagnostic(f.Path, 0, 0, CodeModMatcherBare, "%s matcher %q names the mod's agent %s bare; Claude Code names it %s:%s", event, e.Matcher, alt, m.Name, alt).
						WithHint("match ^%s:%s$", m.Name, alt))
				}
			}
		}
	}
	return out
}
