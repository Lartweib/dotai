package state

import (
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/lartweib/dotai/internal/plan"
)

// ProjectSchema is the version of the project record format: the record
// of a project apply (decision 0041), kept in <project>/.dotai/state.json.
const ProjectSchema = "dotai.project/v1"

// Project is the record of the profiles applied to a project: the pack,
// its variant, the profiles and the harnesses; the project, the top of its
// worktree, and MCPKey, the entry of Claude Code's projects object its MCP
// servers go under; when the first apply ran and when the last one did;
// the backup of each apply that wrote, oldest first; what dotai owns, by
// target; the directories it created in the project, slash paths relative
// to it; and its block in the exclude file. Pending is the journal of an
// apply that is writing, as an install's is (State).
type Project struct {
	Schema     string         `json:"schema"`
	Pack       Pack           `json:"pack"`
	Variant    string         `json:"variant"`
	Profiles   []string       `json:"profiles"`
	Harnesses  []string       `json:"harnesses"`
	Project    string         `json:"project"`
	MCPKey     string         `json:"mcpKey"`
	FirstApply time.Time      `json:"firstApply"`
	AppliedAt  time.Time      `json:"appliedAt"`
	Backups    []string       `json:"backups"`
	Files      []ProjectFile  `json:"files"`
	Dirs       []string       `json:"dirs"`
	Exclude    ProjectExclude `json:"exclude"`
	Pending    *ApplyJournal  `json:"pending,omitempty"`
}

// ProjectFile is a target dotai owns, as plan.ProjectOwned describes it.
type ProjectFile struct {
	Target string `json:"target"`
	plan.ProjectOwned
}

// ProjectExclude is dotai's block in the exclude file: File, the exclude
// file, "" outside a git repository, where the Entries are Pending; Created
// marks an exclude file dotai created, and Newline one whose last line had
// no line break, which dotai added before its block.
type ProjectExclude struct {
	File    string   `json:"file"`
	Pending bool     `json:"pending"`
	Entries []string `json:"entries"`
	Created bool     `json:"created,omitempty"`
	Newline bool     `json:"newline,omitempty"`
}

// ApplyJournal is the journal of a project apply or restore that is
// writing: its backup, and each target it writes, in order. Prior is the
// copy of the record before it in the backup, "" when there was none.
type ApplyJournal struct {
	Backup string      `json:"backup"`
	Prior  string      `json:"prior,omitempty"`
	Ops    []JournalOp `json:"ops"`
}

// JournalOp is one target a project apply writes: Root, the directory it
// is written through, and Name, its slash path inside it; Before, what it
// held, nil when it was missing, with Copy, its copy in the backup; After,
// what it gets, nil when it is removed. A file the harness rewrites, whose
// keys dotai edits as it finds it, has Undo instead: the edits that put
// back its keys. Dirs are the directories the write creates, slash paths
// inside Root, outermost first.
type JournalOp struct {
	Target string         `json:"target"`
	Root   string         `json:"root"`
	Name   string         `json:"name"`
	Before *Content       `json:"before,omitempty"`
	Copy   string         `json:"copy,omitempty"`
	After  *Content       `json:"after,omitempty"`
	Undo   []plan.KeyEdit `json:"undo,omitempty"`
	Dirs   []string       `json:"dirs,omitempty"`
}

// Owned is the record's ownership, by target, as the plan reads it.
func (p Project) Owned() map[string]plan.ProjectOwned {
	out := make(map[string]plan.ProjectOwned, len(p.Files))
	for _, f := range p.Files {
		out[f.Target] = f.ProjectOwned
	}
	return out
}

// reservedDir is the directory of the pack's own files in a project, which
// an apply never writes (decision 0041).
const reservedDir = ".dotai/packs"

// neverWritten are the project files that are the user's or the team's,
// which an apply never writes (decision 0041), compared without case.
var neverWritten = []string{"agents.md", "claude.md", "claude.local.md", ".mcp.json", ".claude/settings.json", "opencode.json", "opencode.jsonc"}

// CheckProjectTarget refuses target, a path dotai would write in project,
// that is one of the files an apply never writes or is inside the pack's
// reserved directory. A target outside project is not checked.
func CheckProjectTarget(project, target string) error {
	rel, err := filepath.Rel(project, target)
	if err != nil || !filepath.IsLocal(rel) {
		return nil
	}
	low := strings.ToLower(filepath.ToSlash(rel))
	if slices.Contains(neverWritten, low) || low == reservedDir || strings.HasPrefix(low, reservedDir+"/") {
		return fmt.Errorf("%s is a file a project apply never writes", target)
	}
	return nil
}

// EncodeProject is the record as indented JSON with a final newline.
func EncodeProject(p Project) ([]byte, error) {
	data, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(data, '\n'), nil
}

// DecodeProject reads a project record of this ProjectSchema for project,
// whose targets are absolute, each owned once, never a file an apply never
// writes, and whose directories are clean slash paths inside the project.
func DecodeProject(data []byte, project string) (Project, error) {
	var p Project
	if err := json.Unmarshal(data, &p); err != nil {
		return Project{}, fmt.Errorf("project record: %w", err)
	}
	if p.Schema != ProjectSchema {
		return Project{}, fmt.Errorf("project record has schema %q, want %q", p.Schema, ProjectSchema)
	}
	if p.Project != project {
		return Project{}, fmt.Errorf("project record is of the project %s, not %s: the project was moved or copied; remove its .dotai/ to apply again from scratch", p.Project, project)
	}
	seen := map[string]bool{}
	for _, f := range p.Files {
		if !filepath.IsAbs(f.Target) || seen[f.Target] {
			return Project{}, fmt.Errorf("project record: target %q is not absolute or is owned twice", f.Target)
		}
		seen[f.Target] = true
		if err := CheckProjectTarget(project, f.Target); err != nil {
			return Project{}, fmt.Errorf("project record: %w", err)
		}
		if f.Hash == "" && len(f.Keys) == 0 {
			return Project{}, fmt.Errorf("project record: %s has neither a hash nor keys", f.Target)
		}
		for _, k := range f.Keys {
			if len(k.Path) == 0 {
				return Project{}, fmt.Errorf("project record: %s: a key has no path", f.Target)
			}
		}
	}
	for _, d := range p.Dirs {
		if d == "" || !filepath.IsLocal(filepath.FromSlash(d)) || path.Clean(d) != d {
			return Project{}, fmt.Errorf("project record: directory %q is not a clean path inside the project", d)
		}
	}
	if p.Pending != nil {
		for _, op := range p.Pending.Ops {
			if !filepath.IsAbs(op.Target) || !filepath.IsAbs(op.Root) || op.Name == "" || !filepath.IsLocal(filepath.FromSlash(op.Name)) {
				return Project{}, errors.New("project record: a pending target is not a path inside its root")
			}
			if err := CheckProjectTarget(project, op.Target); err != nil {
				return Project{}, fmt.Errorf("project record: pending: %w", err)
			}
		}
	}
	for _, list := range []*[]string{&p.Profiles, &p.Harnesses, &p.Backups, &p.Dirs, &p.Exclude.Entries} {
		if *list == nil {
			*list = []string{}
		}
	}
	if p.Files == nil {
		p.Files = []ProjectFile{}
	}
	return p, nil
}

// ProjectsSchema is the version of the list of projects whose keys dotai
// owns in a harness's home files, kept in ~/.dotai/projects.json.
const ProjectsSchema = "dotai.projects/v1"

// Projects is the list a replace install reads to keep the keys project
// applies own in a harness's home file, such as Claude Code's MCP servers
// in ~/.claude.json, which it would otherwise wipe (decision 0041).
type Projects struct {
	Schema   string         `json:"schema"`
	Projects []ProjectEntry `json:"projects"`
}

// ProjectEntry is one project and the keys its apply owns in File, each a
// path from the top of the file, sorted.
type ProjectEntry struct {
	Project string     `json:"project"`
	File    string     `json:"file"`
	Keys    [][]string `json:"keys"`
}

// OwnedKeys is every key of the entries of p in file.
func (p Projects) OwnedKeys(file string) [][]string {
	var out [][]string
	for _, e := range p.Projects {
		if e.File == file {
			out = append(out, e.Keys...)
		}
	}
	return out
}

// With is p with the entry of project in file holding keys, or without it
// when keys is empty, in project order.
func (p Projects) With(project, file string, keys [][]string) Projects {
	out := Projects{Schema: ProjectsSchema, Projects: []ProjectEntry{}}
	for _, e := range p.Projects {
		if e.Project != project {
			out.Projects = append(out.Projects, e)
		}
	}
	if len(keys) > 0 {
		k := slices.Clone(keys)
		slices.SortFunc(k, func(a, b []string) int { return strings.Compare(strings.Join(a, "\x00"), strings.Join(b, "\x00")) })
		out.Projects = append(out.Projects, ProjectEntry{Project: project, File: file, Keys: k})
	}
	slices.SortFunc(out.Projects, func(a, b ProjectEntry) int { return strings.Compare(a.Project, b.Project) })
	return out
}

// EncodeProjects is the list as indented JSON with a final newline.
func EncodeProjects(p Projects) ([]byte, error) {
	data, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(data, '\n'), nil
}

// DecodeProjects reads a list of this ProjectsSchema, whose projects and
// files are absolute and whose keys have paths.
func DecodeProjects(data []byte) (Projects, error) {
	var p Projects
	if err := json.Unmarshal(data, &p); err != nil {
		return Projects{}, fmt.Errorf("projects list: %w", err)
	}
	if p.Schema != ProjectsSchema {
		return Projects{}, fmt.Errorf("projects list has schema %q, want %q", p.Schema, ProjectsSchema)
	}
	for _, e := range p.Projects {
		if !filepath.IsAbs(e.Project) || !filepath.IsAbs(e.File) {
			return Projects{}, fmt.Errorf("projects list: %q or %q is not absolute", e.Project, e.File)
		}
		for _, k := range e.Keys {
			if len(k) == 0 {
				return Projects{}, fmt.Errorf("projects list: %s: a key has no path", e.Project)
			}
		}
	}
	if p.Projects == nil {
		p.Projects = []ProjectEntry{}
	}
	return p, nil
}
