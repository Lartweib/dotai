package app_test

import (
	"context"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/lartweib/dotai/internal/app"
	"github.com/lartweib/dotai/internal/pack"
)

// collect is a context that gathers the steps an operation reports.
func collect(t *testing.T) (context.Context, func() []app.Step) {
	t.Helper()
	var mu sync.Mutex
	var steps []app.Step
	ctx := app.WithSteps(context.Background(), func(s app.Step) {
		mu.Lock()
		defer mu.Unlock()
		steps = append(steps, s)
	})
	return ctx, func() []app.Step { mu.Lock(); defer mu.Unlock(); return slices.Clone(steps) }
}

// phases is the phases of steps, each once, in the order they start.
func phases(steps []app.Step) []app.Phase {
	var out []app.Phase
	for _, s := range steps {
		if len(out) == 0 || out[len(out)-1] != s.Phase {
			out = append(out, s.Phase)
		}
	}
	return out
}

// counted checks that the steps of phase count from 1 to total, one per
// item, each with a path under the home directory, and gives total.
func counted(t *testing.T, steps []app.Step, phase app.Phase) int {
	t.Helper()
	total, n := 0, 0
	for _, s := range steps {
		if s.Phase != phase {
			continue
		}
		n++
		if total == 0 {
			total = s.Total
		}
		if s.Total != total || s.N != n || !strings.HasPrefix(s.Item, "~/") {
			t.Errorf("%s step %+v, want item %d of %d under ~/", phase, s, n, total)
		}
	}
	if n != total {
		t.Errorf("%s reported %d items, want its total %d", phase, n, total)
	}
	return total
}

// TestReplaceInstallAndRestoreReportTheirSteps checks that a replace
// install says each phase it goes through, in order, with a running count
// of the entries it wipes, and that a restore says the moves it makes: what
// a person watching a long wipe needs to tell it from a hang.
func TestReplaceInstallAndRestoreReportTheirSteps(t *testing.T) {
	home, h, _ := replaceHome(t)
	ctx, steps := collect(t)
	f := quiet(t)
	res, err := app.InstallReplace(ctx, newClockFS(t), h, f.fetch, replaceRun(t), examplePack, home, app.InstallOptions{Harnesses: pack.Harnesses})
	if err != nil {
		t.Fatal(err)
	}
	got := steps()
	if want := []app.Phase{app.PhaseBackup, app.PhaseWipe, app.PhaseRecord, app.PhaseWrite}; !slices.Equal(phases(got), want) {
		t.Errorf("install phases = %v, want %v", phases(got), want)
	}
	if n := counted(t, got, app.PhaseWipe); n != len(res.Wipes) || n == 0 {
		t.Errorf("wipe total = %d, want the %d wipes of the result", n, len(res.Wipes))
	}
	if n := counted(t, got, app.PhaseRecord); n != len(res.Wipes) {
		t.Errorf("record total = %d, want the %d wipes of the result", n, len(res.Wipes))
	}
	counted(t, got, app.PhaseWrite)

	ctx, steps = collect(t)
	if _, err := app.RestoreWith(ctx, newClockFS(t), home, "", nil); err != nil {
		t.Fatal(err)
	}
	if n := counted(t, steps(), app.PhaseRestore); n != len(res.Wipes) {
		t.Errorf("restore total = %d, want the %d wipes put back", n, len(res.Wipes))
	}
}

// TestRollbackStepsCountOnlyTheEntriesMovedBack checks that a rollback of a
// replace install counts the entries it moves back from 1 to its total,
// ending at the total: a directory made for an entry that failed to move is
// not one, and must not leave the count short of it.
func TestRollbackStepsCountOnlyTheEntriesMovedBack(t *testing.T) {
	for failAt := 1; failAt <= 8; failAt++ {
		home, h, _ := replaceHome(t)
		ctx, got := collect(t)
		f := quiet(t)
		_, err := app.InstallReplace(ctx, &stepFS{clockFS: newClockFS(t), at: failAt}, h, f.fetch, replaceRun(t), examplePack, home, app.InstallOptions{Harnesses: pack.Harnesses})
		if err == nil {
			t.Fatalf("failing step %d: Install(replace) succeeded, want the injected failure", failAt)
		}
		var back []app.Step
		for _, s := range got() {
			if s.Phase == app.PhaseRollback && s.N > 0 {
				back = append(back, s)
			}
		}
		counted(t, back, app.PhaseRollback) // from 1 to its total, ending at it
	}
}
