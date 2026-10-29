package pack

import (
	"cmp"
	"fmt"
	"math"
	"slices"
	"strconv"
	"strings"

	"go.yaml.in/yaml/v3"
)

// settingsFile is a layer's settings, relative to the layer.
const settingsFile = dotaiDir + "/settings.yaml"

// Settings is a layer's dotai/settings.yaml (0025, 0030): one block per
// harness and the overrides the layer declares. Only valid entries are
// kept.
type Settings struct {
	Blocks    []SettingsBlock // in file order
	Overrides []Override      // in file order
}

// SettingsBlock is the settings of one harness. Value is the YAML mapping
// itself, so later steps see nested values in file order.
type SettingsBlock struct {
	Harness string
	Value   *yaml.Node
}

// Override is an entry of dotai.overrides: a key of a harness block, or
// one variable of claudecode's env, which merges per variable (0023).
type Override struct {
	Harness, Key string
	Var          string // the variable of claudecode.env.<VAR>
	Line, Column int
}

// readSettings decodes a layer's settings.yaml. harnesses are the
// manifest's. It returns nil only when the file is not valid YAML.
func readSettings(r reader, src []byte, kind LayerKind, harnesses []string) *Settings {
	root, ok := r.parse(normalize(src), CodeYAMLInvalid)
	if !ok {
		return nil
	}
	s := &Settings{}
	if root == nil {
		return s
	}
	checkJSON(r, root, "")
	if root.Kind != yaml.MappingNode {
		r.report(root, CodeFieldType, "settings.yaml must be a mapping")
		return s
	}
	allowed := slices.DeleteFunc(slices.Clone(harnesses), func(h string) bool { return !slices.Contains(Harnesses, h) })
	var dotai *yaml.Node
	seen := map[string]bool{}
	for i := 0; i+1 < len(root.Content); i += 2 {
		k, v := root.Content[i], root.Content[i+1]
		if seen[k.Value] || k.Tag != "!!str" || reported(v) {
			continue
		}
		seen[k.Value] = true
		switch {
		case k.Value == "dotai":
			dotai = v
		case !slices.Contains(Harnesses, k.Value):
			r.report(k, CodeHarnessUnknown, "unknown harness %q, want one of %v or dotai", k.Value, allowed)
		case len(allowed) > 0 && !slices.Contains(allowed, k.Value): // no usable list was already reported
			r.report(k, CodeHarnessUnknown, "harness %q is not in the manifest, want one of %v or dotai", k.Value, allowed)
		case kind == LayerProfile && k.Value == "antigravity-cli":
			r.report(k, CodeSettingsScope, "profiles cannot carry antigravity-cli settings: agy has no project settings file")
		case v.Kind != yaml.MappingNode:
			r.report(v, CodeFieldType, "%s must be a mapping", k.Value)
		default:
			s.Blocks = append(s.Blocks, SettingsBlock{k.Value, v})
		}
	}
	if dotai != nil {
		s.Overrides = readOverrides(r, dotai, s.Blocks)
	}
	return s
}

// reported tells whether checkJSON already reported value n.
func reported(n *yaml.Node) bool {
	return n.Kind == yaml.AliasNode || n.Anchor != "" || n.Tag == "!!null"
}

// checkJSON reports what in n, at path, has no JSON form: anchors and
// aliases, null, keys that are not strings, duplicate keys, numbers that
// are not finite and tags other than the core scalar ones. A timestamp is
// kept as its text.
func checkJSON(r reader, n *yaml.Node, path string) {
	at := cmp.Or(path, "settings.yaml")
	if n.Kind == yaml.AliasNode || n.Anchor != "" {
		r.report(n, CodeSettingsValue, "%s: YAML anchors and aliases are not allowed", at)
		return
	}
	switch n.Kind {
	case yaml.MappingNode:
		seen := map[string]bool{}
		for i := 0; i+1 < len(n.Content); i += 2 {
			k, v := n.Content[i], n.Content[i+1]
			switch {
			case k.Kind != yaml.ScalarNode || k.Tag != "!!str":
				r.report(k, CodeSettingsValue, "%s: key %s must be a string", at, k.Value)
			case seen[k.Value]:
				r.report(k, CodeFieldDuplicate, "%s: duplicate field %q", at, k.Value)
			default:
				seen[k.Value] = true
				checkJSON(r, v, strings.TrimPrefix(path+"."+k.Value, "."))
			}
		}
	case yaml.SequenceNode:
		for i, it := range n.Content {
			checkJSON(r, it, fmt.Sprintf("%s[%d]", path, i))
		}
	case yaml.ScalarNode:
		switch n.Tag {
		case "!!str", "!!int", "!!bool", "!!timestamp":
		case "!!null":
			r.report(n, CodeSettingsValue, "%s: null is not allowed: a layer cannot unset a key", at)
		case "!!float":
			f, _ := strconv.ParseFloat(n.Value, 64)
			if v := strings.ToLower(strings.TrimLeft(n.Value, "+-")); v == ".inf" || v == ".nan" || math.IsInf(f, 0) {
				r.report(n, CodeSettingsValue, "%s: %s is not a finite number", at, n.Value)
			}
		default:
			r.report(n, CodeSettingsValue, "%s: tag %s is not allowed", at, n.Tag)
		}
	}
}

// readOverrides decodes the dotai: block, whose only field is overrides.
// Each entry must name a key, or a claudecode env variable, that blocks
// set; whether it replaces an earlier layer is checked when merging.
func readOverrides(r reader, n *yaml.Node, blocks []SettingsBlock) []Override {
	if n.Kind != yaml.MappingNode {
		r.report(n, CodeFieldType, "dotai must be a mapping")
		return nil
	}
	var list *yaml.Node
	for i := 0; i+1 < len(n.Content); i += 2 {
		k, v := n.Content[i], n.Content[i+1]
		switch {
		case k.Tag != "!!str" || reported(v):
		case k.Value != "overrides":
			r.report(k, CodeFieldUnknown, "dotai: unknown field %q", k.Value)
		case list == nil:
			list = v
		}
	}
	if list == nil {
		return nil
	}
	if list.Kind != yaml.SequenceNode {
		r.report(list, CodeFieldType, `dotai: "overrides" must be a list`)
		return nil
	}
	var out []Override
	seen := map[string]bool{}
	for _, it := range list.Content {
		switch {
		case reported(it):
		case it.Kind != yaml.ScalarNode || it.Tag != "!!str":
			r.report(it, CodeFieldType, `dotai: "overrides" items must be strings`)
		case seen[it.Value]:
			r.report(it, CodeFieldDuplicate, "dotai: duplicate override %q", it.Value)
		default:
			seen[it.Value] = true
			if o, ok := readOverride(r, it, blocks); ok {
				out = append(out, o)
			}
		}
	}
	return out
}

// readOverride decodes one override entry: <harness>.<key>, or
// claudecode.env.<VAR>.
func readOverride(r reader, n *yaml.Node, blocks []SettingsBlock) (Override, bool) {
	h, rest, _ := strings.Cut(n.Value, ".")
	key, v, nested := strings.Cut(rest, ".")
	isEnv := h == "claudecode" && key == "env"
	switch {
	case h == "" || key == "" || nested && (!isEnv || v == ""):
		r.report(n, CodeOverridePath, "override %q must be <harness>.<key> or claudecode.env.<VAR>", n.Value)
		return Override{}, false
	case isEnv && !nested:
		r.report(n, CodeOverridePath, "override %q must name one variable, as claudecode.env.<VAR>", n.Value)
		return Override{}, false
	}
	var val *yaml.Node
	if i := slices.IndexFunc(blocks, func(b SettingsBlock) bool { return b.Harness == h }); i >= 0 {
		val = lookup(blocks[i].Value, key)
	}
	if isEnv {
		val = lookup(val, v)
	}
	if val == nil {
		r.report(n, CodeOverrideKeyUnset, "override %q names a key this layer does not set", n.Value)
		return Override{}, false
	}
	return Override{Harness: h, Key: key, Var: v, Line: n.Line + r.offset, Column: n.Column}, true
}

// lookup returns the value at key in mapping n, or nil.
func lookup(n *yaml.Node, key string) *yaml.Node {
	if n == nil || n.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(n.Content); i += 2 {
		if n.Content[i].Value == key {
			return n.Content[i+1]
		}
	}
	return nil
}
