package app

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

// The errors of a fetch of a git pack (decision 0044).

// GitUnavailableError is a git that dotai cannot fetch packs with: missing
// from PATH, or older than 2.41.
type GitUnavailableError struct {
	Version string // what git --version gave, "" when git is missing
	TooOld  bool
	Err     error
}

func (e *GitUnavailableError) Error() string {
	want := fmt.Sprintf("%d.%d", minGit[0], minGit[1])
	switch {
	case e.TooOld:
		return fmt.Sprintf("dotai needs git %s or later to fetch packs, and the git on PATH is %s: update git, or clone the pack yourself and add it with dotai pack add --dir <path>", want, e.Version)
	case e.Version != "":
		return fmt.Sprintf("the git on PATH gave %q for its version, which dotai cannot read: it needs git %s or later", e.Version, want)
	}
	return fmt.Sprintf("dotai needs git %s or later on PATH to fetch packs, and git cannot be run (%v): install git, or add a directory with dotai pack add --dir <path>", want, e.Err)
}

func (e *GitUnavailableError) Unwrap() error { return e.Err }

// FetchFailedError is a fetch of a pack that failed, such as a remote that
// cannot be reached, a ref it does not have or credentials git does not
// get; nothing was registered.
type FetchFailedError struct {
	URL, Ref string
	Stderr   string
	Err      error
}

func (e *FetchFailedError) Error() string {
	ref := e.Ref
	if ref == "" {
		ref = "its default branch"
	}
	msg := fmt.Sprintf("git could not fetch %s of %s: %v", ref, e.URL, e.Err)
	if e.Stderr != "" {
		msg += ": " + e.Stderr
	}
	return msg
}

func (e *FetchFailedError) Unwrap() error { return e.Err }

// FetchByCommitRefusedError is a full commit SHA the remote does not
// serve, such as one no longer reachable from any of its refs. dotai does
// not fall back to a full clone.
type FetchByCommitRefusedError struct {
	URL, Commit string
	Stderr      string
}

func (e *FetchByCommitRefusedError) Error() string {
	return fmt.Sprintf("%s does not serve the commit %s, which may no longer be reachable from any of its branches or tags: give a tag or a branch, or a commit it serves (%s)", e.URL, e.Commit, e.Stderr)
}

// TreeProblem is an entry of a pack's tree that dotai refuses, by its
// path; Path is "" for a problem of the whole tree.
type TreeProblem struct {
	Path    string `json:"path"`
	Problem string `json:"problem"`
}

// PackTreeRefusedError is a commit whose tree holds what a pack never
// holds, or more than a copy of a pack takes: it was not checked out.
type PackTreeRefusedError struct {
	URL, Commit string
	Problems    []TreeProblem
}

func (e *PackTreeRefusedError) Error() string {
	var b strings.Builder
	fmt.Fprintf(&b, "the commit %s of %s is not a pack dotai adds:", e.Commit, e.URL)
	for i, p := range e.Problems {
		if i > 0 {
			b.WriteString(";")
		}
		if p.Path != "" {
			fmt.Fprintf(&b, " %s %s", p.Path, p.Problem)
		} else {
			b.WriteString(" " + p.Problem)
		}
	}
	return b.String()
}

// fetchFailed is err of a fetch of ref of url as a *FetchFailedError, err
// itself when it is a stop, which says why, and git's stderr when it is
// git's.
func fetchFailed(url, ref string, err error) error {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	fe := &FetchFailedError{URL: url, Ref: ref, Err: err}
	if ge, ok := errors.AsType[*gitError](err); ok {
		fe.Err, fe.Stderr = ge.err, ge.stderr
	}
	return fe
}

// gitStopped says why a git call sub that ctx, its caller's context, or
// its own timeout stopped: the user stopped it, the operation's time limit
// ran out, or git took longer than timeout. Each wraps its context error.
func gitStopped(ctx context.Context, sub string, timeout time.Duration) error {
	switch {
	case errors.Is(ctx.Err(), context.Canceled):
		return fmt.Errorf("git %s was stopped: %w", sub, context.Canceled)
	case ctx.Err() != nil:
		return fmt.Errorf("git %s was stopped by the time limit of the operation: %w", sub, ctx.Err())
	}
	return fmt.Errorf("git %s took longer than %s and was stopped: %w", sub, timeout, context.DeadlineExceeded)
}
