package cli

import (
	"cmp"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/lartweib/dotai/internal/app"
	"github.com/lartweib/dotai/internal/envelope"
)

// newPackCmd is dotai pack: the registry of packs, ~/.dotai/packs.json
// (decision 0044).
func newPackCmd(env Env, jsonOut *bool) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "pack",
		Short: "Add, update, list and remove the packs of ~/.dotai/packs.json",
		Long: "The pack registry, ~/.dotai/packs.json, which the web UI shows too. A pack comes from a GitHub repository,\n" +
			"fetched with the git on PATH (2.41 or later) into ~/.dotai/packs/<identity>, or from a local directory, which\n" +
			"stays where it is. A git pack's identity is github.com/<owner>/<repo>, a local one's local/<name>.",
		Args: cobra.NoArgs,
	}
	cmd.AddCommand(newPackAddCmd(env, jsonOut), newPackRemoveCmd(env, jsonOut), newPackListCmd(env, jsonOut))
	return cmd
}

func newPackAddCmd(env Env, jsonOut *bool) *cobra.Command {
	var ref, dir string
	cmd := &cobra.Command{
		Use:   "add <github-url> [--ref <ref>] | add --dir <path>",
		Short: "Add a pack from a GitHub repository or a local directory",
		Long: "Fetch the pack at <github-url>, https://github.com/<owner>/<repo>, ssh://git@github.com/<owner>/<repo> or\n" +
			"git@github.com:<owner>/<repo>, at --ref, a tag, a branch or a full commit SHA (default: the default branch), into\n" +
			"~/.dotai/packs/<identity>. Its hooks, attributes and submodules never run; a link, a submodule, a .git path or\n" +
			"more than 10000 files or 64 MiB is refused; then it is validated as by dotai validate. Nothing is registered\n" +
			"unless every check passes. A branch can move: the pack stays at its commit until dotai pack update.\n" +
			"With --dir, add the pack in that directory, which stays where it is, as local/<name>.",
		Args: func(cmd *cobra.Command, args []string) error {
			switch {
			case dir != "" && len(args) > 0:
				return errors.New("give a GitHub URL or --dir, not both")
			case dir != "" && cmd.Flags().Changed("ref"):
				return errors.New("--ref takes a GitHub URL, not --dir")
			case dir == "" && len(args) != 1:
				return errors.New("give one GitHub URL, or --dir <path>")
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&ref, "ref", "", "tag, branch or full commit SHA to fetch (default: the default branch)")
	cmd.Flags().StringVar(&dir, "dir", "", "add the pack in this local directory instead")
	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		home, err := os.UserHomeDir()
		if err != nil {
			return fmt.Errorf("the home directory is unknown: %w", err)
		}
		var res app.PackAddResult
		if dir != "" {
			abs, err := filepath.Abs(dir)
			if err != nil {
				return err
			}
			res, err = env.AddLocalPack(home, abs)
			if err != nil {
				return err
			}
		} else {
			ctx, stop := interruptible(cmd)
			defer stop()
			res, err = env.AddGitPack(ctx, home, args[0], app.PackGitOptions{Ref: ref, Interactive: true})
			if err != nil {
				return err
			}
		}
		return writePackResult(env, cmd, *jsonOut, res)
	}
	return cmd
}

// writePackResult prints an addition or an update: the envelope with
// --json, or the pack and where it is from, and its warnings on stderr.
func writePackResult(env Env, cmd *cobra.Command, jsonOut bool, res app.PackAddResult) error {
	if jsonOut {
		return envelope.Write(env.Stdout, envelope.Success(commandName(cmd), res))
	}
	var b strings.Builder
	p := res.Pack
	fmt.Fprintf(&b, "%s %s (%s %s)\npath:   %s\n", res.Outcome, p.ID, cmp.Or(p.Name, "?"), cmp.Or(p.Version, "?"), p.Path)
	if s := p.Source; s != nil {
		fmt.Fprintf(&b, "source: %s\ncommit: %s (%s)\n", s.URL, s.Commit, s.FullRef)
	}
	if _, err := io.WriteString(env.Stdout, b.String()); err != nil {
		return err
	}
	writePackWarnings(env.Stderr, res.Warnings)
	return nil
}

func writePackWarnings(w io.Writer, warnings []app.PackWarning) {
	for _, warn := range warnings {
		fmt.Fprintf(w, "warning: %s\n", warn.Message)
	}
}

func newPackRemoveCmd(env Env, jsonOut *bool) *cobra.Command {
	return &cobra.Command{
		Use:   "remove <identity>",
		Short: "Remove a pack from the registry",
		Long: "Remove the pack of <identity> from ~/.dotai/packs.json. A git pack's clone in ~/.dotai/packs/<identity> is\n" +
			"deleted; a local pack's directory is never touched. Installs and projects change only when dotai install or\n" +
			"dotai apply runs again.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			home, err := os.UserHomeDir()
			if err != nil {
				return fmt.Errorf("the home directory is unknown: %w", err)
			}
			res, err := env.RemovePackID(home, args[0])
			if err != nil {
				return err
			}
			if *jsonOut {
				return envelope.Write(env.Stdout, envelope.Success(commandName(cmd), res))
			}
			var b strings.Builder
			for _, e := range res.Removed {
				fmt.Fprintf(&b, "removed %s (%s)\n", e.ID, e.Path)
			}
			if res.Deleted != "" {
				fmt.Fprintf(&b, "deleted %s\n", res.Deleted)
			}
			if _, err := io.WriteString(env.Stdout, b.String()); err != nil {
				return err
			}
			writePackWarnings(env.Stderr, res.Warnings)
			return nil
		},
	}
}

func newPackListCmd(env Env, jsonOut *bool) *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List the packs of the registry",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			home, err := os.UserHomeDir()
			if err != nil {
				return fmt.Errorf("the home directory is unknown: %w", err)
			}
			list, err := env.ListPacks(home)
			if err != nil {
				return err
			}
			if *jsonOut {
				return envelope.Write(env.Stdout, envelope.Success(commandName(cmd), list))
			}
			var b strings.Builder
			if len(list.Packs) == 0 {
				b.WriteString("no packs: add one with dotai pack add\n")
			}
			for _, e := range list.Packs {
				fmt.Fprintf(&b, "%s  %s %s\n  %s\n", cmp.Or(e.ID, "?"), cmp.Or(e.Name, "?"), e.Version, e.Path)
				if s := e.Source; s != nil {
					fmt.Fprintf(&b, "  %s at %s (%s)\n", s.URL, s.Commit, s.FullRef)
				}
				if e.Error != "" {
					fmt.Fprintf(&b, "  error: %s\n", e.Error)
				}
			}
			_, err = io.WriteString(env.Stdout, b.String())
			return err
		},
	}
}
