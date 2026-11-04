package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"slices"

	"github.com/lartweib/dotai/internal/state"
)

// Status is what dotai recorded in a home directory: the install state at
// StatePath, nil when there is no install, and each project it applied to,
// from the records under ~/.dotai/projects and the list of projects at
// ProjectsPath. What cannot be read is reported, StateError for the state
// and ProjectsError for the list, "" when it was read, and the rest is
// still read. Projects is never nil.
type Status struct {
	StatePath     string          `json:"statePath"`
	State         *state.State    `json:"state"`
	StateError    string          `json:"stateError"`
	ProjectsPath  string          `json:"projectsPath"`
	ProjectsError string          `json:"projectsError"`
	Projects      []ProjectStatus `json:"projects"`
}

// ProjectStatus is one project dotai applied to: its path, "" when its
// record does not say, the path of its record and the record, nil when it
// is missing or cannot be read, Error saying why, and whether the list of
// projects names it.
type ProjectStatus struct {
	Project    string         `json:"project"`
	RecordPath string         `json:"recordPath"`
	Record     *state.Project `json:"record"`
	Listed     bool           `json:"listed"`
	Error      string         `json:"error"`
}

// ReadStatus reads what dotai recorded in the absolute home directory
// home, as Status describes, without writing anything and without
// following a link. Only a home that is not absolute is an error.
func ReadStatus(home string) (Status, error) {
	return readStatus(context.Background(), osInstallFS{}, home)
}

func readStatus(ctx context.Context, d installFS, home string) (Status, error) {
	home, err := requireAbs(home)
	if err != nil {
		return Status{}, err
	}
	st := Status{
		StatePath:    filepath.Join(home, dotaiDir, stateFile),
		ProjectsPath: filepath.Join(home, dotaiDir, projectsFile),
		Projects:     []ProjectStatus{},
	}
	s, err := readState(d, home)
	if err != nil {
		st.StateError = err.Error()
	}
	st.State = s
	list, err := readProjects(d, home)
	if err != nil {
		st.ProjectsError = err.Error()
	}
	records, err := readRecords(ctx, d, home)
	if err != nil && st.ProjectsError == "" {
		st.ProjectsError = err.Error()
	} else if err != nil {
		st.ProjectsError += "; " + err.Error()
	}
	for _, r := range records {
		r.Listed = r.Project != "" && list.Entry(r.Project) != nil
		st.Projects = append(st.Projects, r)
	}
	for _, e := range list.Projects {
		if !slices.ContainsFunc(records, func(r ProjectStatus) bool { return r.Project == e.Project }) {
			st.Projects = append(st.Projects, ProjectStatus{Project: e.Project, RecordPath: recordPath(home, e.Project), Listed: true,
				Error: "the list of projects names it, but it has no record " + recordPath(home, e.Project)})
		}
	}
	return st, nil
}

// readRecords reads the record in each project directory under
// ~/.dotai/projects, in the order of their names. A directory without a
// record, which keeps only the backups of a restored project, is skipped;
// a record that cannot be read, or that is not the record of the project
// its directory is named for, is reported with its error.
func readRecords(ctx context.Context, d installFS, home string) ([]ProjectStatus, error) {
	root := filepath.Join(home, dotaiDir, projectsDir)
	ids, err := listDir(d, root)
	if err != nil {
		return nil, err
	}
	var out []ProjectStatus
	for _, id := range ids {
		name := filepath.Join(root, id, recordName)
		data, _, ok, err := readTarget(ctx, d, home, name)
		if err == nil && !ok {
			continue
		}
		ps := ProjectStatus{RecordPath: name}
		if err == nil {
			ps.Project, ps.Record, err = decodeRecord(data, id)
		}
		if err != nil {
			ps.Error = err.Error()
		}
		out = append(out, ps)
	}
	return out, nil
}

// projectNamedFor returns the project that data, a project record or a
// project backup manifest, names in its project field. It fails unless
// the project is absolute and id is its project id, the name of the
// directory under ~/.dotai/projects that data was read from.
func projectNamedFor(data []byte, id string) (string, error) {
	var head struct {
		Project string `json:"project"`
	}
	if err := json.Unmarshal(data, &head); err != nil {
		return "", err
	}
	if !filepath.IsAbs(head.Project) || projectID(head.Project) != id {
		return "", errors.New("it is not the one of the project its directory is named for")
	}
	return head.Project, nil
}

// decodeRecord decodes data, the record in the project directory id, and
// returns its project, and the record when it is valid.
func decodeRecord(data []byte, id string) (string, *state.Project, error) {
	project, err := projectNamedFor(data, id)
	if err != nil {
		return "", nil, fmt.Errorf("project record: %w", err)
	}
	r, err := state.DecodeProject(data, project)
	if err != nil {
		return project, nil, err
	}
	return project, &r, nil
}

// listDir lists the directory dir, not followed when it is a link, in name
// order; nil when it is missing.
func listDir(d installFS, dir string) ([]string, error) {
	err := checkDir(d, dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	names, err := d.List(dir)
	if err != nil {
		return nil, err
	}
	slices.Sort(names)
	return names, nil
}
