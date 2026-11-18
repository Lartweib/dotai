package app

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"testing/fstest"

	"github.com/lartweib/dotai/internal/pack"
	"github.com/lartweib/dotai/internal/packtemplate"
)

// The wizard of decision 0046: a new pack is the embedded template filled
// in with its name, its harnesses and its profiles, written to a new or
// empty directory and added as a local pack, as dotai pack add --dir does.

// PackNewInput is what a new pack is made of: its name, which follows the
// rule for mod names, the harnesses it requires, and its profiles.
type PackNewInput = packtemplate.Answers

// PackNewProfile is a profile of a new pack: its name, which follows the
// rule for mod names too, its description and hints, which the profiling
// skill lists, and its mods, each written from the template's example mod.
type PackNewProfile = packtemplate.Profile

// PackNewPlan is what dotai pack new writes: the directory, the identity
// the pack gets, its harnesses and profiles, the files, the profiling
// skill dotai will generate from its manifest (decision 0045), and the
// warnings of its validation. Every file below the pack's top is the
// template's, so its template-unchanged warnings are left out. Lists are
// never nil.
type PackNewPlan struct {
	Dir       string            `json:"dir"`
	ID        string            `json:"id"`
	Name      string            `json:"name"`
	Harnesses []string          `json:"harnesses"`
	Profiles  []ProfileSummary  `json:"profiles"`
	Files     []string          `json:"files"`
	Skill     string            `json:"skill"`
	Warnings  []pack.Diagnostic `json:"warnings"`
}

// PackNewResult is a new pack written and added: its plan, and the
// addition to the registry, with its warnings.
type PackNewResult struct {
	PackNewPlan
	Added PackAddResult `json:"added"`
}

// PackNewOptions are the choices of a new pack besides its input: Confirm,
// when not nil, is asked with the plan before anything is written, so
// that a caller writes only the plan its user saw; an error stops it.
type PackNewOptions struct {
	Confirm func(PackNewPlan) error
}

// NewRefusedError refuses a new pack before it writes anything: its
// directory is not empty, is not a directory, or has no parent.
type NewRefusedError struct{ msg string }

func (e *NewRefusedError) Error() string { return e.msg }

// PlanPackNew plans a new pack in dir, absolute, for home's registry, and
// writes nothing. dir must be a new directory, whose parent exists, or an
// empty one (a *NewRefusedError otherwise); the pack name, the profile
// names and the mod names must follow the rule for mod names, and the
// pack's identity, local/<name>, must be free in the registry (a
// *PackNameTakenError). The filled template is validated before anything
// is written: a pack with problems, such as no harness, is a
// *PackInvalidError with the result.
func PlanPackNew(home, dir string, in PackNewInput) (PackNewPlan, error) {
	p, _, err := planPackNew(home, dir, in)
	return p, err
}

func planPackNew(home, dir string, in PackNewInput) (PackNewPlan, []packtemplate.File, error) {
	home, err := requireAbs(home)
	if err != nil {
		return PackNewPlan{}, nil, err
	}
	if !filepath.IsAbs(dir) {
		return PackNewPlan{}, nil, &UsageError{msg: fmt.Sprintf("the pack path %q is not absolute", dir)}
	}
	dir = filepath.Clean(dir)
	if problem := localNameProblem(in.Name); problem != "" {
		return PackNewPlan{}, nil, &UsageError{msg: fmt.Sprintf("the pack name %q %s, as a mod name does", in.Name, problem)}
	}
	files, err := packtemplate.Fill(in)
	if err != nil {
		return PackNewPlan{}, nil, &UsageError{msg: err.Error()}
	}
	if err := newTarget(dir); err != nil {
		return PackNewPlan{}, nil, err
	}
	doc, err := readPacks(home)
	if err != nil {
		return PackNewPlan{}, nil, err
	}
	id := localID(in.Name)
	for _, e := range describePacks(home, doc).Packs {
		if e.ID == id {
			return PackNewPlan{}, nil, &PackNameTakenError{ID: id, Path: e.Path}
		}
	}
	fsys := fstest.MapFS{}
	paths := make([]string, 0, len(files))
	for _, f := range files {
		fsys[f.Path] = &fstest.MapFile{Data: f.Data, Mode: f.Mode}
		paths = append(paths, f.Path)
	}
	res, err := ValidateFS(fsys, Selection{})
	if err != nil {
		return PackNewPlan{}, nil, err
	}
	if !res.Valid || res.Pack == nil {
		return PackNewPlan{}, nil, &PackInvalidError{Path: dir, Result: res}
	}
	warnings := slices.DeleteFunc(slices.Clone(res.Warnings), func(d pack.Diagnostic) bool { return d.Code == CodeTemplateUnchanged })
	return PackNewPlan{Dir: dir, ID: id, Name: in.Name, Harnesses: append([]string{}, res.Pack.Harnesses...), Profiles: res.Pack.Profiles,
		Files: paths, Skill: string(ProfilesSkill(*res.Pack)), Warnings: warnings}, files, nil
}

// newTarget checks that dir is a new directory whose parent is a
// directory, or an empty directory.
func newTarget(dir string) error {
	info, err := os.Lstat(dir)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		if parent, err := os.Stat(filepath.Dir(dir)); err != nil || !parent.IsDir() {
			return &NewRefusedError{msg: fmt.Sprintf("the parent directory of %s does not exist", dir)}
		}
		return nil
	case err != nil:
		return err
	case !info.IsDir():
		return &NewRefusedError{msg: fmt.Sprintf("%s exists and is not a directory: name a new or empty directory for the pack", dir)}
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	if len(entries) > 0 {
		return &NewRefusedError{msg: fmt.Sprintf("%s is not empty: name a new or empty directory for the pack", dir)}
	}
	return nil
}

// NewPack plans a new pack as PlanPackNew does, asks opts.Confirm, writes
// it and adds it to home's registry as AddLocalPack does. It writes into a
// new directory next to dir and renames it to dir at the end, so that a
// write that fails leaves nothing; an empty dir is replaced. A pack
// written but not added, as when another directory took its name
// meanwhile, stays where it is, and the error says so.
func NewPack(home, dir string, in PackNewInput, opts PackNewOptions) (PackNewResult, error) {
	p, files, err := planPackNew(home, dir, in)
	if err != nil {
		return PackNewResult{}, err
	}
	if opts.Confirm != nil {
		if err := opts.Confirm(p); err != nil {
			return PackNewResult{}, err
		}
	}
	if err := writeNewPack(p.Dir, files); err != nil {
		return PackNewResult{}, err
	}
	added, err := AddLocalPack(home, p.Dir)
	if err != nil {
		return PackNewResult{}, fmt.Errorf("the pack was written to %s but not added: %w; add it with dotai pack add --dir", p.Dir, err)
	}
	return PackNewResult{PackNewPlan: p, Added: added}, nil
}

// writeNewPack writes files into a temporary sibling of dir, then renames
// it to dir, removing dir first when it is an empty directory.
func writeNewPack(dir string, files []packtemplate.File) error {
	tmp, err := os.MkdirTemp(filepath.Dir(dir), "."+filepath.Base(dir)+".new-")
	if err != nil {
		return err
	}
	fail := func(err error) error { return errors.Join(err, os.RemoveAll(tmp)) }
	for _, f := range files {
		target := filepath.Join(tmp, filepath.FromSlash(f.Path))
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return fail(err)
		}
		if err := os.WriteFile(target, f.Data, f.Mode.Perm()); err != nil {
			return fail(err)
		}
	}
	if err := os.Chmod(tmp, 0o755); err != nil {
		return fail(err)
	}
	if err := newTarget(dir); err != nil {
		return fail(err)
	}
	if _, err := os.Lstat(dir); err == nil {
		if err := os.Remove(dir); err != nil {
			return fail(&NewRefusedError{msg: fmt.Sprintf("%s could not be replaced: %v", dir, err)})
		}
	}
	if err := os.Rename(tmp, dir); err != nil {
		return fail(err)
	}
	return nil
}
