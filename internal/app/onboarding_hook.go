package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/lartweib/dotai/internal/pack"
	"github.com/lartweib/dotai/internal/plan"
	"github.com/lartweib/dotai/internal/state"
)

// fingerprint is what the onboarding record of decision 0045 is a digest
// of: (a) the pack's identity and resolved revision, (b) the names of the
// manifest's profiles, and (c) the sorted names of the profiles with a
// hint that matches a path git tracks.
type fingerprint struct {
	pack     state.OnboardingPack
	profiles []string
	hinted   []string
}

// digest is the SHA-256 in hex of the canonical JSON of f.
func (f fingerprint) digest() string {
	data, err := json.Marshal(struct {
		Pack     state.OnboardingPack `json:"pack"`
		Profiles []string             `json:"profiles"`
		Hinted   []string             `json:"hinted"`
	}{f.pack, f.profiles, f.hinted})
	if err != nil {
		panic(err) // strings always marshal
	}
	h := sha256.Sum256(data)
	return hex.EncodeToString(h[:])
}

// record is the onboarding record of f, taken by source.
func (f fingerprint) record(source string) state.Onboarding {
	return state.Onboarding{Schema: state.OnboardingSchema, Source: source, Digest: f.digest(), Pack: f.pack,
		Profiles: slices.Clone(f.profiles), Hinted: slices.Clone(f.hinted)}
}

// takeFingerprint is the fingerprint of a repository whose index holds
// files, slash paths relative to its top, for the pack of id whose
// profiles are profiles, in the shape profileSummaries gives them, which
// the profiling skill reads too. A hint matches a file by its whole
// relative path, as path.Match reads the pattern, so package.json matches
// no sub/package.json; a pattern path.Match refuses matches nothing.
func takeFingerprint(id state.OnboardingPack, profiles []ProfileSummary, files []string) fingerprint {
	f := fingerprint{pack: id, profiles: []string{}, hinted: []string{}}
	for _, p := range profiles {
		f.profiles = append(f.profiles, p.Name)
		if slices.ContainsFunc(p.Hints, func(h string) bool {
			return slices.ContainsFunc(files, func(name string) bool { ok, _ := path.Match(h, name); return ok })
		}) {
			f.hinted = append(f.hinted, p.Name)
		}
	}
	slices.Sort(f.hinted)
	return f
}

// hinted reports whether a profile of profiles has a hint, without which
// the fingerprint does not depend on the index.
func hinted(profiles []ProfileSummary) bool {
	return slices.ContainsFunc(profiles, func(p ProfileSummary) bool { return len(p.Hints) > 0 })
}

// packIdentity is the identity and the resolved revision of the pack in
// dir, whose manifest has name and version: by its entry in the registry
// of home, the commit of a git pack, and otherwise local/<name> at the
// manifest version.
func packIdentity(home, dir, name, version string) state.OnboardingPack {
	id := state.OnboardingPack{ID: localID(name), Revision: version}
	doc, err := readPacks(home)
	if err != nil {
		return id
	}
	for _, it := range doc.Packs {
		if !samePath(filepath.Clean(it.Path), filepath.Clean(dir)) {
			continue
		}
		if it.ID != "" {
			id.ID = it.ID
		}
		if it.Source != nil && it.Source.Commit != "" {
			id.Revision = it.Source.Commit
		}
		break
	}
	return id
}

// The lines of the session-start hook, each at most 40 tokens (decision
// 0045). Each is dotai's own text: nothing a repository holds, which a
// clone may have committed, such as its marker or the names in its index,
// ever reaches the agent's context.
const (
	hookNotSetUp = "dotai: this repository is not set up with the pack's profiles. Load the dotai-profiles skill to propose them."
	hookChanged  = "dotai: this repository changed since its profiles were last reviewed. Load the dotai-profiles skill to check them."
	hookMismatch = "dotai: the profiles applied here may no longer fit: %s. Load the dotai-profiles skill to check them."
)

// hookMaxLine caps what the hook prints, in bytes: a line past it, or with
// a line break, is silence.
const hookMaxLine = 200

// HookReap is how long the session-start hook waits for the git it runs
// to exit once its bound expired and git was killed, so that it leaves no
// process behind.
const HookReap = 100 * time.Millisecond

// OnboardingHook is what the session-start hook of decision 0045 prints in
// the directory cwd for the user whose home directory is home: one line,
// or "" for none. It is silent outside a git worktree, in the home
// directory or at a root, without an installed pack, and for a pack that
// replaces dotai's onboarding. Without the project marker, it says that the
// repository is not set up, unless a dismissal was taken for the same pack
// and the same hinted profiles; with the marker, it says why the profiles
// may no longer fit when the fingerprint differs from the record's, or the
// record is missing. It finds the worktree without git, and runs git
// ls-files once at most, only when the answer depends on the index; a ctx
// that ends kills that git, and the hook waits HookReap at most for it to
// exit. It never writes anything; an error is the caller's to keep silent.
func OnboardingHook(ctx context.Context, cwd, home string) (string, error) {
	return onboardingHook(ctx, osInstallFS{}, osGit{waitDelay: HookReap}, cwd, home)
}

func onboardingHook(ctx context.Context, d installFS, g gitPort, cwd, home string) (string, error) {
	if !filepath.IsAbs(home) || !filepath.IsAbs(cwd) {
		return "", fmt.Errorf("home directory %q and current directory %q must be absolute", home, cwd)
	}
	home, cwd = filepath.Clean(home), filepath.Clean(cwd)
	project := worktreeTop(cwd)
	if project == "" || checkProject(cwd, home) != nil || checkProject(project, home) != nil {
		return "", nil
	}
	ip, err := readInstalledPack(d, home)
	if err != nil || ip == nil || ip.custom {
		return "", err
	}
	marker, hasMarker, err := readMarker(ctx, d, project)
	if err != nil {
		return "", err
	}
	line, err := hookLine(*ip, marker, hasMarker, readOnboardingRecord(ctx, d, project), func() ([]string, error) { return g.Files(ctx, project) })
	if err != nil || len(line) > hookMaxLine || strings.ContainsAny(line, "\r\n") {
		return "", err
	}
	return line, nil
}

// hookLine is the line of the hook for the installed pack ip, the project
// marker, when there is one, and the onboarding record rec, nil when there
// is none, in a repository whose index files gives, asked only when the
// answer depends on it.
func hookLine(ip installedPack, marker state.Marker, hasMarker bool, rec *state.Onboarding, files func() ([]string, error)) (string, error) {
	names := ip.names()
	if !hasMarker {
		if rec == nil || rec.Source != state.OnboardingDismissed {
			return hookNotSetUp, nil
		}
		if rec.Pack.ID != ip.id.ID {
			return hookChanged, nil
		}
		cur, err := ip.fingerprint(files)
		if err != nil {
			return "", err
		}
		if slices.Equal(rec.Hinted, cur.hinted) {
			return "", nil
		}
		return hookChanged, nil
	}
	// The digest covers the pack and its profiles, so only a record of the
	// same ones can match, and only then is the index asked.
	if rec != nil && rec.Pack == ip.id && slices.Equal(rec.Profiles, names) {
		cur, err := ip.fingerprint(files)
		if err != nil {
			return "", err
		}
		if rec.Digest == cur.digest() {
			return "", nil
		}
	}
	return fmt.Sprintf(hookMismatch, mismatchReason(marker, rec, ip.id, names)), nil
}

// installedPack is the pack the install state records, as the hook and a
// dismissal read it: its identity, its profiles, and whether it replaces
// dotai's onboarding, when only that is set.
type installedPack struct {
	id       state.OnboardingPack
	profiles []ProfileSummary
	custom   bool
}

// names are the names of the profiles of p.
func (p installedPack) names() []string {
	out := make([]string, 0, len(p.profiles))
	for _, pr := range p.profiles {
		out = append(out, pr.Name)
	}
	return out
}

// fingerprint is the fingerprint of the repository whose index files
// gives, asked only when a profile has a hint.
func (p installedPack) fingerprint(files func() ([]string, error)) (fingerprint, error) {
	var index []string
	if hinted(p.profiles) {
		var err error
		if index, err = files(); err != nil {
			return fingerprint{}, err
		}
	}
	return takeFingerprint(p.id, p.profiles, index), nil
}

// readInstalledPack reads the pack the install state of home records from
// its manifest alone, nil when there is none or its manifest cannot be
// used.
func readInstalledPack(d installFS, home string) (*installedPack, error) {
	prev, err := readState(d, home)
	if err != nil || prev == nil || prev.Pack.Path == "" {
		return nil, err
	}
	m, _, err := pack.ReadManifest(os.DirFS(prev.Pack.Path))
	if err != nil || m == nil {
		return nil, err
	}
	if m.Onboarding.Custom != "" {
		return &installedPack{custom: true}, nil
	}
	return &installedPack{id: packIdentity(home, prev.Pack.Path, m.Name, m.Version), profiles: profileSummaries(m.Profiles)}, nil
}

// currentFingerprint is the fingerprint of project for the pack the
// install state of home records, nil when there is none, and whether that
// pack replaces dotai's onboarding.
func currentFingerprint(ctx context.Context, d installFS, g gitPort, home, project string) (*fingerprint, bool, error) {
	ip, err := readInstalledPack(d, home)
	if err != nil || ip == nil || ip.custom {
		return nil, ip != nil && ip.custom, err
	}
	f, err := ip.fingerprint(func() ([]string, error) { return g.Files(ctx, project) })
	if err != nil {
		return nil, false, err
	}
	return &f, false, nil
}

// worktreeTop is the top of the git worktree dir is in, found as git finds
// it but without running git, which can be slow to start: the first
// directory from dir up with a .git, a directory that is a git directory
// or a file naming one, as a linked worktree and a submodule have. It is
// "" when there is none, when dir is inside a git directory, as in a bare
// repository, which has no worktree, and when a .git cannot be read or
// names nothing.
func worktreeTop(dir string) string {
	for {
		if isGitDir(dir) {
			return ""
		}
		name := filepath.Join(dir, ".git")
		fi, err := os.Stat(name)
		switch {
		case err == nil && fi.IsDir() && isGitDir(name):
			return dir
		case err == nil && fi.Mode().IsRegular():
			if isGitFile(name) {
				return dir
			}
			return ""
		case err != nil && !errors.Is(err, fs.ErrNotExist):
			return ""
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
}

// isGitDir reports whether dir is a git directory, as git tells one: a
// HEAD file, and either refs/ and objects/ or, in a linked worktree's, a
// commondir file.
func isGitDir(dir string) bool {
	is := func(name string, wantDir bool) bool {
		fi, err := os.Stat(filepath.Join(dir, name))
		return err == nil && (wantDir && fi.IsDir() || !wantDir && fi.Mode().IsRegular())
	}
	return is("HEAD", false) && (is("refs", true) && is("objects", true) || is("commondir", false))
}

// isGitFile reports whether the file name is a gitfile, which names the
// git directory of a linked worktree or a submodule: one that starts with
// "gitdir: ".
func isGitFile(name string) bool {
	f, err := os.Open(name)
	if err != nil {
		return false
	}
	defer f.Close()
	buf := make([]byte, len("gitdir: "))
	_, err = io.ReadFull(f, buf)
	return err == nil && string(buf) == "gitdir: "
}

// readMarker reads the project marker of project, and whether there is
// one; a marker that is not one dotai writes reads as one without
// profiles.
func readMarker(ctx context.Context, d installFS, project string) (state.Marker, bool, error) {
	data, _, ok, err := readTarget(ctx, d, project, filepath.Join(project, filepath.FromSlash(plan.MarkerFile)))
	if err != nil || !ok {
		return state.Marker{}, false, err
	}
	var m state.Marker
	_ = json.Unmarshal(data, &m)
	return m, true, nil
}

// readOnboardingRecord reads the onboarding record of project, nil when
// it is missing or is not one dotai writes.
func readOnboardingRecord(ctx context.Context, d installFS, project string) *state.Onboarding {
	data, _, ok, err := readTarget(ctx, d, project, filepath.Join(project, filepath.FromSlash(plan.OnboardingFile)))
	if err != nil || !ok {
		return nil
	}
	o, err := state.DecodeOnboarding(data)
	if err != nil {
		return nil
	}
	return &o
}

// mismatchReason says why the profiles of marker may no longer fit the
// repository, for the pack of id whose profiles are names, against the
// record rec, nil when there is none. It is dotai's own text: the marker
// is the repository's, so no name it holds is told.
func mismatchReason(marker state.Marker, rec *state.Onboarding, id state.OnboardingPack, names []string) string {
	for _, p := range marker.Profiles {
		if !slices.Contains(names, p) {
			return "a profile applied here is gone from the pack"
		}
	}
	switch {
	case rec == nil:
		return "no fingerprint was recorded"
	case rec.Pack != id:
		return "the pack changed"
	case !slices.Equal(rec.Profiles, names):
		return "the pack's profiles changed"
	default:
		return "the files the profiles' hints match changed"
	}
}
