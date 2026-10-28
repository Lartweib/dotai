package app

import "context"

// OSStageFS is the real file system port, for fakes that wrap it.
type OSStageFS = osStageFS

// StageFS is the port Stage writes through.
type StageFS = stageFS

// StageWith is Stage writing through w, fetching through fetch and
// running rulesync through run.
func StageWith(w stageFS, fetch fetchFunc, run runFunc, dir, out string, sel Selection, force bool) (StageResult, error) {
	return stage(context.Background(), w, fetch, run, "cache", dir, out, sel, force, nil)
}

// FetchRulesyncWith is FetchRulesync downloading through fetch.
func FetchRulesyncWith(ctx context.Context, fetch fetchFunc, cacheDir, dir string) (RulesyncResult, error) {
	return fetchRulesync(ctx, fetch, cacheDir, dir, nil)
}
