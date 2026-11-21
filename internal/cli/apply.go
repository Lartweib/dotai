package cli

import (
	"errors"
	"fmt"
	"io"
	"os"
	"slices"
	"strings"

	"github.com/spf13/cobra"

	"github.com/lartweib/dotai/internal/app"
	"github.com/lartweib/dotai/internal/envelope"
	"github.com/lartweib/dotai/internal/plan"
)

func newApplyCmd(env Env, jsonOut *bool) *cobra.Command {
	var dryRun, yes bool
	var variant string
	var profiles []string
	cmd := &cobra.Command{
		Use:   "apply [path] --profile <p> [--dry-run] [--yes]",
		Short: "Apply profiles to the project in the current directory",
		Long: "Apply the profiles of the pack in path (default: the pack the install state records) to the project in the\n" +
			"current directory, the top of its git worktree; with --dry-run, print the plan and write nothing. Each\n" +
			"harness's files go to its local targets, such as\n" +
			".claude/settings.local.json, never to the files a team commits; an item whose target git tracks is skipped and\n" +
			"named, every file created is listed for .git/info/exclude (pending outside a git repository), and Claude Code's\n" +
			"MCP servers are keys of its .claude.json for this project. The plan also warns about a harness with no local\n" +
			"place for something, and about items with the name of a user-level item. It refuses the home directory and a\n" +
			"root. Without --variant, the variant the install state records, or the pack's default, is checked against the\n" +
			"profiles. Files and keys dotai does not own, or that changed since it wrote them, are overwritten only with your\n" +
			"consent: on a terminal you are asked once, after the plan; otherwise, and with --json, the apply is refused\n" +
			"unless --yes is given. A backup keeps what the apply overwrites; dotai restore --project undoes the apply.\n" +
			"A pack file that is still an example of dotai's pack template, unchanged (template-unchanged), is installed only\n" +
			"with the same consent: --yes accepts what the plan asks to confirm.",
		// Checked with the arguments, so these are usage errors.
		Args: func(cmd *cobra.Command, args []string) error {
			if !cmd.Flags().Changed("profile") || len(profiles) == 0 {
				return errors.New("apply needs --profile: name the profiles to apply, comma-separated")
			}
			return cobra.MaximumNArgs(1)(cmd, args)
		},
	}
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "print the plan and write nothing")
	cmd.Flags().BoolVar(&yes, "yes", false, "accept what the plan asks to confirm without asking: its conflicts, after backing them up, and template files left unchanged")
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
		if dryRun {
			res, err := env.PlanProject(ctx, cache, dir, project, home, opts, downloadProgress(env, *jsonOut))
			if err != nil {
				return err
			}
			if len(res.Diagnostics) > 0 {
				return rejectPack(env, *jsonOut, *format, res.Pack, res.Diagnostics, res)
			}
			if *jsonOut {
				return envelope.Write(env.Stdout, envelope.Success(commandName(cmd), res))
			}
			return writeProjectPlan(env.Stdout, res, useColor(env.Stdout), "dry run, nothing was written\n")
		}
		color := useColor(env.Stdout)
		asked := false
		consent := func(p app.ProjectPlan) (bool, error) {
			if yes {
				return true, nil
			}
			if *jsonOut || env.Prompt == nil {
				return false, nil
			}
			asked = true
			if err := writeProjectPlan(env.Stdout, p, color, "nothing was written yet\n"); err != nil {
				return false, err
			}
			return env.Prompt(ctx, applyQuestion(p))
		}
		res, err := env.Apply(ctx, cache, dir, project, home, opts, downloadProgress(env, *jsonOut), consent)
		if _, ok := errors.AsType[*app.ApplyConsentError](err); ok && asked {
			return errors.New("the apply was cancelled, and it wrote nothing")
		}
		if err != nil {
			var p *app.ProjectPlan
			if ce, ok := errors.AsType[*app.ApplyConsentError](err); ok {
				p = &ce.Plan
			} else if re, ok := errors.AsType[*app.ApplyRefusedError](err); ok {
				p = &re.Plan
			}
			if p != nil && !*jsonOut {
				if wErr := writeProjectPlan(env.Stdout, *p, color, "refused, the apply wrote nothing\n"); wErr != nil {
					return errors.Join(err, wErr)
				}
			}
			return err
		}
		if len(res.Diagnostics) > 0 {
			return rejectPack(env, *jsonOut, *format, res.Pack, res.Diagnostics, res)
		}
		if *jsonOut {
			return envelope.Write(env.Stdout, envelope.Success(commandName(cmd), res))
		}
		footer := "applied, not the first apply in this project\n"
		if res.FirstApply {
			footer = "applied, the first apply in this project\n"
		}
		if res.Backup != nil {
			footer += "backup: " + res.Backup.Path + "\n"
		} else {
			footer += "backup: none, no file was written\n"
		}
		if asked {
			// The plan was printed before the question: print its summary
			// and the backup, not the plan again.
			return writeProjectSummary(env.Stdout, res.ProjectPlan, footer)
		}
		return writeProjectPlan(env.Stdout, res.ProjectPlan, color, footer)
	}
	return cmd
}

// writeProjectPlan prints a project plan: the pack, the profiles and the
// project, then each harness with its targets and the items it skips, the
// files dotai keeps for itself, the exclude entries, the warnings, and a
// count of each action followed by footer.
func writeProjectPlan(w io.Writer, p app.ProjectPlan, color bool, footer string) error {
	body, tail := renderProjectPlan(p, color, footer)
	_, err := io.WriteString(w, body+"\n"+tail)
	return err
}

// writeProjectSummary prints only the last line of a project plan, the
// count of each action followed by footer, for a plan that was printed
// before.
func writeProjectSummary(w io.Writer, p app.ProjectPlan, footer string) error {
	_, tail := renderProjectPlan(p, false, footer)
	_, err := io.WriteString(w, tail)
	return err
}

// renderProjectPlan is a plan as writeProjectPlan prints it, split in its
// body and the line that counts each action, followed by footer.
func renderProjectPlan(p app.ProjectPlan, color bool, footer string) (body, tail string) {
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
	fmt.Fprintf(&b, "marker:  %s\n", p.Marker)
	for _, n := range p.Notices {
		fmt.Fprintf(&b, "note: %s\n", n)
	}
	// Every harness dotai knows, in the plan's order, as an install plan
	// prints them (decision 0047): one the apply does not plan with its
	// state and detection, one it plans with its targets; a plan without
	// states lists only those it plans.
	states := p.HarnessStates
	if len(states) == 0 {
		for _, id := range p.Harnesses {
			states = append(states, app.HarnessState{ID: id, Status: "supported", Managed: true})
		}
	}
	states = append(slices.Clone(states), app.HarnessState{Status: "supported", Managed: true})
	count := map[plan.Action]int{}
	indent := strings.Repeat(" ", keyIndent)
	for _, st := range states {
		g := st.ID
		if !st.Managed {
			fmt.Fprintf(&b, "\n%s · %s\n", paint(color, styleBold, g), unmanagedText(st))
			writeDetection(&b, st.Detection)
			continue
		}
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
		switch {
		case g == "":
			name = "dotai"
		case st.IsPreview():
			name += " · preview"
		}
		fmt.Fprintf(&b, "\n%s\n", paint(color, styleBold, name))
		for _, op := range ops {
			count[op.Action]++
			fmt.Fprintf(&b, "  %s  %s\n", paintProjectAction(color, op.Action), op.Target)
			if len(op.Keys) > 0 {
				items := make([]string, len(op.Keys))
				for i, k := range op.Keys {
					items[i] = paint(color, projectStyle[k.Action], string(k.Action)) + " " + plan.KeyPath(k.Path)
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
	if len(p.Refusals) > 0 {
		fmt.Fprintf(&b, "\n%s\n", paint(color, styleBold, "refused"))
	}
	for _, r := range p.Refusals {
		fmt.Fprintf(&b, "  %s\n", r.Message)
	}
	tail = fmt.Sprintf("%d create, %d replace, %d merge, %d remove, %d unchanged, %d conflict, %d skipped · ",
		count[plan.Create], count[plan.Replace], count[plan.Merge], count[plan.Remove], count[plan.Unchanged], count[plan.Conflict], len(p.Skipped)) + footer
	return b.String(), tail
}

// projectStyle is the style of each action of a project plan: a create
// green, a merge bold, a conflict red, an unchanged file dim.
var projectStyle = map[plan.Action]string{plan.Create: styleGreen, plan.Replace: styleBold, plan.Merge: styleBold, plan.Remove: styleYellow, plan.Conflict: styleRed, plan.Unchanged: styleDim}

// paintProjectAction pads action to the widest action and colors it.
func paintProjectAction(color bool, a plan.Action) string {
	return paintWord(color, projectStyle[a], string(a))
}

// applyQuestion asks whether the apply may go on with what its plan p
// asks to confirm.
func applyQuestion(p app.ProjectPlan) string {
	var parts []string
	if p.ConflictConsent() {
		parts = append(parts, "overwrites the conflicts, files and keys dotai does not own or that changed since it wrote them, after copying them into its backup")
	}
	if files := p.TemplateFiles(); len(files) > 0 {
		parts = append(parts, "installs "+app.TemplateFilesPhrase(len(files)))
	}
	return "The apply " + strings.Join(parts, ", and it ") + ", as listed above. Go on? [y/N] "
}
