package app

import (
	"context"
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
// name, and each entry that is skipped. Each skill is linked or copied
// into the harnesses that have no such folder, and OpenCode reads them
// itself. Lists are never nil.
type SharedSkillsPlan struct {
	Dir     string         `json:"dir"`
	Found   bool           `json:"found"`
	Skills  []string       `json:"skills"`
	Skipped []SkippedSkill `json:"skipped"`
}

// SkippedSkill is an entry of the shared skills folder that is not linked
// or copied: one that is not a skill (ReasonNotASkill), whose name some OS
// cannot store (ReasonInvalidName), or, for Harness only, a skill of the
// same name as one of the pack's, which wins (ReasonPackSkill), or a skill
// that cannot be copied (ReasonNotCopyable) because File, a slash path
// inside it, is neither a regular file nor a directory, or has a name some
// OS cannot store. Harness is "" when the entry is skipped for every
// harness, and File is "" for every reason but ReasonNotCopyable.
type SkippedSkill struct {
	Name    string `json:"name"`
	Harness string `json:"harness"`
	Reason  string `json:"reason"`
	File    string `json:"file"`
}

// The reasons a shared skill is skipped.
const (
	ReasonNotASkill   = "not-a-skill"
	ReasonInvalidName = "invalid-name"
	ReasonPackSkill   = "pack-skill"
	ReasonNotCopyable = "not-copyable"
)

// Text is why s is skipped, text for people.
func (s SkippedSkill) Text() string {
	switch s.Reason {
	case ReasonPackSkill:
		return fmt.Sprintf("the shared skill %s is not %s into %s: the pack has a skill of that name, which wins", s.Name, sharedVerb(s.Harness), s.Harness)
	case ReasonNotCopyable:
		return fmt.Sprintf("the shared skill %s is not copied into %s: %s in it is not a regular file or a directory, or has a name some OS cannot store", s.Name, s.Harness, s.File)
	case ReasonInvalidName:
		return fmt.Sprintf("%s is skipped: its name is not one every OS can store", s.Name)
	default:
		return fmt.Sprintf("%s is skipped: it is not a skill, a directory with a SKILL.md", s.Name)
	}
}

// sharedVerb is how a shared skill gets into the harness id: "copied" or
// "linked".
func sharedVerb(id string) string {
	if a, err := harness.Lookup([]string{id}); err == nil && a[0].CopySharedSkills {
		return "copied"
	}
	return "linked"
}

// sharedSkills is the shared skills of home and the staged files that make
// them visible in each of adapters that has no shared skills folder of its
// own, with ops the plan of the files staged in tree, the user-scope
// staging tree. A harness gets a link per skill, to the entry in the
// folder, whatever that entry is a link to, or, when it copies them
// (CopySharedSkills), the files of each skill, copied into tree
// (copySkill) and staged like the pack's own. An entry is a skill when it
// is a directory, followed through links, that holds a SKILL.md; hidden
// entries are left out. A skill whose link or copy would sit on or above a
// staged file, such as the pack's skill of the same name, is skipped for
// that harness. A missing folder shares nothing.
func sharedSkills(ctx context.Context, d installFS, tree, home string, adapters []harness.Adapter, ops []plan.Op) ([]plan.File, *SharedSkillsPlan, error) {
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
	var files []plan.File
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
			_, target, _ := plan.Locate(home, harness.Isolated, adapters, source)
			if slices.ContainsFunc(ops, func(op plan.Op) bool { return samePath(op.Target, target) || below(target, op.Target) }) {
				sp.Skipped = append(sp.Skipped, SkippedSkill{Name: name, Harness: a.ID, Reason: ReasonPackSkill})
				continue
			}
			if !a.CopySharedSkills {
				files = append(files, plan.File{Path: source, Link: entry})
				continue
			}
			copied, bad, err := copySkill(ctx, d, entry, tree, source)
			if err != nil {
				return nil, nil, err
			}
			if bad != "" {
				sp.Skipped = append(sp.Skipped, SkippedSkill{Name: name, Harness: a.ID, Reason: ReasonNotCopyable, File: bad})
				continue
			}
			files = append(files, copied...)
		}
	}
	return files, sp, nil
}

// copySkill copies the files of the shared skill entry, a directory that
// is followed when it is a link, into tree at the slash path dir, and
// gives them as staged files, which the plan installs, owns, backs up and
// removes like the pack's own. Nothing below entry is followed: when
// anything below it is neither a regular file nor a directory, such as a
// link, or has a name some OS cannot store, nothing is copied and its
// slash path inside the skill is returned instead. Each file is read as
// Hash reads a target, so a link put in its place after the check is
// refused, not followed.
func copySkill(ctx context.Context, d installFS, entry, tree, dir string) ([]plan.File, string, error) {
	// The trailing separator walks the directory a link at entry points
	// at; without it, the walk stops at the link.
	names, err := d.Files(entry + string(filepath.Separator))
	if err != nil {
		return nil, "", fmt.Errorf("the shared skill %s cannot be read: %w", entry, err)
	}
	slices.Sort(names)
	for _, name := range names {
		if slices.ContainsFunc(strings.Split(name, "/"), func(seg string) bool { return !plan.ValidName(seg) }) {
			return nil, name, nil
		}
		if mode, err := d.Lstat(filepath.Join(entry, filepath.FromSlash(name))); err != nil || !mode.IsRegular() {
			return nil, name, nil
		}
	}
	files := make([]plan.File, 0, len(names))
	for _, name := range names {
		data, cur, err := d.Read(ctx, filepath.Join(entry, filepath.FromSlash(name)))
		if err != nil {
			return nil, "", fmt.Errorf("the shared skill %s cannot be copied: %w", entry, err)
		}
		source := path.Join(dir, name)
		if err := d.WriteFile(tree, source, data, perm(cur.Exec)); err != nil {
			return nil, "", err
		}
		files = append(files, plan.File{Path: source})
	}
	return files, "", nil
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
// of the same name wins over, or that cannot be copied, which the user
// must read before the install writes.
func sharedWarnings(sp *SharedSkillsPlan) []string {
	var out []string
	if sp == nil {
		return out
	}
	for _, s := range sp.Skipped {
		if s.Reason == ReasonPackSkill || s.Reason == ReasonNotCopyable {
			out = append(out, s.Text())
		}
	}
	return out
}
