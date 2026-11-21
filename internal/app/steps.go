package app

import (
	"context"
	"path"

	"github.com/lartweib/dotai/internal/backup"
)

// Phase is a stage of an install or a restore that can take a while: the
// one a person watching it wants to hear of.
type Phase string

const (
	// PhaseBackup copies into the backup what the operation replaces.
	PhaseBackup Phase = "backup"
	// PhaseWipe moves each entry a replace install wipes into the backup.
	PhaseWipe Phase = "wipe"
	// PhaseRecord reads each entry moved into the backup, to record what
	// it holds.
	PhaseRecord Phase = "record"
	// PhaseWrite writes the files of the pack, or those a restore gives
	// back.
	PhaseWrite Phase = "write"
	// PhaseRestore moves each entry a restore gives back out of its backup.
	PhaseRestore Phase = "restore"
	// PhaseRollback puts back what an operation that failed had changed.
	PhaseRollback Phase = "rollback"
)

// Step is the progress of an operation: the Phase it is in, which of Total
// items it is starting (N, counted from 1; both 0 when the phase has no count) and the
// Item, a path under the home directory written as ~/..., or "".
type Step struct {
	Phase Phase
	N     int
	Total int
	Item  string
}

type stepsKey struct{}

// WithSteps gives ctx a function that an install or a restore run with it
// calls, from its own goroutine, as each step starts. It is for the
// terminal: nothing in a result depends on it.
func WithSteps(ctx context.Context, f func(Step)) context.Context {
	return context.WithValue(ctx, stepsKey{}, f)
}

// ReportStep gives s to the function of ctx (WithSteps), if it has one.
func ReportStep(ctx context.Context, s Step) {
	if f, ok := ctx.Value(stepsKey{}).(func(Step)); ok && f != nil {
		f(s)
	}
}

// step reports a step to the function of the context, if it has one.
// A phase with nothing to do is not reported.
func (r runner) step(p Phase, n, total int, item string) {
	if total == 0 {
		return
	}
	ReportStep(r.ctx, Step{Phase: p, N: n, Total: total, Item: item})
}

// tilde is name, an absolute path under the home directory, as ~/..., or
// name itself when it is not under it.
func (r runner) tilde(name string) string {
	if rel, err := backup.Rel(r.home, name); err == nil {
		return path.Join("~", rel)
	}
	return name
}

// announce reports that phase p starts, for one that has no count.
func (r runner) announce(p Phase) {
	ReportStep(r.ctx, Step{Phase: p})
}
