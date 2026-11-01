package proc

import (
	"bytes"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// list reads /proc. A process that ends while it is read is left out.
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
		if p, ok := procInfo(filepath.Join("/proc", e.Name())); ok {
			p.PID = pid
			out = append(out, p)
		}
	}
	return out, nil
}

// procInfo reads the process of the /proc directory dir: its name is that
// of the executable its exe link points at, or else its comm, which the
// kernel cuts at 15 bytes; Argv0 and Args come from its command line.
func procInfo(dir string) (Process, bool) {
	var p Process
	if cmd, err := os.ReadFile(filepath.Join(dir, "cmdline")); err == nil && len(cmd) > 0 {
		args := strings.Split(strings.TrimRight(string(cmd), "\x00"), "\x00")
		p.Argv0, p.Args = baseName(args[0]), strings.Join(args, " ")
	}
	if exe, err := os.Readlink(filepath.Join(dir, "exe")); err == nil {
		p.Name = baseName(strings.TrimSuffix(exe, " (deleted)"))
	} else if comm, err := os.ReadFile(filepath.Join(dir, "comm")); err == nil {
		p.Name = string(bytes.TrimSpace(comm))
	}
	if p.Name == "" {
		p.Name = p.Argv0
	}
	return p, p.Name != ""
}
