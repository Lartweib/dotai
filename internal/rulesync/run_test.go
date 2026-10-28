package rulesync_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/lartweib/dotai/internal/rulesync"
)

// verified returns a binary in a temp directory that matches the 9.9.9
// checksum table of the running platform.
func verified(t *testing.T) rulesync.Result {
	t.Helper()
	rulesync.SetReleases(t, map[string]map[string]rulesync.Asset{
		"9.9.9": {runtime.GOOS + "/" + runtime.GOARCH: {Name: asset, SHA256: sum(binary)}},
	})
	path := filepath.Join(t.TempDir(), asset)
	if err := os.WriteFile(path, binary, 0o755); err != nil {
		t.Fatal(err)
	}
	return rulesync.Result{Version: "9.9.9", Asset: asset, Path: path, SHA256: sum(binary)}
}

// call is one recorded call of the exec fake.
type call struct {
	name, dir string
	args, env []string
}

// recorder is an exec fake that records its calls and answers with
// stdout, stderr and err.
func recorder(calls *[]call, stdout, stderr string, err error) rulesync.Exec {
	return func(_ context.Context, name string, args, env []string, dir string) ([]byte, []byte, error) {
		*calls = append(*calls, call{name, dir, args, env})
		return []byte(stdout), []byte(stderr), err
	}
}

func TestRun(t *testing.T) {
	bin := verified(t)
	t.Chdir(t.TempDir())
	t.Setenv("PATH", "/bin")
	t.Setenv("LC_ALL", "C")
	t.Setenv("GITHUB_TOKEN", "secret")
	t.Setenv("HOME", "/real")
	user, err := filepath.Abs("user")
	if err != nil {
		t.Fatal(err)
	}
	project := filepath.Join(filepath.Dir(user), "project")
	var calls []call
	err = rulesync.Run(context.Background(), recorder(&calls, "", "", nil), bin, []string{"claudecode", "opencode"},
		rulesync.Job{Dir: "user", Global: true}, rulesync.Job{Dir: project})
	if err != nil || len(calls) != 2 {
		t.Fatalf("Run() = %v after %d calls, want nil after 2", err, len(calls))
	}
	want := []call{
		{bin.Path, user, []string{"generate", "--targets", "claudecode,opencode", "--features", "*", "--global"}, nil},
		{bin.Path, project, []string{"generate", "--targets", "claudecode,opencode", "--features", "*"}, nil},
	}
	for i, home := range []string{user, project} {
		tmp := filepath.Join(home, ".dotai-tmp")
		want[i].env = []string{"PATH=/bin", "LC_ALL=C",
			"HOME=" + home,
			"USERPROFILE=" + home,
			"XDG_CONFIG_HOME=" + filepath.Join(home, ".config"),
			"XDG_DATA_HOME=" + filepath.Join(home, ".local", "share"),
			"XDG_CACHE_HOME=" + filepath.Join(home, ".cache"),
			"XDG_STATE_HOME=" + filepath.Join(home, ".local", "state"),
			"APPDATA=" + filepath.Join(home, "AppData", "Roaming"),
			"LOCALAPPDATA=" + filepath.Join(home, "AppData", "Local"),
			"TMP=" + tmp, "TEMP=" + tmp, "TMPDIR=" + tmp,
		}
		if vol := filepath.VolumeName(home); vol != "" {
			want[i].env = append(want[i].env, "HOMEDRIVE="+vol, "HOMEPATH="+home[len(vol):])
		}
		calls[i].env = slicesWithout(calls[i].env, "PATHEXT=", "SystemRoot=", "windir=", "ComSpec=", "SystemDrive=", "TZ=", "LANG=")
		slices.Sort(calls[i].env)
		slices.Sort(want[i].env)
		if _, err := os.Stat(tmp); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("temporary directory %s left behind: %v", tmp, err)
		}
	}
	if !reflect.DeepEqual(calls, want) {
		t.Errorf("calls = %q\nwant %q", calls, want)
	}
}

// slicesWithout drops the variables the host may set on its own, which
// the allowlist passes on.
func slicesWithout(env []string, prefixes ...string) []string {
	var out []string
	for _, kv := range env {
		keep := true
		for _, p := range prefixes {
			if strings.HasPrefix(strings.ToUpper(kv), strings.ToUpper(p)) {
				keep = false
			}
		}
		if keep {
			out = append(out, kv)
		}
	}
	return out
}

func TestRunRefusesABinaryThatIsNotVerified(t *testing.T) {
	for name, change := range map[string]func(path string) error{
		"modified": func(path string) error { return os.WriteFile(path, []byte("tampered"), 0o755) },
		"missing":  os.Remove,
	} {
		t.Run(name, func(t *testing.T) {
			bin := verified(t)
			if err := change(bin.Path); err != nil {
				t.Fatal(err)
			}
			var calls []call
			err := rulesync.Run(context.Background(), recorder(&calls, "", "", nil), bin, []string{"opencode"}, rulesync.Job{Dir: t.TempDir()})
			if err == nil || !strings.Contains(err.Error(), "run dotai rulesync fetch") || len(calls) != 0 {
				t.Errorf("Run() = %v after %d calls, want a refusal that names rulesync fetch", err, len(calls))
			}
		})
	}
}

func TestRunReportsTheEndOfTheOutput(t *testing.T) {
	long := strings.Repeat("warning\n", 20)
	for _, tt := range []struct{ stdout, stderr, last string }{
		{"Written 1 rule\n", long + "Failed to parse frontmatter in rules/a.md\n", "Failed to parse frontmatter in rules/a.md"},
		{long + "Error: out of memory\n", "", "Error: out of memory"},
	} {
		bin := verified(t)
		var calls []call
		err := rulesync.Run(context.Background(), recorder(&calls, tt.stdout, tt.stderr, errors.New("exit status 1")), bin, []string{"opencode"},
			rulesync.Job{Dir: t.TempDir(), Global: true})
		if err == nil || !strings.Contains(err.Error(), "user scope") || !strings.Contains(err.Error(), "exit status 1") || !strings.HasSuffix(err.Error(), tt.last) {
			t.Fatalf("Run() = %v, want the scope, the exit status and %q", err, tt.last)
		}
		if n := strings.Count(err.Error(), "warning"); n > 9 {
			t.Errorf("error has %d warning lines, want only the tail of the output", n)
		}
	}
}

func TestRunCancelledOrTimedOut(t *testing.T) {
	block := func(ctx context.Context, _ string, _, _ []string, _ string) ([]byte, []byte, error) {
		<-ctx.Done()
		return nil, nil, errors.New("signal: killed")
	}
	t.Run("cancelled", func(t *testing.T) {
		bin := verified(t)
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		err := rulesync.Run(ctx, block, bin, []string{"opencode"}, rulesync.Job{Dir: t.TempDir()})
		if !errors.Is(err, context.Canceled) || !strings.Contains(err.Error(), "cancelled") {
			t.Errorf("Run() = %v, want a cancellation", err)
		}
	})
	t.Run("timed out", func(t *testing.T) {
		bin := verified(t)
		rulesync.SetGenerateTimeout(t, time.Millisecond)
		err := rulesync.Run(context.Background(), block, bin, []string{"opencode"}, rulesync.Job{Dir: t.TempDir()})
		if !errors.Is(err, context.DeadlineExceeded) || !strings.Contains(err.Error(), "timed out") {
			t.Errorf("Run() = %v, want a timeout", err)
		}
	})
}

func TestTailBufferKeepsTheEnd(t *testing.T) {
	b := rulesync.NewTailBuffer(4)
	for _, s := range []string{"ab", "cdef", "g"} {
		if n, err := b.Write([]byte(s)); n != len(s) || err != nil {
			t.Fatalf("Write(%q) = %d, %v", s, n, err)
		}
	}
	if got := string(b.Bytes()); got != "defg" {
		t.Errorf("Bytes() = %q, want %q", got, "defg")
	}
}
