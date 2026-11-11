package cli_test

import (
	"bytes"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/lartweib/dotai/internal/cli"
)

// TestTheOnboardingTemplateNamesOnlyRealCommands checks that every dotai
// command the example pack's onboarding agent tells an agent to run exists,
// with every flag it names: the agent runs them as written.
func TestTheOnboardingTemplateNamesOnlyRealCommands(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "examples", "minimal-pack", "base", ".rulesync", "subagents", "onboarding.md"))
	if err != nil {
		t.Fatal(err)
	}
	uses := codeCommands(string(data))
	if len(uses) < 4 {
		t.Fatalf("found %d dotai commands in the template, want the ones it runs", len(uses))
	}
	word := regexp.MustCompile(`^[a-z][a-z-]*$`)
	for _, u := range uses {
		var path, flags []string
		for _, w := range strings.Fields(u) {
			switch {
			case strings.HasPrefix(w, "--"):
				flags = append(flags, strings.SplitN(w, "=", 2)[0])
			// The command, and a subcommand of rulesync, the one command
			// that has subcommands; the rest are arguments.
			case len(flags) == 0 && word.MatchString(w) && (len(path) == 0 || path[0] == "rulesync"):
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
