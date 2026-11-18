package app

import (
	"context"
	"fmt"
	"path/filepath"
	"slices"

	"github.com/lartweib/dotai/internal/backup"
	"github.com/lartweib/dotai/internal/plan"
	"github.com/lartweib/dotai/internal/state"
)

// DismissResult is a dismissal of the onboarding prompt (decision 0045):
// the project, the onboarding record it wrote, <project>/.dotai/
// onboarding.json, the project's record under the home directory, the
// fingerprint it took, whether it found that record there already, the
// backup of the write, nil when nothing was written, and warnings, such as
// a cleanup that failed.
type DismissResult struct {
	Project     string           `json:"project"`
	Onboarding  string           `json:"onboarding"`
	Record      string           `json:"record"`
	Fingerprint state.Onboarding `json:"fingerprint"`
	Unchanged   bool             `json:"unchanged"`
	Backup      *BackupRef       `json:"backup"`
	Warnings    []plan.Warning   `json:"warnings"`
}

// dotaiExclude is the exclude entry of the project's .dotai/.
const dotaiExclude = "/.dotai/"

// DismissOnboarding records in the project of the current directory cwd,
// for the user whose home directory is home, that its onboarding prompt
// was dismissed: the fingerprint of the repository for the pack the
// install state records, in .dotai/onboarding.json, so that the
// session-start hook stays silent until the pack, its profiles or the
// hinted profiles change. It works in a git repository only, where the
// hook speaks, and refuses the home directory and a root, a pack that
// replaces dotai's onboarding and a home without an installed pack. The
// write is an apply's: a journal an apply or a restore left is rolled back
// first, then under the lock, with a backup, a journal and a rollback, it
// writes the record and the project's record under the home directory. In
// a project where no apply ran, it creates .dotai/ and its exclude entry,
// and records them, so that dotai restore --project removes them; in one
// with an apply, it leaves the apply's files, marker and record as they
// are. A record that holds the same fingerprint writes nothing.
func DismissOnboarding(ctx context.Context, cwd, home string) (DismissResult, error) {
	return dismissOnboarding(ctx, osInstallFS{}, osGit{}, cwd, home)
}

func dismissOnboarding(ctx context.Context, d executeFS, g gitPort, cwd, home string) (DismissResult, error) {
	if !filepath.IsAbs(home) || !filepath.IsAbs(cwd) {
		return DismissResult{}, fmt.Errorf("home directory %q and current directory %q must be absolute", home, cwd)
	}
	home, cwd = filepath.Clean(home), filepath.Clean(cwd)
	if err := settleProject(ctx, d, g, cwd, home); err != nil {
		return DismissResult{}, err
	}
	project, mcpKey, repo, err := findProject(ctx, g, cwd, home)
	if err != nil {
		return DismissResult{}, err
	}
	if repo == nil {
		return DismissResult{}, &UsageError{fmt.Sprintf("%s is not in a git repository, where the session-start hook never speaks, so there is nothing to dismiss", cwd)}
	}
	prev, err := readState(d, home)
	if err != nil {
		return DismissResult{}, err
	}
	if prev == nil || prev.Pack.Path == "" {
		return DismissResult{}, &UsageError{"no pack is installed: dotai onboarding dismiss records a fingerprint of the installed pack, so install one first"}
	}
	f, custom, err := currentFingerprint(ctx, d, g, home, project)
	switch {
	case err != nil:
		return DismissResult{}, err
	case custom:
		return DismissResult{}, &UsageError{fmt.Sprintf("the pack %s replaces dotai's onboarding, whose prompt it never shows", prev.Pack.Name)}
	case f == nil:
		return DismissResult{}, fmt.Errorf("the manifest of the installed pack %s cannot be read", prev.Pack.Path)
	}
	target := filepath.Join(project, filepath.FromSlash(plan.OnboardingFile))
	tracked, err := g.Tracked(ctx, project, []string{plan.OnboardingFile})
	if err != nil {
		return DismissResult{}, err
	}
	if tracked[plan.OnboardingFile] {
		return DismissResult{}, fmt.Errorf("git tracks %s, the onboarding record, which stays local: untrack it (git rm --cached), then dismiss again", target)
	}
	rec := f.record(state.OnboardingDismissed)
	data, err := state.EncodeOnboarding(rec)
	if err != nil {
		return DismissResult{}, err
	}
	sc := scopeOf(home, project, mcpKey, repo)
	record, err := readRecord(ctx, d, sc)
	if err != nil {
		return DismissResult{}, err
	}
	shared, err := readProjects(d, home)
	if err != nil {
		return DismissResult{}, err
	}
	pl, err := dismissPlan(ctx, d, home, project, repo, data)
	if err != nil {
		return DismissResult{}, err
	}
	pl.scope, pl.pack, pl.shared = sc, prev.Pack, shared
	if record != nil {
		carryApply(&pl, *record, shared)
	}
	res := ApplyResult{ProjectPlan: pl.plan}
	a := applier{runner: runner{ctx: ctx, d: d, home: home, p: planned{ignoreExec: !d.ExecBit()}}, p: pl}
	if err := a.run(&res); err != nil {
		return DismissResult{}, err
	}
	return DismissResult{Project: project, Onboarding: target, Record: pl.plan.Record, Fingerprint: rec, Unchanged: res.Backup == nil, Backup: res.Backup, Warnings: res.Warnings}, nil
}

// dismissPlan is the plan of a dismissal in the project of repo, before
// any apply ran there: one op, which writes the onboarding record data,
// unchanged when it holds that already, with .dotai/ in the exclude file,
// and a record that owns nothing else, DismissOnly.
func dismissPlan(ctx context.Context, d installFS, home, project string, repo *Repo, data []byte) (projectPlanned, error) {
	target := filepath.Join(project, filepath.FromSlash(plan.OnboardingFile))
	op := plan.ProjectOp{Action: plan.Create, Kind: plan.KindOnboarding, Target: target, Keys: []plan.ProjectKey{}, Data: data, After: contentHash(data)}
	in := plan.ProjectInput{Project: project, Home: home, Repo: true, Disk: map[string]plan.Current{}, Tracked: map[string]bool{}}
	_, cur, exists, err := readTarget(ctx, d, project, target)
	if err != nil {
		return projectPlanned{}, err
	}
	if exists {
		in.Disk[target] = cur
		op.Before, op.Action = cur.Hash, plan.Replace
		if cur.Hash == op.After {
			op.Action = plan.Unchanged
		}
	}
	pp := ProjectPlan{
		Project: project, Record: recordPath(home, project), Marker: filepath.Join(project, filepath.FromSlash(plan.MarkerFile)),
		Profiles: []string{}, Harnesses: []string{}, HarnessStates: []HarnessState{}, Ops: []plan.ProjectOp{op}, Skipped: []plan.Skipped{},
		Exclude:  ExcludePlan{File: repo.Exclude, Entries: []string{dotaiExclude}},
		Warnings: []plan.Warning{}, Refusals: []plan.Warning{}, Notices: []string{}, Diagnostics: []Diagnostic{},
	}
	return projectPlanned{plan: pp, in: in, operation: backup.OperationDismiss, dismissOnly: true}, nil
}

// carryApply makes the dismissal pl keep the project record r of an
// earlier apply, or dismissal, as it is: its variant, profiles and
// harnesses, the files and keys it owns, its exclude block, with .dotai/
// added when it lacks it, its pack and its entry in the list of projects
// shared, so that only the onboarding record and a backup are added.
func carryApply(pl *projectPlanned, r state.Project, shared state.Projects) {
	pl.prev = &r
	pl.plan.Variant, pl.plan.Profiles, pl.plan.Harnesses = r.Variant, slices.Clone(r.Profiles), slices.Clone(r.Harnesses)
	pl.plan.Exclude = ExcludePlan{File: r.Exclude.File, Pending: r.Exclude.Pending, Entries: slices.Clone(r.Exclude.Entries)}
	if !slices.Contains(pl.plan.Exclude.Entries, dotaiExclude) {
		pl.plan.Exclude.Entries = append([]string{dotaiExclude}, pl.plan.Exclude.Entries...)
	}
	pl.pack, pl.kept, pl.dismissOnly = r.Pack, r.Owned(), r.DismissOnly
	if e := shared.Entry(r.Project); e != nil {
		pl.homeMCP = e.File
	}
}
