package app

import (
	"errors"
	"fmt"
	"io/fs"
	"path"
	"path/filepath"
	"slices"
	"strings"

	"github.com/lartweib/dotai/internal/harness"
	"github.com/lartweib/dotai/internal/plan"
)

// SharedSkillsPlan is what an install whose variant shares the user's
// skills (decision 0036) takes from the shared skills folder Dir, as it is
// when the plan is made: whether Dir exists, the Skills found there, by
// name, and each entry that is skipped. Each skill is linked into the
// harnesses that have no such folder, and OpenCode reads them itself.
// Lists are never nil.
type SharedSkillsPlan struct {
	Dir     string         `json:"dir"`
	Found   bool           `json:"found"`
	Skills  []string       `json:"skills"`
	Skipped []SkippedSkill `json:"skipped"`
}

// SkippedSkill is an entry of the shared skills folder that is not linked:
// one that is not a skill (ReasonNotASkill), whose name some OS cannot
// store (ReasonInvalidName), or, for Harness only, a skill of the same name
// as one of the pack's, which wins (ReasonPackSkill). Harness is "" when
// the entry is skipped for every harness.
type SkippedSkill struct {
	Name    string `json:"name"`
	Harness string `json:"harness"`
	Reason  string `json:"reason"`
}

// The reasons a shared skill is skipped.
const (
	ReasonNotASkill   = "not-a-skill"
	ReasonInvalidName = "invalid-name"
	ReasonPackSkill   = "pack-skill"
)

// Text is why s is skipped, text for people.
func (s SkippedSkill) Text() string {
	switch s.Reason {
	case ReasonPackSkill:
		return fmt.Sprintf("the shared skill %s is not linked into %s: the pack has a skill of that name, which wins", s.Name, s.Harness)
	case ReasonInvalidName:
		return fmt.Sprintf("%s is skipped: its name is not one every OS can store", s.Name)
	default:
		return fmt.Sprintf("%s is skipped: it is not a skill, a directory with a SKILL.md", s.Name)
	}
}

// sharedSkills is the shared skills of home and the links that make them
// visible in each of adapters that has no shared skills folder of its
// own, with ops the plan of the staged files. Each link points at the
// entry in the folder, whatever that entry is a link to. An entry is a
// skill when it is a directory, followed through links, that holds a
// SKILL.md; hidden entries are left out. A skill whose link would sit on
// or above a staged file, such as the pack's skill of the same name, is
// skipped for that harness. A missing folder shares nothing.
func sharedSkills(d installFS, home string, adapters []harness.Adapter, ops []plan.Op) ([]plan.File, *SharedSkillsPlan, error) {
	dir := filepath.Join(home, filepath.FromSlash(harness.SharedSkillsDir))
	sp := &SharedSkillsPlan{Dir: dir, Skills: []string{}, Skipped: []SkippedSkill{}}
	names, err := d.List(dir)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return nil, sp, nil
	case err != nil:
		return nil, nil, fmt.Errorf("the shared skills folder %s cannot be read: %w", dir, err)
	}
	sp.Found = true
	slices.Sort(names)
	var links []plan.File
	for _, name := range names {
		if strings.HasPrefix(name, ".") {
			continue
		}
		if !plan.ValidName(name) {
			sp.Skipped = append(sp.Skipped, SkippedSkill{Name: name, Reason: ReasonInvalidName})
			continue
		}
		entry := filepath.Join(dir, name)
		if !isSkill(d, entry) {
			sp.Skipped = append(sp.Skipped, SkippedSkill{Name: name, Reason: ReasonNotASkill})
			continue
		}
		sp.Skills = append(sp.Skills, name)
		for _, a := range adapters {
			if a.SharedSkills == "" {
				continue // it reads the folder itself
			}
			source := path.Join(a.Staging, a.SharedSkills, name)
			_, target, _ := plan.Locate(home, adapters, source)
			if slices.ContainsFunc(ops, func(op plan.Op) bool { return op.Target == target || below(target, op.Target) }) {
				sp.Skipped = append(sp.Skipped, SkippedSkill{Name: name, Harness: a.ID, Reason: ReasonPackSkill})
				continue
			}
			links = append(links, plan.File{Path: source, Link: entry})
		}
	}
	return links, sp, nil
}

// isSkill reports whether entry is a directory, followed through links,
// that holds a regular SKILL.md.
func isSkill(d installFS, entry string) bool {
	mode, err := d.Stat(entry)
	if err != nil || !mode.IsDir() {
		return false
	}
	mode, err = d.Stat(filepath.Join(entry, "SKILL.md"))
	return err == nil && mode.IsRegular()
}

// sharedWarnings is a warning for each shared skill the pack's own skill
// of the same name wins over, which the user must read before the install
// writes.
func sharedWarnings(sp *SharedSkillsPlan) []string {
	var out []string
	if sp == nil {
		return out
	}
	for _, s := range sp.Skipped {
		if s.Reason == ReasonPackSkill {
			out = append(out, s.Text())
		}
	}
	return out
}
