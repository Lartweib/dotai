package merge

import (
	"cmp"
	"maps"
	"slices"

	"go.yaml.in/yaml/v3"

	"github.com/lartweib/dotai/internal/pack"
)

// settingsFile is a layer's settings, merged from pack.Layer.Settings
// instead of staged as a file.
const settingsFile = "dotai/settings.yaml"

// Setting is one top-level key of a harness's merged settings. Claude
// Code's env merges per variable: its Value is one mapping built from the
// winning variables, and Origins gives the layer of each.
type Setting struct {
	Key     string
	Value   *yaml.Node
	Layer   string            // path of the layer that provides it; "" for a merged env
	Origins map[string]string // for a merged env, the layer path of each variable
}

// setting is a value of a scope's settings with where it comes from. Its
// identity is <harness>.<key>, or claudecode.env.<VAR>.
type setting struct {
	harness, key, name string // name is the env variable, or ""
	layer              pack.Layer
	file               string
	k, v               *yaml.Node
	seq                int // the order the identity first appeared in the scope
}

func (st setting) id() string {
	id := st.harness + "." + st.key
	if st.name != "" {
		id += "." + st.name
	}
	return id
}

// mergeSettings applies the Single values row to the settings of layer l:
// one value per identity, replaced by a later layer only when it declares
// the override, and never across profiles.
func (s *scope) mergeSettings(l pack.Layer) {
	overrides := map[string]pack.Override{}
	for _, o := range l.Settings.Overrides {
		overrides[setting{harness: o.Harness, key: o.Key, name: o.Var}.id()] = o
	}
	for _, b := range l.Settings.Blocks {
		for i := 0; i+1 < len(b.Value.Content); i += 2 {
			k, v := b.Value.Content[i], b.Value.Content[i+1]
			st := setting{harness: b.Harness, key: k.Value, layer: l, file: l.Path + "/" + settingsFile, k: k, v: v}
			if b.Harness != "claudecode" || k.Value != "env" || v.Kind != yaml.MappingNode {
				s.claimSetting(st, overrides)
				continue
			}
			for j := 0; j+1 < len(v.Content); j += 2 {
				st.name, st.k, st.v = v.Content[j].Value, v.Content[j], v.Content[j+1]
				s.claimSetting(st, overrides)
			}
		}
	}
}

// claimSetting gives st's identity to st, or reports why it cannot have
// it. A replaced value keeps its place in the order.
func (s *scope) claimSetting(st setting, overrides map[string]pack.Override) {
	id := st.id()
	o, declared := overrides[id]
	prev, taken := s.settings[id]
	at := func(code, format string, args ...any) pack.Diagnostic {
		return pack.NewDiagnostic(st.file, st.k.Line, st.k.Column, code, format, args...).WithRelated(prev.file, prev.k.Line)
	}
	switch {
	case !taken:
		if declared {
			s.report(pack.NewDiagnostic(st.file, o.Line, o.Column, CodeOverrideWithoutTarget, "setting %s has nothing to override", id).
				WithHint("remove it from dotai.overrides; no earlier layer of its scope sets it"))
		}
		st.seq = s.seq
		s.seq++
	case prev.layer.Kind == pack.LayerProfile && st.layer.Kind == pack.LayerProfile:
		s.report(at(CodeProfileConflict, "setting %s is also set by profile %s", id, prev.layer.Name).
			WithHint("keep it in one profile; profiles cannot override each other"))
		return
	case !declared:
		s.report(at(CodeOverrideUndeclared, "setting %s already exists in %s", id, prev.layer.Path).
			WithHint("add %s to dotai.overrides to replace it", id))
		return
	default:
		st.seq = prev.seq
	}
	s.settings[id] = st
}

// mergedSettings returns the scope's settings by harness. Keys keep the
// order their identity first appeared in: the first layer's key order,
// then each later layer's new keys.
func (s *scope) mergedSettings() map[string][]Setting {
	out := map[string][]Setting{}
	env := map[string]int{} // index of the merged env in out[harness]
	for _, st := range slices.SortedFunc(maps.Values(s.settings), func(a, b setting) int { return cmp.Compare(a.seq, b.seq) }) {
		if st.name == "" {
			out[st.harness] = append(out[st.harness], Setting{Key: st.key, Value: st.v, Layer: st.layer.Path})
			continue
		}
		i, ok := env[st.harness]
		if !ok {
			i, env[st.harness] = len(out[st.harness]), len(out[st.harness])
			out[st.harness] = append(out[st.harness], Setting{Key: st.key, Value: &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}, Origins: map[string]string{}})
		}
		e := &out[st.harness][i]
		e.Value.Content = append(e.Value.Content, st.k, st.v)
		e.Origins[st.name] = st.layer.Path
	}
	return out
}
