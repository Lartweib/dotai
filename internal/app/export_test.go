package app

import "context"

// OSStageFS is the real file system port, for fakes that wrap it.
type OSStageFS = osStageFS

// StageFS is the port Stage writes through.
type StageFS = stageFS

// StageWith is Stage writing through w, fetching through fetch and
// running rulesync through run.
func StageWith(w stageFS, fetch fetchFunc, run runFunc, dir, out string, sel Selection, tools ToolPaths, force bool) (StageResult, error) {
	return stage(context.Background(), w, fetch, run, "cache", dir, out, sel, tools, force, nil)
}

// FetchRulesyncWith is FetchRulesync downloading through fetch.
func FetchRulesyncWith(ctx context.Context, fetch fetchFunc, cacheDir, dir string) (RulesyncResult, error) {
	return fetchRulesync(ctx, fetch, cacheDir, dir, nil)
}

// ExpandPaths is the path variable expansion of a staged file.
var ExpandPaths = expandPaths

// EncodeSettings is the settings file of one harness.
var EncodeSettings = encodeSettings

// OSInstallFS is the real file system port of PlanInstall.
type OSInstallFS = osInstallFS

// PlanInstallWith is PlanInstall reading the disk through d, fetching
// through fetch and running rulesync through run.
func PlanInstallWith(ctx context.Context, d installFS, fetch fetchFunc, run runFunc, dir, variant, home string) (InstallPlan, error) {
	return planInstall(ctx, d, fetch, run, "cache", dir, variant, home, nil)
}

// ExecuteFS is the port Install writes through.
type ExecuteFS = executeFS

// InstallWith is Install through d, fetching through fetch and running
// rulesync through run.
func InstallWith(ctx context.Context, d executeFS, fetch fetchFunc, run runFunc, dir, variant, home string) (InstallResult, error) {
	return install(ctx, d, fetch, run, "cache", dir, variant, home, nil, nil)
}

// InstallWarnWith is InstallWith giving the warnings to read before it
// writes to warn.
func InstallWarnWith(ctx context.Context, d executeFS, fetch fetchFunc, run runFunc, dir, variant, home string, warn Warn) (InstallResult, error) {
	return install(ctx, d, fetch, run, "cache", dir, variant, home, nil, warn)
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
	return restore(ctx, d, home, id, nil, overwrite)
}

// RestoreWarnWith is RestoreWith giving the warnings to read before it
// writes to warn.
func RestoreWarnWith(ctx context.Context, d executeFS, home, id string, warn Warn, overwrite func(RestoreResult) (bool, error)) (RestoreResult, error) {
	return restore(ctx, d, home, id, warn, overwrite)
}

// PlanRunWith is PlanRun with environ as the current environment, lookPath
// finding the harness's executable and goos as the OS.
func PlanRunWith(home, id string, args, environ []string, lookPath func(string) (string, error), goos string) (RunPlan, error) {
	return planRun(osInstallFS{}, lookPath, goos, home, id, args, environ)
}
