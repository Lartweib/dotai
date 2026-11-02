package cli

import (
	"errors"
	"fmt"
	"io"
	"os"
	"regexp"
	"slices"
	"strings"

	"github.com/spf13/cobra"

	"github.com/lartweib/dotai/internal/app"
	"github.com/lartweib/dotai/internal/cli/output"
	"github.com/lartweib/dotai/internal/plan"
)

func newApplyCmd(env Env, jsonOut *bool) *cobra.Command {
	var dryRun bool
	var variant string
	var profiles []string
	cmd := &cobra.Command{
		Use:   "apply [path] --profile <p> --dry-run",
		Short: "Print the plan of applying profiles to the project in the current directory",
		Long: "Plan applying the profiles of the pack in path (default: the pack the install state records) to the project in\n" +
			"the current directory, and write nothing. Each harness's files go to its local targets, such as\n" +
			".claude/settings.local.json, never to the files a team commits; an item whose target git tracks is skipped and\n" +
			"named, every file created is listed for .git/info/exclude (pending outside a git repository), and Claude Code's\n" +
			"MCP servers are keys of its .claude.json for this project. The plan also warns about a harness with no local\n" +
			"place for something, and about items with the name of a user-level item. It refuses the home directory and a\n" +
			"root. Without --variant, the variant the install state records, or the pack's default, is checked against the\n" +
			"profiles. Only --dry-run is available so far.",
		// Checked with the arguments, so these are usage errors.
		Args: func(cmd *cobra.Command, args []string) error {
			if !cmd.Flags().Changed("profile") || len(profiles) == 0 {
				return errors.New("apply needs --profile: name the profiles to apply, comma-separated")
			}
			if !dryRun {
				return errors.New("apply without --dry-run is not available yet: run it with --dry-run to print its plan")
			}
			return cobra.MaximumNArgs(1)(cmd, args)
		},
	}
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "print the plan and write nothing")
	cmd.Flags().StringVar(&variant, "variant", "", "variant of the user scope the profiles are checked against")
	cmd.Flags().StringSliceVar(&profiles, "profile", nil, "profiles to apply, comma-separated")
	format := formatFlag(cmd)
	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		home, err := os.UserHomeDir()
		if err != nil {
			return fmt.Errorf("the home directory is unknown: %w", err)
		}
		project, err := os.Getwd()
		if err != nil {
			return fmt.Errorf("the current directory is unknown: %w", err)
		}
		cache, err := cacheDir()
		if err != nil {
			return err
		}
		dir := ""
		if len(args) == 1 {
			dir = args[0]
		}
		ctx, stop := interruptible(cmd)
		defer stop()
		opts := app.ApplyOptions{Profiles: append([]string{}, profiles...), Variant: variant}
		res, err := env.PlanProject(ctx, cache, dir, project, home, opts, downloadProgress(env, *jsonOut))
		if err != nil {
			return err
		}
		if len(res.Diagnostics) > 0 {
			return rejectPack(env, *jsonOut, *format, res.Pack, res.Diagnostics, res)
		}
		if *jsonOut {
			return output.Write(env.Stdout, output.Success(commandName(cmd), res))
		}
		return writeProjectPlan(env.Stdout, res, useColor(env.Stdout), "dry run, nothing was written\n")
	}
	return cmd
}

// writeProjectPlan prints a project plan: the pack, the profiles and the
// project, then each harness with its targets and the items it skips, the
// files dotai keeps for itself, the exclude entries, the warnings, and a
// count of each action followed by footer.
func writeProjectPlan(w io.Writer, p app.ProjectPlan, color bool, footer string) error {
	var b strings.Builder
	subject := ""
	if p.Pack != nil {
		subject = p.Pack.Name + " " + p.Pack.Version + " · "
	}
	word := "profile"
	if len(p.Profiles) > 1 {
		word = "profiles"
	}
	variant := "no variant"
	if p.Variant != "" {
		variant = "variant " + p.Variant
	}
	fmt.Fprintf(&b, "Apply plan · %s%s %s · %s\n", subject, word, strings.Join(p.Profiles, ", "), variant)
	fmt.Fprintf(&b, "project: %s\n", p.Project)
	fmt.Fprintf(&b, "record:  %s\n", p.Record)
	for _, n := range p.Notices {
		fmt.Fprintf(&b, "note: %s\n", n)
	}
	groups := append(slices.Clone(p.Harnesses), "")
	count := map[plan.Action]int{}
	indent := strings.Repeat(" ", keyIndent)
	for _, g := range groups {
		var ops []plan.ProjectOp
		for _, op := range p.Ops {
			if op.Harness == g {
				ops = append(ops, op)
			}
		}
		var skipped []plan.Skipped
		for _, s := range p.Skipped {
			if s.Harness == g {
				skipped = append(skipped, s)
			}
		}
		if len(ops) == 0 && len(skipped) == 0 {
			continue
		}
		name := g
		if g == "" {
			name = "dotai"
		}
		fmt.Fprintf(&b, "\n%s\n", paint(color, styleBold, name))
		for _, op := range ops {
			count[op.Action]++
			fmt.Fprintf(&b, "  %s  %s\n", paintProjectAction(color, op.Action), op.Target)
			if len(op.Keys) > 0 {
				items := make([]string, len(op.Keys))
				for i, k := range op.Keys {
					items[i] = paint(color, projectStyle[k.Action], string(k.Action)) + " " + projectKey(k.Path)
				}
				fmt.Fprintf(&b, "%skeys: %s\n", indent, strings.Join(items, ", "))
			}
		}
		for _, s := range skipped {
			fmt.Fprintf(&b, "  %s  %s: git tracks %s\n", paintWord(color, styleYellow, "skipped"), s.Item, s.Target)
		}
	}
	if p.Exclude.Pending {
		fmt.Fprintf(&b, "\n%s · pending, not in a git repository\n", paint(color, styleBold, "exclude"))
	} else {
		fmt.Fprintf(&b, "\n%s · %s\n", paint(color, styleBold, "exclude"), p.Exclude.File)
	}
	for _, e := range p.Exclude.Entries {
		fmt.Fprintf(&b, "  %s\n", e)
	}
	if len(p.Warnings) > 0 {
		b.WriteString("\n")
	}
	for _, w := range p.Warnings {
		fmt.Fprintf(&b, "%s %s\n", paint(color, styleYellow, "warning:"), w.Message)
	}
	fmt.Fprintf(&b, "\n%d create, %d merge, %d unchanged, %d conflict, %d skipped · ",
		count[plan.Create]+count[plan.Replace], count[plan.Merge], count[plan.Unchanged], count[plan.Conflict], len(p.Skipped))
	b.WriteString(footer)
	_, err := io.WriteString(w, b.String())
	return err
}

// projectStyle is the style of each action of a project plan: a create
// green, a merge bold, a conflict red, an unchanged file dim.
var projectStyle = map[plan.Action]string{plan.Create: styleGreen, plan.Replace: styleBold, plan.Merge: styleBold, plan.Conflict: styleRed, plan.Unchanged: styleDim}

// paintProjectAction pads action to the widest action and colors it.
func paintProjectAction(color bool, a plan.Action) string {
	return paintWord(color, projectStyle[a], string(a))
}

// simpleKey is a key name that reads as it is after a dot.
var simpleKey = regexp.MustCompile(`^[A-Za-z0-9_$-]+$`)

// projectKey is a key of a file merged by key as people read it: each
// name after a dot, or quoted in brackets when it is not a simple name,
// such as projects["C:/work"].mcpServers.browser.
func projectKey(key []string) string {
	var b strings.Builder
	for i, k := range key {
		switch {
		case !simpleKey.MatchString(k):
			fmt.Fprintf(&b, "[%q]", k)
		case i > 0:
			b.WriteString("." + k)
		default:
			b.WriteString(k)
		}
	}
	return b.String()
}
