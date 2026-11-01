//go:build !windows && !linux

package proc

import (
	"bufio"
	"bytes"
	"os/exec"
	"strconv"
	"strings"
)

// list runs ps, as on macOS and the BSDs, for the id and the command of
// every process; the command is the executable's path, which may hold
// spaces.
func list() ([]Process, error) {
	out, err := exec.Command("ps", "-axo", "pid=,comm=").Output()
	if err != nil {
		return nil, err
	}
	return parsePS(out), nil
}

// parsePS reads the lines of ps -o pid=,comm=.
func parsePS(out []byte) []Process {
	var procs []Process
	sc := bufio.NewScanner(bytes.NewReader(out))
	for sc.Scan() {
		pid, comm, ok := strings.Cut(strings.TrimSpace(sc.Text()), " ")
		n, err := strconv.Atoi(pid)
		if !ok || err != nil {
			continue
		}
		procs = append(procs, Process{PID: n, Name: baseName(comm)})
	}
	return procs
}
