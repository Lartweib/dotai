package app

import (
	"os/exec"
	"unsafe"

	"golang.org/x/sys/windows"
)

// prepareTree puts cmd, once started, in a job object that ends with it:
// cancelling cmd terminates the job, and its end closes the job, which
// ends what cmd started, such as the git-remote-https or credential helper
// of a fetch, which Windows does not end with their parent. A process cmd
// starts before started assigns it to the job, in the instant after it
// starts, escapes it; git starts its helpers later. Without a job, as when
// the OS refuses one, cmd runs as before.
func prepareTree(cmd *exec.Cmd) (started func(), done func()) {
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return func() {}, func() {}
	}
	info := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{}
	info.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
	if _, err := windows.SetInformationJobObject(job, windows.JobObjectExtendedLimitInformation,
		uintptr(unsafe.Pointer(&info)), uint32(unsafe.Sizeof(info))); err != nil {
		_ = windows.CloseHandle(job)
		return func() {}, func() {}
	}
	cmd.Cancel = func() error {
		if err := windows.TerminateJobObject(job, 1); err != nil && cmd.Process != nil {
			return cmd.Process.Kill()
		}
		return nil
	}
	started = func() {
		h, err := windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE, false, uint32(cmd.Process.Pid))
		if err != nil {
			return
		}
		_ = windows.AssignProcessToJobObject(job, h)
		_ = windows.CloseHandle(h)
	}
	return started, func() { _ = windows.CloseHandle(job) }
}
