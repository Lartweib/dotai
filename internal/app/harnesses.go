package app

import "github.com/lartweib/dotai/internal/harness"

// HarnessInfo is a harness dotai supports: its id, as a manifest names
// it, and its name, for people.
type HarnessInfo struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// Harnesses lists every harness dotai supports, in the order of its
// adapters.
func Harnesses() []HarnessInfo {
	out := []HarnessInfo{}
	for _, a := range harness.Adapters() {
		out = append(out, HarnessInfo{ID: a.ID, Name: a.Name})
	}
	return out
}
