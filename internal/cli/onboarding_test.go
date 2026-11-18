package cli_test

import (
	"bytes"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/lartweib/dotai/internal/app"
	"github.com/lartweib/dotai/internal/cli"
)

// TestTheProfilesSkillNamesOnlyRealCommands checks that every dotai
// command the profiling skill dotai generates (decision 0045) tells an
// agent to run exists, with every flag it names: the agent runs them as
// written.
func TestTheProfilesSkillNamesOnlyRealCommands(t *testing.T) {
	res, err := app.Validate(examplePack, app.Selection{})
	if err != nil || res.Pack == nil {
		t.Fatalf("Validate() = %+v, %v", res, err)
	}
	checkCommands(t, string(app.ProfilesSkill(*res.Pack)), 3)
}

// commandsWithSubcommands are the commands whose second word is a
// subcommand; the rest are arguments.
var commandsWithSubcommands = []string{"rulesync", "onboarding", "pack"}

// checkCommands checks that md names at least `least` dotai commands in its
// code, and that each exists with every flag it names.
func checkCommands(t *testing.T, md string, least int) {
	t.Helper()
	uses := codeCommands(md)
	if len(uses) < least {
		t.Fatalf("found %d dotai commands in the text, want the ones it runs", len(uses))
	}
	word := regexp.MustCompile(`^[a-z][a-z-]*$`)
	for _, u := range uses {
		var path, flags []string
		for _, w := range strings.Fields(u) {
			switch {
			case strings.HasPrefix(w, "--"):
				flags = append(flags, strings.SplitN(w, "=", 2)[0])
			case len(flags) == 0 && word.MatchString(w) && (len(path) == 0 || len(path) == 1 && slices.Contains(commandsWithSubcommands, path[0])):
				path = append(path, w)
			}
		}
		var stdout, stderr bytes.Buffer
		if code := cli.Run(append(append([]string{}, path...), "--help"), testEnv(&stdout, &stderr)); code != cli.ExitOK {
			t.Errorf("dotai %s: %q is not a command (exit %d, %s)", u, strings.Join(path, " "), code, stderr.String())
			continue
		}
		help := stdout.String()
		if !strings.Contains(help, "dotai "+strings.Join(path, " ")) {
			t.Errorf("dotai %s: the help of %q is not its own:\n%s", u, strings.Join(path, " "), help)
		}
		for _, f := range flags {
			if !strings.Contains(help, f+" ") {
				t.Errorf("dotai %s: %s has no flag %s", u, strings.Join(path, " "), f)
			}
		}
	}
}

// codeCommands lists the dotai commands in the code of a Markdown text, its
// inline code spans and the lines of its fenced blocks, without "dotai ".
func codeCommands(md string) []string {
	const tick = "`"
	var code []string
	fenced := false
	for line := range strings.SplitSeq(md, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, tick+tick+tick) {
			fenced = !fenced
			continue
		}
		if fenced {
			code = append(code, line)
			continue
		}
		for i, part := range strings.Split(line, tick) {
			if i%2 == 1 {
				code = append(code, part)
			}
		}
	}
	var out []string
	for _, c := range code {
		if rest, ok := strings.CutPrefix(c, "dotai "); ok {
			out = append(out, rest)
		}
	}
	return out
}
