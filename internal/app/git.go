package app

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

// gitTimeout bounds each git command a project plan runs, and gitWaitDelay
// how long it waits for its output once git exits or is killed, so that a
// process git started that still holds its pipes does not hold the plan.
const (
	gitTimeout   = 30 * time.Second
	gitWaitDelay = 5 * time.Second
)

// Repo is the git repository a project is in (decision 0041): Top, the top
// of the worktree the project is in, which is the project; Main, the top of
// the main worktree, which Claude Code keys its local MCP servers by from
// every worktree (HARNESS-NOTES); and Exclude, the exclude file, shared by
// the worktrees, the one git rev-parse --git-path info/exclude gives. Each
// is absolute, with the OS separator.
type Repo struct {
	Top     string
	Main    string
	Exclude string
}

// gitPort is how a project plan asks git about the directory dir: the
// repository it is in, nil outside one, and which of paths, slash paths
// relative to dir, git tracks, by those paths, a directory with a trailing
// slash when git tracks any file in it. Neither writes anything.
type gitPort interface {
	Repo(ctx context.Context, dir string) (*Repo, error)
	Tracked(ctx context.Context, dir string, paths []string) (map[string]bool, error)
}

// GitMissingError is a project inside a git repository, as a .git found
// above it shows, while git is not on PATH: without git, dotai cannot tell
// which files the repository tracks, and it writes none of them.
type GitMissingError struct {
	Dir, Git string
}

func (e *GitMissingError) Error() string {
	return fmt.Sprintf("%s is in a git repository (%s), but git is not on PATH, and dotai needs it to tell which files the repository tracks: install git, then plan again", e.Dir, e.Git)
}

// osGit is the gitPort of the git on PATH.
type osGit struct{}

// gitDropped are the variables of the user's environment that would point
// git at another repository, index or configuration than the project's;
// a trailing * is a prefix.
var gitDropped = []string{"GIT_DIR", "GIT_WORK_TREE", "GIT_INDEX_FILE", "GIT_COMMON_DIR", "GIT_OBJECT_DIRECTORY", "GIT_NAMESPACE",
	"GIT_CONFIG", "GIT_CONFIG_COUNT", "GIT_CONFIG_KEY_*", "GIT_CONFIG_VALUE_*", "GIT_CONFIG_PARAMETERS"}

// gitEnv is environ without gitDropped, by name compared without case
// where fold is set, as on Windows, with the messages of git in English,
// so that they can be matched, and without the optional locks git may take
// on the index.
func gitEnv(environ []string, fold bool) []string {
	out := slices.DeleteFunc(slices.Clone(environ), func(kv string) bool {
		name, _, _ := strings.Cut(kv, "=")
		if fold {
			name = strings.ToUpper(name)
		}
		if name == "LC_ALL" || name == "LANGUAGE" || name == "GIT_OPTIONAL_LOCKS" {
			return true
		}
		return slices.ContainsFunc(gitDropped, func(v string) bool {
			if prefix, ok := strings.CutSuffix(v, "*"); ok {
				return strings.HasPrefix(name, prefix)
			}
			return name == v
		})
	})
	return append(out, "LC_ALL=C", "LANGUAGE=C", "GIT_OPTIONAL_LOCKS=0")
}

// git runs git with args in dir, without the file system monitor, and
// gives its standard output.
func (osGit) git(ctx context.Context, dir string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, gitTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", append([]string{"-c", "core.fsmonitor=false", "-C", dir}, args...)...)
	cmd.Env = gitEnv(os.Environ(), foldCase)
	cmd.WaitDelay = gitWaitDelay
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	if ctx.Err() != nil {
		return nil, fmt.Errorf("git %s in %s: %w", args[0], dir, ctx.Err())
	}
	if err != nil {
		return stdout.Bytes(), &gitError{args: args, dir: dir, err: err, stderr: strings.TrimSpace(stderr.String())}
	}
	return stdout.Bytes(), nil
}

// gitError is a git command that failed, with its stderr.
type gitError struct {
	args   []string
	dir    string
	err    error
	stderr string
}

func (e *gitError) Error() string {
	msg := fmt.Sprintf("git %s in %s: %v", e.args[0], e.dir, e.err)
	if e.stderr != "" {
		msg += ": " + e.stderr
	}
	return msg
}

func (e *gitError) Unwrap() error { return e.err }

// Repo finds the repository dir is in. Without a .git from dir up, it is
// in none, and git is not asked; otherwise git rev-parse gives the top of
// the worktree, the common directory, whose parent is the main worktree,
// and the exclude file. When git then says dir is in no repository, as
// past GIT_CEILING_DIRECTORIES, it is in none, and when git is missing it
// is a *GitMissingError.
func (g osGit) Repo(ctx context.Context, dir string) (*Repo, error) {
	found := findGit(dir)
	if found == "" {
		return nil, nil
	}
	out, err := g.git(ctx, dir, "rev-parse", "--path-format=absolute", "--show-toplevel", "--git-common-dir", "--git-path", "info/exclude")
	if errors.Is(err, exec.ErrNotFound) {
		return nil, &GitMissingError{Dir: dir, Git: found}
	}
	if ge, ok := errors.AsType[*gitError](err); ok && strings.Contains(ge.stderr, "not a git repository") {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	lines := strings.Split(strings.TrimRight(strings.ReplaceAll(string(out), "\r\n", "\n"), "\n"), "\n")
	if len(lines) != 3 {
		return nil, fmt.Errorf("git rev-parse in %s gave %q, want the top of the worktree, the common directory and the exclude file", dir, out)
	}
	paths := make([]string, 3)
	for i, l := range lines {
		if paths[i] = filepath.Clean(filepath.FromSlash(l)); !filepath.IsAbs(paths[i]) {
			return nil, fmt.Errorf("git rev-parse in %s gave %q, which is not absolute", dir, l)
		}
	}
	return &Repo{Top: paths[0], Main: filepath.Dir(paths[1]), Exclude: paths[2]}, nil
}

// trackedBatch is how many paths one git ls-files is given, which keeps
// its command line short on every OS.
const trackedBatch = 100

// Tracked asks git ls-files which of paths the index of dir's repository
// holds, each path read literally. Where the file system ignores case, as
// foldCase says, a path matches an index entry spelled another way, which
// is the same file there, and git gives the index's spelling: the answer
// is by the paths asked. A path with a trailing slash is a directory, held
// when the index holds any file in it.
func (g osGit) Tracked(ctx context.Context, dir string, paths []string) (map[string]bool, error) {
	magic := ":(literal)"
	if foldCase {
		magic = ":(literal,icase)"
	}
	asked := map[string]string{}
	for _, p := range paths {
		asked[foldKey(p)] = p
	}
	out := map[string]bool{}
	for start := 0; start < len(paths); start += trackedBatch {
		args := []string{"ls-files", "-z", "--"}
		for _, p := range paths[start:min(start+trackedBatch, len(paths))] {
			args = append(args, magic+p)
		}
		list, err := g.git(ctx, dir, args...)
		if err != nil {
			return nil, err
		}
		for name := range strings.SplitSeq(string(list), "\x00") {
			if p, ok := asked[foldKey(name)]; ok && name != "" {
				out[p] = true
			}
			// A directory asked with a trailing slash is tracked when git
			// tracks any file in it.
			for dir := path.Dir(name); name != "" && dir != "." && dir != "/"; dir = path.Dir(dir) {
				if p, ok := asked[foldKey(dir+"/")]; ok {
					out[p] = true
				}
			}
		}
	}
	return out, nil
}

// foldKey is p as paths are compared on this OS: without case where
// foldCase says so.
func foldKey(p string) string {
	if foldCase {
		return strings.ToLower(p)
	}
	return p
}

// findGit is the first .git, a directory or a file as a linked worktree
// has, from dir up to the root, "" when there is none.
func findGit(dir string) string {
	for {
		name := filepath.Join(dir, ".git")
		if _, err := os.Lstat(name); err == nil || !errors.Is(err, fs.ErrNotExist) {
			return name
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
}
