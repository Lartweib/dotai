package app

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// gitTimeout bounds each git command a project plan runs.
const gitTimeout = 30 * time.Second

// Repo is the git repository a project is in: Prefix, the project's
// directory relative to its top, a slash path that ends in /, "" at the
// top, and Exclude, the absolute path of its exclude file, the one git
// rev-parse --git-path info/exclude gives (decision 0041).
type Repo struct {
	Prefix  string
	Exclude string
}

// gitPort is how a project plan asks git about the project dir: the
// repository it is in, nil outside one, and which of paths, slash paths
// relative to dir, git tracks. Neither writes anything.
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

// git runs git with args in dir, with no optional lock taken and pathspecs
// read literally, and gives its standard output.
func (osGit) git(ctx context.Context, dir string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, gitTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", append([]string{"--literal-pathspecs", "-C", dir}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_OPTIONAL_LOCKS=0")
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

// gitError is a git command that failed, with the end of its stderr.
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

// Repo asks git rev-parse for the top of the repository dir is in and its
// exclude file. Outside a repository it gives nil; so it does when git is
// missing and no .git is found from dir up, and a .git found there is a
// *GitMissingError.
func (g osGit) Repo(ctx context.Context, dir string) (*Repo, error) {
	out, err := g.git(ctx, dir, "rev-parse", "--show-prefix", "--git-path", "info/exclude")
	if errors.Is(err, exec.ErrNotFound) {
		if found := findGit(dir); found != "" {
			return nil, &GitMissingError{Dir: dir, Git: found}
		}
		return nil, nil
	}
	if ge, ok := errors.AsType[*gitError](err); ok && strings.Contains(ge.stderr, "not a git repository") {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	lines := strings.Split(strings.TrimRight(strings.ReplaceAll(string(out), "\r\n", "\n"), "\n"), "\n")
	if len(lines) != 2 {
		return nil, fmt.Errorf("git rev-parse in %s gave %q, want the prefix and the exclude file", dir, out)
	}
	exclude := filepath.FromSlash(lines[1])
	if !filepath.IsAbs(exclude) {
		exclude = filepath.Join(dir, exclude)
	}
	return &Repo{Prefix: lines[0], Exclude: exclude}, nil
}

// trackedBatch is how many paths one git ls-files is given, which keeps
// its command line short on every OS.
const trackedBatch = 100

// Tracked asks git ls-files which of paths the index of dir's repository
// holds.
func (g osGit) Tracked(ctx context.Context, dir string, paths []string) (map[string]bool, error) {
	out := map[string]bool{}
	for start := 0; start < len(paths); start += trackedBatch {
		batch := paths[start:min(start+trackedBatch, len(paths))]
		list, err := g.git(ctx, dir, append([]string{"ls-files", "-z", "--"}, batch...)...)
		if err != nil {
			return nil, err
		}
		for name := range strings.SplitSeq(string(list), "\x00") {
			if name != "" {
				out[name] = true
			}
		}
	}
	return out, nil
}

// findGit is the first .git, a directory or a file as a worktree has, from
// dir up to the root, "" when there is none.
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
