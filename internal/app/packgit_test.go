package app_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/lartweib/dotai/internal/app"
)

// gitHome isolates git from the user's configuration: a new home, whose
// global configuration the test may write, and no system configuration.
func gitHome(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not on PATH")
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	t.Setenv("GIT_CONFIG_GLOBAL", filepath.Join(home, ".gitconfig"))
	return home
}

// runGit runs git with args in dir, as a test sets up a repository.
func runGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-c", "user.name=t", "-c", "user.email=t@example.com", "-c", "init.defaultBranch=main",
		"-c", "core.autocrlf=false", "-c", "commit.gpgSign=false", "-c", "tag.gpgSign=false"}, args...)...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

// remoteRepo is a repository in a new directory holding the example pack,
// committed on main, and the URL the test seam serves it at.
type remoteRepo struct {
	dir string
	url string
}

// newRemote creates a remote with the files of the example pack, as the
// repository of url, and makes the fetches of the test reach it.
func newRemote(t *testing.T, url string) *remoteRepo {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "remote")
	if err := os.CopyFS(dir, os.DirFS(filepath.Join("..", "..", "examples", "minimal-pack"))); err != nil {
		t.Fatal(err)
	}
	runGit(t, dir, "init", "-q")
	runGit(t, dir, "add", "-A")
	runGit(t, dir, "commit", "-q", "-m", "first")
	r := &remoteRepo{dir: dir, url: url}
	serveRemotes(t, r)
	return r
}

// serveRemotes points the fetches of the test at the remotes, by the
// identity of their URL, in any of its forms. A GitHub URL of no remote
// reaches a directory that does not exist, never the network; any other
// URL is left as it is, for the protocol allowlist to refuse.
func serveRemotes(t *testing.T, remotes ...*remoteRepo) {
	t.Helper()
	byID := map[string]string{}
	for _, r := range remotes {
		src, err := app.ParsePackURL(r.url)
		if err != nil {
			t.Fatal(err)
		}
		byID[src.ID] = "file://" + filepath.ToSlash(r.dir)
	}
	nowhere := "file://" + filepath.ToSlash(filepath.Join(t.TempDir(), "no-remote"))
	t.Cleanup(app.SetPackRemote(func(url string) string {
		src, err := app.ParsePackURL(url)
		if err != nil {
			return url
		}
		if to, ok := byID[src.ID]; ok {
			return to
		}
		return nowhere
	}))
}

// commit writes the files, relative paths to content ("" deletes), and
// commits them, returning the commit.
func (r *remoteRepo) commit(t *testing.T, files map[string]string) string {
	t.Helper()
	for name, content := range files {
		p := filepath.Join(r.dir, filepath.FromSlash(name))
		if content == "" {
			if err := os.RemoveAll(p); err != nil {
				t.Fatal(err)
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	runGit(t, r.dir, "add", "-A")
	runGit(t, r.dir, "commit", "-q", "-m", "change")
	return r.head(t)
}

func (r *remoteRepo) head(t *testing.T) string { return runGit(t, r.dir, "rev-parse", "HEAD") }

func TestParseGitVersion(t *testing.T) {
	for out, want := range map[string]error{
		"git version 2.51.0.windows.1\n": nil,
		"git version 2.41.0":             nil,
		"git version 3.0.0":              nil,
		"git version 2.47.3":             nil,
	} {
		if err := app.CheckGitVersion(out); err != want {
			t.Errorf("CheckGitVersion(%q) = %v, want %v", out, err, want)
		}
	}
	for _, out := range []string{"git version 2.40.1", "git version 1.9.5", "git version 2.9.0"} {
		err := app.CheckGitVersion(out)
		if ge, ok := errors.AsType[*app.GitUnavailableError](err); !ok || !ge.TooOld || !strings.Contains(err.Error(), "2.41") {
			t.Errorf("CheckGitVersion(%q) = %v, want a GitUnavailableError, too old, naming 2.41", out, err)
		}
	}
	if _, ok := errors.AsType[*app.GitUnavailableError](app.CheckGitVersion("not git")); !ok {
		t.Error("CheckGitVersion(garbage) is not a GitUnavailableError")
	}
}

func TestAMissingGitIsGitUnavailable(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	err := app.RequireGit(context.Background())
	if ge, ok := errors.AsType[*app.GitUnavailableError](err); !ok || ge.TooOld {
		t.Errorf("RequireGit() = %v, want a GitUnavailableError for a missing git", err)
	}
}

func TestEveryPackGitCallCarriesTheOptionSet(t *testing.T) {
	empty := filepath.Join(t.TempDir(), "empty")
	for _, interactive := range []bool{true, false} {
		args := app.PackGitArgs(interactive, empty, "fetch", "x")
		want := []string{
			"-c", "core.hooksPath=" + empty,
			"-c", "core.attributesFile=",
			"--attr-source=4b825dc642cb6eb9a060e54bf8d69288fbee4904",
			"-c", "core.autocrlf=false", "-c", "core.eol=lf",
			"-c", "protocol.allow=never", "-c", "protocol.https.allow=always", "-c", "protocol.ssh.allow=always",
			"-c", "transfer.fsckObjects=true", "-c", "fetch.fsckObjects=true", "-c", "core.fsmonitor=false",
			"-c", "core.protectNTFS=true", "-c", "core.protectHFS=true",
		}
		if runtime.GOOS == "windows" {
			want = append(want, "-c", "core.longpaths=true")
		}
		if !interactive {
			want = append(want, "-c", "credential.interactive=false", "-c", "core.askPass=")
		}
		for i := 0; i < len(want); i++ {
			if want[i] == "-c" {
				if !slices.Contains(pairs(args), want[i+1]) {
					t.Errorf("interactive %v: args %q lack -c %s", interactive, args, want[i+1])
				}
				i++
			} else if !slices.Contains(args, want[i]) {
				t.Errorf("interactive %v: args %q lack %s", interactive, args, want[i])
			}
		}
		if got := args[len(args)-2:]; !slices.Equal(got, []string{"fetch", "x"}) {
			t.Errorf("args end with %q, want the subcommand", got)
		}
		if interactive && slices.Contains(pairs(args), "credential.interactive=false") {
			t.Error("the command line turns git's prompts off")
		}
	}
}

// pairs is the values after each -c of args.
func pairs(args []string) []string {
	var out []string
	for i := 0; i+1 < len(args); i++ {
		if args[i] == "-c" {
			out = append(out, args[i+1])
		}
	}
	return out
}

func TestPackGitEnvSetsTheGuardsAndDropsWhatWidensTheProtocols(t *testing.T) {
	environ := []string{"PATH=/bin", "GIT_ALLOW_PROTOCOL=file:ext", "GIT_PROTOCOL_FROM_USER=1", "GIT_DIR=/x", "GIT_ATTR_SOURCE=HEAD", "GIT_SSH_COMMAND=evil",
		"GIT_ASKPASS=/bin/ask", "SSH_ASKPASS=/bin/ask"}
	for _, interactive := range []bool{true, false} {
		env := app.PackGitEnv(environ, interactive)
		for _, kv := range []string{"GIT_ATTR_NOSYSTEM=1", "GIT_LFS_SKIP_SMUDGE=1", "PATH=/bin"} {
			if !slices.Contains(env, kv) {
				t.Errorf("interactive %v: env %q lacks %s", interactive, env, kv)
			}
		}
		for _, name := range []string{"GIT_ALLOW_PROTOCOL=", "GIT_PROTOCOL_FROM_USER=", "GIT_DIR=", "GIT_ATTR_SOURCE="} {
			if slices.ContainsFunc(env, func(kv string) bool { return strings.HasPrefix(kv, name) }) {
				t.Errorf("interactive %v: env %q keeps %s", interactive, env, name)
			}
		}
		quiet := []string{"GIT_TERMINAL_PROMPT=0", "GCM_INTERACTIVE=never", "GIT_SSH_COMMAND=ssh -o BatchMode=yes"}
		for _, kv := range quiet {
			if got := slices.Contains(env, kv); got == interactive {
				t.Errorf("interactive %v: %s in env = %v", interactive, kv, got)
			}
		}
		if !interactive && slices.Contains(env, "GIT_SSH_COMMAND=evil") {
			t.Error("the non-interactive env keeps the user's GIT_SSH_COMMAND")
		}
		for _, kv := range []string{"GIT_ASKPASS=/bin/ask", "SSH_ASKPASS=/bin/ask"} {
			if got := slices.Contains(env, kv); got != interactive {
				t.Errorf("interactive %v: %s in env = %v", interactive, kv, got)
			}
		}
	}
}

func TestParseFetchHead(t *testing.T) {
	sha := strings.Repeat("a", 40)
	for _, tt := range []struct{ line, want, wanted string }{
		{sha + "\t\tbranch 'main' of https://github.com/a/b\n", "refs/heads/main", "main"},
		{sha + "\t\tbranch 'feature/x' of https://github.com/a/b\n", "refs/heads/feature/x", "feature/x"},
		{sha + "\t\ttag 'v1.0' of https://github.com/a/b\n", "refs/tags/v1.0", "v1.0"},
		{sha + "\t\t'refs/pull/1/head' of https://github.com/a/b\n", "refs/pull/1/head", "refs/pull/1/head"},
		{sha + "\t\t'" + sha + "' of https://github.com/a/b\n", sha, sha},
		{sha + "\t\tbranch 'main' of https://github.com/a/b\n", "refs/heads/main", "refs/heads/main"},
	} {
		got, err := app.ParseFetchHead(tt.line, tt.wanted)
		if err != nil || got != tt.want {
			t.Errorf("ParseFetchHead(%q) = %q, %v, want %q", tt.line, got, err, tt.want)
		}
	}
	if _, err := app.ParseFetchHead(sha+"\t\thttps://github.com/a/b\n", "main"); err == nil {
		t.Error("a FETCH_HEAD without a ref name was accepted for a named ref")
	}
}

// lsTree is the output of git ls-tree -r -z -l for entries, each mode,
// type, size and path.
func lsTree(entries ...[4]string) []byte {
	var b strings.Builder
	for _, e := range entries {
		fmt.Fprintf(&b, "%s %s %s %7s\t%s\x00", e[0], e[1], strings.Repeat("0", 40), e[2], e[3])
	}
	return []byte(b.String())
}

// TestCheckPackTreeRefusesLinksSubmodulesAndGitPaths checks the refused
// entries of a tree, and that files and executables pass.
func TestCheckPackTreeRefusesLinksSubmodulesAndGitPaths(t *testing.T) {
	out := lsTree(
		[4]string{"100644", "blob", "12", "dotai.yaml"},
		[4]string{"100755", "blob", "3", "base/dotai/tools/a b.sh"},
		[4]string{"120000", "blob", "9", "base/link"},
		[4]string{"160000", "commit", "-", "vendor/sub"},
		[4]string{"100644", "blob", "1", "a/.git/config"},
		[4]string{"100644", "blob", "1", "b/.GIT/hooks/x"},
		[4]string{"100644", "blob", "1", "c/.git. /x"},
		[4]string{"100644", "blob", "1", "d/.GIT:stream/x"},
		[4]string{"100644", "blob", "1", "e/git~1/x"},
		[4]string{"100644", "blob", "1", "f/GIT~2/x"},
		[4]string{"100644", "blob", "1", "g/.g\u200cit/x"},
		[4]string{"100644", "blob", "1", "h/\ufeff.git/x"},
		[4]string{"100644", "blob", "1", "i/.gitignore"},
		[4]string{"100644", "blob", "1", "j/git~x/y"},
	)
	problems := app.CheckPackTree(out)
	var paths []string
	for _, p := range problems {
		paths = append(paths, p.Path)
	}
	want := []string{"base/link", "vendor/sub", "a/.git/config", "b/.GIT/hooks/x", "c/.git. /x", "d/.GIT:stream/x", "e/git~1/x", "f/GIT~2/x",
		"g/.g\u200cit/x", "h/\ufeff.git/x"}
	if !slices.Equal(paths, want) {
		t.Errorf("refused %q, want %q (%+v)", paths, want, problems)
	}
}

func TestCheckPackTreeRefusesMoreThanACopyTakes(t *testing.T) {
	var many [][4]string
	for i := range 10001 {
		many = append(many, [4]string{"100644", "blob", "1", fmt.Sprintf("f%d", i)})
	}
	if problems := app.CheckPackTree(lsTree(many...)); len(problems) != 1 || !strings.Contains(problems[0].Problem, "10000") {
		t.Errorf("CheckPackTree(10001 files) = %+v, want one problem naming the bound", problems)
	}
	big := lsTree([4]string{"100644", "blob", fmt.Sprint(40 << 20), "a"}, [4]string{"100644", "blob", fmt.Sprint(30 << 20), "b"})
	if problems := app.CheckPackTree(big); len(problems) != 1 || !strings.Contains(problems[0].Problem, "64 MiB") {
		t.Errorf("CheckPackTree(70 MiB) = %+v, want one problem naming the bound", problems)
	}
}

func TestFetchPackGetsTheCommitAndTheFullRef(t *testing.T) {
	gitHome(t)
	r := newRemote(t, "https://github.com/acme/pack")
	first := r.head(t)
	runGit(t, r.dir, "tag", "-a", "v1", "-m", "v1")
	runGit(t, r.dir, "tag", "light")
	second := r.commit(t, map[string]string{"base/.rulesync/rules/extra.md": "# extra\n"})
	runGit(t, r.dir, "branch", "dev", first)
	for _, tt := range []struct{ ref, commit, fullRef string }{
		{"", second, "refs/heads/main"},
		{"main", second, "refs/heads/main"},
		{"dev", first, "refs/heads/dev"},
		{"v1", first, "refs/tags/v1"},
		{"light", first, "refs/tags/light"},
		{"refs/heads/main", second, "refs/heads/main"},
		{first, first, first},
	} {
		dir := filepath.Join(t.TempDir(), "clone")
		got, err := app.FetchPack(context.Background(), dir, r.url, tt.ref)
		if err != nil {
			t.Errorf("FetchPack(%q) error = %v", tt.ref, err)
			continue
		}
		if got.Commit != tt.commit || got.FullRef != tt.fullRef {
			t.Errorf("FetchPack(%q) = %+v, want commit %s and %s", tt.ref, got, tt.commit, tt.fullRef)
		}
		if _, err := os.Stat(filepath.Join(dir, "dotai.yaml")); err != nil {
			t.Errorf("FetchPack(%q) checked nothing out: %v", tt.ref, err)
		}
	}
}

func TestFetchPackOfAnUnservedCommitIsRefused(t *testing.T) {
	gitHome(t)
	r := newRemote(t, "https://github.com/acme/pack")
	_, err := app.FetchPack(context.Background(), filepath.Join(t.TempDir(), "clone"), r.url, strings.Repeat("1", 40))
	if _, ok := errors.AsType[*app.FetchByCommitRefusedError](err); !ok {
		t.Errorf("error = %v, want a FetchByCommitRefusedError", err)
	}
}

func TestFetchPackOfAMissingRefOrRemoteFails(t *testing.T) {
	gitHome(t)
	r := newRemote(t, "https://github.com/acme/pack")
	_, err := app.FetchPack(context.Background(), filepath.Join(t.TempDir(), "clone"), r.url, "nope")
	if fe, ok := errors.AsType[*app.FetchFailedError](err); !ok || !strings.Contains(err.Error(), r.url) {
		t.Errorf("error = %v (%T), want a FetchFailedError naming the URL", err, fe)
	}
	// The protocol allowlist refuses the file protocol outside the test seam.
	_, err = app.FetchPack(context.Background(), filepath.Join(t.TempDir(), "clone"), "file://"+filepath.ToSlash(r.dir), "main")
	if _, ok := errors.AsType[*app.FetchFailedError](err); !ok {
		t.Errorf("a file:// fetch outside the seam = %v, want a FetchFailedError", err)
	}
}

func TestFetchPackRunsNoHookAndNoAttribute(t *testing.T) {
	home := gitHome(t)
	// The remote first, so that only the fetch can run the hooks of the
	// user's configuration written below.
	r := newRemote(t, "https://github.com/acme/pack")
	r.commit(t, map[string]string{".gitattributes": "*.md eol=crlf ident\n", "base/.rulesync/rules/id.md": "$Id$\nline\n"})
	marker := filepath.Join(t.TempDir(), "hook-ran")
	hooks := filepath.Join(home, "hooks")
	if err := os.MkdirAll(hooks, 0o755); err != nil {
		t.Fatal(err)
	}
	hook := "#!/bin/sh\necho ran >> '" + filepath.ToSlash(marker) + "'\n"
	for _, name := range []string{"reference-transaction", "post-checkout"} {
		if err := os.WriteFile(filepath.Join(hooks, name), []byte(hook), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	attrs := filepath.Join(home, "attributes")
	if err := os.WriteFile(attrs, []byte("* eol=crlf\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	templates := filepath.Join(home, "templates", "hooks")
	if err := os.MkdirAll(templates, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(templates, "post-checkout"), []byte(hook), 0o755); err != nil {
		t.Fatal(err)
	}
	config := fmt.Sprintf("[core]\n\thooksPath = %s\n\tattributesFile = %s\n\tautocrlf = true\n[init]\n\ttemplateDir = %s\n",
		filepath.ToSlash(hooks), filepath.ToSlash(attrs), filepath.ToSlash(filepath.Dir(templates)))
	if err := os.WriteFile(filepath.Join(home, ".gitconfig"), []byte(config), 0o644); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(t.TempDir(), "clone")
	if _, err := app.FetchPack(context.Background(), dir, r.url, ""); err != nil {
		t.Fatalf("FetchPack() error = %v", err)
	}
	if _, err := os.Stat(marker); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("a hook ran during the fetch: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(dir, "base", ".rulesync", "rules", "id.md"))
	if err != nil || string(got) != "$Id$\nline\n" {
		t.Errorf("the checkout = %q, %v, want the blob's bytes", got, err)
	}
	if entries, _ := os.ReadDir(filepath.Join(dir, ".git", "hooks")); len(entries) != 0 {
		t.Errorf("the clone has hooks: %v", entries)
	}
}

func TestPackTreeOfACommitWithALinkIsRefused(t *testing.T) {
	gitHome(t)
	r := newRemote(t, "https://github.com/acme/pack")
	blob := runGitIn(t, r.dir, "target\n", "hash-object", "-w", "--stdin")
	runGit(t, r.dir, "update-index", "--add", "--cacheinfo", "120000,"+blob+",base/link")
	runGit(t, r.dir, "update-index", "--add", "--cacheinfo", "160000,"+r.head(t)+",vendor/sub")
	runGit(t, r.dir, "commit", "-q", "-m", "link")
	dir := filepath.Join(t.TempDir(), "clone")
	_, err := app.FetchPack(context.Background(), dir, r.url, "")
	tr, ok := errors.AsType[*app.PackTreeRefusedError](err)
	if !ok || len(tr.Problems) != 2 {
		t.Fatalf("error = %v, want a PackTreeRefusedError for the link and the submodule", err)
	}
	if _, err := os.Lstat(filepath.Join(dir, "base")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("a refused tree was checked out: %v", err)
	}
}

// runGitIn runs git with stdin in dir and gives its output.
func runGitIn(t *testing.T, dir, stdin string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Stdin = strings.NewReader(stdin)
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("git %s: %v", strings.Join(args, " "), err)
	}
	return strings.TrimSpace(string(out))
}

func TestFetchPackTellsAHexTagFromAnAbbreviatedSHA(t *testing.T) {
	gitHome(t)
	r := newRemote(t, "https://github.com/acme/pack")
	runGit(t, r.dir, "tag", "abcdef1")
	got, err := app.FetchPack(context.Background(), filepath.Join(t.TempDir(), "clone"), r.url, "abcdef1")
	if err != nil || got.FullRef != "refs/tags/abcdef1" {
		t.Errorf("FetchPack(abcdef1) = %+v, %v, want the tag", got, err)
	}
	_, err = app.FetchPack(context.Background(), filepath.Join(t.TempDir(), "clone"), r.url, r.head(t)[:12])
	if !errors.As(err, new(*app.UsageError)) || !strings.Contains(err.Error(), "full") || !strings.Contains(err.Error(), "refs/tags/") {
		t.Errorf("FetchPack(an abbreviated SHA) error = %v, want a usage error asking for the full SHA or refs/tags/<name>", err)
	}
}

func TestFetchPackRefusesARemoteWithoutADefaultBranch(t *testing.T) {
	gitHome(t)
	r := newRemote(t, "https://github.com/acme/pack")
	runGit(t, r.dir, "checkout", "-q", "--detach")
	_, err := app.FetchPack(context.Background(), filepath.Join(t.TempDir(), "clone"), r.url, "")
	if !errors.As(err, new(*app.UsageError)) || !strings.Contains(err.Error(), "--ref") {
		t.Errorf("error = %v, want a usage error asking for --ref", err)
	}
}

func TestFetchPackSaysWhyItStopped(t *testing.T) {
	gitHome(t)
	r := newRemote(t, "https://github.com/acme/pack")
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := app.FetchPack(cancelled, filepath.Join(t.TempDir(), "clone"), r.url, "")
	if !errors.Is(err, context.Canceled) || !strings.Contains(err.Error(), "stopped") {
		t.Errorf("a cancelled fetch = %v, want it stopped", err)
	}
	late, stop := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer stop()
	_, err = app.FetchPack(late, filepath.Join(t.TempDir(), "clone"), r.url, "")
	if !errors.Is(err, context.DeadlineExceeded) || !strings.Contains(err.Error(), "time limit of the operation") {
		t.Errorf("a fetch past its caller's deadline = %v, want the operation's time limit named", err)
	}
	defer app.SetPackFetchTimeout(time.Nanosecond)()
	_, err = app.FetchPack(context.Background(), filepath.Join(t.TempDir(), "clone"), r.url, "")
	if !errors.Is(err, context.DeadlineExceeded) || !strings.Contains(err.Error(), "took longer than") {
		t.Errorf("a fetch past its own timeout = %v, want git's timeout named", err)
	}
}

// TestFetchPackFromGitHub fetches a tiny public repository anonymously,
// only with DOTAI_NET_TESTS=1: every other test stays off the network.
func TestFetchPackFromGitHub(t *testing.T) {
	if os.Getenv("DOTAI_NET_TESTS") != "1" {
		t.Skip("set DOTAI_NET_TESTS=1 to fetch from github.com")
	}
	gitHome(t)
	dir := filepath.Join(t.TempDir(), "clone")
	got, err := app.FetchPack(context.Background(), dir, "https://github.com/octocat/Hello-World", "")
	if err != nil {
		t.Fatalf("FetchPack() error = %v", err)
	}
	if len(got.Commit) != 40 || !strings.HasPrefix(got.FullRef, "refs/heads/") {
		t.Errorf("FetchPack() = %+v, want a commit and the default branch", got)
	}
	if _, err := os.Stat(filepath.Join(dir, "README")); err != nil {
		t.Errorf("nothing was checked out: %v", err)
	}
}
