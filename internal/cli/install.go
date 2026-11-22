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
	"github.com/lartweib/dotai/internal/harness"
	"github.com/lartweib/dotai/internal/plan"
)

func newInstallCmd(env Env, jsonOut *bool) *cobra.Command {
	var dryRun, yes, ignoreRunning, reset bool
	var variant, mode string
	var harnesses []string
	cmd := &cobra.Command{
		Use:   "install [path]",
		Short: "Install the pack in path, or print its plan with --dry-run",
		Long: "Install the user scope (base and variant) of the pack in path (default: current directory) isolated, under\n" +
			"~/.dotai/isolated, and print the plan: how each harness runs isolated, what still leaks from your own setup,\n" +
			"and each file it creates, replaces, removes or leaves unchanged. Without --variant, install uses the variant the\n" +
			"install state records for this pack, if the pack still declares it (else it warns), or the pack's default one.\n" +
			"In each harness's settings file dotai owns only the keys the pack puts there: the keys the harness writes stay,\n" +
			"and a key dotai owns that was changed since is named and not overwritten. A variant with shared-skills also\n" +
			"links each skill of ~/.agents/skills into Claude Code's and agy's isolated skills, unless the pack has a skill\n" +
			"of that name; a skill added there later shows up after the next install.\n" +
			"It manages the pack's compatible harnesses that are installed and supported, found by their command on PATH or\n" +
			"their configuration directory (agy by its command only), or those --harness names; a preview harness, such as\n" +
			"opencode, only when --harness or the saved choice names it. A required harness that is missing or left out\n" +
			"stops the install, another one is skipped with a note. A --harness choice is saved, and a plain install reuses it.\n" +
			"The pack is staged as by stage, into a temporary directory that is removed. Before the first write, each file\n" +
			"that is replaced or removed is copied into a backup under ~/.dotai/backups; a failure rolls back from it.\n" +
			"The install state is written last, to ~/.dotai/state.json. With --dry-run, nothing but the rulesync cache is written.\n" +
			"--mode replace resets each selected harness to a fresh install, then installs the pack in its own configuration\n" +
			"directory: each entry it wipes is moved into the backup first, with the keys of ~/.claude.json it wipes, and\n" +
			"dotai restore moves them back. It wipes what dotai does not own only with your consent: on a terminal you are\n" +
			"asked once, after the plan; otherwise, and with --json, it is refused unless --yes is given. It is refused, even\n" +
			"with --yes, when its plan lists refusals, and while a harness it resets, or one it only updates and whose settings\n" +
			"file it would write, which that harness rewrites while it runs, is running, unless --ignore-running (decision 0053).\n" +
			"With --dry-run it lists what would be wiped, kept and written per harness, and why it would refuse. Without\n" +
			"--mode, install uses the mode the install state records, or isolated; it never switches a replace install to\n" +
			"isolated: run dotai restore first.\n" +
			"A replace install of a harness that the install state records as already installed in the replace mode does not\n" +
			"reset it again: it only updates the pack there, writing dotai's own files and keys, wiping nothing and keeping\n" +
			"what was added since, and it still backs up whatever it overwrites. On a terminal you are asked which one you\n" +
			"want, with what a reset would wipe listed; with --yes, --json, no terminal and --dry-run it updates only, and\n" +
			"says what --reset would wipe. --reset resets again, as the first replace install did, with the same consent; for\n" +
			"a harness that has no replace install yet, which is always reset, it changes nothing.\n" +
			"A pack file that is still an example of dotai's pack template, unchanged (template-unchanged), is installed only\n" +
			"with the same consent, in either mode: --yes accepts what the plan asks to confirm.",
		// Checked with the arguments, so these are usage errors.
		Args: func(cmd *cobra.Command, args []string) error {
			if cmd.Flags().Changed("profile") {
				return errors.New("install takes no --profile: profiles are project scope, and install covers the user scope only")
			}
			return cobra.MaximumNArgs(1)(cmd, args)
		},
	}
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "print the plan and write nothing")
	cmd.Flags().StringVar(&variant, "variant", "", "variant applied on top of the base (default: the variant the install state records for the pack, or the pack's default)")
	cmd.Flags().StringVar(&mode, "mode", "", "install mode: isolated or replace (default: the mode the install state records, or isolated)")
	cmd.Flags().BoolVar(&yes, "yes", false, "accept what the plan asks to confirm without asking: what a replace install wipes into the backup, and template files left unchanged")
	cmd.Flags().BoolVar(&ignoreRunning, "ignore-running", false, "in the replace mode, install even when a harness it resets, or whose live settings file it writes, looks like it is running")
	cmd.Flags().BoolVar(&reset, "reset", false, "in the replace mode, reset again a harness that already has a replace install, instead of only updating the pack there")
	cmd.Flags().StringSliceVar(&harnesses, "harness", nil, "harnesses to manage, comma-separated (default: the saved selection, or the pack's harnesses that are installed)")
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
		progress := downloadProgress(env, *jsonOut)
		opts := app.InstallOptions{Variant: variant, Mode: mode, IgnoreRunning: ignoreRunning, Reset: reset}
		if cmd.Flags().Changed("harness") {
			opts.Harnesses = append([]string{}, harnesses...)
		}
		if dryRun {
			res, err := env.PlanInstall(ctx, cache, packDir(args), home, opts, progress)
			if err != nil {
				return err
			}
			if len(res.Diagnostics) > 0 {
				return rejectPack(env, *jsonOut, *format, res.Pack, res.Diagnostics, res)
			}
			if *jsonOut {
				return envelope.Write(env.Stdout, envelope.Success(commandName(cmd), res))
			}
			return writePlan(env.Stdout, res, useColor(env.Stdout), "dry run, nothing was written\n")
		}
		warn, warned := earlyWarnings(env, *jsonOut)
		color := useColor(env.Stdout)
		asked := false
		consent := func(p app.InstallPlan) (bool, error) {
			if yes {
				return true, nil
			}
			if *jsonOut || env.Prompt == nil {
				return false, nil
			}
			asked = true
			if err := writePlan(env.Stdout, p, color, "nothing was written yet\n"); err != nil {
				return false, err
			}
			return env.Prompt(ctx, installQuestion(p))
		}
		if !yes && !reset && !*jsonOut && env.Prompt != nil {
			// On a terminal a later replace install asks whether to reset
			// again, with what it would wipe in front of the user.
			opts.ChooseReset = func(c app.ResetChoice) (bool, error) {
				if err := writeResetChoice(env.Stdout, c, color); err != nil {
					return false, err
				}
				return env.Prompt(ctx, resetQuestion(c))
			}
		}
		steps := newStepPrinter(env, *jsonOut)
		if steps != nil {
			ctx = app.WithSteps(ctx, steps.report)
		}
		res, err := env.Install(ctx, cache, packDir(args), home, opts, progress, warn, consent)
		steps.finish(err == nil)
		if _, ok := errors.AsType[*app.ConsentError](err); ok && asked {
			return errors.New("the install was cancelled, and it wrote nothing")
		}
		if err != nil {
			// The plan explains a refusal; with --json, the envelope has it.
			var p *app.InstallPlan
			if ce, ok := errors.AsType[*app.ConsentError](err); ok {
				p = &ce.Plan
			} else if re, ok := errors.AsType[*app.RefusedError](err); ok && re.Plan.Mode != "" {
				p = &re.Plan
			}
			if p != nil && !*jsonOut {
				if wErr := writePlan(env.Stdout, *p, color, "refused, the install wrote nothing\n"); wErr != nil {
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
		backup := "none, no file was written"
		if res.Backup != nil {
			backup = res.Backup.Path
		}
		footer := "installed\nbackup: " + backup + "\nstate:  " + res.State + "\n"
		if asked {
			// The plan was printed before the question: print its summary
			// and where things went, not the plan again.
			err = writePlanSummary(env.Stdout, res.InstallPlan, footer)
		} else {
			err = writePlan(env.Stdout, res.InstallPlan, color, footer)
		}
		if err != nil {
			return err
		}
		for _, w := range res.Warnings {
			if !warned[w] {
				fmt.Fprintf(env.Stderr, "warning: %s\n", w)
			}
		}
		return nil
	}
	return cmd
}

// earlyWarnings is the warn port of install and restore: without --json, it
// prints each warning on stderr as soon as the use case gives it, before
// anything is written, and records it in warned, so that it is not printed
// again with the warnings that come after. With --json it is nil: the
// warnings are in data.
func earlyWarnings(env Env, jsonOut bool) (app.Warn, map[string]bool) {
	warned := map[string]bool{}
	if jsonOut {
		return nil, warned
	}
	return func(text string) {
		if !warned[text] {
			warned[text] = true
			fmt.Fprintf(env.Stderr, "warning: %s\n", text)
		}
	}, warned
}

// unmanagedText says why the plan does not manage h, a harness dotai
// knows (decision 0047): the pack is not compatible with it, or it is
// compatible, required or not, and a preview or not.
func unmanagedText(h app.HarnessState) string {
	if !h.Compatible {
		return "not compatible with the pack, not managed"
	}
	parts := []string{}
	if h.Required {
		parts = append(parts, "required")
	}
	if h.IsPreview() {
		parts = append(parts, "preview")
	}
	return strings.Join(append(parts, "not managed"), ", ")
}

// writePlan prints an install plan: the pack and mode, the shared skills
// of a variant that shares them, how each harness runs isolated, the
// operations of each harness and then of the tools, and a count of each
// action followed by footer.
func writePlan(w io.Writer, p app.InstallPlan, color bool, footer string) error {
	body, tail := renderPlan(p, color, footer)
	_, err := io.WriteString(w, body+"\n"+tail)
	return err
}

// writePlanSummary prints only the last line of a plan, the count of each
// action followed by footer, for a plan that was printed before.
func writePlanSummary(w io.Writer, p app.InstallPlan, footer string) error {
	_, tail := renderPlan(p, false, footer)
	_, err := io.WriteString(w, tail)
	return err
}

// renderPlan is a plan as writePlan prints it, split in its body and the
// line that counts each action, followed by footer.
func renderPlan(p app.InstallPlan, color bool, footer string) (body, tail string) {
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
	writeShared(&b, p.SharedSkills)
	for _, n := range p.Notices {
		fmt.Fprintf(&b, "note: %s\n", n)
	}
	// Before the confirmation, and with --yes too (decision 0045).
	for _, w := range p.Warnings {
		fmt.Fprintf(&b, "%s %s: %s\n", paint(color, styleYellow, "warning:"), w.Code, w.Message)
	}

	for _, h := range p.Harnesses {
		if !h.Managed {
			fmt.Fprintf(&b, "\n%s · %s\n", paint(color, styleBold, h.ID), unmanagedText(h.State()))
			writeDetection(&b, h.Detection)
			continue
		}
		id := h.ID
		if h.IsPreview() {
			id += " · preview"
		}
		if p.Mode == app.ModeReplace {
			fmt.Fprintf(&b, "\n%s · replace%s · %s\n", paint(color, styleBold, id), reinstallText(p, h.ID), loginText(h.Relogin))
			writeDetection(&b, h.Detection)
			writeKeeps(&b, p.Keeps, h.ID)
			writeWouldReset(&b, p, h.ID)
			for _, c := range h.Tools {
				fmt.Fprintf(&b, "  %s runs tool %s: %s\n", c.Key, c.Tool, c.Command)
			}
			continue
		}
		fmt.Fprintf(&b, "\n%s · %s isolation · %s\n", paint(color, styleBold, id), h.Level, loginText(h.Relogin))
		writeDetection(&b, h.Detection)
		for _, ptr := range h.Pointers {
			fmt.Fprintf(&b, "  %s=%s\n", ptr.Name, ptr.Dir)
		}
		if len(h.Leaks) == 0 {
			fmt.Fprintf(&b, "  leaks: nothing known\n")
		}
		for _, leak := range h.Leaks {
			fmt.Fprintf(&b, "  leaks: %s\n", leak)
		}
		for _, c := range h.Tools {
			fmt.Fprintf(&b, "  %s runs tool %s: %s\n", c.Key, c.Tool, c.Command)
		}
	}

	groups := make([]string, 0, len(p.Harnesses)+1)
	for _, h := range p.Harnesses {
		groups = append(groups, h.ID)
	}
	for _, op := range p.Ops { // a harness the pack dropped, whose files are removed
		if op.Harness != "" && !slices.Contains(groups, op.Harness) {
			groups = append(groups, op.Harness)
		}
	}
	wipes, keys := 0, 0
	groups = append(groups, "") // the tools come last
	count := map[plan.Action]int{}
	for _, g := range groups {
		var ops []app.InstallOp
		for _, op := range p.Ops {
			if op.Harness == g {
				ops = append(ops, op)
			}
		}
		var wiped []app.WipePlan
		for _, w := range p.Wipes {
			if w.Harness == g && g != "" {
				wiped = append(wiped, w)
			}
		}
		var wipedKeys []app.WipeKey
		for _, k := range p.WipeKeys {
			if k.Harness == g && g != "" {
				wipedKeys = append(wipedKeys, k)
			}
		}
		if len(ops) == 0 && len(wiped) == 0 && len(wipedKeys) == 0 {
			continue
		}
		name := g
		if g == "" {
			name = "tools"
		}
		fmt.Fprintf(&b, "\n%s\n", paint(color, styleBold, name))
		indent := strings.Repeat(" ", keyIndent)
		for _, w := range wiped {
			wipes++
			fmt.Fprintf(&b, "  %s  %s\n", paintWord(color, styleRed, "wipe"), w.Path)
			if w.Collateral != "" {
				fmt.Fprintf(&b, "%swarning: %s\n", indent, w.Collateral)
			}
		}
		for _, k := range wipedKeys {
			keys++
			fmt.Fprintf(&b, "  %s  %s · %s\n", paintWord(color, styleRed, "wipe key"), k.File, keyPath(k.Key))
		}
		for _, op := range ops {
			count[op.Action]++
			fmt.Fprintf(&b, "  %s  %s%s\n", paintAction(color, op.Action), op.Target, linkTo(op.Link, op.BeforeLink))
			writeKeys(&b, color, keyIndent, op.Keys)
		}
	}
	if len(p.Refusals) > 0 {
		fmt.Fprintf(&b, "\n%s\n", paint(color, styleBold, "refused"))
		for _, r := range p.Refusals {
			fmt.Fprintf(&b, "  %s\n", r.Message)
		}
	}
	var t strings.Builder
	fmt.Fprintf(&t, "%d create, %d replace, %d remove, %d unchanged · ", count[plan.Create], count[plan.Replace], count[plan.Remove], count[plan.Unchanged])
	if p.Mode == app.ModeReplace {
		fmt.Fprintf(&t, "%d wipe, %d key wipe, %d refused · ", wipes, keys, len(p.Refusals))
	}
	t.WriteString(footer)
	return b.String(), t.String()
}

// reinstallText is how a replace plan says what it does to the harness
// id when the install state records it in the replace mode already
// (decision 0051): " · update only", or " · reset" when it resets it again.
func reinstallText(p app.InstallPlan, id string) string {
	switch {
	case !slices.Contains(p.Reinstall, id):
		return ""
	case p.Reset:
		return " · reset"
	}
	return " · update only"
}

// writeWouldReset says, under a harness that a replace plan only
// updates, that it wipes nothing, and what --reset would wipe there.
func writeWouldReset(b *strings.Builder, p app.InstallPlan, id string) {
	if p.WouldReset == nil || !slices.Contains(p.Reinstall, id) {
		return
	}
	entries, keys, refused := 0, 0, 0
	for _, w := range p.WouldReset.Wipes {
		if w.Harness == id {
			entries++
		}
	}
	for _, k := range p.WouldReset.WipeKeys {
		if k.Harness == id {
			keys++
		}
	}
	for _, r := range p.WouldReset.Refusals {
		if r.Harness == id {
			refused++
		}
	}
	line := "  wipes nothing; --reset would wipe " + countText(entries, "entry", "entries") + " and " + countText(keys, "key", "keys")
	if refused > 0 {
		line += ", and would be refused for " + countText(refused, "path", "paths")
	}
	fmt.Fprintf(b, "%s\n", line)
}

// countText is n with the word for it.
func countText(n int, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return fmt.Sprintf("%d %s", n, many)
}

// writeResetChoice prints, before the question of a later replace install,
// what resetting the harnesses it updates only would wipe.
func writeResetChoice(w io.Writer, c app.ResetChoice, color bool) error {
	var b strings.Builder
	fmt.Fprintf(&b, "%s already has a replace install, so this install only updates the pack there, wiping nothing.\n", strings.Join(c.Harnesses, ", "))
	fmt.Fprintf(&b, "A reset would instead wipe %s and %s into the backup:\n", countText(len(c.Preview.Wipes), "entry", "entries"), countText(len(c.Preview.WipeKeys), "key", "keys"))
	for _, wp := range c.Preview.Wipes {
		fmt.Fprintf(&b, "  %s  %s\n", paintWord(color, styleRed, "wipe"), wp.Path)
		if wp.Collateral != "" {
			fmt.Fprintf(&b, "%swarning: %s\n", strings.Repeat(" ", keyIndent), wp.Collateral)
		}
	}
	for _, k := range c.Preview.WipeKeys {
		fmt.Fprintf(&b, "  %s  %s · %s\n", paintWord(color, styleRed, "wipe key"), k.File, keyPath(k.Key))
	}
	if n := len(c.Preview.Refusals); n > 0 {
		fmt.Fprintf(&b, "%s, so a reset would be refused: see dotai install --dry-run --reset\n", countText(n, "path looks like a login, a secret or a history, or cannot be read", "paths look like a login, a secret or a history, or cannot be read"))
	}
	b.WriteString("\n")
	_, err := io.WriteString(w, b.String())
	return err
}

// resetQuestion asks whether the harnesses of c are reset again.
func resetQuestion(c app.ResetChoice) string {
	return "Reset " + strings.Join(c.Harnesses, ", ") + " again, wiping what is listed above, instead of only updating the pack? [y/N] "
}

// actionWidth is the width of the action column of a plan, that of its
// widest action, and keyIndent the indent of the lines under an operation,
// such as its keys, which start under its target.
const (
	actionWidth = len(plan.Unchanged)
	keyIndent   = len("  ") + actionWidth + len("  ")
)

// loginText is how relogin reads in a plan: whether the user needs a new
// login, keeps theirs or, where it was not verified, may need one.
func loginText(relogin string) string {
	switch relogin {
	case string(harness.ReloginYes):
		return "needs a new login"
	case string(harness.ReloginNo):
		return "keeps your login"
	}
	return "may need a new login"
}

// writeKeeps prints what a replace install keeps of the harness id, one
// line per kind, such as "keeps login: <path>".
func writeKeeps(b *strings.Builder, keeps []app.KeepPlan, id string) {
	var kinds []string
	paths := map[string][]string{}
	for _, k := range keeps {
		if k.Harness != id {
			continue
		}
		if _, ok := paths[k.Kind]; !ok {
			kinds = append(kinds, k.Kind)
		}
		paths[k.Kind] = append(paths[k.Kind], k.Path)
	}
	for _, kind := range kinds {
		fmt.Fprintf(b, "  keeps %s: %s\n", kind, strings.Join(paths[kind], ", "))
	}
}

// keyPath is a key of a split file as people read it: its first name, then
// each name inside it, the last after a dot and the others quoted, such as
// projects["C:/work"].allowedTools.
func keyPath(key []string) string {
	if len(key) == 0 {
		return ""
	}
	s := key[0]
	for i, k := range key[1:] {
		if i == len(key)-2 {
			s += "." + k
			continue
		}
		s += fmt.Sprintf("[%q]", k)
	}
	return s
}

// paintWord pads word to the widest action and colors it with style.
func paintWord(color bool, style, word string) string {
	pad := ""
	if n := actionWidth - len(word); n > 0 {
		pad = strings.Repeat(" ", n)
	}
	return paint(color, style, word) + pad
}

// writeDetection prints the two signals that detect a harness: its
// command on PATH and its configuration directory.
func writeDetection(b *strings.Builder, d app.Detection) {
	if d.Binary == "" {
		fmt.Fprintf(b, "  binary: not found on PATH\n")
	} else {
		fmt.Fprintf(b, "  binary: %s\n", d.Binary)
	}
	if d.ConfigFound {
		fmt.Fprintf(b, "  config: %s\n", d.Config)
	} else {
		fmt.Fprintf(b, "  config: %s (missing)\n", d.Config)
	}
}

// writeShared prints the shared skills a variant that shares them takes
// (decision 0036), and each entry it skips; nothing for a variant that
// does not.
func writeShared(b *strings.Builder, sp *app.SharedSkillsPlan) {
	switch {
	case sp == nil:
		return
	case !sp.Found:
		fmt.Fprintf(b, "shared skills: %s is missing, so there is nothing to share\n", sp.Dir)
	case len(sp.Skills) == 0:
		fmt.Fprintf(b, "shared skills: %s · no skill\n", sp.Dir)
	default:
		fmt.Fprintf(b, "shared skills: %s · %s\n", sp.Dir, strings.Join(sp.Skills, ", "))
	}
	for _, s := range sp.Skipped {
		fmt.Fprintf(b, "  note: %s\n", s.Text())
	}
}

// linkTo is " -> <destination>" for an op on a link: where it points once
// the op is carried out, or, for a remove, where it pointed; "" for a file.
func linkTo(link, before string) string {
	if link == "" {
		link = before
	}
	if link == "" {
		return ""
	}
	return " -> " + link
}

// writeKeys prints the keys an operation writes into or deletes from a
// settings file, on one line under its target, indented by indent.
func writeKeys(b *strings.Builder, color bool, indent int, keys []plan.KeyChange) {
	if len(keys) == 0 {
		return
	}
	items := make([]string, len(keys))
	for i, k := range keys {
		items[i] = paint(color, actionStyle[k.Action], string(k.Action)) + " " + k.Key
	}
	fmt.Fprintf(b, "%skeys: %s\n", strings.Repeat(" ", indent), strings.Join(items, ", "))
}

// actionStyle is the style of each action: a create green, a replace
// bold, a remove red, an unchanged file dim.
var actionStyle = map[plan.Action]string{plan.Create: styleGreen, plan.Replace: styleBold, plan.Remove: styleRed, plan.Unchanged: styleDim}

// paintAction pads action to the widest action and colors it: a create
// green, a replace bold, a remove red, an unchanged file dim.
func paintAction(color bool, a plan.Action) string {
	pad := strings.Repeat(" ", actionWidth-len(a))
	style := actionStyle[a]
	if style == "" {
		return string(a) + pad
	}
	return paint(color, style, string(a)) + pad
}

// installQuestion asks whether the install may go on with what its plan p
// asks to confirm.
func installQuestion(p app.InstallPlan) string {
	var parts []string
	if p.ConsentForeign {
		parts = append(parts, "moves into its backup what dotai does not own: the entries it wipes, the files it overwrites and the keys of ~/.claude.json it deletes")
	}
	if files := p.TemplateFiles(); len(files) > 0 {
		parts = append(parts, "installs "+app.TemplateFilesPhrase(len(files)))
	}
	return "The install " + strings.Join(parts, ", and it ") + ", as listed above. Go on? [y/N] "
}
