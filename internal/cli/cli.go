// Package cli wires the dotai commands. It only parses flags, calls the use
// cases in internal/app and prints their results as text or as the JSON
// envelope of internal/cli/output.
package cli

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
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
	ExitInvalid  = 3 // the input was read and is not valid
)

// Env holds the streams and use cases a run depends on.
type Env struct {
	Stdout, Stderr io.Writer
	Version        func() app.VersionInfo
	Validate       func(dir string, sel app.Selection) (app.ValidateResult, error)
	Stage          func(ctx context.Context, cacheDir, dir, out string, sel app.Selection, tools app.ToolPaths, force bool, progress app.Progress) (app.StageResult, error)
	FetchRulesync  func(ctx context.Context, cacheDir, dir string, progress app.Progress) (app.RulesyncResult, error)
	PlanInstall    func(ctx context.Context, cacheDir, dir, home string, opts app.InstallOptions, progress app.Progress) (app.InstallPlan, error)
	Install        func(ctx context.Context, cacheDir, dir, home string, opts app.InstallOptions, progress app.Progress, warn app.Warn, consent app.Consent) (app.InstallResult, error)
	Restore        func(ctx context.Context, home, id string, opts app.RestoreOptions, warn app.Warn, overwrite func(app.RestoreResult) (bool, error)) (app.RestoreResult, error)
	// PlanProject is dotai apply --dry-run: the plan of applying profiles
	// to the project directory.
	PlanProject func(ctx context.Context, cacheDir, dir, project, home string, opts app.ApplyOptions, progress app.Progress) (app.ProjectPlan, error)
	// PlanRun and Launch are dotai run: Launch gives the harness's exit
	// code, and on Unix replaces dotai with the harness.
	PlanRun func(home, id string, args []string) (app.RunPlan, error)
	Launch  func(app.RunPlan) (int, error)
	// Prompt asks the user a yes or no question; nil when no one can
	// answer, such as when stdin is not a terminal.
	Prompt func(ctx context.Context, question string) (bool, error)
}

// invalidError is a command outcome that rejects its input, such as a pack
// with errors. data is the result that explains why.
type invalidError struct {
	msg  string
	data any
}

func (e *invalidError) Error() string { return e.msg }

// Run executes dotai with args (without the program name) and returns the
// process exit code.
func Run(args []string, env Env) int {
	// parsed is set by a command that parsed its flags before started is,
	// so that the parsed --json flag decides on an error.
	var jsonOut, started, parsed bool

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
	root.AddCommand(newVersionCmd(env, &jsonOut), newValidateCmd(env, &jsonOut), newStageCmd(env, &jsonOut), newRulesyncCmd(env, &jsonOut), newInstallCmd(env, &jsonOut), newApplyCmd(env, &jsonOut), newRestoreCmd(env, &jsonOut), newRunCmd(env, &jsonOut, &parsed))

	// In JSON mode help is a result like any other: one envelope whose data
	// holds the help text.
	textHelp := root.HelpFunc()
	root.SetHelpFunc(func(cmd *cobra.Command, a []string) {
		if !jsonOut {
			textHelp(cmd, a)
			return
		}
		var buf strings.Builder
		root.SetOut(&buf)
		textHelp(cmd, a)
		root.SetOut(env.Stdout)
		_ = output.Write(env.Stdout, output.Success(commandName(cmd), map[string]string{"help": buf.String()}))
	})

	cmd, err := root.ExecuteC()
	if err == nil {
		return ExitOK
	}

	if exitCode, ok := errors.AsType[*exitCodeError](err); ok {
		return exitCode.code
	}

	code, exit := output.CodeInternal, ExitInternal
	var invalid *invalidError
	var usage *app.UsageError
	var data any // what explains a refusal, in the envelope
	consent, refused, running := (*app.ConsentError)(nil), (*app.RefusedError)(nil), (*app.RunningError)(nil)
	project := (*app.ProjectRefusedError)(nil)
	switch {
	case !started, errors.As(err, &usage):
		code, exit = output.CodeUsage, ExitUsage
	case errors.As(err, &invalid):
		code, exit = output.CodeInvalid, ExitInvalid
	case errors.As(err, &consent):
		code, data = output.CodeConsent, consent.Plan
	case errors.As(err, &refused):
		code, data = output.CodeRefused, refused.Plan
	case errors.As(err, &running):
		code, data = output.CodeRunning, map[string]any{"harnesses": running.Harnesses, "running": runningList(running.Running)}
	case errors.As(err, &project):
		code = output.CodeProjectRefused
	}
	if parsed && jsonOut || !parsed && jsonRequested(root, args) {
		e := output.Failure(commandName(cmd), code, err.Error())
		e.Data = data
		if conflict, ok := errors.AsType[*app.ConflictError](err); ok {
			e.Data = map[string]any{"conflicts": conflict.Conflicts}
		}
		if invalid != nil {
			e = output.Invalid(commandName(cmd), err.Error(), invalid.data)
		}
		_ = output.Write(env.Stdout, e)
		return exit
	}
	if invalid != nil {
		return exit // the report on stdout already explains it
	}
	fmt.Fprintf(env.Stderr, "Error: %v\n", err)
	if exit == ExitUsage {
		fmt.Fprintln(env.Stderr, "Run 'dotai --help' for usage.")
	}
	return exit
}

// runningList is never nil, so that it is [] in an envelope.
func runningList(r []app.RunningHarness) []app.RunningHarness {
	if r == nil {
		return []app.RunningHarness{}
	}
	return r
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

func newValidateCmd(env Env, jsonOut *bool) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "validate [path]",
		Short: "Check the pack in path (default: current directory) and its merge rules",
		Long: "Check the pack in path (default: current directory): its structure, then the merge rules.\n" +
			"Without --variant, the base is checked with each variant; without --profile, all profiles are checked together.",
		Args: cobra.MaximumNArgs(1),
	}
	sel := selectionFlags(cmd)
	format := formatFlag(cmd)
	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		res, err := env.Validate(packDir(args), sel(cmd))
		if err != nil {
			return err
		}
		if !res.Valid {
			err := rejectPack(env, *jsonOut, *format, res.Pack, res.Diagnostics, res)
			if !*jsonOut {
				if werr := writeWarnings(env.Stdout, res.Warnings, useColor(env.Stdout)); werr != nil {
					return werr
				}
			}
			return err
		}
		if *jsonOut {
			return output.Write(env.Stdout, output.Success(commandName(cmd), res))
		}
		if res.Pack == nil {
			return errors.New("validation returned no pack")
		}
		if err := writeValid(env.Stdout, res.Pack, useColor(env.Stdout)); err != nil {
			return err
		}
		return writeWarnings(env.Stdout, res.Warnings, useColor(env.Stdout))
	}
	return cmd
}

func newStageCmd(env Env, jsonOut *bool) *cobra.Command {
	var out string
	var force bool
	cmd := &cobra.Command{
		Use:   "stage [path] --out <dir>",
		Short: "Merge the layers of the pack in path and write one staging tree per scope",
		Long: "Merge the layers of the pack in path (default: current directory) and write <dir>/user\n" +
			"(base and variant) and <dir>/project (profiles). Without --variant, the default variant is used.\n" +
			"The rulesync the pack pins is fetched as by rulesync fetch and writes each scope's harness files.",
		// Checked with the arguments, so a missing --out is a usage error.
		Args: func(cmd *cobra.Command, args []string) error {
			if out == "" {
				return errors.New(`required flag "out" not set`)
			}
			return cobra.MaximumNArgs(1)(cmd, args)
		},
	}
	cmd.Flags().StringVar(&out, "out", "", "directory to write the staging trees to")
	cmd.Flags().BoolVar(&force, "force", false, "replace an earlier staging in --out")
	sel := selectionFlags(cmd)
	format := formatFlag(cmd)
	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		cache, err := cacheDir()
		if err != nil {
			return err
		}
		ctx, stop := interruptible(cmd)
		defer stop()
		res, err := env.Stage(ctx, cache, packDir(args), out, sel(cmd), toolPaths(), force, downloadProgress(env, *jsonOut))
		if err != nil {
			return err
		}
		if len(res.Diagnostics) > 0 {
			return rejectPack(env, *jsonOut, *format, res.Pack, res.Diagnostics, res)
		}
		if *jsonOut {
			return output.Write(env.Stdout, output.Success(commandName(cmd), res))
		}
		for _, f := range res.Files {
			if _, err := fmt.Fprintf(env.Stdout, "%s/%s  %s\n", f.Scope, f.Path, cmp.Or(f.Layer, f.Generator)); err != nil {
				return err
			}
		}
		return nil
	}
	return cmd
}

// toolPaths are the tools directories under the user's home and the
// current directory, the project. A directory that cannot be found fails
// the stage only when a staged file needs it.
func toolPaths() app.ToolPaths {
	home, homeErr := os.UserHomeDir()
	project, projectErr := os.Getwd()
	t := app.ToolsIn(home, project)
	if homeErr != nil {
		t.UserErr = fmt.Errorf("the home directory is unknown: %w", homeErr)
	}
	if projectErr != nil {
		t.ProjectErr = fmt.Errorf("the current directory is unknown: %w", projectErr)
	}
	return t
}

// selectionFlags adds --variant and --profile to cmd. The returned function
// reads them; Profiles is nil when --profile is not given.
func selectionFlags(cmd *cobra.Command) func(*cobra.Command) app.Selection {
	var sel app.Selection
	cmd.Flags().StringVar(&sel.Variant, "variant", "", "variant applied on top of the base")
	cmd.Flags().StringSliceVar(&sel.Profiles, "profile", nil, "profiles to apply, comma-separated, in order")
	return func(cmd *cobra.Command) app.Selection {
		if !cmd.Flags().Changed("profile") {
			sel.Profiles = nil
		}
		return sel
	}
}

// formatFlag adds --format to cmd, for the diagnostics of a pack with
// problems.
func formatFlag(cmd *cobra.Command) *formatValue {
	f := formatValue(formatPretty)
	cmd.Flags().Var(&f, "format", "diagnostics format without --json: pretty or compact")
	return &f
}

// packDir is the pack directory argument, the current directory by default.
func packDir(args []string) string {
	if len(args) == 1 {
		return args[0]
	}
	return "."
}

// rejectPack prints the diagnostics of a pack with problems as text in
// format and returns the invalid outcome that carries data.
func rejectPack(env Env, jsonOut bool, format formatValue, p *app.PackSummary, diags []app.Diagnostic, data any) error {
	if !jsonOut {
		subject := ""
		if p != nil {
			subject = p.Name + " " + p.Version
		}
		if err := writeReport(env.Stdout, subject, diags, format == formatCompact, useColor(env.Stdout)); err != nil {
			return err
		}
	}
	return &invalidError{msg: fmt.Sprintf("pack is invalid: %d problem(s)", len(diags)), data: data}
}

// commandName is the command path without the program name; the root
// command is reported as "dotai".
func commandName(cmd *cobra.Command) string {
	if cmd == nil || !cmd.HasParent() {
		return "dotai"
	}
	return strings.TrimPrefix(cmd.CommandPath(), cmd.Root().Name()+" ")
}

// jsonRequested reports whether errors must be printed as an envelope. A
// parsed --json flag decides; if parsing failed or stopped before reaching
// it, any --json or --json=<value> argument counts as a request for JSON.
func jsonRequested(root *cobra.Command, args []string) bool {
	if f := root.PersistentFlags().Lookup("json"); f != nil && f.Changed {
		return f.Value.String() == "true"
	}
	for _, a := range args {
		if a == "--" {
			return false
		}
		if a == "--json" || strings.HasPrefix(a, "--json=") {
			return true
		}
	}
	return false
}

func orUnknown(s string) string {
	if s == "" {
		return "unknown"
	}
	return s
}
