//go:build !windows

package app

import (
	"fmt"
	"syscall"
)

// Launch replaces dotai with p's program, which keeps dotai's process, its
// terminal, stdin, stdout, stderr, current directory and signals, with p's
// arguments and environment. Its exit code is then the process's own, so
// Launch returns only when the program cannot be started.
func Launch(p RunPlan) (int, error) {
	err := syscall.Exec(p.Program, append([]string{p.Command}, p.Args...), p.Env)
	return 0, fmt.Errorf("launching %s: %w", p.Program, err)
}
