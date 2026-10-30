package cli

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/lartweib/dotai/internal/app"
	"github.com/lartweib/dotai/internal/harness"
)

// exitCodeError ends dotai with the exit code of the harness it ran, and
// prints nothing: the harness already spoke.
type exitCodeError struct{ code int }

func (e *exitCodeError) Error() string { return fmt.Sprintf("the harness exited with code %d", e.code) }

// newRunCmd is dotai run. Its flags end at the harness, so everything after
// it, flags and --json included, is the harness's; a -- right after the
// harness is dropped. parsed is set once the flags were parsed, since a
// --json after the harness must not turn an error into an envelope.
func newRunCmd(env Env, jsonOut, parsed *bool) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "run <harness> [-- args...]",
		Short: "Launch a harness against its isolated install",
		Long: "Launch the harness against the isolated install of dotai install, with the variables or flags that point it\n" +
			"there (for claudecode, CLAUDE_CONFIG_DIR=~/.dotai/isolated/claude; for antigravity-cli, a first\n" +
			"--gemini_dir=~/.dotai/isolated/gemini), the terminal, the current directory and the rest of the environment,\n" +
			"and exit with its exit code. Everything after the harness goes to it untouched; a -- right after the harness\n" +
			"is dropped. Before launching, the isolation level, the login and what the user's own setup still provides are\n" +
			"printed to stderr. claudecode and antigravity-cli can be launched so far. --json is not supported: the\n" +
			"harness takes over the terminal.",
		Args: func(cmd *cobra.Command, args []string) error {
			*parsed = true
			if *jsonOut {
				return errors.New("run does not support --json: the harness takes over the terminal")
			}
			return cobra.MinimumNArgs(1)(cmd, args)
		},
	}
	cmd.Flags().SetInterspersed(false)
	cmd.RunE = func(_ *cobra.Command, args []string) error {
		home, err := os.UserHomeDir()
		if err != nil {
			return fmt.Errorf("the home directory is unknown: %w", err)
		}
		rest := args[1:]
		if len(rest) > 0 && rest[0] == "--" {
			rest = rest[1:]
		}
		p, err := env.PlanRun(home, args[0], append([]string{}, rest...))
		if err != nil {
			return err
		}
		if err := writeLaunch(env.Stderr, p); err != nil {
			return err
		}
		code, err := env.Launch(p)
		if err != nil {
			return err
		}
		if code != ExitOK {
			return &exitCodeError{code: code}
		}
		return nil
	}
	return cmd
}

// writeLaunch prints, before the harness starts, what decision 0018
// requires to show of its isolation: its level and pointers, the login
// and each leak, then each note about the launch, one line each.
func writeLaunch(w io.Writer, p app.RunPlan) error {
	h := p.Harness
	var b strings.Builder
	pointers := make([]string, 0, len(h.Pointers))
	for _, ptr := range h.Pointers {
		pointers = append(pointers, ptr.Name+"="+ptr.Dir)
	}
	fmt.Fprintf(&b, "dotai: %s · %s isolation · %s\n", h.ID, h.Level, strings.Join(pointers, ", "))
	switch h.Relogin {
	case string(harness.ReloginYes):
		b.WriteString("dotai: login: you must log in again in the isolated configuration\n")
	case string(harness.ReloginNo):
		b.WriteString("dotai: login: keeps your login\n")
	default:
		b.WriteString("dotai: login: you may need to log in again in the isolated configuration (not verified on this OS)\n")
	}
	if len(h.Leaks) == 0 {
		b.WriteString("dotai: leaks: nothing known\n")
	}
	for _, leak := range h.Leaks {
		fmt.Fprintf(&b, "dotai: leaks: %s\n", leak)
	}
	for _, note := range p.Notes {
		fmt.Fprintf(&b, "dotai: note: %s\n", note)
	}
	_, err := io.WriteString(w, b.String())
	return err
}
