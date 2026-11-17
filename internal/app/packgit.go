package app

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"time"
)

// How dotai fetches a git pack (decision 0044): with the git on PATH, 2.41
// or later, into a new repository that receives neither the remote's hooks
// nor its configuration, with options on every call that keep the user's
// configuration and the pack's attributes from running anything or
// changing the checkout.

// emptyTree is the id of git's empty tree, the source of attributes of
// every call: the pack's .gitattributes then has no effect.
const emptyTree = "4b825dc642cb6eb9a060e54bf8d69288fbee4904"

// minGit is the first git release with --attr-source.
var minGit = [2]int{2, 41}

// packFetchTimeout bounds a git call that reaches the remote, a fetch or
// an ls-remote; every other call is bounded by gitTimeout.
var packFetchTimeout = 10 * time.Minute

// packRemote gives the URL git reaches for url, and the options it adds:
// url itself and none, but a test reaches a local repository through it.
var packRemote = func(url string) (string, []string) { return url, nil }

// packGit runs git for a pack. Interactive leaves git's prompts on, as on
// the command line; otherwise, from the web UI, git asks nothing. empty is
// an empty directory, the template and the hooks of every call.
type packGit struct {
	interactive bool
	empty       string
}

// newPackGit checks the git on PATH and makes the empty directory of its
// calls, which done removes.
func newPackGit(ctx context.Context, interactive bool) (g packGit, done func(), err error) {
	if err := requireGit(ctx); err != nil {
		return packGit{}, nil, err
	}
	empty, err := os.MkdirTemp("", "dotai-git-empty-")
	if err != nil {
		return packGit{}, nil, err
	}
	return packGit{interactive: interactive, empty: empty}, func() { _ = os.Remove(empty) }, nil
}

// packGitArgs is the command line of git for sub: the option set of every
// call (decision 0044), then sub.
func packGitArgs(interactive bool, empty string, sub ...string) []string {
	args := []string{
		"-c", "core.hooksPath=" + empty,
		"-c", "core.attributesFile=",
		"--attr-source=" + emptyTree,
		"-c", "core.autocrlf=false", "-c", "core.eol=lf",
		"-c", "protocol.allow=never", "-c", "protocol.https.allow=always", "-c", "protocol.ssh.allow=always",
		"-c", "transfer.fsckObjects=true", "-c", "fetch.fsckObjects=true", "-c", "core.fsmonitor=false",
		"-c", "core.protectNTFS=true", "-c", "core.protectHFS=true",
	}
	if runtime.GOOS == "windows" {
		args = append(args, "-c", "core.longpaths=true")
	}
	if !interactive {
		args = append(args, "-c", "credential.interactive=false", "-c", "core.askPass=")
	}
	return append(args, sub...)
}

// packGitDropped are the variables that would widen the protocols git may
// use or give the pack's attributes back, besides those gitEnv drops;
// packGitQuiet are those that would ask the user, dropped without a
// prompt.
var (
	packGitDropped = []string{"GIT_ALLOW_PROTOCOL", "GIT_PROTOCOL_FROM_USER", "GIT_ATTR_SOURCE", "GIT_TEMPLATE_DIR"}
	packGitQuiet   = []string{"GIT_SSH_COMMAND", "GIT_ASKPASS", "SSH_ASKPASS"}
)

// packGitEnv is the environment of a git call for a pack: environ as gitEnv
// gives it, without packGitDropped, with no system attributes and no LFS
// smudge, and, unless interactive, without any prompt: SSH ignores
// GIT_TERMINAL_PROMPT, so it runs in batch mode, which overrides the
// user's core.sshCommand, and no askpass program runs.
func packGitEnv(environ []string, interactive bool) []string {
	env := slices.DeleteFunc(gitEnv(environ, foldCase), func(kv string) bool {
		name, _, _ := strings.Cut(kv, "=")
		if foldCase {
			name = strings.ToUpper(name)
		}
		return slices.Contains(packGitDropped, name) || !interactive && slices.Contains(packGitQuiet, name)
	})
	env = append(env, "GIT_ATTR_NOSYSTEM=1", "GIT_LFS_SKIP_SMUDGE=1")
	if !interactive {
		env = append(env, "GIT_TERMINAL_PROMPT=0", "GCM_INTERACTIVE=never", "GIT_SSH_COMMAND=ssh -o BatchMode=yes")
	}
	return env
}

// run runs git with the option set and extra, then sub, in dir when it is
// not "", for at most timeout, with stdin closed, and gives its stdout. A
// call that stops says why (gitStopped).
func (g packGit) run(ctx context.Context, timeout time.Duration, dir string, extra []string, sub ...string) ([]byte, error) {
	call, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	args := packGitArgs(g.interactive, g.empty, append(slices.Clone(extra), sub...)...)
	if dir != "" {
		args = append([]string{"-C", dir}, args...)
	}
	cmd := exec.CommandContext(call, "git", args...)
	cmd.Env = packGitEnv(os.Environ(), g.interactive)
	cmd.WaitDelay = gitWaitDelay
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	if call.Err() != nil {
		return nil, gitStopped(ctx, sub[0], timeout)
	}
	if err != nil {
		return stdout.Bytes(), &gitError{args: sub, dir: dir, err: err, stderr: strings.TrimSpace(stderr.String())}
	}
	return stdout.Bytes(), nil
}

// gitVersion matches the version git --version gives.
var gitVersion = regexp.MustCompile(`^git version (\d+)\.(\d+)`)

// checkGitVersion checks the output of git --version against minGit.
func checkGitVersion(out string) error {
	out = strings.TrimSpace(out)
	m := gitVersion.FindStringSubmatch(out)
	if m == nil {
		return &GitUnavailableError{Version: out}
	}
	major, _ := strconv.Atoi(m[1])
	minor, _ := strconv.Atoi(m[2])
	if major < minGit[0] || major == minGit[0] && minor < minGit[1] {
		return &GitUnavailableError{Version: strings.TrimPrefix(out, "git version "), TooOld: true}
	}
	return nil
}

// requireGit checks that the git on PATH can fetch packs. It runs git
// --version alone: an older git refuses --attr-source, of the option set,
// before it prints its version.
func requireGit(ctx context.Context) error {
	call, cancel := context.WithTimeout(ctx, gitTimeout)
	defer cancel()
	cmd := exec.CommandContext(call, "git", "--version")
	cmd.Env = packGitEnv(os.Environ(), false)
	cmd.WaitDelay = gitWaitDelay
	out, err := cmd.Output()
	if call.Err() != nil {
		return gitStopped(ctx, "--version", gitTimeout)
	}
	if err != nil {
		return &GitUnavailableError{Err: err}
	}
	return checkGitVersion(string(out))
}

// PackFetch is what a fetch of a pack got: the commit and the full name of
// the ref fetched.
type PackFetch struct {
	Commit  string
	FullRef string
}

// fetchPack fetches ref of url ("" for the remote's default branch) into
// dir, a new repository, checks the tree of the commit and checks that
// commit out. dir must not exist, or be empty; its parent must exist. On
// an error dir may hold part of the fetch, and the caller removes it.
func (g packGit) fetchPack(ctx context.Context, dir, url, ref string) (PackFetch, error) {
	remote, extra := packRemote(url)
	if _, err := g.run(ctx, gitTimeout, "", nil, "init", "-q", "--template="+g.empty, "-b", "main", dir); err != nil {
		return PackFetch{}, err
	}
	want, err := g.resolveRef(ctx, dir, url, remote, extra, ref)
	if err != nil {
		return PackFetch{}, err
	}
	if _, err := g.run(ctx, packFetchTimeout, dir, extra, "fetch", "-q", "--depth", "1", "--no-tags", "--no-recurse-submodules", "--", remote, want); err != nil {
		if ge, ok := errors.AsType[*gitError](err); ok && fullSHA(want) && commitRefused.MatchString(ge.stderr) {
			return PackFetch{}, &FetchByCommitRefusedError{URL: url, Commit: want, Stderr: ge.stderr}
		}
		return PackFetch{}, fetchFailed(url, ref, err)
	}
	out, err := g.run(ctx, gitTimeout, dir, nil, "rev-parse", "FETCH_HEAD^{commit}")
	if err != nil {
		return PackFetch{}, err
	}
	commit := strings.TrimSpace(string(out))
	if !fullSHA(commit) {
		return PackFetch{}, fmt.Errorf("git rev-parse FETCH_HEAD^{commit} in %s gave %q, not a commit", dir, commit)
	}
	head, err := os.ReadFile(filepath.Join(dir, ".git", "FETCH_HEAD"))
	if err != nil {
		return PackFetch{}, err
	}
	fullRef, err := parseFetchHead(string(head), want)
	if err != nil {
		return PackFetch{}, err
	}
	tree, err := g.run(ctx, gitTimeout, dir, nil, "ls-tree", "-r", "-z", "-l", commit)
	if err != nil {
		return PackFetch{}, err
	}
	if problems := checkPackTree(tree); len(problems) > 0 {
		return PackFetch{}, &PackTreeRefusedError{URL: url, Commit: commit, Problems: problems}
	}
	// The commit checked, not FETCH_HEAD, which a tag object names.
	if _, err := g.run(ctx, gitTimeout, dir, nil, "checkout", "-q", "--no-recurse-submodules", "--detach", commit); err != nil {
		return PackFetch{}, err
	}
	return PackFetch{Commit: commit, FullRef: fullRef}, nil
}

// resolveRef is what fetchPack fetches for ref: the branch the remote's
// HEAD points to for "", which must be one; a tag or a branch for a run of
// hexadecimal digits shorter than a full SHA, which must name one, since
// an abbreviated SHA cannot be fetched; ref itself otherwise.
func (g packGit) resolveRef(ctx context.Context, dir, url, remote string, extra []string, ref string) (string, error) {
	switch {
	case ref == "":
		out, err := g.run(ctx, packFetchTimeout, dir, extra, "ls-remote", "--symref", "--", remote, "HEAD")
		if err != nil {
			return "", fetchFailed(url, ref, err)
		}
		branch := headBranch(string(out))
		if branch == "" {
			return "", &UsageError{msg: fmt.Sprintf("%s names no default branch: its HEAD is not a branch, so give the ref to fetch with --ref", url)}
		}
		return branch, nil
	case len(ref) >= 7 && hexRef.MatchString(ref) && !fullSHA(ref):
		out, err := g.run(ctx, packFetchTimeout, dir, extra, "ls-remote", "--", remote, "refs/tags/"+ref, "refs/heads/"+ref)
		if err != nil {
			return "", fetchFailed(url, ref, err)
		}
		if strings.TrimSpace(string(out)) == "" {
			return "", &UsageError{msg: fmt.Sprintf("%s has no tag or branch %q, and an abbreviated commit SHA cannot be fetched: give the full SHA, or refs/tags/<name> or refs/heads/<name>", url, ref)}
		}
	}
	return ref, nil
}

// commitRefused matches what git says of a commit the remote does not serve.
var commitRefused = regexp.MustCompile(`not our ref|unadvertised object|couldn't find remote ref|no such remote ref|not a valid object`)

// headBranch is the branch HEAD points to in the output of git ls-remote
// --symref, "" when it names none.
func headBranch(out string) string {
	for line := range strings.SplitSeq(out, "\n") {
		if rest, ok := strings.CutPrefix(line, "ref: "); ok {
			if target, name, ok := strings.Cut(strings.TrimRight(rest, "\r"), "\t"); ok && name == "HEAD" && strings.HasPrefix(target, "refs/heads/") {
				return target
			}
		}
	}
	return ""
}

// fetchHeadKinds are how FETCH_HEAD describes a ref, in the order they are
// tried, and the prefix of its full name.
var fetchHeadKinds = []struct{ prefix, full string }{
	{"branch '", "refs/heads/"},
	{"tag '", "refs/tags/"},
	{"'", ""},
}

// parseFetchHead is the full name of the ref that the first line of
// FETCH_HEAD records, wanted being the ref asked for: refs/heads/... for a
// branch, refs/tags/... for a tag, the ref itself when git names it in
// full, and the commit when one was asked for. Git ref names hold no
// space, so the name ends at the first "' of ".
func parseFetchHead(data, wanted string) (string, error) {
	line, _, _ := strings.Cut(data, "\n")
	fields := strings.SplitN(line, "\t", 3)
	if len(fields) != 3 {
		return "", fmt.Errorf("FETCH_HEAD %q has no ref", line)
	}
	if fullSHA(wanted) {
		return strings.ToLower(wanted), nil
	}
	for _, k := range fetchHeadKinds {
		if rest, ok := strings.CutPrefix(fields[2], k.prefix); ok {
			if name, _, ok := strings.Cut(rest, "' of "); ok && name != "" {
				return k.full + name, nil
			}
		}
	}
	return "", fmt.Errorf("FETCH_HEAD %q does not name the ref fetched for %q", line, wanted)
}
