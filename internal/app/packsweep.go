package app

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

// The sweep of ~/.dotai/packs/github.com, under the lock, before every
// addition, update, removal and list of the registry: what a swap killed
// halfway left there. A swap moves the clone aside to .<repo>.old-*, or a
// removal to .<repo>.remove-*, renames the fetch, .<repo>.fetch-*, into
// place and writes the registry; each of those steps runs under the lock,
// so a sibling found under the lock is one a swap left. A fetch runs
// outside the lock, so only one older than staleFetch is deleted.

// staleFetch is the age past which a temporary fetch is one a killed
// command left: longer than a fetch may run.
var staleFetch = time.Hour

// The kinds of the temporary siblings of a clone.
var siblingKinds = []string{"fetch", "old", "remove"}

// sweepPacks puts back, for each git pack of the registry whose clone is
// missing or at another commit than the registry names, the clone a swap
// moved aside at that commit, or the most recent one when the clone is
// missing, and gives the clones it put back. It deletes the other
// siblings a swap left, the temporary fetches older than staleFetch, and
// the directories of owners left empty. It does its best: what it cannot
// read or move stays.
func sweepPacks(home string) []string {
	root := filepath.Join(home, dotaiDir, packsDir, gitHubHost)
	owners, err := os.ReadDir(root)
	if err != nil {
		return nil
	}
	commits := map[string]string{} // the commit the registry names, by clone path
	if doc, err := readPacks(home); err == nil {
		for _, p := range doc.Packs {
			if target, err := clonePath(home, p.ID); err == nil && p.Source != nil && samePath(p.Path, target) {
				commits[target] = p.Source.Commit
			}
		}
	}
	var recovered []string
	for _, owner := range owners {
		dir := filepath.Join(root, owner.Name())
		if !owner.IsDir() {
			continue
		}
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		asides := map[string][]string{} // the old and remove siblings, by repository
		for _, e := range entries {
			repo, kind, ok := siblingOf(e.Name())
			if !ok {
				continue
			}
			p := filepath.Join(dir, e.Name())
			if kind == "fetch" {
				if info, err := e.Info(); err == nil && time.Since(info.ModTime()) > staleFetch {
					_ = os.RemoveAll(p)
				}
				continue
			}
			asides[repo] = append(asides[repo], p)
		}
		for repo, candidates := range asides {
			target := filepath.Join(dir, repo)
			if back := restoreAside(target, commits, candidates); back != "" {
				recovered = append(recovered, target)
				candidates = slices.DeleteFunc(candidates, func(c string) bool { return c == back })
			}
			for _, c := range candidates {
				_ = os.RemoveAll(c)
			}
		}
		_ = os.Remove(dir) // only when it is empty
	}
	_ = os.Remove(root)
	return recovered
}

// siblingOf parses name as .<repo>.<kind>-<suffix>, a temporary sibling of
// the clone of repo.
func siblingOf(name string) (repo, kind string, ok bool) {
	if !strings.HasPrefix(name, ".") {
		return "", "", false
	}
	for _, k := range siblingKinds {
		if i := strings.LastIndex(name, "."+k+"-"); i > 1 {
			return name[1:i], k, true
		}
	}
	return "", "", false
}

// restoreAside puts back over target the candidate at the commit the
// registry names for it, or, when target is missing, the most recent
// candidate, and gives the one it put back, "" when it put back none, as
// when the registry does not name target or target is at its commit.
func restoreAside(target string, commits map[string]string, candidates []string) string {
	commit, registered := commits[target]
	if !registered {
		return ""
	}
	_, statErr := os.Lstat(target)
	missing := os.IsNotExist(statErr)
	if !missing && headCommit(target) == commit {
		return ""
	}
	pick := ""
	for _, c := range candidates {
		if headCommit(c) == commit {
			pick = c
		}
	}
	if pick == "" && missing {
		slices.SortFunc(candidates, func(a, b string) int { return modTime(b).Compare(modTime(a)) })
		pick = candidates[0]
	}
	if pick == "" {
		return ""
	}
	if !missing {
		wrong, err := moveAside(target, "remove")
		if err != nil {
			return ""
		}
		if err := os.Rename(pick, target); err != nil {
			_ = wrong.restore()
			return ""
		}
		_ = wrong.discard()
		return pick
	}
	if err := os.Rename(pick, target); err != nil {
		return ""
	}
	return pick
}

// headCommit is the commit a clone dotai fetched has checked out, its
// detached HEAD, "" when it cannot be read.
func headCommit(dir string) string {
	data, err := os.ReadFile(filepath.Join(dir, ".git", "HEAD"))
	if err != nil {
		return ""
	}
	if c := strings.TrimSpace(string(data)); fullSHA(c) {
		return strings.ToLower(c)
	}
	return ""
}

func modTime(p string) time.Time {
	info, err := os.Stat(p)
	if err != nil {
		return time.Time{}
	}
	return info.ModTime()
}

// sweepIfFree sweeps, for a list, only when the lock is free and there is
// something to sweep: a list never waits for another command, nor creates
// ~/.dotai.
func sweepIfFree(home string) []string {
	if _, err := os.Stat(filepath.Join(home, dotaiDir, packsDir)); err != nil {
		return nil
	}
	r := runner{ctx: context.Background(), d: osInstallFS{}, home: home}
	unlock, err := r.lock()
	if err != nil {
		return nil
	}
	defer func() { _ = unlock() }()
	return sweepPacks(home)
}
