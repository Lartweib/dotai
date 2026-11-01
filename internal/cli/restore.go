package cli

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"slices"
	"strings"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/lartweib/dotai/internal/app"
	"github.com/lartweib/dotai/internal/cli/output"
	"github.com/lartweib/dotai/internal/plan"
)

func newRestoreCmd(env Env, jsonOut *bool) *cobra.Command {
	var yes, ignoreRunning bool
	cmd := &cobra.Command{
		Use:   "restore [backup-id]",
		Short: "Undo the last install, or every install down to backup-id, from the backups",
		Long: "Undo the last install that wrote files, from the backup it took under ~/.dotai/backups: each file it created is\n" +
			"deleted and each file it replaced or removed is put back, with its exec bit, and the install state is put back\n" +
			"as it was before. In a harness's settings file, only the keys an install wrote or deleted are put back; the keys\n" +
			"the harness wrote stay, and the file is deleted only when no key is left. With backup-id, every install from the\n" +
			"last one down to the one that took backup-id is undone, newest first, so the disk ends as it was before that install;\n" +
			"the oldest backup undoes everything dotai installed.\n" +
			"Every backup and every file is checked before anything is written. A file, or a key of dotai's in a settings file,\n" +
			"changed after the install is a conflict: on a terminal you are asked once whether to overwrite it; otherwise, and\n" +
			"with --json, the restore is refused unless\n" +
			"--yes is given. What the restore overwrites or deletes is first copied into a new backup. Close the harnesses first:\n" +
			"the restore of a replace install is refused while a harness it writes into is running, unless --ignore-running.",
		Args: cobra.MaximumNArgs(1),
	}
	cmd.Flags().BoolVar(&yes, "yes", false, "overwrite files changed after the install, after copying them into a new backup")
	cmd.Flags().BoolVar(&ignoreRunning, "ignore-running", false, "restore a replace install even when a harness it writes into looks like it is running")
	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		home, err := os.UserHomeDir()
		if err != nil {
			return fmt.Errorf("the home directory is unknown: %w", err)
		}
		ctx, stop := interruptible(cmd)
		defer stop()
		id := ""
		if len(args) == 1 {
			id = args[0]
		}
		color := useColor(env.Stdout)
		warn, warned := earlyWarnings(env, *jsonOut)
		asked := false
		overwrite := func(p app.RestoreResult) (bool, error) {
			if yes {
				return true, nil
			}
			if *jsonOut || env.Prompt == nil {
				return false, nil
			}
			asked = true // the warnings of the plan were printed before
			if err := writeConflicts(env.Stdout, p.Conflicts); err != nil {
				return false, err
			}
			q := "Overwrite them? [y/N] "
			if len(p.Conflicts) == 1 {
				q = "Overwrite it? [y/N] "
			}
			return env.Prompt(ctx, q)
		}
		res, err := env.Restore(ctx, home, id, app.RestoreOptions{IgnoreRunning: ignoreRunning}, warn, overwrite)
		if _, ok := errors.AsType[*app.ConflictError](err); ok && asked {
			return errors.New("the restore was cancelled: nothing was written")
		}
		if err != nil {
			return err
		}
		if *jsonOut {
			return output.Write(env.Stdout, output.Success(commandName(cmd), res))
		}
		if err := writeRestore(env.Stdout, res, color); err != nil {
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

// writeConflicts prints the files, and the keys of settings files, that a
// restore would overwrite or delete although they changed after the
// install, before the question.
func writeConflicts(w io.Writer, conflicts []app.RestoreConflict) error {
	var b strings.Builder
	keys := slices.ContainsFunc(conflicts, func(c app.RestoreConflict) bool { return c.Key != "" })
	switch {
	case keys && len(conflicts) == 1:
		b.WriteString("1 key was changed after dotai installed it; the restore overwrites or deletes it, after copying its file into a new backup:\n")
	case keys:
		fmt.Fprintf(&b, "%d files or keys were changed after dotai installed them; the restore overwrites or deletes them, after copying their files into a new backup:\n", len(conflicts))
	case len(conflicts) == 1:
		b.WriteString("1 file was changed after dotai installed it; the restore overwrites or deletes it, after copying it into a new backup:\n")
	default:
		fmt.Fprintf(&b, "%d files were changed after dotai installed them; the restore overwrites or deletes them, after copying them into a new backup:\n", len(conflicts))
	}
	for _, c := range conflicts {
		if c.Key != "" {
			fmt.Fprintf(&b, "  key %s in %s\n", c.Key, c.Target)
			continue
		}
		fmt.Fprintf(&b, "  %s\n", c.Target)
	}
	_, err := io.WriteString(w, b.String())
	return err
}

// writeRestore prints a restore: the installs it undid, newest first, what
// it put back and deleted for each harness and then for the tools, with
// the files that had changed marked, and a count, the restore's backup and
// the install state.
func writeRestore(w io.Writer, r app.RestoreResult, color bool) error {
	var b strings.Builder
	installs := "1 install"
	if len(r.Undone) != 1 {
		installs = fmt.Sprintf("%d installs", len(r.Undone))
	}
	fmt.Fprintf(&b, "Restore · undid %s, newest first\n", installs)
	for _, u := range r.Undone {
		variant := "no variant"
		if u.Variant != "" {
			variant = "variant " + u.Variant
		}
		fmt.Fprintf(&b, "  %s · %s %s · %s\n", u.ID, u.Pack.Name, u.Pack.Version, variant)
	}
	groups := slices.Clone(r.Harnesses)
	for _, op := range r.Ops {
		if op.Harness != "" && !slices.Contains(groups, op.Harness) {
			groups = append(groups, op.Harness)
		}
	}
	for _, w := range r.Wipes {
		if !slices.Contains(groups, w.Harness) {
			groups = append(groups, w.Harness)
		}
	}
	groups = append(groups, "") // the tools come last
	restored, deleted := 0, 0
	for _, g := range groups {
		var ops []app.RestoreOp
		for _, op := range r.Ops {
			if op.Harness == g {
				ops = append(ops, op)
			}
		}
		var wipes []app.RestoreWipe
		for _, w := range r.Wipes {
			if w.Harness == g {
				wipes = append(wipes, w)
			}
		}
		var keys []app.WipeKey
		for _, k := range r.WipeKeys {
			if k.Harness == g {
				keys = append(keys, k)
			}
		}
		if len(ops) == 0 && len(wipes) == 0 && len(keys) == 0 {
			continue
		}
		name := g
		if g == "" {
			name = "tools"
		}
		fmt.Fprintf(&b, "\n%s\n", paint(color, styleBold, name))
		for _, op := range ops {
			verb := paint(color, styleGreen, "restore")
			if op.Action == plan.Remove {
				verb = paint(color, styleRed, "delete") + " "
				deleted++
			} else {
				restored++
			}
			changed := ""
			if op.Conflict {
				changed = "  " + paint(color, styleDim, "(changed after the install, kept in the restore backup)")
			}
			fmt.Fprintf(&b, "  %s  %s%s%s\n", verb, op.Target, linkTo(op.Link, op.BeforeLink), changed)
			writeKeys(&b, color, len("  restore  "), op.Keys)
		}
		// A replace install's wiped entries and keys (decision 0038).
		for _, w := range wipes {
			changed := ""
			if w.Conflict {
				changed = "  " + paint(color, styleDim, "(something else was there, kept in the restore backup)")
			}
			fmt.Fprintf(&b, "  %s  %s%s\n", paint(color, styleGreen, "move back"), w.Path, changed)
		}
		for i, k := range keys {
			if i == 0 || keys[i-1].File != k.File {
				fmt.Fprintf(&b, "  %s  %s\n", paint(color, styleGreen, "restore keys"), k.File)
			}
			fmt.Fprintf(&b, "    %s\n", keyPath(k.Key))
		}
	}
	backup := "none, no file was written"
	if r.Backup != nil {
		backup = r.Backup.Path
	}
	st := r.State
	if r.Uninstalled {
		st = "removed, nothing dotai installed is left (" + r.State + ")"
	}
	moved := ""
	if len(r.Wipes) > 0 {
		moved = fmt.Sprintf(", %d move back", len(r.Wipes))
	}
	fmt.Fprintf(&b, "\n%d restore, %d delete%s · restored\nbackup: %s\nstate:  %s\n", restored, deleted, moved, backup, st)
	_, err := io.WriteString(w, b.String())
	return err
}

// TerminalPrompt is the Prompt of Env that asks on the terminal in and
// out, or nil when either is not a terminal: no one can answer, or, with
// out piped elsewhere such as to tee, no one sees the question, which
// would wait for an answer that never comes.
func TerminalPrompt(in, out *os.File) func(context.Context, string) (bool, error) {
	return terminalPrompt(in, out, term.IsTerminal)
}

func terminalPrompt(in, out *os.File, isTerminal func(fd int) bool) func(context.Context, string) (bool, error) {
	if !isTerminal(int(in.Fd())) || !isTerminal(int(out.Fd())) {
		return nil
	}
	return func(ctx context.Context, question string) (bool, error) { return ask(ctx, in, out, question) }
}

// ask prints question to out and reads one line from in: y or yes, in any
// case, is a yes, anything else a no. It gives up when ctx is done, as on
// Ctrl-C, which would otherwise wait for the line.
func ask(ctx context.Context, in io.Reader, out io.Writer, question string) (bool, error) {
	if _, err := io.WriteString(out, question); err != nil {
		return false, err
	}
	type answer struct {
		line string
		err  error
	}
	got := make(chan answer, 1)
	go func() {
		line, err := bufio.NewReader(in).ReadString('\n')
		got <- answer{line, err}
	}()
	select {
	case <-ctx.Done():
		return false, ctx.Err()
	case a := <-got:
		if a.err != nil && !errors.Is(a.err, io.EOF) {
			return false, a.err
		}
		switch strings.ToLower(strings.TrimSpace(a.line)) {
		case "y", "yes":
			return true, nil
		}
		return false, nil
	}
}
