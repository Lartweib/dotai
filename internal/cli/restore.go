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
	var yes bool
	cmd := &cobra.Command{
		Use:   "restore [backup-id]",
		Short: "Undo the last install, or every install down to backup-id, from the backups",
		Long: "Undo the last install that wrote files, from the backup it took under ~/.dotai/backups: each file it created is\n" +
			"deleted and each file it replaced or removed is put back, with its exec bit, and the install state is put back\n" +
			"as it was before. With backup-id, every install from the last one down to the one that took backup-id is undone,\n" +
			"newest first, so the disk ends as it was before that install; the oldest backup undoes everything dotai installed.\n" +
			"Every backup and every file is checked before anything is written. A file changed after the install is a conflict:\n" +
			"on a terminal you are asked once whether to overwrite it; otherwise, and with --json, the restore is refused unless\n" +
			"--yes is given. What the restore overwrites or deletes is first copied into a new backup. Close the harnesses first.",
		Args: cobra.MaximumNArgs(1),
	}
	cmd.Flags().BoolVar(&yes, "yes", false, "overwrite files changed after the install, after copying them into a new backup")
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
		warned := map[string]bool{}
		asked := false
		overwrite := func(p app.RestoreResult) (bool, error) {
			if yes {
				return true, nil
			}
			if *jsonOut || env.Prompt == nil {
				return false, nil
			}
			asked = true
			for _, w := range p.Warnings {
				fmt.Fprintf(env.Stderr, "warning: %s\n", w)
				warned[w] = true
			}
			if err := writeConflicts(env.Stdout, p.Conflicts); err != nil {
				return false, err
			}
			q := "Overwrite them? [y/N] "
			if len(p.Conflicts) == 1 {
				q = "Overwrite it? [y/N] "
			}
			return env.Prompt(ctx, q)
		}
		res, err := env.Restore(ctx, home, id, overwrite)
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

// writeConflicts prints the files a restore would overwrite or delete
// although they changed after the install, before the question.
func writeConflicts(w io.Writer, conflicts []app.RestoreConflict) error {
	var b strings.Builder
	if len(conflicts) == 1 {
		b.WriteString("1 file was changed after dotai installed it; the restore overwrites or deletes it, after copying it into a new backup:\n")
	} else {
		fmt.Fprintf(&b, "%d files were changed after dotai installed them; the restore overwrites or deletes them, after copying them into a new backup:\n", len(conflicts))
	}
	for _, c := range conflicts {
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
	groups = append(groups, "") // the tools come last
	restored, deleted := 0, 0
	for _, g := range groups {
		var ops []app.RestoreOp
		for _, op := range r.Ops {
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
			fmt.Fprintf(&b, "  %s  %s%s\n", verb, op.Target, changed)
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
	fmt.Fprintf(&b, "\n%d restore, %d delete · restored\nbackup: %s\nstate:  %s\n", restored, deleted, backup, st)
	_, err := io.WriteString(w, b.String())
	return err
}

// TerminalPrompt is the Prompt of Env that asks on the terminal in and
// out, or nil when in is not a terminal, so that no one can answer.
func TerminalPrompt(in *os.File, out io.Writer) func(context.Context, string) (bool, error) {
	if !term.IsTerminal(int(in.Fd())) {
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
