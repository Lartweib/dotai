package app

import "context"

// OSStageFS is the real file system port, for fakes that wrap it.
type OSStageFS = osStageFS

// StageWith is Stage writing through w.
func StageWith(w stageFS, rs Rulesync, dir, out string, sel Selection, force bool) (StageResult, error) {
	return stage(context.Background(), w, rs, dir, out, sel, force)
}

// FetchRulesyncWith is FetchRulesync downloading through fetch.
func FetchRulesyncWith(ctx context.Context, fetch fetchFunc, cacheDir, dir string) (RulesyncResult, error) {
	return fetchRulesync(ctx, fetch, cacheDir, dir, nil)
}
