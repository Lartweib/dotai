// Package cli wires the dotai commands. It only parses flags, calls the use
// cases in internal/app and prints their results as text or as the JSON
// envelope of internal/cli/output.
package cli

import (
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/lartweib/dotai/internal/app"
	"github.com/lartweib/dotai/internal/cli/output"
)

// Exit codes.
const (
	ExitOK       = 0
	ExitInternal = 1 // the command failed while running
	ExitUsage    = 2 // invalid command, argument or flag
)

// Env holds the streams and use cases a run depends on.
type Env struct {
	Stdout, Stderr io.Writer
	Version        func() app.VersionInfo
}

// Run executes dotai with args (without the program name) and returns the
// process exit code.
func Run(args []string, env Env) int {
	var jsonOut, started bool

	root := &cobra.Command{
		Use:               "dotai",
		Short:             "Cross-harness configuration engine for AI coding agents",
		SilenceErrors:     true,
		SilenceUsage:      true,
		CompletionOptions: cobra.CompletionOptions{DisableDefaultCmd: true},
		// Runs only after flags and arguments were accepted, so any error
		// returned before it is a usage error.
		PersistentPreRun: func(*cobra.Command, []string) { started = true },
	}
	root.PersistentFlags().BoolVar(&jsonOut, "json", false, "print the result as a JSON envelope")
	root.SetArgs(args)
	root.SetOut(env.Stdout)
	root.SetErr(env.Stderr)
	root.AddCommand(newVersionCmd(env, &jsonOut))

	cmd, err := root.ExecuteC()
	if err == nil {
		return ExitOK
	}

	code, exit := output.CodeInternal, ExitInternal
	if !started {
		code, exit = output.CodeUsage, ExitUsage
	}
	// Flag parsing may stop before --json is read, so look at the raw args.
	if jsonOut || jsonRequested(args) {
		_ = output.Write(env.Stdout, output.Failure(commandName(cmd), code, err.Error()))
		return exit
	}
	fmt.Fprintf(env.Stderr, "Error: %v\n", err)
	if exit == ExitUsage {
		fmt.Fprintln(env.Stderr, "Run 'dotai --help' for usage.")
	}
	return exit
}

func newVersionCmd(env Env, jsonOut *bool) *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print the dotai version",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			v := env.Version()
			if *jsonOut {
				return output.Write(env.Stdout, output.Success(commandName(cmd), v))
			}
			_, err := fmt.Fprintf(env.Stdout,
				"dotai %s\ncommit:   %s\nbuilt:    %s\ngo:       %s\nplatform: %s/%s\n",
				v.Version, orUnknown(v.Commit), orUnknown(v.BuildDate), v.GoVersion, v.OS, v.Arch)
			return err
		},
	}
}

// commandName is the command path without the program name; the root
// command is reported as "dotai".
func commandName(cmd *cobra.Command) string {
	if cmd == nil || !cmd.HasParent() {
		return "dotai"
	}
	return strings.TrimPrefix(cmd.CommandPath(), cmd.Root().Name()+" ")
}

func jsonRequested(args []string) bool {
	on := false
	for _, a := range args {
		switch {
		case a == "--":
			return on
		case a == "--json":
			on = true
		case strings.HasPrefix(a, "--json="):
			on, _ = strconv.ParseBool(strings.TrimPrefix(a, "--json="))
		}
	}
	return on
}

func orUnknown(s string) string {
	if s == "" {
		return "unknown"
	}
	return s
}
