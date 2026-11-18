package merge

import (
	"encoding/json"
	"slices"

	"github.com/lartweib/dotai/internal/pack"
)

// EngineLayer is the layer of what dotai adds to the user scope after the
// base and the variant (decision 0045), as the origins of a merged file
// name it.
var EngineLayer = pack.Layer{Kind: "engine", Name: "dotai", Path: "dotai"}

// Engine is the engine layer of the user scope: the profiling skill, and
// the hooks that run at the start of a session, each for the tool-scoped
// block of a harness.
type Engine struct {
	Skill []byte
	Hooks []BlockHook
}

// BlockHook is a sessionStart hook of the tool-scoped block Block, a
// rulesync target such as claudecode, as the JSON entry of a hooks file.
type BlockHook struct {
	Block string
	Entry json.RawMessage
}

// ProfilesSkillFile is where the profiling skill is staged.
const ProfilesSkillFile = ".rulesync/skills/" + pack.ProfilesSkill + "/SKILL.md"

// sessionStart is the hook event of the engine's hooks.
const sessionStart = "sessionStart"

// AddEngine stages e after the base and the variant of the user scope:
// its skill, made by dotai, and each of its hooks in its block's
// sessionStart, in the merged hooks.jsonc, which gets an empty object of
// shared hooks when it has none. A block that does not declare
// sessionStart gets the shared sessionStart hooks first, since it replaces
// them for its harness (rulesync 41), so that the pack's hooks still reach
// it; an entry that is there already is kept once. A pack without hooks
// gets a hooks file of the engine's hooks alone.
func (s *Staging) AddEngine(e Engine) {
	u := s.user
	u.put(staged{File: File{Path: ProfilesSkillFile, Data: e.Skill, Generator: GeneratorDotai}, src: ProfilesSkillFile})
	if len(e.Hooks) > 0 {
		n, ok := u.lists[HooksFile]
		if !ok {
			n = &node{kind: '{', vals: map[string]*node{}, layer: EngineLayer}
			n.set("version", engineLeaf([]byte("1")))
			u.lists[HooksFile] = n
		}
		// rulesync 41 refuses a hooks file without the shared hooks.
		n.child(sharedHooks)
		var shared []*node
		if h := n.vals[sharedHooks]; h != nil && h.kind == '{' {
			if ev := h.vals[sessionStart]; ev != nil && ev.kind == '[' {
				shared = ev.items
			}
		}
		for _, h := range e.Hooks {
			block := n.child(h.Block)
			hooks := block.child(sharedHooks)
			ev, ok := hooks.vals[sessionStart]
			if !ok {
				ev = &node{kind: '[', items: slices.Clone(shared), layer: EngineLayer}
				hooks.set(sessionStart, ev)
			}
			it := engineLeaf(h.Entry)
			if !slices.ContainsFunc(ev.items, func(d *node) bool { return d.canon == it.canon }) {
				ev.items = append(ev.items, it)
			}
		}
	}
	s.User = u.tree()
}

// engineLeaf is the leaf of the standard JSON raw in the engine layer.
func engineLeaf(raw []byte) *node {
	n, err := leaf(raw, EngineLayer, "", fileLine)
	if err != nil {
		panic("merge: an engine entry is not JSON: " + err.Error()) // the engine builds them
	}
	return n
}

// set adds the member k to the object n.
func (n *node) set(k string, v *node) {
	n.keys = append(n.keys, k)
	n.vals[k] = v
}

// child is the object member k of the object n, added when it is missing.
// A member that is not an object is replaced: the engine's block must be
// one, and a pack's that is not is no hooks block rulesync reads.
func (n *node) child(k string) *node {
	if c, ok := n.vals[k]; ok && c.kind == '{' {
		return c
	}
	c := &node{kind: '{', vals: map[string]*node{}, layer: EngineLayer}
	if _, ok := n.vals[k]; ok {
		n.vals[k] = c
		return c
	}
	n.set(k, c)
	return c
}
