package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"slices"
	"time"

	"github.com/lartweib/dotai/internal/backup"
)

// Backups is every backup dotai keeps in a home directory: those of the
// installs and restores of the user scope under ~/.dotai/backups, and
// those of each project under ~/.dotai/projects. Lists are never nil.
type Backups struct {
	Home     []BackupInfo     `json:"home"`
	Projects []ProjectBackups `json:"projects"`
}

// ProjectBackups lists the backups of one project directory under
// ~/.dotai/projects, newest first. Project is "" when neither the record
// nor a manifest names the project, and Path is the directory of the
// backups. Error says why the directory could not be read, such as an
// entry that is a file or a link, and is "" otherwise.
type ProjectBackups struct {
	Project string       `json:"project"`
	Path    string       `json:"path"`
	Error   string       `json:"error"`
	Backups []BackupInfo `json:"backups"`
}

// BackupInfo is one backup: its id, directory, operation and time, read
// from its id, and whether its manifest is one dotai writes, Error saying
// why not. A home backup also tells what its operation installed, whether
// a restore undid it and whether it is the backup the install state names,
// the one a plain restore undoes. Targets is what it covers, sorted: the
// files and entries its operation wrote, moved or rewrote by key, never
// nil, and empty when the manifest cannot be read.
type BackupInfo struct {
	ID        string    `json:"id"`
	Path      string    `json:"path"`
	Operation string    `json:"operation"`
	CreatedAt time.Time `json:"createdAt"`
	Valid     bool      `json:"valid"`
	Error     string    `json:"error"`
	Targets   []string  `json:"targets"`
	// Of a home backup only.
	Mode      string       `json:"mode,omitempty"`
	Pack      *backup.Pack `json:"pack,omitempty"`
	Variant   string       `json:"variant,omitempty"`
	Harnesses []string     `json:"harnesses,omitempty"`
	Restored  bool         `json:"restored"`
	Head      bool         `json:"head"`
}

// ListBackups lists the backups in the absolute home directory home, as
// Backups describes, without writing anything. A backup whose manifest is
// missing, cannot be read or is not one dotai writes is listed as invalid
// with the reason, and the rest are still read. A project directory that
// cannot be read, or an entry under ~/.dotai/projects that is not a
// directory, is listed with its error and no backups. A name under the
// backups that is not a backup id is left out. ListBackups fails only for
// a home that is not absolute, or when ~/.dotai/backups or
// ~/.dotai/projects itself cannot be listed.
func ListBackups(home string) (Backups, error) {
	return listBackups(context.Background(), osInstallFS{}, home)
}

func listBackups(ctx context.Context, d installFS, home string) (Backups, error) {
	if !filepath.IsAbs(home) {
		return Backups{}, fmt.Errorf("home directory %q is not absolute", home)
	}
	home = filepath.Clean(home)
	out := Backups{Home: []BackupInfo{}, Projects: []ProjectBackups{}}
	head := ""
	if s, err := readState(d, home); err == nil && s != nil {
		head = s.Backup
	}
	root := filepath.Join(home, dotaiDir, backupsDir)
	ids, err := backupIDs(d, root)
	if err != nil {
		return Backups{}, err
	}
	for _, id := range ids {
		b := homeBackup(d, home, root, id)
		b.Head = id == head
		out.Home = append(out.Home, b)
	}
	projects := filepath.Join(home, dotaiDir, projectsDir)
	dirs, err := listDir(d, projects)
	if err != nil {
		return Backups{}, err
	}
	for _, dir := range dirs {
		p := projectBackupList(ctx, d, home, filepath.Join(projects, dir), dir)
		if len(p.Backups) > 0 || p.Error != "" {
			out.Projects = append(out.Projects, p)
		}
	}
	return out, nil
}

// backupIDs is the names in root that are backup ids, newest first.
func backupIDs(d installFS, root string) ([]string, error) {
	names, err := listDir(d, root)
	if err != nil {
		return nil, err
	}
	var ids []string
	for _, n := range names {
		if _, ok := backup.ParseID(n); ok {
			ids = append(ids, n)
		}
	}
	slices.Reverse(ids)
	return ids, nil
}

// backupHead is the BackupInfo of the backup id in root from its id alone.
func backupHead(root, id string) BackupInfo {
	t, _ := backup.ParseID(id)
	op, _ := backup.Operation(id)
	return BackupInfo{ID: id, Path: filepath.Join(root, id), Operation: op, CreatedAt: t, Targets: []string{}}
}

// readManifest reads the manifest of the backup at dir.
func readManifest(d installFS, dir string) ([]byte, error) {
	data, err := d.ReadFile(dir, backup.ManifestName)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("the backup has no %s: it is incomplete", backup.ManifestName)
	}
	return data, err
}

// homeBackup reads the backup id in root, the backups of home.
func homeBackup(d installFS, home, root, id string) BackupInfo {
	b := backupHead(root, id)
	data, err := readManifest(d, b.Path)
	var m backup.Manifest
	if err == nil {
		m, err = backup.Decode(data)
	}
	if err == nil {
		err = backup.Check(m, id, home)
	}
	if err != nil {
		b.Error = err.Error()
		return b
	}
	b.Valid = true
	b.Mode, b.Variant, b.Harnesses, b.Restored = m.Mode, m.Variant, m.Harnesses, m.Restored != nil
	pack := m.Pack
	b.Pack = &pack
	for _, op := range m.Ops {
		b.Targets = append(b.Targets, op.Target)
	}
	for _, w := range m.Wipes {
		b.Targets = append(b.Targets, w.Path)
	}
	for _, s := range m.Splits {
		b.Targets = append(b.Targets, s.Target)
	}
	slices.Sort(b.Targets)
	b.Targets = slices.Compact(b.Targets)
	return b
}

// projectBackupList reads the backups of the project directory dir, named
// id. Its project is the one of its record, else the one its newest
// readable manifest names.
func projectBackupList(ctx context.Context, d installFS, home, dir, id string) ProjectBackups {
	root := filepath.Join(dir, backupsDir)
	p := ProjectBackups{Path: root, Backups: []BackupInfo{}}
	if err := checkDir(d, dir); err != nil {
		p.Error = err.Error()
		return p
	}
	if data, _, ok, err := readTarget(ctx, d, home, filepath.Join(dir, recordName)); err == nil && ok {
		if project, _, err := decodeRecord(data, id); err == nil || project != "" {
			p.Project = project
		}
	}
	ids, err := backupIDs(d, root)
	if err != nil {
		p.Error = err.Error()
		return p
	}
	for _, bid := range ids {
		b := backupHead(root, bid)
		data, err := readManifest(d, b.Path)
		if err == nil && p.Project == "" {
			p.Project = manifestProject(data, id)
		}
		var m backup.ProjectManifest
		if err == nil {
			m, err = backup.DecodeProject(data, bid, p.Project)
		}
		if err != nil {
			b.Error = err.Error()
			p.Backups = append(p.Backups, b)
			continue
		}
		b.Valid = true
		for _, c := range m.Copies {
			b.Targets = append(b.Targets, c.Target)
		}
		slices.Sort(b.Targets)
		b.Targets = slices.Compact(b.Targets)
		p.Backups = append(p.Backups, b)
	}
	return p
}

// manifestProject is the project the project manifest data names, "" when
// it names none that the directory id is for.
func manifestProject(data []byte, id string) string {
	var head struct {
		Project string `json:"project"`
	}
	if json.Unmarshal(data, &head) != nil || !filepath.IsAbs(head.Project) || projectID(head.Project) != id {
		return ""
	}
	return head.Project
}
