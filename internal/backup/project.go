package backup

import (
	"encoding/json"
	"fmt"
	"path"
	"path/filepath"
	"time"
)

// A project apply (decision 0041) backs up what it overwrites or deletes
// in the project, its exclude file and dotai's own files, before it writes,
// into a backup of its own under ~/.dotai/projects/<project id>/backups/,
// outside the project, so that a clean of the worktree or the restore of
// the project, which removes .dotai/, keeps it. A harness's home file whose
// keys the apply edits, such as ~/.claude.json, is not copied: the
// project record keeps the value each key had before.

// ProjectSchema is the version of the manifest of a project backup.
const ProjectSchema = "dotai.project-backup/v1"

// The operations of a project backup: OperationApply for a project apply,
// OperationFix for the fix of dotai doctor, which writes a project's
// exclude block as an apply does; a restore of the project takes one of
// OperationRestore.
const (
	OperationApply = "apply"
	OperationFix   = "fix"
)

// ProjectOperation reports whether op is the operation of a project backup.
func ProjectOperation(op string) bool {
	return op == OperationApply || op == OperationFix || op == OperationRestore
}

// ProjectManifest describes one project backup: its id, operation and
// time, the project, and each copy it holds.
type ProjectManifest struct {
	Schema    string    `json:"schema"`
	ID        string    `json:"id"`
	Operation string    `json:"operation"`
	CreatedAt time.Time `json:"createdAt"`
	Project   string    `json:"project"`
	Copies    []Copy    `json:"copies"`
}

// Copy is a copy of Target, an absolute path, at File, a slash path in the
// backup directory, with the SHA-256 in hex and the exec bit it had.
type Copy struct {
	Target string `json:"target"`
	File   string `json:"file"`
	SHA256 string `json:"sha256"`
	Exec   bool   `json:"exec"`
}

// CopyFile is the name of the n-th copy of a project backup.
func CopyFile(n int) string { return path.Join(FilesDir, fmt.Sprint(n)) }

// Find is the copy of target in m.
func (m ProjectManifest) Find(target string) (Copy, bool) {
	for _, c := range m.Copies {
		if c.Target == target {
			return c, true
		}
	}
	return Copy{}, false
}

// EncodeProject is the manifest as indented JSON with a final newline.
func EncodeProject(m ProjectManifest) ([]byte, error) {
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(data, '\n'), nil
}

// DecodeProject reads a project manifest of this ProjectSchema taken as id
// for project, with the operation of id, one of a project backup, whose
// copies have absolute targets and their files under FilesDir.
func DecodeProject(data []byte, id, project string) (ProjectManifest, error) {
	var m ProjectManifest
	if err := json.Unmarshal(data, &m); err != nil {
		return ProjectManifest{}, fmt.Errorf("project backup manifest: %w", err)
	}
	op, _ := Operation(id)
	switch {
	case m.Schema != ProjectSchema:
		return ProjectManifest{}, fmt.Errorf("project backup manifest has schema %q, want %q", m.Schema, ProjectSchema)
	case m.ID != id:
		return ProjectManifest{}, fmt.Errorf("project backup manifest has the id %q, want %q", m.ID, id)
	case !ProjectOperation(op) || m.Operation != op:
		return ProjectManifest{}, fmt.Errorf("project backup manifest %s has the operation %q, want %q", id, m.Operation, op)
	case m.Project != project:
		return ProjectManifest{}, fmt.Errorf("project backup %s was taken for the project %s, not %s", id, m.Project, project)
	}
	for i, c := range m.Copies {
		if !filepath.IsAbs(c.Target) || c.File != CopyFile(i) || c.SHA256 == "" {
			return ProjectManifest{}, fmt.Errorf("project backup manifest %s: the copy of %q is not one this format writes", id, c.Target)
		}
	}
	return m, nil
}
