package cli

import (
	"fmt"
	"io"
	"os"
	"slices"
	"strings"

	"github.com/spf13/cobra"

	"github.com/lartweib/dotai/internal/app"
	"github.com/lartweib/dotai/internal/cli/output"
)

func newDoctorCmd(env Env, jsonOut *bool) *cobra.Command {
	var fix bool
	cmd := &cobra.Command{
		Use:   "doctor [--fix]",
		Short: "Report what changed outside dotai since it installed or applied files",
		Long: "Compare what dotai recorded with the disk: each file of the install state and of every project record, by\n" +
			"its hash, and each key dotai owns in a settings file or a file merged by key, Claude Code's MCP servers in its\n" +
			".claude.json included; the marker of each project; the entries of ~/.dotai/projects.json and the records whose\n" +
			"project is gone; and the exclude block of each project, pending until it is in a git repository. It writes\n" +
			"nothing, and exits with 3 when it finds a problem. With --fix, it writes the exclude block of each project whose\n" +
			"entries are pending or missing, as its record lists them, after a backup, as the next apply would; it repairs\n" +
			"nothing else: apply or install again, or restore, for the rest.",
		Args: cobra.NoArgs,
	}
	cmd.Flags().BoolVar(&fix, "fix", false, "write the exclude entries that are pending or missing")
	cmd.RunE = func(cmd *cobra.Command, _ []string) error {
		home, err := os.UserHomeDir()
		if err != nil {
			return fmt.Errorf("the home directory is unknown: %w", err)
		}
		ctx, stop := interruptible(cmd)
		defer stop()
		res, err := env.Doctor(ctx, home, app.DoctorOptions{Fix: fix})
		if err != nil {
			return err
		}
		problems := res.Problems()
		if *jsonOut && problems == 0 {
			return output.Write(env.Stdout, output.Success(commandName(cmd), res))
		}
		if !*jsonOut {
			if err := writeDoctor(env.Stdout, res, useColor(env.Stdout)); err != nil {
				return err
			}
		}
		if problems == 0 {
			return nil
		}
		return &invalidError{msg: fmt.Sprintf("doctor found %s", plural(problems, "problem")), data: res}
	}
	return cmd
}

// writeDoctor prints what doctor found: the install state and the projects
// it checked, each finding with its code, those it fixed marked, a count,
// and the backups of the fix.
func writeDoctor(w io.Writer, res app.DoctorResult, color bool) error {
	var b strings.Builder
	problems, fixed := res.Problems(), len(res.Findings)-res.Problems()
	summary := "nothing changed outside dotai"
	switch {
	case problems > 0 && fixed > 0:
		summary = fmt.Sprintf("%s, %d fixed", plural(problems, "problem"), fixed)
	case problems > 0:
		summary = plural(problems, "problem")
	case fixed > 0:
		summary = fmt.Sprintf("%d fixed, nothing else changed outside dotai", fixed)
	}
	fmt.Fprintf(&b, "Doctor · %s\n", summary)
	st := res.State
	if st == "" {
		st = "none, nothing installed"
	}
	fmt.Fprintf(&b, "state:    %s\n", st)
	if len(res.Projects) == 0 {
		b.WriteString("projects: none applied\n")
	}
	for i, p := range res.Projects {
		label := "projects:"
		if i > 0 {
			label = "         "
		}
		fmt.Fprintf(&b, "%s %s\n", label, p)
	}
	width := 0
	for _, f := range res.Findings {
		width = max(width, len(f.Code))
	}
	if len(res.Findings) > 0 {
		b.WriteString("\n")
	}
	for _, f := range res.Findings {
		style, note := styleRed, ""
		if f.Fixed {
			style, note = styleGreen, paint(color, styleGreen, "(fixed) ")
		}
		fmt.Fprintf(&b, "  %s%s  %s%s\n", paint(color, style, f.Code), strings.Repeat(" ", width-len(f.Code)), note, f.Message)
	}
	for _, ref := range res.Backups {
		fmt.Fprintf(&b, "\nbackup: %s", ref.Path)
	}
	if len(res.Backups) > 0 {
		b.WriteString("\n")
	}
	if slices.ContainsFunc(res.Findings, func(f app.Finding) bool { return f.Fixable && !f.Fixed }) {
		b.WriteString("\nrun dotai doctor --fix to write the exclude entries that are pending or missing\n")
	}
	_, err := io.WriteString(w, b.String())
	return err
}
