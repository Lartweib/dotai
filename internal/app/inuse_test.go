package app

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
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
