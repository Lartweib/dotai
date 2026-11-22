package harness

import "slices"

// PreviewForTest makes the adapter of id a preview harness with reason,
// and returns the function that restores it. No harness is a preview in
// the shipped adapters (decision 0047, update 2026-11-22), so the tests
// of the preview handling (never selected by default, asked about,
// warned, kept by an earlier install) use this seam instead of a real
// preview adapter. It is for tests only: nothing in the engine calls it,
// and it is not safe to use from parallel tests.
func PreviewForTest(id, reason string) (restore func()) {
	i := slices.IndexFunc(adapters, func(a Adapter) bool { return a.ID == id })
	if i < 0 {
		panic("harness.PreviewForTest: no adapter " + id)
	}
	was := adapters[i]
	adapters[i].Status = Preview
	adapters[i].StatusReason = reason
	return func() { adapters[i] = was }
}
