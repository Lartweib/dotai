package app

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"

	"github.com/tailscale/hujson"
	"go.yaml.in/yaml/v3"

	"github.com/lartweib/dotai/internal/merge"
)

// Generators of the settings files, in StagedFile.Generator.
const (
	GeneratorDotai         = "dotai"          // created from dotai/settings.yaml alone
	GeneratorRulesyncDotai = "rulesync+dotai" // rulesync's file with the keys of dotai/settings.yaml appended
)

// ErrSettingsConflict is a top-level key that both rulesync and
// dotai/settings.yaml write to one settings file.
var ErrSettingsConflict = errors.New("settings-conflict")

// settingsFiles is where each harness keeps its settings in a scope's
// staging tree (decision 0025). A missing scope has no settings file.
var settingsFiles = map[string]map[string]string{
	"claudecode":      {ScopeUser: ".claude/settings.json", ScopeProject: ".claude/settings.json"},
	"antigravity-cli": {ScopeUser: ".gemini/antigravity-cli/settings.json"},
	"opencode":        {ScopeUser: ".config/opencode/opencode.jsonc", ScopeProject: "opencode.jsonc"},
}

// renderSettings writes the merged settings of a scope into the settings
// file of each harness, in the order of harnesses, inside the scope tree
// dir. It runs after rulesync, which replaces keys of a file it merges
// into. A file rulesync wrote keeps its keys, and the settings are
// appended to them.
func renderSettings(w stageFS, dir, scope string, harnesses []string, settings map[string][]merge.Setting, tools ToolPaths) ([]StagedFile, error) {
	var out []StagedFile
	for _, h := range harnesses {
		name, ok := settingsFiles[h][scope]
		if !ok || len(settings[h]) == 0 {
			continue
		}
		f := StagedFile{Scope: scope, Path: name, Origins: map[string]string{}, Generator: GeneratorDotai}
		existing, err := w.ReadFile(dir, name)
		switch {
		case err == nil:
			f.Generator = GeneratorRulesyncDotai
		case !errors.Is(err, fs.ErrNotExist):
			return nil, err
		}
		data, err := encodeSettings(existing, settings[h], tools)
		if err != nil {
			return nil, fmt.Errorf("%s/%s: %w", scope, name, err)
		}
		if err := w.WriteFile(dir, name, data, 0o644); err != nil {
			return nil, err
		}
		for _, s := range settings[h] {
			if s.Origins == nil {
				f.Origins[s.Key] = s.Layer
			}
			for k, layer := range s.Origins {
				f.Origins[s.Key+"."+k] = layer
			}
		}
		out = append(out, f)
	}
	return out, nil
}

// encodeSettings returns the settings file made of the top-level keys of
// existing, a JSON or JSONC object or nil, followed by settings: JSON with
// 2-space indentation and a final newline. The path variables of string
// values are expanded before they are encoded.
func encodeSettings(existing []byte, settings []merge.Setting, tools ToolPaths) ([]byte, error) {
	var b bytes.Buffer
	b.WriteByte('{')
	written := map[string]bool{}
	if existing != nil {
		v, err := hujson.Parse(existing)
		if err != nil {
			return nil, err
		}
		v.Standardize()
		top, ok := v.Value.(*hujson.Object)
		if !ok {
			return nil, errors.New("the file rulesync wrote is not a JSON object")
		}
		for _, m := range top.Members {
			written[m.Name.Value.(hujson.Literal).String()] = true
			comma(&b)
			b.Write(m.Name.Value.(hujson.Literal))
			b.WriteByte(':')
			b.Write(m.Value.Pack())
		}
	}
	for _, s := range settings {
		if written[s.Key] {
			return nil, fmt.Errorf("%w: key %q is written by both rulesync and dotai/settings.yaml", ErrSettingsConflict, s.Key)
		}
		comma(&b)
		if err := encodeJSON(&b, s.Key); err != nil {
			return nil, err
		}
		b.WriteByte(':')
		if err := encodeNode(&b, s.Value, tools); err != nil {
			return nil, err
		}
	}
	b.WriteByte('}')
	var out bytes.Buffer
	if err := json.Indent(&out, b.Bytes(), "", "  "); err != nil {
		return nil, err
	}
	out.WriteByte('\n')
	return out.Bytes(), nil
}

// comma separates a member or element from the one before it.
func comma(b *bytes.Buffer) {
	if last := b.Bytes()[b.Len()-1]; last != '{' && last != '[' {
		b.WriteByte(',')
	}
}

// encodeNode writes n as compact JSON. Mappings keep their key order, and
// a timestamp stays the text it was written as.
func encodeNode(b *bytes.Buffer, n *yaml.Node, tools ToolPaths) error {
	switch n.Kind {
	case yaml.MappingNode, yaml.SequenceNode:
		open, close := byte('['), byte(']')
		if n.Kind == yaml.MappingNode {
			open, close = '{', '}'
		}
		b.WriteByte(open)
		for i, c := range n.Content {
			var err error
			switch {
			case n.Kind == yaml.SequenceNode:
				comma(b)
				err = encodeNode(b, c, tools)
			case i%2 == 0: // a key, which is never expanded
				comma(b)
				err = encodeJSON(b, c.Value)
				b.WriteByte(':')
			default:
				err = encodeNode(b, c, tools)
			}
			if err != nil {
				return err
			}
		}
		b.WriteByte(close)
		return nil
	}
	switch n.Tag {
	case "!!int", "!!float", "!!bool":
		var v any
		if err := n.Decode(&v); err != nil {
			return err
		}
		return encodeJSON(b, v)
	case "!!str":
		s, err := expandPaths([]byte(n.Value), tools)
		if err != nil {
			return err
		}
		return encodeJSON(b, string(s))
	}
	return encodeJSON(b, n.Value)
}

// encodeJSON writes v as JSON without escaping HTML.
func encodeJSON(b *bytes.Buffer, v any) error {
	e := json.NewEncoder(b)
	e.SetEscapeHTML(false)
	if err := e.Encode(v); err != nil {
		return err
	}
	b.Truncate(b.Len() - 1) // Encode ends with a newline
	return nil
}
