package rulesync

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
	"time"
)

// Exec is the process port: it runs name with args and env in dir and
// returns the end of what it wrote to stdout and stderr.
type Exec func(ctx context.Context, name string, args, env []string, dir string) (stdout, stderr []byte, err error)

const (
	outputLimit = 64 << 10 // bytes of stdout and stderr kept by OSExec
	waitDelay   = 5 * time.Second
	errorLines  = 8            // output lines a failure reports
	tmpDir      = ".dotai-tmp" // the temporary directory of a run, inside its home
)

// generateTimeout bounds one rulesync generate.
var generateTimeout = 5 * time.Minute

// OSExec is the Exec of the operating system. It keeps the last 64 KiB of
// each stream and waits at most 5 seconds for them once the process ends
// or ctx kills it.
func OSExec(ctx context.Context, name string, args, env []string, dir string) ([]byte, []byte, error) {
	stdout, stderr := &tailBuffer{max: outputLimit}, &tailBuffer{max: outputLimit}
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Env, cmd.Dir, cmd.Stdout, cmd.Stderr, cmd.WaitDelay = env, dir, stdout, stderr, waitDelay
	err := cmd.Run()
	return stdout.Bytes(), stderr.Bytes(), err
}

// Job is one rulesync generate over the .rulesync/ in Dir, which is also
// its home directory. Global selects the user scope, written under home.
type Job struct {
	Dir    string
	Global bool
}

// Run verifies bin against the checksum table again, since the cache is
// writable by the user (0029), then runs each job for targets. A failure
// reports the end of rulesync's stderr, or of its stdout when stderr is
// empty.
func Run(ctx context.Context, run Exec, bin Result, targets []string, jobs ...Job) error {
	a, err := Resolve(bin.Version, runtime.GOOS, runtime.GOARCH)
	if err != nil {
		return err
	}
	got, err := hashFile(bin.Path)
	if err == nil && got != a.SHA256 {
		err = fmt.Errorf("checksum mismatch: got %s, want %s", got, a.SHA256)
	}
	if err != nil {
		return fmt.Errorf("rulesync binary %s cannot be used, run dotai rulesync fetch: %w", bin.Path, err)
	}
	for _, j := range jobs {
		scope := "project"
		if j.Global {
			scope = "user"
		}
		if err := generate(ctx, run, bin.Path, targets, j); err != nil {
			return fmt.Errorf("rendering the %s scope: %w", scope, err)
		}
	}
	return nil
}

func generate(ctx context.Context, run Exec, bin string, targets []string, j Job) error {
	home, err := filepath.Abs(j.Dir)
	if err != nil {
		return err
	}
	tmp := filepath.Join(home, tmpDir)
	if err := os.MkdirAll(tmp, 0o755); err != nil {
		return err
	}
	defer os.RemoveAll(tmp)
	ctx, cancel := context.WithTimeout(ctx, generateTimeout)
	defer cancel()
	stdout, stderr, err := run(ctx, bin, generateArgs(j.Global, targets), isolatedEnv(os.Environ(), home, tmp), home)
	switch {
	case errors.Is(ctx.Err(), context.DeadlineExceeded):
		return fmt.Errorf("rulesync generate timed out after %s: %w", generateTimeout, ctx.Err())
	case ctx.Err() != nil:
		return fmt.Errorf("rulesync generate cancelled: %w", ctx.Err())
	case err != nil:
		out := strings.TrimSpace(string(stderr))
		if out == "" {
			out = strings.TrimSpace(string(stdout))
		}
		lines := strings.Split(out, "\n")
		return fmt.Errorf("rulesync generate failed: %w: %s", err, strings.Join(lines[max(0, len(lines)-errorLines):], "\n"))
	}
	return nil
}

// generateArgs make rulesync generate every feature of targets from the
// .rulesync/ in its working directory.
func generateArgs(global bool, targets []string) []string {
	args := []string{"generate", "--targets", strings.Join(targets, ","), "--features", "*"}
	if global {
		args = append(args, "--global")
	}
	return args
}

// inherited are the variables a child process keeps from environ: what
// finding programs, the system, the time zone and the locale need.
var inherited = []string{"PATH", "PATHEXT", "SYSTEMROOT", "WINDIR", "COMSPEC", "SYSTEMDRIVE", "TZ", "LANG"}

// isolatedEnv keeps the inherited variables and LC_* of environ, and sets
// the home, config, data and temporary directories inside home, so that
// rulesync cannot read or write the real ones: with --global it writes
// under the home directory (USERPROFILE on Windows, HOME elsewhere).
func isolatedEnv(environ []string, home, tmp string) []string {
	var env []string
	for _, kv := range environ {
		k, _, _ := strings.Cut(kv, "=")
		k = strings.ToUpper(k)
		if strings.HasPrefix(k, "LC_") || slices.Contains(inherited, k) {
			env = append(env, kv)
		}
	}
	env = append(env,
		"HOME="+home,
		"USERPROFILE="+home,
		"XDG_CONFIG_HOME="+filepath.Join(home, ".config"),
		"XDG_DATA_HOME="+filepath.Join(home, ".local", "share"),
		"XDG_CACHE_HOME="+filepath.Join(home, ".cache"),
		"XDG_STATE_HOME="+filepath.Join(home, ".local", "state"),
		"APPDATA="+filepath.Join(home, "AppData", "Roaming"),
		"LOCALAPPDATA="+filepath.Join(home, "AppData", "Local"),
		"TMP="+tmp, "TEMP="+tmp, "TMPDIR="+tmp,
	)
	if vol := filepath.VolumeName(home); vol != "" {
		env = append(env, "HOMEDRIVE="+vol, "HOMEPATH="+home[len(vol):])
	}
	return env
}

// tailBuffer is a writer that keeps the last max bytes written to it.
type tailBuffer struct {
	buf []byte
	max int
}

func (t *tailBuffer) Write(p []byte) (int, error) {
	t.buf = append(t.buf, p...)
	if over := len(t.buf) - t.max; over > 0 {
		t.buf = t.buf[:copy(t.buf, t.buf[over:])]
	}
	return len(p), nil
}

func (t *tailBuffer) Bytes() []byte { return t.buf }
