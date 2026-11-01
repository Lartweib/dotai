package proc

import (
	"bytes"
	"os"
	"strconv"
	"strings"
)

// list reads /proc: the name of each process is that of the executable
// its exe link points at, or else of the first word of its command line,
// or else its comm, which the kernel cuts at 15 bytes. A process that ends
// while it is read is left out.
func list() ([]Process, error) {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return nil, err
	}
	var out []Process
	for _, e := range entries {
		pid, err := strconv.Atoi(e.Name())
		if err != nil || pid <= 0 {
			continue
		}
		if name := procName("/proc/" + e.Name()); name != "" {
			out = append(out, Process{PID: pid, Name: name})
		}
	}
	return out, nil
}

func procName(dir string) string {
	if exe, err := os.Readlink(dir + "/exe"); err == nil {
		return baseName(strings.TrimSuffix(exe, " (deleted)"))
	}
	if cmd, err := os.ReadFile(dir + "/cmdline"); err == nil && len(cmd) > 0 {
		if arg0, _, _ := bytes.Cut(cmd, []byte{0}); len(arg0) > 0 {
			return baseName(string(arg0))
		}
	}
	if comm, err := os.ReadFile(dir + "/comm"); err == nil {
		return strings.TrimSpace(string(comm))
	}
	return ""
}
