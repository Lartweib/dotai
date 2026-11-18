package app

import (
	"cmp"
	"context"
	"fmt"
	"path/filepath"
	"runtime"
	"time"

	"github.com/lartweib/dotai/internal/proc"
	"github.com/lartweib/dotai/internal/state"
)

// OSStageFS is the real file system port, for fakes that wrap it.
type OSStageFS = osStageFS

// StageFS is the port Stage writes through.
type StageFS = stageFS

// StageWith is Stage writing through w, fetching through fetch and
// running rulesync through run.
func StageWith(w stageFS, fetch fetchFunc, run runFunc, dir, out string, sel Selection, tools ToolPaths, force bool) (StageResult, error) {
	return stage(context.Background(), w, fetch, run, "cache", dir, out, sel, nil, tools, force, nil)
}

// FetchRulesyncWith is FetchRulesync downloading through fetch.
func FetchRulesyncWith(ctx context.Context, fetch fetchFunc, cacheDir, dir string) (RulesyncResult, error) {
	return fetchRulesync(ctx, fetch, cacheDir, dir, nil)
}

// ExpandPaths is the path variable expansion of a staged file.
var ExpandPaths = expandPaths

// EncodeSettings is the settings file of one harness.
var EncodeSettings = encodeSettings

// ExpandSettingIn is the expansion of a string value of the settings of
// the harness h in scope.
func ExpandSettingIn(s string, tools ToolPaths, scope, h string) (string, error) {
	return expandSetting(s, tools.inScope(scope).forHarness(h))
}

// ExpandHooksIn is the expansion of the tool commands in the merged hooks
// file data of scope, for harnesses.
func ExpandHooksIn(data []byte, tools ToolPaths, scope string, harnesses []string) ([]byte, error) {
	return expandHooks(data, nil, tools, scope, harnesses)
}

// OSInstallFS is the real file system port of PlanInstall.
type OSInstallFS = osInstallFS

// PlanInstallWith is PlanInstall reading the disk through d, fetching
// through fetch and running rulesync through run, on a host that finds
// every harness and sets no variable.
func PlanInstallWith(ctx context.Context, d installFS, fetch fetchFunc, run runFunc, dir, variant, home string) (InstallPlan, error) {
	return planInstall(ctx, d, everyHarness, fetch, run, "cache", dir, home, InstallOptions{Variant: variant}, nil)
}

// yes is the consent of a user who answers yes.
func yes(InstallPlan) (bool, error) { return true, nil }

// Host is what an install reads of the machine besides the disk.
type Host = host

// NewHost is a host that finds commands through lookPath, with the
// environment environ, on goos, runtime.GOOS when empty.
func NewHost(lookPath func(string) (string, error), environ []string, goos string) Host {
	return host{lookPath: lookPath, environ: environ, goos: cmp.Or(goos, runtime.GOOS)}
}

// everyHarness is a host whose PATH has every harness's command.
var everyHarness = NewHost(func(name string) (string, error) { return filepath.Join(string(filepath.Separator)+"bin", name), nil }, nil, "")

// PlanInstallHost is PlanInstall through d on the host h.
func PlanInstallHost(ctx context.Context, d installFS, h Host, fetch fetchFunc, run runFunc, dir, home string, opts InstallOptions) (InstallPlan, error) {
	return planInstall(ctx, d, h, fetch, run, "cache", dir, home, opts, nil)
}

// InstallHost is Install through d on the host h.
func InstallHost(ctx context.Context, d executeFS, h Host, fetch fetchFunc, run runFunc, dir, home string, opts InstallOptions) (InstallResult, error) {
	return install(ctx, d, h, fetch, run, "cache", dir, home, opts, nil, nil, yes)
}

// InstallConsent is InstallHost asking consent.
func InstallConsent(ctx context.Context, d executeFS, h Host, fetch fetchFunc, run runFunc, dir, home string, opts InstallOptions, consent Consent) (InstallResult, error) {
	return install(ctx, d, h, fetch, run, "cache", dir, home, opts, nil, nil, consent)
}

// NewHostListing is NewHost whose processes are procs, with self the id of
// dotai's own.
func NewHostListing(lookPath func(string) (string, error), environ []string, procs func() ([]proc.Process, error), self int) Host {
	h := NewHost(lookPath, environ, "")
	h.processes, h.self = procs, self
	return h
}

// RestoreHost is RestoreWith on the host h with opts.
func RestoreHost(ctx context.Context, d executeFS, h Host, home, id string, opts RestoreOptions, overwrite func(RestoreResult) (bool, error)) (RestoreResult, error) {
	return restore(ctx, d, h, home, id, opts, nil, overwrite)
}

// Detect and SelectHarnesses are the detection of a harness and the
// selection of the harnesses an install manages.
var (
	Detect          = detect
	SelectHarnesses = selectHarnesses
)

// ExecuteFS is the port Install writes through.
type ExecuteFS = executeFS

// InstallWith is Install through d, fetching through fetch and running
// rulesync through run.
func InstallWith(ctx context.Context, d executeFS, fetch fetchFunc, run runFunc, dir, variant, home string) (InstallResult, error) {
	return install(ctx, d, everyHarness, fetch, run, "cache", dir, home, InstallOptions{Variant: variant}, nil, nil, yes)
}

// InstallWarnWith is InstallWith giving the warnings to read before it
// writes to warn.
func InstallWarnWith(ctx context.Context, d executeFS, fetch fetchFunc, run runFunc, dir, variant, home string, warn Warn) (InstallResult, error) {
	return install(ctx, d, everyHarness, fetch, run, "cache", dir, home, InstallOptions{Variant: variant}, nil, warn, yes)
}

// RetryWith, Transient and NarrowMode are the retry of a move or a removal,
// the errors it retries on this OS, and the mode of a replaced file.
var (
	RetryWith  = retryWith
	Transient  = transient
	NarrowMode = narrowMode
)

// RestoreWith is Restore through d.
func RestoreWith(ctx context.Context, d executeFS, home, id string, overwrite func(RestoreResult) (bool, error)) (RestoreResult, error) {
	return restore(ctx, d, host{}, home, id, RestoreOptions{}, nil, overwrite)
}

// RestoreWarnWith is RestoreWith giving the warnings to read before it
// writes to warn.
func RestoreWarnWith(ctx context.Context, d executeFS, home, id string, warn Warn, overwrite func(RestoreResult) (bool, error)) (RestoreResult, error) {
	return restore(ctx, d, host{}, home, id, RestoreOptions{}, warn, overwrite)
}

// PlanRunWith is PlanRun with environ as the current environment, lookPath
// finding the harness's executable and goos as the OS.
func PlanRunWith(home, id string, args, environ []string, lookPath func(string) (string, error), goos string) (RunPlan, error) {
	return planRun(osInstallFS{}, lookPath, goos, home, "", id, args, environ)
}

// HomePatterns are the claudeMdExcludes patterns of the forms of a home
// directory, and HomeForms those forms.
var (
	HomePatterns = homePatterns
	HomeForms    = homeForms
)

// UnderHome is the check of a working directory under the home directory.
var UnderHome = underHome

// PlanRunAt is PlanRunWith from the working directory cwd.
func PlanRunAt(home, cwd, id string, args, environ []string, lookPath func(string) (string, error), goos string) (RunPlan, error) {
	return planRun(osInstallFS{}, lookPath, goos, home, cwd, id, args, environ)
}

// InstallRoot is the directory a target is written through.
var InstallRoot = installRoot

// OrderLinks is the order an install or a restore carries out its ops in.
var OrderLinks = orderLinks

// MakeLinkWith is how an install, a restore and a rollback make the link
// name in root at target: checked to point at its shared skill first.
func MakeLinkWith(d executeFS, home, root, name, target, dest string) error {
	return runner{d: d, home: home}.link(root, name, target, dest)
}

// InstallReplace is Install through d on the host h in the replace mode,
// with the user's consent.
func InstallReplace(ctx context.Context, d executeFS, h Host, fetch fetchFunc, run runFunc, dir, home string, opts InstallOptions) (InstallResult, error) {
	opts.Mode = ModeReplace
	return install(ctx, d, h, fetch, run, "cache", dir, home, opts, nil, nil, yes)
}

// InstallAgainReplace is Install through d on the host h with the mode the
// install state records, with the user's consent.
func InstallAgainReplace(ctx context.Context, d executeFS, h Host, fetch fetchFunc, run runFunc, dir, home string, opts InstallOptions) (InstallResult, error) {
	return install(ctx, d, h, fetch, run, "cache", dir, home, opts, nil, nil, yes)
}

// PinnedBackup is the id of the pinned first replace backup that the
// install state of home leads to, "" for none.
func PinnedBackup(t interface{ Fatal(...any) }, home string) string {
	s, err := readState(osInstallFS{}, home)
	if err != nil {
		t.Fatal(err)
	}
	if s == nil {
		return ""
	}
	r := runner{ctx: context.Background(), d: osInstallFS{}, home: home}
	chain := r.pinnedChain(r.historyOrder(settledBackup(*s)))
	if len(chain) == 0 {
		return ""
	}
	return chain[len(chain)-1]
}

// GitPort is how a project plan asks git about the project.
type GitPort = gitPort

// OSGit is the git on PATH.
type OSGit = osGit

// PlanProjectWith is PlanProject reading the disk through d, asking git
// through g, fetching through fetch and running rulesync through run, on a
// host that finds every harness.
func PlanProjectWith(ctx context.Context, d installFS, g gitPort, fetch fetchFunc, run runFunc, dir, project, home string, opts ApplyOptions) (ProjectPlan, error) {
	return planProject(ctx, d, everyHarness, g, fetch, run, "cache", dir, project, home, opts, nil)
}

// PlanProjectWithRulesync is PlanProject asking git through g, with the
// real rulesync from cacheDir, on a host that finds every harness.
func PlanProjectWithRulesync(ctx context.Context, g gitPort, cacheDir, dir, project, home string, opts ApplyOptions) (ProjectPlan, error) {
	return planProject(ctx, osInstallFS{}, everyHarness, g, download, generate, cacheDir, dir, project, home, opts, nil)
}

// FoldCase is whether paths are compared without case on this OS.
var FoldCase = foldCase

// ApplyWith is Apply writing through d, asking git through g, fetching
// through fetch and running rulesync through run, on a host that finds
// every harness.
func ApplyWith(ctx context.Context, d executeFS, g gitPort, fetch fetchFunc, run runFunc, dir, project, home string, opts ApplyOptions, consent ApplyConsent) (ApplyResult, error) {
	return applyProject(ctx, d, everyHarness, g, fetch, run, "cache", dir, project, home, opts, nil, consent)
}

// RestoreProjectWith is RestoreProject writing through d and asking git
// through g.
func RestoreProjectWith(ctx context.Context, d executeFS, g gitPort, project, home string, overwrite func(ProjectRestoreResult) (bool, error)) (ProjectRestoreResult, error) {
	return restoreProject(ctx, d, g, project, home, ProjectRestoreOptions{}, overwrite)
}

// ApplyWithRulesync is Apply asking git through g, with the real rulesync
// from cacheDir, on a host that finds every harness.
func ApplyWithRulesync(ctx context.Context, g gitPort, cacheDir, dir, project, home string, opts ApplyOptions) (ApplyResult, error) {
	return applyProject(ctx, osInstallFS{}, everyHarness, g, download, generate, cacheDir, dir, project, home, opts, nil, nil)
}

// RecordPath is the record of project under home.
func RecordPath(home, project string) string { return recordPath(home, project) }

// DoctorWith is Doctor reading and writing through d and asking git
// through g.
func DoctorWith(ctx context.Context, d executeFS, g gitPort, home string, opts DoctorOptions) (DoctorResult, error) {
	return doctor(ctx, d, g, home, opts)
}

// PreviewProjectRestoreWith is PreviewProjectRestore reading through d,
// with every write refused, and asking git through g.
func PreviewProjectRestoreWith(ctx context.Context, d executeFS, g gitPort, cwd, home string) (ProjectRestoreResult, error) {
	return previewProjectRestore(ctx, readOnlyFS{d}, g, cwd, home)
}

// RestoreOptsWith is Restore through d with opts.
func RestoreOptsWith(ctx context.Context, d executeFS, home, id string, opts RestoreOptions, overwrite func(RestoreResult) (bool, error)) (RestoreResult, error) {
	return restore(ctx, d, host{}, home, id, opts, nil, overwrite)
}

// RestoreProjectOptsWith is RestoreProjectWith with opts.
func RestoreProjectOptsWith(ctx context.Context, d executeFS, g gitPort, project, home string, opts ProjectRestoreOptions, overwrite func(ProjectRestoreResult) (bool, error)) (ProjectRestoreResult, error) {
	return restoreProject(ctx, d, g, project, home, opts, overwrite)
}

// CopyPackWith is CopyPack calling afterFile after each file it copies,
// whose error stops the copy, and beforeRename before the copy takes its
// name.
func CopyPackWith(ctx context.Context, from, to string, afterFile func(n int) error, beforeRename func()) (CopyResult, error) {
	return copyPack(ctx, from, to, copyHooks{afterFile: afterFile, beforeRename: beforeRename})
}

// SetPackIdentify makes the pack list read each pack's name and version
// with f and wait at most wait for it, until the returned function puts
// the real ones back.
func SetPackIdentify(f func(dir string) (string, string, error), wait time.Duration) func() {
	oldF, oldWait := identifyPack, identifyWait
	identifyPack, identifyWait = f, wait
	return func() { identifyPack, identifyWait = oldF, oldWait }
}

// HookCommand is the command of the session-start hook of decision 0045
// that runs bin for the harness id on goos.
func HookCommand(bin, goos, id string) (string, error) { return hookCommand(bin, goos, id) }

// WithExe is h whose dotai binary, the one the session-start hook of
// decision 0045 runs, is what exe gives.
func WithExe(h Host, exe func() (string, error)) Host {
	h.exe = exe
	return h
}

// EveryHarness is a host whose PATH has every harness's command.
var EveryHarness = everyHarness

// DotaiBinaryWith is DotaiBinary finding dotai on PATH by lookPath and
// the binary that runs by executable.
func DotaiBinaryWith(lookPath func(string) (string, error), executable func() (string, error)) (string, error) {
	return dotaiBinary(lookPath, executable)
}

// OnboardingHookWith is OnboardingHook asking git through g.
func OnboardingHookWith(ctx context.Context, g gitPort, cwd, home string) (string, error) {
	return onboardingHook(ctx, osInstallFS{}, g, cwd, home)
}

// TakeFingerprint is the onboarding record an apply takes in a repository
// whose index holds files, for the pack the install state of home records.
func TakeFingerprint(home string, files []string) (state.Onboarding, error) {
	f, _, err := currentFingerprint(context.Background(), osInstallFS{}, listGit(files), home, "")
	if err != nil || f == nil {
		return state.Onboarding{}, fmt.Errorf("no fingerprint: %v", err)
	}
	return f.record(state.OnboardingApplied), nil
}

// listGit is a git whose index holds files.
type listGit []string

func (listGit) Repo(context.Context, string) (*Repo, error)                        { return nil, nil }
func (listGit) Tracked(context.Context, string, []string) (map[string]bool, error) { return nil, nil }
func (g listGit) Files(context.Context, string) ([]string, error)                  { return g, nil }

// DismissOnboardingWith is DismissOnboarding writing through d and asking
// git through g.
func DismissOnboardingWith(ctx context.Context, d executeFS, g gitPort, cwd, home string) (DismissResult, error) {
	return dismissOnboarding(ctx, d, g, cwd, home)
}
