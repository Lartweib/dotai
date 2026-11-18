package app

import (
	"encoding/json"
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/lartweib/dotai/internal/harness"
	"github.com/lartweib/dotai/internal/merge"
	"github.com/lartweib/dotai/internal/pack"
	"github.com/lartweib/dotai/internal/plan"
)

// The warnings of the onboarding in a stage and an install plan (decision
// 0045): a pack that replaces dotai's onboarding, and a harness that does
// not get the session-start hook.
const (
	CodeOnboardingReplaced    = pack.CodeOnboardingReplaced
	CodeOnboardingHookSkipped = "onboarding-hook-skipped"
)

// OnboardingBinary is the dotai binary the session-start hook of decision
// 0045 runs: Path, absolute, or Err, why it is unknown.
type OnboardingBinary struct {
	Path string
	Err  error
}

// profilesDescription is the description of the profiling skill, the only
// part of it that is always in a harness's context: at most 60 tokens
// (decision 0045).
const profilesDescription = "Proposes the dotai profiles that fit this repository and applies them after the user approves. " +
	"Use when the session says the repository is not set up, or that its profiles may no longer fit."

// ProfilesSkill is the SKILL.md of the profiling skill dotai generates
// from the pack p (decision 0045): the fixed procedure, then each profile
// in declaration order with its description, hints and mods.
func ProfilesSkill(p PackSummary) []byte {
	var b strings.Builder
	fmt.Fprintf(&b, "---\nname: %s\ndescription: %s\n---\n\n", pack.ProfilesSkill, profilesDescription)
	fmt.Fprintf(&b, "# Set up this repository with the profiles of %s\n\n", oneLine(p.Name))
	b.WriteString("dotai generated this skill from the manifest of the pack " + oneLine(p.Name) + " " + oneLine(p.Version) +
		", and `dotai install` writes it again. dotai never picks a profile: you propose, the user approves, and `dotai apply` writes.\n\n")
	b.WriteString(`## Procedure

1. Use only the profiles listed below, by their names. Never invent one.
2. When ` + "`.dotai/state.json`" + ` exists at the top of the repository, profiles were applied already: follow "Profiles applied" below instead.
3. Look at the working tree, tracked and untracked files, without ` + "`.git/`" + ` and ` + "`.dotai/`" + `. When it holds nothing besides a README, a license or ignore files, the repository is empty: ask the user what the project will be, and propose profiles from the answers.
4. Otherwise, detect the stack by content: read the manifests, the configuration and the source files. A profile's hints are evidence, never proof. Ask the user only what the analysis leaves open.
5. Show the plan of the profiles you propose, comma-separated: ` + "`dotai apply --profile <a,b> --dry-run`" + `.
6. Run ` + "`dotai apply --profile <a,b>`" + ` only after the user approves that plan. Add ` + "`--yes`" + ` only when the plan lists conflicts and the user approved overwriting them.
7. Never commit: what dotai writes stays out of git.
8. When the user wants no profiles here, run ` + "`dotai onboarding dismiss`" + `, so that new sessions stop asking.

## Profiles applied

` + "`.dotai/state.json`" + ` lists the profiles applied. Analyze the repository again, as in steps 3 and 4, and report each mismatch: a profile gone from the pack, one the content now suggests, or one it no longer supports. Only report and propose: applying needs the same plan and approval as above. When the user keeps the profiles as they are, run ` + "`dotai onboarding dismiss`" + `.

`)
	fmt.Fprintf(&b, "## Profiles of %s\n", oneLine(p.Name))
	if len(p.Profiles) == 0 {
		b.WriteString("\nThe pack has no profiles: there is nothing to apply.\n")
	}
	for _, pr := range p.Profiles {
		fmt.Fprintf(&b, "\n### %s\n\n%s\n", oneLine(pr.Name), oneLine(pr.Description))
		if len(pr.Hints) > 0 {
			b.WriteString("\n- Hints: " + codeList(pr.Hints) + "\n")
		}
		if len(pr.Mods) > 0 {
			if len(pr.Hints) == 0 {
				b.WriteString("\n")
			}
			b.WriteString("- Mods: " + codeList(pr.Mods) + "\n")
		}
	}
	return []byte(b.String())
}

// oneLine is s, the pack's text, on one line.
func oneLine(s string) string { return strings.Join(strings.Fields(s), " ") }

// codeList is each of vs as inline code, comma-separated, without the
// backticks a value holds.
func codeList(vs []string) string {
	out := make([]string, len(vs))
	for i, v := range vs {
		out[i] = "`" + strings.ReplaceAll(oneLine(v), "`", "") + "`"
	}
	return strings.Join(out, ", ")
}

// hookCommand is the command a harness's session-start hook runs: the
// dotai binary bin, as the harness id reads a command on goos, then
// onboarding hook --harness <id>. A harness that runs the command through
// a shell gets the path in double quotes, with forward slashes, which
// bash and Bun Shell both run; one that passes quotes on, as agy does
// through cmd /c, gets it bare in its own separators, and a path with
// whitespace or a character a bare path cannot hold is refused for it, as
// a quoted one with a character the quotes do not protect, or a network
// path on Windows, is for every harness.
func hookCommand(bin, goos, id string) (string, error) {
	if goos == "windows" && (strings.HasPrefix(bin, `\\`) || strings.HasPrefix(bin, "//")) {
		return "", fmt.Errorf("the dotai binary %s starts with \\\\ or //, as a network path does, which a harness's shell does not read as written", bin)
	}
	suffix := " onboarding hook --harness " + id
	if quotingOf(id) == harness.Unquoted {
		if i := strings.IndexFunc(bin, func(r rune) bool { return unicode.IsSpace(r) || strings.ContainsRune(bareUnsafe, r) }); i >= 0 {
			r, _ := utf8.DecodeRuneInString(bin[i:])
			return "", fmt.Errorf("%s runs a hook through cmd /c and passes quotes on, so the path of the dotai binary is bare, and %s holds %q, which a bare path cannot hold", id, bin, r)
		}
		if goos == "windows" {
			return strings.ReplaceAll(bin, "/", `\`) + suffix, nil
		}
		return bin + suffix, nil
	}
	if i := strings.IndexAny(bin, unquoted(goos)); i >= 0 {
		return "", fmt.Errorf("the dotai binary %s holds %q, which a harness's shell would not take literally inside double quotes", bin, bin[i])
	}
	return `"` + strings.ReplaceAll(bin, `\`, "/") + `"` + suffix, nil
}

// hookEntry is the sessionStart entry of the hooks file that runs command
// for the harness id: Claude Code's runs at a new session only, not at a
// resumed or compacted one, which agy and OpenCode cannot tell apart.
func hookEntry(id, command string) json.RawMessage {
	entry := map[string]string{"command": command}
	if id == "claudecode" {
		entry["matcher"] = "startup"
	}
	data, err := marshalNoEscape(entry)
	if err != nil {
		panic(err) // a map of strings always marshals
	}
	return data
}

// marshalNoEscape is json.Marshal without HTML escaping, so that a
// command keeps its & and > as written.
func marshalNoEscape(v any) ([]byte, error) {
	var b strings.Builder
	e := json.NewEncoder(&b)
	e.SetEscapeHTML(false)
	if err := e.Encode(v); err != nil {
		return nil, err
	}
	return []byte(strings.TrimSuffix(b.String(), "\n")), nil
}

// onboardingEngine is the engine layer an install stages for the pack p,
// whose summary is s, for harnesses (decision 0045), and the warnings of
// its plan. A pack that replaces dotai's onboarding gets no engine layer,
// and a warning that names its item; otherwise every harness gets the
// profiling skill, and each one whose command can name bin, the hook.
func onboardingEngine(p *pack.Pack, s PackSummary, harnesses []string, bin OnboardingBinary, goos string) (*merge.Engine, []plan.Warning) {
	if l, it, ok := p.CustomOnboarding(); ok {
		msg := fmt.Sprintf("the pack %s replaces dotai's onboarding with its %s %s: dotai installs neither the profiling skill %s nor its session-start hook, which propose profiles from the manifest and apply them only with your approval; the pack's onboarding is at its author's own risk",
			s.Name, it.Kind, it.Path, pack.ProfilesSkill)
		if l.Kind == pack.LayerVariant {
			msg += fmt.Sprintf(", and it comes only with the variant %s", l.Name)
		}
		return nil, []plan.Warning{{Code: CodeOnboardingReplaced, Item: it.Path, Message: msg}}
	}
	if p.Manifest.Onboarding.Custom != "" {
		return nil, nil // an invalid custom item, which validate reports
	}
	e := &merge.Engine{Skill: ProfilesSkill(s)}
	var warnings []plan.Warning
	for _, id := range harnesses {
		cmd, err := bin.Path, bin.Err
		if err == nil {
			cmd, err = hookCommand(bin.Path, goos, id)
		}
		if err != nil {
			warnings = append(warnings, plan.Warning{Code: CodeOnboardingHookSkipped, Harness: id, Item: pack.ProfilesSkill,
				Message: fmt.Sprintf("%s gets the profiling skill %s but not the session-start hook that tells a session when to load it: %v; install dotai at a path without such characters, then install again", id, pack.ProfilesSkill, err)})
			continue
		}
		e.Hooks = append(e.Hooks, merge.BlockHook{Block: id, Entry: hookEntry(id, cmd)})
	}
	return e, warnings
}
