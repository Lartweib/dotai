package app

import (
	"context"

	"github.com/lartweib/dotai/internal/rulesync"
)

// Ports are what the install and restore use cases reach besides the
// disk: fetching and running rulesync, and listing the running processes
// for the running-harness check (decision 0038). A nil field is the real
// one, so the zero value is what PlanInstall, Install and Restore use; a
// test of the command line sets fakes, so that it needs no network and
// sees no real harness running.
type Ports struct {
	FetchRulesync func(ctx context.Context, cacheDir, version string, progress Progress) (rulesync.Result, error)
	RunRulesync   func(ctx context.Context, bin rulesync.Result, harnesses []string, jobs ...rulesync.Job) error
	Processes     processList
}

func (p Ports) fetch() fetchFunc {
	if p.FetchRulesync == nil {
		return download
	}
	return p.FetchRulesync
}

func (p Ports) run() runFunc {
	if p.RunRulesync == nil {
		return generate
	}
	return p.RunRulesync
}

func (p Ports) host() host {
	h := osHost()
	if p.Processes != nil {
		h.processes = p.Processes
	}
	return h
}

// PlanInstall is the package's PlanInstall through p.
func (p Ports) PlanInstall(ctx context.Context, cacheDir, dir, home string, opts InstallOptions, progress Progress) (InstallPlan, error) {
	return planInstall(ctx, osInstallFS{}, p.host(), p.fetch(), p.run(), cacheDir, dir, home, opts, progress)
}

// Install is the package's Install through p.
func (p Ports) Install(ctx context.Context, cacheDir, dir, home string, opts InstallOptions, progress Progress, warn Warn, consent Consent) (InstallResult, error) {
	return install(ctx, osInstallFS{}, p.host(), p.fetch(), p.run(), cacheDir, dir, home, opts, progress, warn, consent)
}

// Restore is the package's Restore through p.
func (p Ports) Restore(ctx context.Context, home, id string, opts RestoreOptions, warn Warn, overwrite func(RestoreResult) (bool, error)) (RestoreResult, error) {
	return restore(ctx, osInstallFS{}, p.host(), home, id, opts, warn, overwrite)
}

// PlanProject is the package's PlanProject through p, asking the git on
// PATH.
func (p Ports) PlanProject(ctx context.Context, cacheDir, dir, cwd, home string, opts ApplyOptions, progress Progress) (ProjectPlan, error) {
	return planProject(ctx, osInstallFS{}, p.host(), osGit{}, p.fetch(), p.run(), cacheDir, dir, cwd, home, opts, progress)
}

// Apply is the package's Apply through p, asking the git on PATH.
func (p Ports) Apply(ctx context.Context, cacheDir, dir, cwd, home string, opts ApplyOptions, progress Progress, consent ApplyConsent) (ApplyResult, error) {
	return applyProject(ctx, osInstallFS{}, p.host(), osGit{}, p.fetch(), p.run(), cacheDir, dir, cwd, home, opts, progress, consent)
}
