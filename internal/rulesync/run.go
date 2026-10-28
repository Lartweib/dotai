package rulesync

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
)

// Exec is the process port: it runs name with args and env in dir and
// returns what it wrote to stdout and stderr.
type Exec func(ctx context.Context, name string, args, env []string, dir string) (stdout, stderr []byte, err error)

// OSExec is the Exec of the operating system.
func OSExec(ctx context.Context, name string, args, env []string, dir string) ([]byte, []byte, error) {
	var stdout, stderr bytes.Buffer
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Env, cmd.Dir, cmd.Stdout, cmd.Stderr = env, dir, &stdout, &stderr
	err := cmd.Run()
	return stdout.Bytes(), stderr.Bytes(), err
}

// stderrLines is how many of the last stderr lines a failure reports.
const stderrLines = 8

// Args are the arguments that make rulesync generate every feature of
// targets from the .rulesync/ in its working directory. global selects
// the user scope, written under the home directory.
func Args(global bool, targets []string) []string {
	args := []string{"generate", "--targets", strings.Join(targets, ","), "--features", "*"}
	if global {
		args = append(args, "--global")
	}
	return args
}

// Env is environ with home as the home and config directories, so that
// rulesync cannot write to the real ones: with --global it writes under
// the home directory (USERPROFILE on Windows, HOME elsewhere).
func Env(environ []string, home string) []string {
	set := [][2]string{
		{"HOME", home},
		{"USERPROFILE", home},
		{"XDG_CONFIG_HOME", filepath.Join(home, ".config")},
		{"APPDATA", filepath.Join(home, "AppData", "Roaming")},
		{"LOCALAPPDATA", filepath.Join(home, "AppData", "Local")},
	}
	env := slices.DeleteFunc(slices.Clone(environ), func(kv string) bool {
		k, _, _ := strings.Cut(kv, "=")
		return slices.ContainsFunc(set, func(s [2]string) bool { return strings.EqualFold(k, s[0]) })
	})
	for _, s := range set {
		env = append(env, s[0]+"="+s[1])
	}
	return env
}

// Run verifies bin against the checksum table again, since the cache is
// writable by the user (0029), then runs it in dir with dir as its home,
// rendering the .rulesync/ there for targets. A failure reports the end of
// rulesync's stderr.
func Run(ctx context.Context, run Exec, bin Result, dir string, global bool, targets []string) error {
	want, ok := checksum(bin.Version, bin.Asset)
	if !ok {
		return fmt.Errorf("no checksum for rulesync %s asset %s", bin.Version, bin.Asset)
	}
	if got, err := hashFile(bin.Path); err != nil {
		return err
	} else if got != want {
		return fmt.Errorf("checksum mismatch for %s: got %s, want %s; run dotai rulesync fetch again", bin.Path, got, want)
	}
	_, stderr, err := run(ctx, bin.Path, Args(global, targets), Env(os.Environ(), dir), dir)
	if err != nil {
		lines := strings.Split(strings.TrimSpace(string(stderr)), "\n")
		return fmt.Errorf("rulesync generate failed: %w: %s", err, strings.Join(lines[max(0, len(lines)-stderrLines):], "\n"))
	}
	return nil
}

// checksum is the SHA-256 the table holds for asset of version.
func checksum(version, asset string) (string, bool) {
	for _, a := range releases[version] {
		if a.Name == asset {
			return a.SHA256, true
		}
	}
	return "", false
}
