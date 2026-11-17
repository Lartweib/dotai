//go:build !windows

package app

import (
	"os/exec"
	"syscall"
)

// prepareTree makes cmd the leader of a process group of its own, so that
// cancelling it, or its end, also ends what it started, such as the
// git-remote-https or credential helper of a fetch. started is called once
// cmd started, done once it ended.
func prepareTree(cmd *exec.Cmd) (started func(), done func()) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	kill := func() error {
		if cmd.Process == nil {
			return nil
		}
		return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
	cmd.Cancel = kill
	return func() {}, func() { _ = kill() }
}
