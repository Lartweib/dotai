//go:build !windows && !linux

package proc

import (
	"bufio"
	"bytes"
	"os/exec"
	"strconv"
	"strings"
)

// ps is ps by its absolute path, so that no other ps on PATH answers.
const ps = "/bin/ps"

// list runs ps, as on macOS and the BSDs, twice: for the command of every
// process, the executable's path, which may hold spaces, and for its
// command line.
func list() ([]Process, error) {
	comm, err := exec.Command(ps, "-axo", "pid=,comm=").Output()
	if err != nil {
		return nil, err
	}
	args, err := exec.Command(ps, "-axo", "pid=,args=").Output()
	if err != nil {
		return nil, err
	}
	return parsePS(comm, args), nil
}

// parsePS reads the lines of ps -o pid=,comm= and of ps -o pid=,args=.
func parsePS(comm, args []byte) []Process {
	lines := func(out []byte) map[int]string {
		m := map[int]string{}
		sc := bufio.NewScanner(bytes.NewReader(out))
		sc.Buffer(nil, 1<<20)
		for sc.Scan() {
			pid, rest, ok := strings.Cut(strings.TrimSpace(sc.Text()), " ")
			if n, err := strconv.Atoi(pid); ok && err == nil {
				m[n] = strings.TrimSpace(rest)
			}
		}
		return m
	}
	cmdlines := lines(args)
	var procs []Process
	sc := bufio.NewScanner(bytes.NewReader(comm))
	for sc.Scan() {
		pid, c, ok := strings.Cut(strings.TrimSpace(sc.Text()), " ")
		n, err := strconv.Atoi(pid)
		if !ok || err != nil {
			continue
		}
		p := Process{PID: n, Name: baseName(c), Args: cmdlines[n]}
		if first, _, _ := strings.Cut(p.Args, " "); first != "" {
			p.Argv0 = baseName(first)
		}
		procs = append(procs, p)
	}
	return procs
}
