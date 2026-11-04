package cli

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/lartweib/dotai/internal/envelope"
)

func newUICmd(env Env, jsonOut *bool) *cobra.Command {
	var port int
	var noOpen bool
	cmd := &cobra.Command{
		Use:   "ui",
		Short: "Open the local web UI in the browser",
		Long: "Serve the local web UI on 127.0.0.1, on a free port unless --port names one, print its address and open it\n" +
			"in the browser unless --no-open. The address carries a one-time code, good for two minutes, that the page trades\n" +
			"for the session token of this run; open it once, and restart dotai ui for a new one. Every request must name the\n" +
			"server by 127.0.0.1 or localhost and come from its own page. The UI shows the state, the backups, the plans and\n" +
			"doctor; it writes nothing in the home directory or a project. A plan, as with --dry-run, fetches rulesync into\n" +
			"the cache, stages into a temporary directory and runs git in the project. It runs until Ctrl+C. With --json it\n" +
			"prints one envelope, whose data has the url, once it listens.",
		// Checked with the arguments, so a port out of range is a usage error.
		Args: func(cmd *cobra.Command, args []string) error {
			if port < 0 || port > 65535 {
				return fmt.Errorf("--port %d is not between 0 and 65535", port)
			}
			return cobra.NoArgs(cmd, args)
		},
	}
	cmd.Flags().IntVar(&port, "port", 0, "port to listen on, on 127.0.0.1 (default: a free one)")
	cmd.Flags().BoolVar(&noOpen, "no-open", false, "print the address without opening the browser")
	cmd.RunE = func(cmd *cobra.Command, _ []string) error {
		home, err := os.UserHomeDir()
		if err != nil {
			return fmt.Errorf("the home directory is unknown: %w", err)
		}
		cache, err := cacheDir()
		if err != nil {
			return err
		}
		ctx, stop := interruptible(cmd)
		defer stop()
		printed := false // the envelope of the running server
		ready := func(url string) error {
			if *jsonOut {
				printed = true
				if err := envelope.Write(env.Stdout, envelope.Success(commandName(cmd), map[string]string{"url": url})); err != nil {
					return err
				}
			} else if _, err := fmt.Fprintf(env.Stdout, "dotai ui is running at %s\nPress Ctrl+C to stop it.\n", url); err != nil {
				return err
			}
			return nil
		}
		opts := UIOptions{Home: home, CacheDir: cache, Port: port, Ready: ready}
		if !noOpen && env.OpenBrowser != nil {
			// The browser gets a page that holds the code, never the code
			// on its command line, which other users may read.
			opts.Open = func(page string) error {
				if err := env.OpenBrowser(page); err != nil {
					fmt.Fprintf(env.Stderr, "the browser could not be opened (%v): open the address above\n", err)
				}
				return nil
			}
		}
		err = env.UI(ctx, opts)
		if err != nil && printed {
			// stdout already holds the one envelope of this run.
			fmt.Fprintf(env.Stderr, "Error: %v\n", err)
			return &exitCodeError{code: ExitInternal}
		}
		return err
	}
	return cmd
}
