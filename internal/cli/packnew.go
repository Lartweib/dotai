package cli

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/spf13/cobra"
	"go.yaml.in/yaml/v3"

	"github.com/lartweib/dotai/internal/app"
	"github.com/lartweib/dotai/internal/envelope"
)

// newPackNewCmd is dotai pack new: the wizard of decision 0046 without
// the UI, its answers as flags or as a file.
func newPackNewCmd(env Env, jsonOut *bool) *cobra.Command {
	var name, answers string
	var harnesses, profiles, hints, mods []string
	var dryRun bool
	cmd := &cobra.Command{
		Use:   "new <dir> (--name <n> --harness a,b [--profile <p>:<description>]... | --answers <file>) [--dry-run]",
		Short: "Create a pack from dotai's template and add it",
		Long: "Write dotai's pack template into <dir>, a new directory whose parent exists or an empty one, filled in with\n" +
			"the pack's name, the harnesses it requires and its profiles, then add it as dotai pack add --dir does. The\n" +
			"template holds one commented example of each file and folder a pack holds; every example below the top is\n" +
			"template-unchanged until you edit it, and dotai install and dotai apply ask before installing it.\n" +
			"--profile <name>:<description> adds a profile, in order; --hint <profile>:<glob> and --mod <profile>:<mod> add\n" +
			"its hints and mods, and each mod gets the template's example mod. The names follow the rule for mod names:\n" +
			"lower-case letters, digits and single hyphens. --answers reads the same answers from a YAML or JSON file:\n" +
			"name, harnesses, and profiles, each with name, description, hints and mods. --dry-run prints the files and the\n" +
			"profiling skill dotai generates from the manifest, and writes nothing.",
		Args: func(cmd *cobra.Command, args []string) error {
			if len(args) != 1 {
				return errors.New("give the directory of the new pack")
			}
			if answers != "" {
				for _, f := range []string{"name", "harness", "profile", "hint", "mod"} {
					if cmd.Flags().Changed(f) {
						return fmt.Errorf("give --answers or --%s, not both", f)
					}
				}
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&name, "name", "", "the pack's name, which a local pack's identity local/<name> takes")
	cmd.Flags().StringSliceVar(&harnesses, "harness", nil, "the harnesses the pack requires, comma-separated: claudecode, antigravity-cli, opencode")
	cmd.Flags().StringArrayVar(&profiles, "profile", nil, "a profile, as <name>:<description>; repeat it for each profile")
	cmd.Flags().StringArrayVar(&hints, "hint", nil, "a hint of a profile, as <profile>:<glob>; repeat it for each hint")
	cmd.Flags().StringArrayVar(&mods, "mod", nil, "a mod of a profile, as <profile>:<mod>; repeat it for each mod")
	cmd.Flags().StringVar(&answers, "answers", "", "read the answers from this YAML or JSON file")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "print the files and the profiling skill, and write nothing")
	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		home, err := os.UserHomeDir()
		if err != nil {
			return fmt.Errorf("the home directory is unknown: %w", err)
		}
		dir, err := filepath.Abs(args[0])
		if err != nil {
			return err
		}
		var in app.PackNewInput
		if answers != "" {
			in, err = readAnswers(answers)
		} else {
			in, err = flagAnswers(name, harnesses, profiles, hints, mods)
		}
		if err != nil {
			return err
		}
		if dryRun {
			p, err := env.PlanPackNew(home, dir, in)
			if err != nil {
				return err
			}
			if *jsonOut {
				return envelope.Write(env.Stdout, envelope.Success(commandName(cmd), p))
			}
			return writeNewPlan(env.Stdout, p, "dry run, nothing was written\n")
		}
		res, err := env.NewPack(home, dir, in, app.PackNewOptions{})
		if err != nil {
			return err
		}
		if *jsonOut {
			return envelope.Write(env.Stdout, envelope.Success(commandName(cmd), res))
		}
		if err := writeNewPlan(env.Stdout, res.PackNewPlan, ""); err != nil {
			return err
		}
		a := res.Added
		if _, err := fmt.Fprintf(env.Stdout, "%s %s (%s %s)\npath:   %s\nnext:   edit the example files, then run dotai validate %s\n",
			a.Outcome, termSafe(a.Pack.ID), termSafe(a.Pack.Name), termSafe(a.Pack.Version), termSafe(a.Pack.Path), termSafe(res.Dir)); err != nil {
			return err
		}
		writePackWarnings(env.Stderr, a.Warnings)
		return nil
	}
	return cmd
}

// flagAnswers are the answers of the flags: each profile in the order
// given, then its hints and mods.
func flagAnswers(name string, harnesses, profiles, hints, mods []string) (app.PackNewInput, error) {
	in := app.PackNewInput{Name: name, Harnesses: append([]string{}, harnesses...), Profiles: []app.PackNewProfile{}}
	for _, p := range profiles {
		n, desc, ok := strings.Cut(p, ":")
		if !ok || n == "" {
			return app.PackNewInput{}, envelope.Usage(fmt.Sprintf("--profile %q: give <name>:<description>", p))
		}
		in.Profiles = append(in.Profiles, app.PackNewProfile{Name: strings.TrimSpace(n), Description: strings.TrimSpace(desc), Hints: []string{}, Mods: []string{}})
	}
	for _, f := range []struct {
		flag   string
		values []string
		add    func(*app.PackNewProfile, string)
	}{
		{"hint", hints, func(p *app.PackNewProfile, v string) { p.Hints = append(p.Hints, v) }},
		{"mod", mods, func(p *app.PackNewProfile, v string) { p.Mods = append(p.Mods, v) }},
	} {
		for _, v := range f.values {
			profile, value, ok := strings.Cut(v, ":")
			i := slices.IndexFunc(in.Profiles, func(p app.PackNewProfile) bool { return p.Name == strings.TrimSpace(profile) })
			if !ok || i < 0 || strings.TrimSpace(value) == "" {
				return app.PackNewInput{}, envelope.Usage(fmt.Sprintf("--%s %q: give <profile>:<value> for a profile of --profile", f.flag, v))
			}
			f.add(&in.Profiles[i], strings.TrimSpace(value))
		}
	}
	return in, nil
}

// readAnswers reads the answers file at path, YAML or JSON, which YAML
// reads too; an unknown field is an error.
func readAnswers(path string) (app.PackNewInput, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return app.PackNewInput{}, envelope.Usage(fmt.Sprintf("the answers file %s cannot be read: %v", path, err))
	}
	var in app.PackNewInput
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(&in); err != nil && !errors.Is(err, io.EOF) {
		return app.PackNewInput{}, envelope.Usage(fmt.Sprintf("the answers file %s is not YAML or JSON of name, harnesses and profiles: %v", path, err))
	}
	return in, nil
}

// writeNewPlan prints the plan of a new pack: where it goes, its files,
// its warnings and the profiling skill, then end.
func writeNewPlan(w io.Writer, p app.PackNewPlan, end string) error {
	var b strings.Builder
	fmt.Fprintf(&b, "new pack %s\ndir:    %s\nfiles:  %d\n", termSafe(p.ID), termSafe(p.Dir), len(p.Files))
	for _, f := range p.Files {
		fmt.Fprintf(&b, "  %s\n", termSafe(f))
	}
	for _, d := range p.Warnings {
		fmt.Fprintf(&b, "warning: %s: %s: %s\n", termSafe(d.File), d.Code, termSafe(d.Message))
	}
	b.WriteString("\nthe profiling skill dotai generates from the manifest at each install:\n\n")
	b.WriteString(termText(p.Skill))
	if end != "" {
		b.WriteString("\n" + end)
	} else {
		b.WriteString("\n")
	}
	_, err := io.WriteString(w, b.String())
	return err
}
