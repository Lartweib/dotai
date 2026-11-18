package app

import "github.com/lartweib/dotai/internal/harness"

// HarnessInfo is a harness dotai supports: its id, as a manifest names
// it, its name, for people, and its status, supported or preview, with
// why it is a preview, "" for a supported one (decision 0047).
type HarnessInfo struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	Status       string `json:"status"`
	StatusReason string `json:"statusReason"`
}

// Harnesses lists every harness dotai supports, in the order of its
// adapters.
func Harnesses() []HarnessInfo {
	out := []HarnessInfo{}
	for _, a := range harness.Adapters() {
		out = append(out, HarnessInfo{ID: a.ID, Name: a.Name, Status: string(a.Status), StatusReason: a.StatusReason})
	}
	return out
}
