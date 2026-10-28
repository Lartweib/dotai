package app

// OSStageFS is the real file system port, for fakes that wrap it.
type OSStageFS = osStageFS

// StageWith is Stage writing through w.
func StageWith(w stageFS, dir, out string, sel Selection, force bool) (StageResult, error) {
	return stage(w, dir, out, sel, force)
}

// FetchRulesyncWith is FetchRulesync downloading through fetch.
func FetchRulesyncWith(fetch fetchFunc, dir string) (RulesyncResult, error) {
	return fetchRulesync(fetch, dir)
}
