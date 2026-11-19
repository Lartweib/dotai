package cli

import (
	"bytes"
	"context"
	"testing"
	"time"

	"github.com/lartweib/dotai/internal/app"
)

// TestRunHookWaitsForTheHookItStopped checks that once the hook's ctx
// ends, runHook waits for the hook to return, as the git it runs is
// killed, so that no process is left behind. The ctx ends by its parent,
// cancelled once the hook runs, and the hook returns only when the test
// lets it: no step depends on how fast the machine is, and the bound,
// generous here, is a hang guard only.
func TestRunHookWaitsForTheHookItStopped(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("USERPROFILE", t.TempDir())
	var stdout, stderr bytes.Buffer
	env := Env{Stdout: &stdout, Stderr: &stderr, HookBound: time.Minute}
	running, stopped, finish := make(chan struct{}), make(chan struct{}), make(chan struct{})
	returned := false
	env.OnboardingHook = func(ctx context.Context, _, _ string) (string, error) {
		close(running)
		<-ctx.Done()
		close(stopped)
		<-finish // git, killed, exits
		returned = true
		return "dotai: late", nil
	}
	parent, cancel := context.WithCancel(context.Background())
	defer cancel()
	line := make(chan string, 1)
	go func() { line <- runHook(parent, env) }()
	<-running
	cancel()
	<-stopped
	// A runHook that did not wait for the hook would return now. Checked
	// for a while only: it can miss that fault, never report a false one.
	select {
	case l := <-line:
		t.Fatalf("runHook returned %q before the hook it stopped", l)
	case <-time.After(100 * time.Millisecond):
	}
	close(finish)
	if l := <-line; l != "" || !returned {
		t.Errorf("runHook = %q, the hook returned: %v; want silence, after the hook returned", l, returned)
	}
}

// TestRunHookEndsTheHookBeforeItsBound checks the default bound of 1 s:
// the hook's ctx ends app.HookReap before it, at the latest, which leaves
// the hook that time to reap the git it runs. The hook returns at once,
// so the test takes no time and checks nothing that a slow machine moves.
func TestRunHookEndsTheHookBeforeItsBound(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("USERPROFILE", t.TempDir())
	var stdout, stderr bytes.Buffer
	env := Env{Stdout: &stdout, Stderr: &stderr}
	var left time.Duration
	hasDeadline := false
	env.OnboardingHook = func(ctx context.Context, _, _ string) (string, error) {
		var deadline time.Time
		deadline, hasDeadline = ctx.Deadline()
		left = time.Until(deadline)
		return "dotai: line", nil
	}
	runHook(context.Background(), env)
	if want := hookBound - app.HookReap; !hasDeadline || left > want {
		t.Errorf("the hook's ctx has a deadline: %v, %v ahead; want one at most %v ahead", hasDeadline, left, want)
	}
}
