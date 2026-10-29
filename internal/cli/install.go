package cli

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/lartweib/dotai/internal/app"
	"github.com/lartweib/dotai/internal/cli/output"
	"github.com/lartweib/dotai/internal/plan"
)

func newInstallCmd(env Env, jsonOut *bool) *cobra.Command {
	var dryRun bool
	var variant string
	cmd := &cobra.Command{
		Use:   "install [path] --dry-run",
		Short: "Show the plan of an isolated install of the pack in path",
		Long: "Show the plan of an isolated install of the user scope (base and variant) of the pack in path\n" +
			"(default: current directory): how each harness runs isolated, what still leaks from your own setup,\n" +
			"and each file it would create, replace or leave unchanged. Without --variant, the default variant is used.\n" +
			"The pack is staged as by stage, into a temporary directory that is removed; apart from the rulesync cache, nothing else is written.\n" +
			"Installing itself is not available yet.",
		// Checked with the arguments, so these are usage errors.
		Args: func(cmd *cobra.Command, args []string) error {
			if cmd.Flags().Changed("profile") {
				return errors.New("install takes no --profile: profiles are project scope, and install covers the user scope only")
			}
			if !dryRun {
				return errors.New("installing is not available yet, it comes with backups and the install state; use --dry-run to see the plan")
			}
			return cobra.MaximumNArgs(1)(cmd, args)
		},
	}
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "print the plan and write nothing")
	cmd.Flags().StringVar(&variant, "variant", "", "variant applied on top of the base")
	cmd.Flags().StringSlice("profile", nil, "not accepted: profiles are project scope")
	_ = cmd.Flags().MarkHidden("profile")
	format := formatFlag(cmd)
	cmd.RunE = func(cmd *cobra.Command, args []string) error {
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
		res, err := env.PlanInstall(ctx, cache, packDir(args), variant, home, downloadProgress(env, *jsonOut))
		if err != nil {
			return err
		}
		if len(res.Diagnostics) > 0 {
			return rejectPack(env, *jsonOut, *format, res.Pack, res.Diagnostics, res)
		}
		if *jsonOut {
			return output.Write(env.Stdout, output.Success(commandName(cmd), res))
		}
		return writePlan(env.Stdout, res, useColor(env.Stdout))
	}
	return cmd
}

// writePlan prints an install plan: the pack and mode, how each harness
// runs isolated, the operations of each harness and then of the tools,
// and a count of each action.
func writePlan(w io.Writer, p app.InstallPlan, color bool) error {
	var b strings.Builder
	subject := ""
	if p.Pack != nil {
		subject = p.Pack.Name + " " + p.Pack.Version + " · "
	}
	variant := "no variant"
	if p.Variant != "" {
		variant = "variant " + p.Variant
	}
	fmt.Fprintf(&b, "Install plan · %s%s · mode %s\n", subject, variant, p.Mode)
	fmt.Fprintf(&b, "root: %s\n", p.Root)

	for _, h := range p.Harnesses {
		login := "keeps your login"
		if h.Relogin {
			login = "needs a new login"
		}
		fmt.Fprintf(&b, "\n%s · %s isolation · %s\n", paint(color, styleBold, h.ID), h.Level, login)
		for _, ptr := range h.Pointers {
			sep := "="
			if ptr.Mechanism == "flag" {
				sep = " "
			}
			fmt.Fprintf(&b, "  %s%s%s\n", ptr.Name, sep, ptr.Dir)
		}
		if len(h.Leaks) == 0 {
			fmt.Fprintf(&b, "  leaks: nothing known\n")
		}
		for _, leak := range h.Leaks {
			fmt.Fprintf(&b, "  leaks: %s\n", leak)
		}
	}

	groups := make([]string, 0, len(p.Harnesses)+1)
	for _, h := range p.Harnesses {
		groups = append(groups, h.ID)
	}
	groups = append(groups, "") // the tools come last
	count := map[plan.Action]int{}
	for _, g := range groups {
		var ops []app.InstallOp
		for _, op := range p.Ops {
			if op.Harness == g {
				ops = append(ops, op)
			}
		}
		if len(ops) == 0 {
			continue
		}
		name := g
		if g == "" {
			name = "tools"
		}
		fmt.Fprintf(&b, "\n%s\n", paint(color, styleBold, name))
		for _, op := range ops {
			count[op.Action]++
			fmt.Fprintf(&b, "  %s  %s\n", paintAction(color, op.Action), op.Target)
		}
	}
	fmt.Fprintf(&b, "\n%d create, %d replace, %d unchanged · dry run, nothing was written\n", count[plan.Create], count[plan.Replace], count[plan.Unchanged])
	_, err := io.WriteString(w, b.String())
	return err
}

// paintAction pads action to the widest action and colors it: a create
// green, a replace bold, an unchanged file dim.
func paintAction(color bool, a plan.Action) string {
	pad := strings.Repeat(" ", len(plan.Unchanged)-len(a))
	style := map[plan.Action]string{plan.Create: styleGreen, plan.Replace: styleBold, plan.Unchanged: styleDim}[a]
	if style == "" {
		return string(a) + pad
	}
	return paint(color, style, string(a)) + pad
}
