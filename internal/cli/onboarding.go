package cli

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"time"

	"github.com/spf13/cobra"

	"github.com/lartweib/dotai/internal/app"
	"github.com/lartweib/dotai/internal/envelope"
)

// hookBound is how long the session-start hook may take in all before it
// gives up in silence (decision 0045), so that it never holds a session
// back: the hook's ctx ends app.HookReap before it, which kills the git it
// runs, and the command waits for the hook until the bound, so that no
// git is left behind.
const hookBound = time.Second

func newOnboardingCmd(env Env, jsonOut *bool) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "onboarding",
		Short: "The onboarding dotai installs: the session-start hook and the dismissal of its prompt",
		Args:  cobra.NoArgs,
	}
	cmd.AddCommand(newOnboardingHookCmd(env, jsonOut))
	return cmd
}

func newOnboardingHookCmd(env Env, jsonOut *bool) *cobra.Command {
	var id string
	cmd := &cobra.Command{
		Use:   "hook --harness <name>",
		Short: "Print one line for the agent when the repository is not set up or its profiles may no longer fit",
		Long: "The session-start hook that dotai install puts in each harness (decision 0045). In a git repository, for the\n" +
			"pack the install state records, it prints one line when the repository has no profiles applied and no dismissal,\n" +
			"when its files changed since a dismissal, or when the profiles applied may no longer fit; otherwise nothing. For\n" +
			"agy the line is an injectSteps JSON object. It never reads stdin, never writes, and never fails a session: on any\n" +
			"error, for a harness it does not know, or after 1 s, it prints nothing and exits 0.",
		Args: cobra.NoArgs,
	}
	cmd.Flags().StringVar(&id, "harness", "", "the harness that runs the hook: claudecode, antigravity-cli or opencode")
	_ = cmd.MarkFlagRequired("harness")
	cmd.RunE = func(cmd *cobra.Command, _ []string) error {
		if !app.HookHarness(id) {
			// Never a failure: the session goes on without the line.
			fmt.Fprintf(env.Stderr, "dotai: unknown harness %s, the session-start hook prints nothing\n", termText(strconv.Quote(id)))
			return nil
		}
		line := runHook(cmd.Context(), env)
		if *jsonOut {
			return envelope.Write(env.Stdout, envelope.Success(commandName(cmd), map[string]string{"line": line}))
		}
		_, err := fmt.Fprint(env.Stdout, app.HookOutput(id, line))
		return err
	}
	return cmd
}

// runHook is the line of env.OnboardingHook in the current directory, ""
// on any error or past hookBound. Its ctx ends app.HookReap before the
// bound, which kills the git the hook runs, and runHook waits for the
// hook to return until the bound, which it does once git is reaped.
func runHook(parent context.Context, env Env) string {
	home, herr := os.UserHomeDir()
	cwd, cerr := os.Getwd()
	if herr != nil || cerr != nil || env.OnboardingHook == nil {
		return ""
	}
	bound := time.NewTimer(hookBound)
	defer bound.Stop()
	ctx, cancel := context.WithTimeout(parent, hookBound-app.HookReap)
	defer cancel()
	done := make(chan string, 1)
	go func() {
		l, err := env.OnboardingHook(ctx, cwd, home)
		if err != nil {
			l = ""
		}
		done <- l
	}()
	select {
	case line := <-done:
		if ctx.Err() != nil {
			return "" // the answer came past the hook's bound
		}
		return line
	case <-bound.C:
		return ""
	}
}
