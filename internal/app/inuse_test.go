package app

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"
)

// denied is the error a rename gives, as the operating system words it, for
// a path another program holds open.
func denied(t *testing.T, errno syscall.Errno) error {
	t.Helper()
	return &os.LinkError{Op: "renameat", Old: ".agents/skills", New: ".dotai/backups/b1/files/.agents/skills", Err: errno}
}

// TestInstallErrorNamesAPathInUse checks that a move refused for access, on
// Windows, says the path is probably in use by another program and how to
// find it, with the rollback sentence kept, whatever language the system
// words the refusal in: the detection is of the error code, not of its text.
func TestInstallErrorNamesAPathInUse(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("another program holding a path open refuses a move on Windows only")
	}
	for name, errno := range map[string]syscall.Errno{"access denied": 5, "sharing violation": 32} {
		t.Run(name, func(t *testing.T) {
			got := (&InstallError{Cause: denied(t, errno), Backup: "b1"}).Error()
			for _, want := range []string{
				".agents/skills", "in use by another program", "Resource Monitor", "Associated Handles",
				"everything it wrote was rolled back from the backup b1",
			} {
				if !strings.Contains(got, want) {
					t.Errorf("message = %q, want it to hold %q", got, want)
				}
			}
			// With a failed rollback the hint stays and the rollback is reported.
			failed := (&InstallError{Cause: fmt.Errorf("wipe: %w", denied(t, errno)), Rollback: errors.New("boom"), Backup: "b1"}).Error()
			if !strings.Contains(failed, "in use by another program") || !strings.Contains(failed, "the rollback failed: boom") {
				t.Errorf("message with a failed rollback = %q", failed)
			}
			if got := (&ApplyError{Cause: denied(t, errno), Backup: "b1"}).Error(); !strings.Contains(got, "in use by another program") {
				t.Errorf("apply message = %q, want the hint too", got)
			}
		})
	}
}

// TestInstallErrorSaysNothingOfUseForOtherFailures checks that no hint is
// added to a failure that is not a path held by another program: a missing
// path, a full disk, or a move that is not one.
func TestInstallErrorSaysNothingOfUseForOtherFailures(t *testing.T) {
	for name, cause := range map[string]error{
		"missing":      &os.LinkError{Op: "renameat", Old: "a", New: "b", Err: fs.ErrNotExist},
		"plain":        errors.New("disk full"),
		"not a move":   &fs.PathError{Op: "open", Path: "a", Err: fs.ErrPermission},
		"no path held": fmt.Errorf("write: %w", syscall.EINVAL),
	} {
		t.Run(name, func(t *testing.T) {
			if got := (&InstallError{Cause: cause, Backup: "b1"}).Error(); strings.Contains(got, "in use") {
				t.Errorf("message = %q, want no hint of a path in use", got)
			}
		})
	}
}

// TestInUseHintNamesThePathThatIsHeld checks, on every system, which path the
// hint names and how: the target of a write, not the temporary file renamed
// over it; and the absolute path with the separators of the system that the
// install file system gives, which is what Resource Monitor shows.
func TestInUseHintNamesThePathThatIsHeld(t *testing.T) {
	root := t.TempDir()
	temp, err := tempName("settings.json")
	if err != nil {
		t.Fatal(err)
	}
	write := &os.LinkError{Op: "renameat", Old: temp, New: "settings.json", Err: fs.ErrPermission}
	if got := inUseHint(write, true); !strings.Contains(got, "settings.json is probably in use") || strings.Contains(got, ".dotai-") {
		t.Errorf("hint for a write = %q, want it to name settings.json and not the temporary file %s", got, temp)
	}
	held := at(root, ".agents/skills", &os.LinkError{Op: "renameat", Old: ".agents/skills", New: ".dotai/backups/b1/files/.agents/skills", Err: fs.ErrPermission})
	want := filepath.Join(root, ".agents", "skills")
	if got := inUseHint(held, true); !strings.HasPrefix(got, want+" is probably in use") || !strings.HasSuffix(got, "search for "+want) {
		t.Errorf("hint for a move = %q, want the absolute path %s twice", got, want)
	}
	if got := inUseHint(held, false); got != "" {
		t.Errorf("hint outside Windows = %q, want none", got)
	}
	if got := inUseHint(at(root, "a", &os.LinkError{Op: "renameat", Old: "a", New: "b", Err: fs.ErrNotExist}), true); got != "" {
		t.Errorf("hint for a missing path = %q, want none", got)
	}
	// The messages of the three operations carry it on Windows.
	if runtime.GOOS == "windows" {
		for name, msg := range map[string]string{
			"install": (&InstallError{Cause: held, Backup: "b1"}).Error(),
			"apply":   (&ApplyError{Cause: held, Backup: "b1"}).Error(),
			"restore": (&RestoreError{Cause: held, Backup: "b1"}).Error(),
		} {
			if !strings.Contains(msg, want+" is probably in use") {
				t.Errorf("%s message = %q, want the hint", name, msg)
			}
		}
		if msg := (&RestoreError{Cause: held, Rollback: errors.New("boom"), Backup: "b1"}).Error(); !strings.Contains(msg, "probably in use") || !strings.Contains(msg, "failed: boom") {
			t.Errorf("restore message with a failed rollback = %q", msg)
		}
	}
}
