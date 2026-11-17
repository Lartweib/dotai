package app

import (
	"context"
	"time"
)

// SetPackRemote makes the pack fetches reach to(url) instead of url, with
// the file protocol allowed for a URL it rewrites, until restore is called.
// The production allowlist is unchanged: a URL to leaves as it is still
// gets protocol.allow=never.
func SetPackRemote(to func(url string) string) (restore func()) {
	old := packRemote
	packRemote = func(url string) (string, []string) {
		if r := to(url); r != url {
			return r, []string{"-c", "protocol.file.allow=always"}
		}
		return url, nil
	}
	return func() { packRemote = old }
}

// CheckGitVersion checks the output of git --version.
var CheckGitVersion = checkGitVersion

// RequireGit checks the git on PATH.
var RequireGit = requireGit

// PackGitArgs is the command line of a git call for a pack.
var PackGitArgs = packGitArgs

// PackGitEnv is the environment of a git call for a pack.
var PackGitEnv = packGitEnv

// ParseFetchHead is the full ref FETCH_HEAD records.
var ParseFetchHead = parseFetchHead

// CheckPackTree checks the output of git ls-tree -r -z -l.
var CheckPackTree = checkPackTree

// FetchPack fetches ref of url into the new directory dir, as pack add
// does, non-interactively.
func FetchPack(ctx context.Context, dir, url, ref string) (PackFetch, error) {
	g, done, err := newPackGit(ctx, false)
	if err != nil {
		return PackFetch{}, err
	}
	defer done()
	return g.fetchPack(ctx, dir, url, ref)
}

// SetPackFetchTimeout bounds the git calls that reach the remote by d,
// until restore is called.
func SetPackFetchTimeout(d time.Duration) (restore func()) {
	old := packFetchTimeout
	packFetchTimeout = d
	return func() { packFetchTimeout = old }
}

// SetPackFault fails the step of a placement for which fault gives an
// error, until restore is called.
func SetPackFault(fault func(step string) error) (restore func()) {
	old := packFault
	packFault = fault
	return func() { packFault = old }
}
