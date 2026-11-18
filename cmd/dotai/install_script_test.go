package main

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// The install scripts at the root of the repository (0048) run here
// against a fake release served on 127.0.0.1 through
// DOTAI_RELEASE_BASE_URL, so no test needs the network. install.sh runs
// under sh, and bash when present, on Linux and macOS; install.ps1 under
// Windows PowerShell, and pwsh when present, on Windows. In CI a missing
// sh or Windows PowerShell fails the test instead of skipping it. Every
// run has a temporary home, and install.ps1 writes the user Path to a
// test-only registry key (DOTAI_TEST_USER_ENV_KEY), never to the real one.

const fakeTag = "v1.2.3"

// release is a fake GitHub releases URL: <base>/latest redirects to
// <base>/tag/<latest>, and <base>/download/<tag>/<name> serves files.
type release struct {
	// latest is the tag /latest redirects to; "fail" makes /latest fail,
	// and "none" redirects it to the release list, as GitHub does when a
	// repository has no release.
	latest string
	files  map[string][]byte
	server *httptest.Server
}

func newRelease(t *testing.T, latest string, files map[string][]byte) *release {
	t.Helper()
	r := &release{latest: latest, files: files}
	r.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		// A /mirror prefix is served like the root, for a base URL with a path.
		switch p := strings.TrimPrefix(req.URL.Path, "/mirror"); {
		case p == "/latest" && r.latest == "none":
			http.Redirect(w, req, "/list", http.StatusFound)
		case p == "/latest" && r.latest != "fail":
			http.Redirect(w, req, "/tag/"+r.latest, http.StatusFound)
		case p == "/tag/"+fakeTag, p == "/list":
			w.WriteHeader(http.StatusOK)
		case strings.HasPrefix(p, "/download/"+fakeTag+"/"):
			body, ok := r.files[strings.TrimPrefix(p, "/download/"+fakeTag+"/")]
			if !ok {
				http.NotFound(w, req)
				return
			}
			_, _ = w.Write(body)
		case strings.HasPrefix(p, "/download/"):
			http.NotFound(w, req)
		default:
			http.Error(w, "not here", http.StatusInternalServerError)
		}
	}))
	t.Cleanup(r.server.Close)
	return r
}

// archiveFor packs bin as the release archive of goos/goarch and returns
// its name and bytes.
func archiveFor(t *testing.T, bin []byte, goos, goarch string) (string, []byte) {
	t.Helper()
	var buf bytes.Buffer
	version := strings.TrimPrefix(fakeTag, "v")
	if goos == "windows" {
		zw := zip.NewWriter(&buf)
		for name, body := range map[string][]byte{"dotai.exe": bin, "LICENSE": []byte("license\n")} {
			f, err := zw.Create(name)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := f.Write(body); err != nil {
				t.Fatal(err)
			}
		}
		if err := zw.Close(); err != nil {
			t.Fatal(err)
		}
		return fmt.Sprintf("dotai_%s_windows_%s.zip", version, goarch), buf.Bytes()
	}
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for _, e := range []struct {
		name string
		mode int64
		body []byte
	}{{"dotai", 0o755, bin}, {"LICENSE", 0o644, []byte("license\n")}} {
		if err := tw.WriteHeader(&tar.Header{Name: e.name, Mode: e.mode, Size: int64(len(e.body))}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write(e.body); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return fmt.Sprintf("dotai_%s_%s_%s.tar.gz", version, goos, goarch), buf.Bytes()
}

// shell runs one of the install scripts.
type shell struct {
	name string
	// run returns the command that runs the script with the arguments,
	// which only install.sh takes.
	run func(ctx context.Context, script string, args ...string) *exec.Cmd
}

// onCI reports whether the tests run in CI, where a missing shell fails.
func onCI() bool { return os.Getenv("CI") != "" || os.Getenv("GITHUB_ACTIONS") == "true" }

// installShells lists the shells of this OS that are installed, and the
// script they run. In CI, sh (Linux, macOS) and Windows PowerShell must be
// there; bash and pwsh run when present.
func installShells(t *testing.T) (string, []shell) {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	var shells []shell
	find := func(name string, required bool, run func(path string) func(context.Context, string, ...string) *exec.Cmd) {
		path, err := exec.LookPath(name)
		if err != nil {
			if required && onCI() {
				t.Fatalf("%s is required in CI to test the install script: %v", name, err)
			}
			t.Logf("%s not found; its install script cases are skipped", name)
			return
		}
		shells = append(shells, shell{name, run(path)})
	}
	if runtime.GOOS == "windows" {
		ps := func(path string) func(context.Context, string, ...string) *exec.Cmd {
			return func(ctx context.Context, script string, _ ...string) *exec.Cmd {
				// As irm | iex runs it: the script text through Invoke-Expression.
				return exec.CommandContext(ctx, path, "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-Command",
					"Get-Content -Raw -LiteralPath '"+script+"' | Invoke-Expression")
			}
		}
		find("powershell", true, ps)
		find("pwsh", false, ps)
		return filepath.Join(root, "install.ps1"), shells
	}
	posix := func(path string) func(context.Context, string, ...string) *exec.Cmd {
		return func(ctx context.Context, script string, args ...string) *exec.Cmd {
			return exec.CommandContext(ctx, path, append([]string{script}, args...)...)
		}
	}
	find("sh", true, posix)
	find("bash", false, posix)
	return filepath.Join(root, "install.sh"), shells
}

// opts are the inputs of one run that a test sets.
type opts struct {
	home  string   // the home directory; empty makes a new one
	key   string   // install.ps1's user environment key; empty makes a new one
	args  []string // install.sh's arguments
	env   []string // extra variables, after the defaults
	unset []string // variables to leave out
}

// result is what one run left behind.
type result struct {
	out            string
	err            error
	home, dir, tmp string
	key            string
}

// installEnv is the environment of one run: a temporary home, temp
// directory and install directory, the fake release, no other DOTAI_
// variable, and /bin/sh as the user's shell.
func installEnv(r result, base string, o opts) []string {
	var env []string
	for _, kv := range os.Environ() {
		k, _, _ := strings.Cut(kv, "=")
		switch strings.ToUpper(k) {
		case "HOME", "USERPROFILE", "LOCALAPPDATA", "TMPDIR", "TMP", "TEMP", "SHELL",
			"PROCESSOR_ARCHITECTURE", "PROCESSOR_ARCHITEW6432":
			continue
		}
		if strings.HasPrefix(strings.ToUpper(k), "DOTAI_") {
			continue
		}
		env = append(env, kv)
	}
	env = append(env, "HOME="+r.home, "USERPROFILE="+r.home, "LOCALAPPDATA="+r.home,
		"TMPDIR="+r.tmp, "TMP="+r.tmp, "TEMP="+r.tmp, "SHELL=/bin/sh",
		"DOTAI_INSTALL_DIR="+r.dir, "DOTAI_RELEASE_BASE_URL="+base)
	if runtime.GOOS == "windows" {
		env = append(env, "PROCESSOR_ARCHITECTURE=AMD64", "DOTAI_TEST_USER_ENV_KEY="+r.key)
	}
	// exec keeps the last value of a variable that is set twice.
	env = append(env, o.env...)
	kept := env[:0]
	for _, kv := range env {
		k, _, _ := strings.Cut(kv, "=")
		if !contains(o.unset, k) {
			kept = append(kept, kv)
		}
	}
	return kept
}

func contains(list []string, s string) bool {
	for _, e := range list {
		if e == s {
			return true
		}
	}
	return false
}

// pathWith is PATH with dir in front.
func pathWith(dir string) string {
	return "PATH=" + dir + string(os.PathListSeparator) + os.Getenv("PATH")
}

// fakeUname writes a uname that reports sys and machine, and returns the
// PATH variable that puts it first.
func fakeUname(t *testing.T, sys, machine string) string {
	t.Helper()
	dir := t.TempDir()
	script := fmt.Sprintf("#!/bin/sh\ncase $1 in -m) echo %s ;; *) echo %s ;; esac\n", machine, sys)
	if err := os.WriteFile(filepath.Join(dir, "uname"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return pathWith(dir)
}

// unameMachine is what uname -m prints for this architecture.
func unameMachine() string {
	if runtime.GOARCH == "amd64" {
		return "x86_64"
	}
	return runtime.GOARCH
}

// fixture is the release of one test run: the binary, its archives and
// SHA256SUMS, and the helpers that run a script against it.
type fixture struct {
	script  string
	exe     string
	bin     []byte
	archive string // the archive of this platform
	body    []byte
	sum     string
	files   map[string][]byte // archives and a good SHA256SUMS
}

func TestInstallScripts(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("builds the binary")
	}
	if runtime.GOOS == "windows" && runtime.GOARCH != "amd64" {
		t.Skip("the Windows release is amd64 only")
	}
	script, shells := installShells(t)
	if len(shells) == 0 {
		t.Skip("no shell for the install script of " + runtime.GOOS)
	}

	f := &fixture{script: script, exe: "dotai"}
	if runtime.GOOS == "windows" {
		f.exe = "dotai.exe"
	}
	binPath := filepath.Join(t.TempDir(), f.exe)
	goTool, err := exec.LookPath("go")
	if err != nil {
		t.Fatalf("the go command is not on PATH: %v", err)
	}
	build := exec.Command(goTool, "build", "-trimpath",
		"-ldflags", "-X github.com/lartweib/dotai/internal/app.version="+fakeTag, "-o", binPath, ".")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("go build: %v\n%s", err, out)
	}
	if f.bin, err = os.ReadFile(binPath); err != nil {
		t.Fatal(err)
	}
	f.archive, f.body = archiveFor(t, f.bin, runtime.GOOS, runtime.GOARCH)
	f.sum = fmt.Sprintf("%x", sha256.Sum256(f.body))
	f.files = map[string][]byte{f.archive: f.body}
	sums := fmt.Sprintf("%064x  dotai_1.2.3_other_os.tar.gz\n%s  %s\n", 0, f.sum, f.archive)
	if runtime.GOOS != "windows" {
		// The same binary as a macOS archive, for the cases that make
		// uname report Darwin on Linux.
		name, body := archiveFor(t, f.bin, "darwin", runtime.GOARCH)
		f.files[name] = body
		sums += fmt.Sprintf("%x  %s\n", sha256.Sum256(body), name)
	}
	f.files["SHA256SUMS"] = []byte(sums)

	for _, sh := range shells {
		t.Run(sh.name, func(t *testing.T) {
			testInstall(t, f, sh)
			if runtime.GOOS == "windows" {
				t.Run("swap", func(t *testing.T) { testSwap(t, f, sh) })
				t.Run("user Path", func(t *testing.T) { testUserPath(t, f, sh) })
			} else {
				t.Run("shell profile", func(t *testing.T) { testShellProfile(t, f, sh) })
			}
		})
	}
}

// good serves the fixture's release with /latest on its tag.
func (f *fixture) good(t *testing.T) *release { return newRelease(t, fakeTag, f.files) }

// install runs the script with an install directory that holds an old
// dotai, and a timeout.
func (f *fixture) install(t *testing.T, sh shell, rel *release, o opts) result {
	t.Helper()
	r := result{home: o.home, tmp: t.TempDir(), dir: filepath.Join(t.TempDir(), "bin"), key: o.key}
	if r.home == "" {
		r.home = t.TempDir()
	}
	if r.key == "" && runtime.GOOS == "windows" {
		r.key = testUserEnvKey(t)
	}
	if err := os.MkdirAll(r.dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(r.dir, f.exe), []byte("old"), 0o755); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	cmd := sh.run(ctx, f.script, o.args...)
	cmd.Env = installEnv(r, rel.server.URL, o)
	// A child that keeps the output pipe open cannot hold the test past
	// the timeout.
	cmd.WaitDelay = 5 * time.Second
	out, err := cmd.CombinedOutput()
	r.out, r.err = string(out), err
	if ctx.Err() != nil {
		t.Fatalf("the install script did not finish in time:\n%s", out)
	}
	return r
}

// assertTmpEmpty checks that the script removed its temp directory. An
// antivirus can hold a file there for a moment after the script ends, so
// it looks again for up to three seconds.
func assertTmpEmpty(t *testing.T, tmp string) {
	t.Helper()
	var left []os.DirEntry
	for deadline := time.Now().Add(3 * time.Second); ; time.Sleep(100 * time.Millisecond) {
		if left, _ = os.ReadDir(tmp); len(left) == 0 || time.Now().After(deadline) {
			break
		}
	}
	if len(left) > 0 {
		t.Errorf("the temp directory still holds %v", left)
	}
}

// assertFailed checks that the run failed with want in its output and
// left the old dotai, alone, in the install directory.
func (f *fixture) assertFailed(t *testing.T, r result, want string) {
	t.Helper()
	if r.err == nil {
		t.Fatalf("install succeeded, want a failure\n%s", r.out)
	}
	if !strings.Contains(r.out, want) {
		t.Errorf("output lacks %q:\n%s", want, r.out)
	}
	entries, _ := os.ReadDir(r.dir)
	if got, _ := os.ReadFile(filepath.Join(r.dir, f.exe)); string(got) != "old" || len(entries) != 1 {
		t.Errorf("install directory changed: %v, %s = %q", entries, f.exe, got)
	}
	assertTmpEmpty(t, r.tmp)
}

func assertOK(t *testing.T, r result, want ...string) {
	t.Helper()
	if r.err != nil {
		t.Fatalf("install: %v\n%s", r.err, r.out)
	}
	for _, w := range append(want, "dotai "+fakeTag) {
		if !strings.Contains(r.out, w) {
			t.Errorf("output lacks %q:\n%s", w, r.out)
		}
	}
}

// versionOpts names version as install.sh's argument, or for install.ps1
// in DOTAI_VERSION.
func versionOpts(version string) opts {
	if runtime.GOOS == "windows" {
		return opts{env: []string{"DOTAI_VERSION=" + version}}
	}
	return opts{args: []string{"--version", version}, env: []string{"DOTAI_VERSION=9.9.9"}}
}

// testInstall checks the download, the verification and the install,
// common to both scripts.
func testInstall(t *testing.T, f *fixture, sh shell) {
	t.Run("latest replaces the old dotai", func(t *testing.T) {
		r := f.install(t, sh, f.good(t), opts{})
		assertOK(t, r, "checksum verified", "platform: "+runtime.GOOS)
		if got, _ := os.ReadFile(filepath.Join(r.dir, f.exe)); !bytes.Equal(got, f.bin) {
			t.Errorf("%s is not the release binary (%d bytes)", f.exe, len(got))
		}
		if entries, _ := os.ReadDir(r.dir); len(entries) != 1 {
			t.Errorf("install directory holds %v, want only %s", entries, f.exe)
		}
		assertTmpEmpty(t, r.tmp)
	})
	t.Run("explicit version skips latest", func(t *testing.T) {
		o := versionOpts(fakeTag)
		if runtime.GOOS == "windows" {
			o = versionOpts("1.2.3")
		}
		assertOK(t, f.install(t, sh, newRelease(t, "fail", f.files), o))
	})
	t.Run("checksum mismatch installs nothing", func(t *testing.T) {
		bad := map[string][]byte{f.archive: f.body, "SHA256SUMS": []byte(fmt.Sprintf("%064x  %s\n", 1, f.archive))}
		f.assertFailed(t, f.install(t, sh, newRelease(t, fakeTag, bad), opts{}), "checksum mismatch")
	})
	t.Run("missing checksum line installs nothing", func(t *testing.T) {
		other := map[string][]byte{f.archive: f.body, "SHA256SUMS": []byte(f.sum + "  dotai_1.2.3_other_os.tar.gz\n")}
		f.assertFailed(t, f.install(t, sh, newRelease(t, fakeTag, other), opts{}), "has no line for "+f.archive)
	})
	t.Run("missing archive installs nothing", func(t *testing.T) {
		r := f.install(t, sh, f.good(t), versionOpts("9.9.9"))
		f.assertFailed(t, r, "release v9.9.9 may not exist")
	})
	t.Run("no release", func(t *testing.T) {
		f.assertFailed(t, f.install(t, sh, newRelease(t, "none", f.files), opts{}), "no release found at ")
	})
	t.Run("plain http refused", func(t *testing.T) {
		for _, base := range []string{
			"http://example.invalid/releases",
			"http://127.0.0.1:80@evil.example/releases",
			"http://localhost:x@host/releases",
			"http://localhost.evil.com/releases",
			"http://127.0.0.1@evil/releases",
			"http://127.0.0.1:/releases",
		} {
			r := f.install(t, sh, f.good(t), opts{env: []string{"DOTAI_RELEASE_BASE_URL=" + base}})
			f.assertFailed(t, r, "must be an https URL")
		}
	})
	t.Run("loopback http with a path accepted", func(t *testing.T) {
		rel := f.good(t)
		assertOK(t, f.install(t, sh, rel, opts{env: []string{"DOTAI_RELEASE_BASE_URL=" + rel.server.URL + "/mirror/"}}))
	})
	t.Run("relative install directory refused", func(t *testing.T) {
		r := f.install(t, sh, f.good(t), opts{env: []string{"DOTAI_INSTALL_DIR=" + filepath.Join("rel", "bin")}})
		f.assertFailed(t, r, "must be an absolute path")
	})
	t.Run("unsupported architecture", func(t *testing.T) {
		env := []string{"PROCESSOR_ARCHITECTURE=ARM64"}
		if runtime.GOOS != "windows" {
			env = []string{fakeUname(t, "Linux", "riscv64")}
		}
		f.assertFailed(t, f.install(t, sh, f.good(t), opts{env: env}), "unsupported architecture")
	})
}

// testShellProfile checks how install.sh puts the install directory on
// PATH: one marked line in the profile of $SHELL, once per directory.
func testShellProfile(t *testing.T, f *fixture, sh shell) {
	const marker = "# added by the dotai installer"
	exportLine := func(dir string) string { return "export PATH='" + dir + "':\"$PATH\"" }
	assertHomeEmpty := func(t *testing.T, r result) {
		t.Helper()
		if left, _ := os.ReadDir(r.home); len(left) > 0 {
			t.Errorf("home holds %v, want nothing written", left)
		}
	}
	// profiles lists the files a shell's run writes into an empty home.
	profiles := map[string][]string{
		"zsh":  {".zshrc"},
		"bash": {".bashrc"},
		"fish": {filepath.Join(".config", "fish", "conf.d", "dotai.fish")},
	}
	if runtime.GOOS == "darwin" {
		profiles["bash"] = append(profiles["bash"], ".bash_profile")
	}
	for _, name := range []string{"zsh", "bash", "fish"} {
		t.Run(name+" profile written once per directory", func(t *testing.T) {
			home := t.TempDir()
			const mine = "# the user's own line\n"
			first := filepath.Join(home, profiles[name][0])
			if err := os.MkdirAll(filepath.Dir(first), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(first, []byte(mine), 0o644); err != nil {
				t.Fatal(err)
			}
			o := opts{home: home, env: []string{"SHELL=/usr/bin/" + name}}
			r := f.install(t, sh, f.good(t), o)
			assertOK(t, r, "added "+r.dir+" to PATH in "+first, "open a new terminal, or run: ")
			want := exportLine(r.dir)
			if name == "fish" {
				want = "fish_add_path '" + r.dir + "'"
			}
			for _, p := range profiles[name] {
				got, _ := os.ReadFile(filepath.Join(home, p))
				if !strings.HasSuffix(string(got), "\n"+marker+"\n"+want+"\n") {
					t.Errorf("%s = %q, want it to end with the marker and %q", p, got, want)
				}
			}
			if got, _ := os.ReadFile(first); !strings.HasPrefix(string(got), mine) {
				t.Errorf("%s lost the user's content: %q", first, got)
			}
			before, _ := os.ReadFile(first)
			again := f.install(t, sh, f.good(t), opts{home: home, env: append(o.env, "DOTAI_INSTALL_DIR="+r.dir)})
			assertOK(t, again, first+" already puts "+r.dir+" on PATH")
			if after, _ := os.ReadFile(first); !bytes.Equal(after, before) {
				t.Errorf("a second run changed %s:\n%s", first, after)
			}
			// Another directory gets its own line.
			other := f.install(t, sh, f.good(t), o)
			assertOK(t, other, "added "+other.dir+" to PATH in "+first)
			if after, _ := os.ReadFile(first); !strings.Contains(string(after), r.dir) || !strings.Contains(string(after), other.dir) {
				t.Errorf("%s lacks one of the directories:\n%s", first, after)
			}
		})
	}
	t.Run("macOS bash login profile", func(t *testing.T) {
		darwin := fakeUname(t, "Darwin", unameMachine())
		// An existing ~/.profile is the login profile; no ~/.bash_profile
		// is created to hide it.
		home := t.TempDir()
		if err := os.WriteFile(filepath.Join(home, ".profile"), []byte("# mine\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		r := f.install(t, sh, f.good(t), opts{home: home, env: []string{"SHELL=/bin/bash", darwin}})
		assertOK(t, r, "added "+r.dir+" to PATH in "+filepath.Join(home, ".profile"), "run: . ~/.profile")
		if _, err := os.Stat(filepath.Join(home, ".bash_profile")); err == nil {
			t.Error("~/.bash_profile was created next to ~/.profile")
		}
		for _, p := range []string{".profile", ".bashrc"} {
			if got, _ := os.ReadFile(filepath.Join(home, p)); !strings.Contains(string(got), exportLine(r.dir)) {
				t.Errorf("~/%s = %q, want the PATH line", p, got)
			}
		}
		// With no login profile, ~/.bash_profile is created.
		empty := f.install(t, sh, f.good(t), opts{env: []string{"SHELL=/bin/bash", darwin}})
		assertOK(t, empty, "added "+empty.dir+" to PATH in "+filepath.Join(empty.home, ".bash_profile"), "run: . ~/.bash_profile")
		// When only ~/.bashrc could be written, the hint names it.
		blocked := t.TempDir()
		if err := os.Mkdir(filepath.Join(blocked, ".bash_profile"), 0o755); err != nil {
			t.Fatal(err)
		}
		half := f.install(t, sh, f.good(t), opts{home: blocked, env: []string{"SHELL=/bin/bash", darwin}})
		assertOK(t, half, ".bash_profile could not be written", "run: . ~/.bashrc")
		if strings.Contains(half.out, "run: . ~/.bash_profile") {
			t.Errorf("the hint names the profile that was not written:\n%s", half.out)
		}
	})
	t.Run("unwritable profile still installs", func(t *testing.T) {
		home := t.TempDir()
		// A directory where the profile should be cannot be appended to,
		// even by root, as in the Docker checks.
		if err := os.Mkdir(filepath.Join(home, ".zshrc"), 0o755); err != nil {
			t.Fatal(err)
		}
		r := f.install(t, sh, f.good(t), opts{home: home, env: []string{"SHELL=/bin/zsh"}})
		assertOK(t, r, "dotai is installed, but "+filepath.Join(home, ".zshrc")+" could not be written", exportLine(r.dir))
		if strings.Contains(r.out, "open a new terminal") {
			t.Errorf("output says to open a new terminal though nothing was written:\n%s", r.out)
		}
	})
	t.Run("read-only profile still installs", func(t *testing.T) {
		if os.Geteuid() == 0 {
			t.Skip("root writes a read-only file")
		}
		home := t.TempDir()
		rc := filepath.Join(home, ".zshrc")
		if err := os.WriteFile(rc, []byte("# mine\n"), 0o444); err != nil {
			t.Fatal(err)
		}
		r := f.install(t, sh, f.good(t), opts{home: home, env: []string{"SHELL=/bin/zsh"}})
		assertOK(t, r, "could not be written; add this line to it by hand")
		if got, _ := os.ReadFile(rc); string(got) != "# mine\n" {
			t.Errorf("the read-only profile changed: %q", got)
		}
	})
	t.Run("directory with a space, a quote and a dollar", func(t *testing.T) {
		dir := filepath.Join(t.TempDir(), "my bin's $HOME `x`")
		home := t.TempDir()
		r := f.install(t, sh, f.good(t), opts{home: home, env: []string{"SHELL=/bin/zsh", "DOTAI_INSTALL_DIR=" + dir}})
		assertOK(t, r, "added "+dir+" to PATH")
		// Sourcing the profile puts the directory, as it is, on PATH.
		cmd := exec.Command("sh", "-c", `. "$1" && printf '%s' "$PATH"`, "sh", filepath.Join(home, ".zshrc"))
		cmd.Env = []string{"PATH=/usr/bin:/bin", "HOME=" + home}
		out, err := cmd.Output()
		if err != nil || !strings.HasPrefix(string(out), dir+":") {
			t.Errorf("PATH after sourcing = %q (%v), want it to start with %q", out, err, dir)
		}
	})
	t.Run("already on PATH changes nothing", func(t *testing.T) {
		for _, c := range []struct{ install, path string }{{"", ""}, {"", "/"}, {"//", ""}} {
			dir := filepath.Join(t.TempDir(), "bin")
			r := f.install(t, sh, f.good(t), opts{env: []string{
				"SHELL=/bin/zsh", "DOTAI_INSTALL_DIR=" + dir + c.install, pathWith(dir + c.path)}})
			assertOK(t, r)
			if strings.Contains(r.out, "to PATH") || strings.Contains(r.out, "your PATH") {
				t.Errorf("install %q, PATH %q: output mentions PATH:\n%s", dir+c.install, dir+c.path, r.out)
			}
			assertHomeEmpty(t, r)
		}
	})
	t.Run("unknown shell prints the line", func(t *testing.T) {
		r := f.install(t, sh, f.good(t), opts{env: []string{"SHELL=/bin/tcsh"}})
		assertOK(t, r, r.dir+" is not on your PATH", exportLine(r.dir))
		assertHomeEmpty(t, r)
	})
	t.Run("no HOME", func(t *testing.T) {
		r := f.install(t, sh, f.good(t), opts{env: []string{"SHELL=/bin/zsh"}, unset: []string{"HOME"}})
		assertOK(t, r, r.dir+" is not on your PATH", exportLine(r.dir))
		assertHomeEmpty(t, r)
		r = f.install(t, sh, f.good(t), opts{unset: []string{"HOME", "DOTAI_INSTALL_DIR"}})
		if r.err == nil || !strings.Contains(r.out, "set HOME or DOTAI_INSTALL_DIR") {
			t.Errorf("install without HOME nor DOTAI_INSTALL_DIR: %v\n%s", r.err, r.out)
		}
	})
	for _, o := range []opts{
		{args: []string{"--no-modify-path"}, env: []string{"SHELL=/bin/zsh"}},
		{env: []string{"SHELL=/bin/zsh", "DOTAI_NO_MODIFY_PATH=1"}},
	} {
		t.Run("opt-out "+strings.Join(append(o.args, o.env[len(o.env)-1]), " "), func(t *testing.T) {
			r := f.install(t, sh, f.good(t), o)
			assertOK(t, r, exportLine(r.dir))
			assertHomeEmpty(t, r)
		})
	}
}

// testSwap checks that a failed update of dotai.exe keeps the old one and
// leaves no file behind, and that a leftover old one in use does not block
// the next update.
func testSwap(t *testing.T, f *fixture, sh shell) {
	t.Run("rollback after the old one moved aside", func(t *testing.T) {
		r := f.install(t, sh, f.good(t), opts{env: []string{"DOTAI_TEST_FAIL_SWAP=1"}})
		f.assertFailed(t, r, "the previous dotai.exe is kept")
	})
	t.Run("old one in use", func(t *testing.T) {
		dir := filepath.Join(t.TempDir(), "bin")
		exe := filepath.Join(dir, f.exe)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(exe, []byte("old"), 0o755); err != nil {
			t.Fatal(err)
		}
		unlock := lockFile(t, exe)
		r := f.install(t, sh, f.good(t), opts{env: []string{"DOTAI_INSTALL_DIR=" + dir}})
		unlock()
		if r.err == nil || !strings.Contains(r.out, "Close any running dotai") {
			t.Fatalf("install over a locked dotai.exe: %v\n%s", r.err, r.out)
		}
		entries, _ := os.ReadDir(dir)
		if got, _ := os.ReadFile(exe); string(got) != "old" || len(entries) != 1 {
			t.Errorf("install directory = %v, dotai.exe = %q; want only the old dotai.exe", entries, got)
		}
	})
	t.Run("a failed update keeps earlier old ones", func(t *testing.T) {
		dir := filepath.Join(t.TempDir(), "bin")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		for name, body := range map[string]string{f.exe: "old", "dotai.exe.old-earlier": "earlier"} {
			if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o755); err != nil {
				t.Fatal(err)
			}
		}
		r := f.install(t, sh, f.good(t), opts{env: []string{"DOTAI_INSTALL_DIR=" + dir, "DOTAI_TEST_FAIL_SWAP=1"}})
		if r.err == nil {
			t.Fatalf("install succeeded, want a failure\n%s", r.out)
		}
		entries, _ := os.ReadDir(dir)
		earlier, _ := os.ReadFile(filepath.Join(dir, "dotai.exe.old-earlier"))
		if exe, _ := os.ReadFile(filepath.Join(dir, f.exe)); string(exe) != "old" || string(earlier) != "earlier" || len(entries) != 2 {
			t.Errorf("install directory = %v; want the old dotai.exe and dotai.exe.old-earlier only", entries)
		}
	})
	t.Run("leftover old one in use does not block", func(t *testing.T) {
		dir := filepath.Join(t.TempDir(), "bin")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		stale := filepath.Join(dir, "dotai.exe.old-stale")
		if err := os.WriteFile(stale, []byte("stale"), 0o755); err != nil {
			t.Fatal(err)
		}
		unlock := lockFile(t, stale)
		defer unlock()
		r := f.install(t, sh, f.good(t), opts{env: []string{"DOTAI_INSTALL_DIR=" + dir}})
		assertOK(t, r, "installed "+filepath.Join(dir, f.exe))
	})
}

// testUserPath checks how install.ps1 puts the install directory on the
// user's Path: appended to the raw value, kept unexpanded and of its kind,
// never twice.
func testUserPath(t *testing.T, f *fixture, sh shell) {
	const existing = `%USERPROFILE%\tools;C:\other`
	t.Run("appended once, unexpanded entries kept", func(t *testing.T) {
		key := testUserEnvKey(t)
		setTestUserPath(t, key, existing, true)
		r := f.install(t, sh, f.good(t), opts{key: key})
		assertOK(t, r, "added "+r.dir+" to the Path of the test key", "the real user Path was not changed")
		value, expand, ok := testUserPathValue(t, key)
		if want := existing + ";" + r.dir; !ok || value != want || !expand {
			t.Errorf("user Path = %q (REG_EXPAND_SZ %v), want %q as REG_EXPAND_SZ", value, expand, want)
		}
		// A rerun into the same directory, from a session whose Path still
		// lacks it, adds nothing.
		again := f.install(t, sh, f.good(t), opts{key: key, env: []string{"DOTAI_INSTALL_DIR=" + r.dir}})
		assertOK(t, again, "is on your user Path already; added it to this session's Path")
		if value2, _, _ := testUserPathValue(t, key); value2 != value {
			t.Errorf("a second run changed the user Path to %q", value2)
		}
	})
	t.Run("kind and trailing separator kept", func(t *testing.T) {
		key := testUserEnvKey(t)
		setTestUserPath(t, key, `C:\plain;`, false)
		r := f.install(t, sh, f.good(t), opts{key: key})
		assertOK(t, r)
		if value, expand, _ := testUserPathValue(t, key); value != `C:\plain;`+r.dir || expand {
			t.Errorf("user Path = %q (REG_EXPAND_SZ %v), want REG_SZ %q", value, expand, `C:\plain;`+r.dir)
		}
	})
	t.Run("no user Path yet", func(t *testing.T) {
		key := testUserEnvKey(t)
		r := f.install(t, sh, f.good(t), opts{key: key})
		assertOK(t, r)
		if value, expand, _ := testUserPathValue(t, key); value != r.dir || !expand {
			t.Errorf("user Path = %q (REG_EXPAND_SZ %v), want %q", value, expand, r.dir)
		}
	})
	t.Run("an unexpanded entry for the directory counts", func(t *testing.T) {
		key := testUserEnvKey(t)
		dir := filepath.Join(t.TempDir(), "bin")
		setTestUserPath(t, key, `C:\other;%INSTALLTEST_DIR%\`, true)
		r := f.install(t, sh, f.good(t), opts{key: key, env: []string{"DOTAI_INSTALL_DIR=" + dir, "INSTALLTEST_DIR=" + dir}})
		assertOK(t, r, "is on your user Path already")
		if value, _, _ := testUserPathValue(t, key); value != `C:\other;%INSTALLTEST_DIR%\` {
			t.Errorf("user Path = %q, want it unchanged", value)
		}
	})
	t.Run("already on Path changes nothing", func(t *testing.T) {
		key := testUserEnvKey(t)
		dir := filepath.Join(t.TempDir(), "bin")
		r := f.install(t, sh, f.good(t), opts{key: key, env: []string{"DOTAI_INSTALL_DIR=" + dir + `\`, pathWith(dir)}})
		assertOK(t, r)
		if strings.Contains(r.out, "user Path") || strings.Contains(r.out, "your Path") {
			t.Errorf("output mentions Path:\n%s", r.out)
		}
		if value, _, ok := testUserPathValue(t, key); ok {
			t.Errorf("user Path = %q, want none written", value)
		}
	})
	t.Run("opt-out prints the command", func(t *testing.T) {
		key := testUserEnvKey(t)
		dir := filepath.Join(t.TempDir(), "it's bin")
		r := f.install(t, sh, f.good(t), opts{key: key, env: []string{"DOTAI_NO_MODIFY_PATH=1", "DOTAI_INSTALL_DIR=" + dir}})
		assertOK(t, r, dir+" is not on your Path",
			"[Environment]::SetEnvironmentVariable('Path', [Environment]::GetEnvironmentVariable('Path', 'User') + ';"+
				strings.ReplaceAll(dir, "'", "''")+"', 'User')")
		if value, _, ok := testUserPathValue(t, key); ok {
			t.Errorf("user Path = %q, want none written", value)
		}
	})
	t.Run("bare UNC prefix refused", func(t *testing.T) {
		r := f.install(t, sh, f.good(t), opts{key: testUserEnvKey(t), env: []string{`DOTAI_INSTALL_DIR=\\`}})
		f.assertFailed(t, r, "must be an absolute path")
	})
}
