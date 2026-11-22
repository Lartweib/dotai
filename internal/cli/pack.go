package cli

import (
	"cmp"
	"context"
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
		Short: "Create, add, update, list, copy and remove the packs of ~/.dotai/packs.json",
		Long: "The pack registry, ~/.dotai/packs.json, which the web UI shows too. A pack comes from a GitHub repository,\n" +
			"fetched with the git on PATH (2.41 or later) into ~/.dotai/packs/<identity>, or from a local directory, which\n" +
			"stays where it is. A git pack's identity is github.com/<owner>/<repo>, a local one's local/<name>.",
		Args: cobra.NoArgs,
	}
	cmd.AddCommand(newPackAddCmd(env, jsonOut), newPackUpdateCmd(env, jsonOut), newPackRemoveCmd(env, jsonOut), newPackListCmd(env, jsonOut), newPackNewCmd(env, jsonOut), newPackCopyCmd(env, jsonOut))
	return cmd
}

func newPackAddCmd(env Env, jsonOut *bool) *cobra.Command {
	var ref, dir string
	var yes bool
	cmd := &cobra.Command{
		Use:   "add <github-url> [--ref <ref>] | add --dir <path>",
		Short: "Add a pack from a GitHub repository or a local directory",
		Long: "Fetch the pack at <github-url>, https://github.com/<owner>/<repo>, ssh://git@github.com/<owner>/<repo> or\n" +
			"git@github.com:<owner>/<repo>, at --ref, a tag, a branch, a full commit SHA or latest (default: the default branch),\n" +
			"into ~/.dotai/packs/<identity>. --ref latest follows the highest release tag vX.Y.Z, with no pre-release, which dotai\n" +
			"pack update moves forward; with no such tag it is refused. Its hooks, attributes and submodules never run; a link, a submodule, a .git path or\n" +
			"more than 10000 files or 64 MiB is refused; then it is validated as by dotai validate. Nothing is registered\n" +
			"unless every check passes. A branch can move: the pack stays at its commit until dotai pack update.\n" +
			"Adding a pack of the same identity again, from any form of its URL, is an update, as dotai pack update, to\n" +
			"--ref when given, and keeps the URL last used. With --dir, add the pack in that directory, which stays where it\n" +
			"is, as local/<name>.",
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
	cmd.Flags().StringVar(&ref, "ref", "", "tag, branch, full commit SHA or latest, the highest release tag vX.Y.Z (default: the default branch)")
	cmd.Flags().StringVar(&dir, "dir", "", "add the pack in this local directory instead")
	cmd.Flags().BoolVar(&yes, "yes", false, "update a pack already added without asking")
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
			var asked bool
			res, err = env.AddGitPack(ctx, home, args[0], app.PackGitOptions{Ref: ref, Interactive: true, Consent: packConsent(ctx, env, *jsonOut, yes, &asked)})
			if err != nil {
				return packUpdateFailed(env, *jsonOut, asked, err)
			}
			return writePackResult(env, cmd, *jsonOut, res, asked)
		}
		return writePackResult(env, cmd, *jsonOut, res, false)
	}
	return cmd
}

func newPackUpdateCmd(env Env, jsonOut *bool) *cobra.Command {
	var yes bool
	cmd := &cobra.Command{
		Use:   "update <identity> [--yes]",
		Short: "Fetch a git pack again and show what changed in its executable content",
		Long: "Fetch the ref recorded for the git pack <identity> again, by its full name, check and validate it as dotai pack\n" +
			"add does, and show what changed in executable content: every layer's .rulesync/hooks.jsonc, .rulesync/mcp.jsonc\n" +
			"and dotai/settings.yaml, dotai/tools/ and mods/. Once you confirm, or with --yes, the new clone replaces the old\n" +
			"one, which is put back on any failure. A pack added with --ref latest looks for its highest release tag again: it\n" +
			"moves to it when it is newer, or reports that it is already on the latest release. With --json and without --yes, a move to another commit fails with\n" +
			"consent-required and the plan. An unreachable remote leaves the pack as it was. Installs and projects change only\n" +
			"when dotai install or dotai apply runs again.",
		Args: cobra.ExactArgs(1),
	}
	cmd.Flags().BoolVar(&yes, "yes", false, "update without asking")
	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		home, err := os.UserHomeDir()
		if err != nil {
			return fmt.Errorf("the home directory is unknown: %w", err)
		}
		ctx, stop := interruptible(cmd)
		defer stop()
		var asked bool
		res, err := env.UpdatePack(ctx, home, args[0], app.PackGitOptions{Interactive: true, Consent: packConsent(ctx, env, *jsonOut, yes, &asked)})
		if err != nil {
			return packUpdateFailed(env, *jsonOut, asked, err)
		}
		return writePackResult(env, cmd, *jsonOut, res, asked)
	}
	return cmd
}

// packConsent is the consent of an update: --yes, or the answer to the
// prompt once the plan is shown; none with --json or without a terminal.
// asked is set once the plan was shown.
func packConsent(ctx context.Context, env Env, jsonOut, yes bool, asked *bool) func(app.PackUpdatePlan) (bool, error) {
	return func(p app.PackUpdatePlan) (bool, error) {
		if yes {
			return true, nil
		}
		if jsonOut || env.Prompt == nil {
			return false, nil
		}
		*asked = true
		if err := writeUpdatePlan(env.Stdout, p); err != nil {
			return false, err
		}
		return env.Prompt(ctx, fmt.Sprintf("Update %s from %s to %s, with the changes above? [y/N] ", termSafe(p.ID), termSafe(revision(p.From)), termSafe(revision(p.To))))
	}
}

// revision is where a source is, for people: its tag and its short commit,
// v1.3.0 (0123456789ab), or just the short commit when it is not a tag.
func revision(s app.PackSource) string {
	if tag, ok := strings.CutPrefix(s.FullRef, "refs/tags/"); ok {
		return tag + " (" + app.ShortCommit(s.Commit) + ")"
	}
	return app.ShortCommit(s.Commit)
}

// packUpdateFailed is err of an addition or an update: one the user
// refused says so; one without consent shows its plan in text.
func packUpdateFailed(env Env, jsonOut, asked bool, err error) error {
	ce, ok := errors.AsType[*app.PackConsentError](err)
	switch {
	case ok && asked:
		return errors.New("the update was cancelled, and nothing changed")
	case ok && !jsonOut:
		if werr := writeUpdatePlan(env.Stdout, ce.Plan); werr != nil {
			return errors.Join(err, werr)
		}
	}
	return err
}

// writeUpdatePlan prints the plan of an update: the move and each change
// of executable content, with its diff.
func writeUpdatePlan(w io.Writer, p app.PackUpdatePlan) error {
	var b strings.Builder
	fmt.Fprintf(&b, "update %s\nfrom:   %s (%s)\nto:     %s (%s)\n", termSafe(p.ID), termSafe(p.From.Commit), termSafe(p.From.FullRef), termSafe(p.To.Commit), termSafe(p.To.FullRef))
	if p.Missing {
		b.WriteString("the clone is gone: every file shows as added\n")
	}
	if len(p.Changes) == 0 {
		b.WriteString("no change in executable content (hooks, MCP servers, settings, tools, mods)\n")
	} else {
		fmt.Fprintf(&b, "%d changes in executable content:\n", len(p.Changes))
	}
	for _, c := range p.Changes {
		fmt.Fprintf(&b, "\n%s %s\n", c.Change, termSafe(c.Path))
		if c.Note != "" {
			fmt.Fprintf(&b, "%s (before %s, after %s)\n", c.Note, cmp.Or(c.Before, "none"), cmp.Or(c.After, "none"))
		}
		// A diff is text without control characters (app.ExecChange): it is
		// escaped all the same.
		b.WriteString(termText(c.Diff))
	}
	b.WriteString("\n")
	_, err := io.WriteString(w, b.String())
	return err
}

// writePackResult prints an addition or an update: the envelope with
// --json, or the plan of an update unless shown is set, the pack and
// where it is from, and its warnings on stderr.
func writePackResult(env Env, cmd *cobra.Command, jsonOut bool, res app.PackAddResult, shown bool) error {
	if jsonOut {
		return envelope.Write(env.Stdout, envelope.Success(commandName(cmd), res))
	}
	if res.Update != nil && res.Update.Moves() && !shown {
		if err := writeUpdatePlan(env.Stdout, *res.Update); err != nil {
			return err
		}
	}
	var b strings.Builder
	p := res.Pack
	fmt.Fprintf(&b, "%s %s (%s %s)\npath:   %s\n", res.Outcome, termSafe(p.ID), termSafe(cmp.Or(p.Name, "?")), termSafe(cmp.Or(p.Version, "?")), termSafe(p.Path))
	if s := p.Source; s != nil {
		fmt.Fprintf(&b, "source: %s\ncommit: %s (%s)\n", termSafe(s.URL), termSafe(s.Commit), termSafe(s.FullRef))
		if tracking := s.Tracking(); tracking != "" {
			if res.Outcome == app.PackUnchanged {
				tracking += ", already on the latest release"
			}
			fmt.Fprintf(&b, "tracks: %s\n", termSafe(tracking))
		}
	}
	if _, err := io.WriteString(env.Stdout, b.String()); err != nil {
		return err
	}
	writePackWarnings(env.Stderr, res.Warnings)
	return nil
}

func writePackWarnings(w io.Writer, warnings []app.PackWarning) {
	for _, warn := range warnings {
		fmt.Fprintf(w, "warning: %s\n", termSafe(warn.Message))
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
				fmt.Fprintf(&b, "removed %s (%s)\n", termSafe(e.ID), termSafe(e.Path))
			}
			if res.Deleted != "" {
				fmt.Fprintf(&b, "deleted %s\n", termSafe(res.Deleted))
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
				fmt.Fprintf(&b, "%s  %s %s\n  %s\n", termSafe(cmp.Or(e.ID, "?")), termSafe(cmp.Or(e.Name, "?")), termSafe(e.Version), termSafe(e.Path))
				if s := e.Source; s != nil {
					fmt.Fprintf(&b, "  %s at %s (%s)\n", termSafe(s.URL), termSafe(s.Commit), termSafe(s.FullRef))
					if tracking := s.Tracking(); tracking != "" {
						fmt.Fprintf(&b, "  tracks %s\n", termSafe(tracking))
					}
				}
				if e.Error != "" {
					fmt.Fprintf(&b, "  error: %s\n", termSafe(e.Error))
				}
			}
			_, err = io.WriteString(env.Stdout, b.String())
			return err
		},
	}
}

// newPackCopyCmd is dotai pack copy: the copy of the web UI's Packs board
// (decision 0046).
func newPackCopyCmd(env Env, jsonOut *bool) *cobra.Command {
	return &cobra.Command{
		Use:   "copy <pack-dir> [<to>]",
		Short: "Copy a pack to a new directory to edit",
		Long: "Copy the pack in <pack-dir>, which needs a dotai.yaml dotai can read, to <to>, a new directory whose parent\n" +
			"exists, outside the pack (default: the first free <name>-copy, <name>-copy-2 and on next to the pack). Regular\n" +
			"files and directories are copied with their permissions, .git is skipped, and a link or any other kind of file,\n" +
			"a file over 1 MiB, or more files or bytes than a copy takes, is refused before anything is written. The copy\n" +
			"is not added: edit it, then run dotai pack add --dir <to>.",
		Args: cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			from, err := filepath.Abs(args[0])
			if err != nil {
				return err
			}
			to := ""
			if len(args) == 2 {
				if to, err = filepath.Abs(args[1]); err != nil {
					return err
				}
			}
			ctx, stop := interruptible(cmd)
			defer stop()
			res, err := env.CopyPack(ctx, from, to)
			if err != nil {
				return err
			}
			if *jsonOut {
				return envelope.Write(env.Stdout, envelope.Success(commandName(cmd), res))
			}
			_, err = fmt.Fprintf(env.Stdout, "copied %s to %s, %d files\nnext:   edit it, then run dotai pack add --dir %s\n", termSafe(res.From), termSafe(res.To), res.Files, termSafe(res.To))
			return err
		},
	}
}
