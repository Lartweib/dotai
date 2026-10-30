package app

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
)

// Launch runs p's program as a child with the terminal's stdin, stdout and
// stderr, the current directory and p's arguments and environment, waits
// for it and gives its exit code. Ctrl-C reaches every process on the
// console, so the harness gets it; dotai handles it meanwhile and keeps
// waiting, so that it never exits before the harness does.
func Launch(p RunPlan) (int, error) {
	cmd := exec.Command(p.Program, p.Args...)
	cmd.Env = p.Env
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	// Notify, not Ignore: on Windows an ignored Ctrl-C falls through to the
	// default handler, which ends the process.
	interrupts := make(chan os.Signal, 1)
	signal.Notify(interrupts, os.Interrupt)
	defer signal.Stop(interrupts)
	err := cmd.Run()
	if exit, ok := errors.AsType[*exec.ExitError](err); ok {
		return exit.ExitCode(), nil
	}
	if err != nil {
		return 0, fmt.Errorf("launching %s: %w", p.Program, err)
	}
	return 0, nil
}
