package pack_test

import (
	"fmt"
	"reflect"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/lartweib/dotai/internal/pack"
)

// at formats where a diagnostic is and its code, without the message,
// which is text for people.
func at(diags []pack.Diagnostic) []string {
	out := []string{}
	for _, d := range diags {
		out = append(out, fmt.Sprintf("%s:%d:%d: %s", d.File, d.Line, d.Column, d.Code))
	}
	return out
}

// withCustom adds an onboarding block to the manifest of f.
func withCustom(custom string) func(fstest.MapFS) {
	return func(f fstest.MapFS) {
		f["dotai.yaml"] = file(manifest + "onboarding:\n  custom: " + custom + "\n")
	}
}

func TestLoadOnboarding(t *testing.T) {
	// The custom value of withCustom: after the manifest's 18 lines and the
	// onboarding key.
	const customLine = "dotai.yaml:20:11"
	tests := []struct {
		name   string
		mutate func(fstest.MapFS)
		want   []string
		mode   string // the onboarding the pack keeps: dotai or custom
		layer  string // the layer of a custom item
		item   pack.ItemRef
	}{
		{name: "no onboarding block keeps dotai's", mutate: func(fstest.MapFS) {}, want: []string{}, mode: pack.OnboardingDotai},
		{
			name: "a subagent of the base replaces it", mutate: withCustom("base/.rulesync/subagents/review/reviewer.md"), want: []string{},
			mode: pack.OnboardingCustom, layer: "base", item: pack.ItemRef{Kind: pack.ItemSubagent, Name: "reviewer"},
		},
		{
			name: "a skill directory of a variant replaces it",
			mutate: func(f fstest.MapFS) {
				f["variants/orca/.rulesync/skills/setup/SKILL.md"] = file("---\nname: setup\ndescription: Sets up.\n---\n")
				withCustom("variants/orca/.rulesync/skills/setup")(f)
			},
			want: []string{}, mode: pack.OnboardingCustom, layer: "orca", item: pack.ItemRef{Kind: pack.ItemSkill, Name: "setup"},
		},
		{name: "a missing path", mutate: withCustom("base/.rulesync/subagents/onboarding.md"), want: []string{customLine + ": onboarding-invalid"}, mode: pack.OnboardingCustom},
		{name: "a rule is neither a skill nor a subagent", mutate: withCustom("base/.rulesync/rules/overview.md"), want: []string{customLine + ": onboarding-invalid"}, mode: pack.OnboardingCustom},
		{name: "a skill's file, not its directory", mutate: withCustom("base/.rulesync/skills/commit/SKILL.md"), want: []string{customLine + ": onboarding-invalid"}, mode: pack.OnboardingCustom},
		{name: "a path outside the pack", mutate: withCustom("../other/base/.rulesync/subagents/a.md"), want: []string{customLine + ": onboarding-invalid"}, mode: pack.OnboardingCustom},
		{name: "an absolute path", mutate: withCustom("/base/.rulesync/subagents/review/reviewer.md"), want: []string{customLine + ": onboarding-invalid"}, mode: pack.OnboardingCustom},
		{
			name: "a profile is project scope",
			mutate: func(f fstest.MapFS) {
				f["profiles/web/.rulesync/subagents/setup.md"] = file("Setup.\n")
				withCustom("profiles/web/.rulesync/subagents/setup.md")(f)
			},
			want: []string{customLine + ": onboarding-invalid"}, mode: pack.OnboardingCustom,
		},
		{
			name: "the custom item takes the reserved name",
			mutate: func(f fstest.MapFS) {
				f["base/.rulesync/subagents/dotai-profiles.md"] = file("Mine.\n")
				withCustom("base/.rulesync/subagents/dotai-profiles.md")(f)
			},
			want: []string{customLine + ": onboarding-invalid"}, mode: pack.OnboardingCustom,
		},
		{
			name:   "a skill named dotai-profiles without custom",
			mutate: func(f fstest.MapFS) { f["base/.rulesync/skills/dotai-profiles/SKILL.md"] = file("Mine.\n") },
			want:   []string{"base/.rulesync/skills/dotai-profiles/SKILL.md:0:0: onboarding-name-reserved"}, mode: pack.OnboardingDotai,
		},
		{
			name:   "a subagent of a profile named so in another case",
			mutate: func(f fstest.MapFS) { f["profiles/web/.rulesync/subagents/Dotai-Profiles.md"] = file("Mine.\n") },
			want:   []string{"profiles/web/.rulesync/subagents/Dotai-Profiles.md:0:0: onboarding-name-reserved"}, mode: pack.OnboardingDotai,
		},
		{
			name: "the reserved name is free once the pack replaces dotai's onboarding",
			mutate: func(f fstest.MapFS) {
				f["base/.rulesync/skills/dotai-profiles/SKILL.md"] = file("Mine.\n")
				withCustom("base/.rulesync/subagents/review/reviewer.md")(f)
			},
			want: []string{}, mode: pack.OnboardingCustom, layer: "base", item: pack.ItemRef{Kind: pack.ItemSubagent, Name: "reviewer"},
		},
		{
			name:   "an unknown field",
			mutate: func(f fstest.MapFS) { f["dotai.yaml"] = file(manifest + "onboarding:\n  agent: x\n") },
			want:   []string{"dotai.yaml:19:1: field-required", "dotai.yaml:20:3: field-unknown"}, mode: pack.OnboardingDotai,
		},
		{
			name:   "not a mapping",
			mutate: func(f fstest.MapFS) { f["dotai.yaml"] = file(manifest + "onboarding: custom\n") },
			want:   []string{"dotai.yaml:19:13: field-type"}, mode: pack.OnboardingDotai,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := validPack()
			tt.mutate(f)
			p, diags, err := pack.Load(f)
			if err != nil || p == nil {
				t.Fatalf("Load() = %v, %v", p, err)
			}
			if got := at(diags); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("diagnostics = %v, want %v (%v)", got, tt.want, diags)
			}
			if got := p.Manifest.Onboarding.Mode(); got != tt.mode {
				t.Errorf("mode = %q, want %q", got, tt.mode)
			}
			l, it, ok := p.CustomOnboarding()
			if ok != (tt.layer != "") || ok && (l.Name != tt.layer || it.Ref() != tt.item) {
				t.Errorf("CustomOnboarding() = %s, %v, %v, want %s, %v", l.Name, it.Ref(), ok, tt.layer, tt.item)
			}
		})
	}
}

func TestOnboardingWarnings(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(fstest.MapFS)
		want   []string
		says   string
	}{
		{name: "dotai's onboarding", mutate: func(fstest.MapFS) {}, want: []string{}},
		{
			name: "a replacement is at the author's own risk", mutate: withCustom("base/.rulesync/subagents/review/reviewer.md"),
			want: []string{"dotai.yaml:20:11: onboarding-replaced"}, says: "base/.rulesync/subagents/review/reviewer.md",
		},
		{
			name:   "an onboarding subagent besides dotai's",
			mutate: func(f fstest.MapFS) { f["base/.rulesync/subagents/onboarding.md"] = file("Old.\n") },
			want:   []string{"base/.rulesync/subagents/onboarding.md:0:0: onboarding-duplicate"}, says: "onboarding.custom",
		},
		{
			name: "an onboarding subagent declared as custom is no duplicate",
			mutate: func(f fstest.MapFS) {
				f["base/.rulesync/subagents/onboarding.md"] = file("Old.\n")
				withCustom("base/.rulesync/subagents/onboarding.md")(f)
			},
			want: []string{"dotai.yaml:20:11: onboarding-replaced"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := validPack()
			tt.mutate(f)
			p, diags, err := pack.Load(f)
			if err != nil || len(diags) > 0 {
				t.Fatalf("Load() = %v, %v", diags, err)
			}
			w := p.Warnings()
			if got := at(w); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("Warnings() = %v, want %v", got, tt.want)
			}
			if tt.says != "" && (len(w) == 0 || !strings.Contains(w[0].Message+w[0].Hint, tt.says)) {
				t.Errorf("Warnings() = %v, want it to name %s", w, tt.says)
			}
		})
	}
}

func TestReadManifestReadsTheManifestAlone(t *testing.T) {
	f := validPack()
	withCustom("base/.rulesync/subagents/review/reviewer.md")(f)
	f["base/.rulesync/rules/broken.md"] = file("---\nroot: [\n---\n") // a layer's problem, not the manifest's
	m, diags, err := pack.ReadManifest(f)
	if err != nil || len(diags) > 0 || m == nil {
		t.Fatalf("ReadManifest() = %v, %v, %v", m, diags, err)
	}
	if m.Name != "test-pack" || len(m.Profiles) != 1 || m.Profiles[0].Hints[0] != "astro.config.*" || m.Onboarding.Custom != "base/.rulesync/subagents/review/reviewer.md" {
		t.Errorf("manifest = %+v", m)
	}
	delete(f, "dotai.yaml")
	if m, diags, err := pack.ReadManifest(f); m != nil || len(diags) != 1 || err != nil {
		t.Errorf("ReadManifest() without a manifest = %v, %v, %v", m, diags, err)
	}
}

// TestHintWarnings checks that a hint the fingerprint of decision 0045
// cannot match as its author means is a warning: one path.Match refuses,
// which matches nothing, and one with **, which path.Match reads as * in
// one path segment.
func TestHintWarnings(t *testing.T) {
	f := validPack()
	f["dotai.yaml"] = file(strings.Replace(manifest, "hints: [astro.config.*]", `hints: [astro.config.*, "[bad", "**/*.rs", src/*.go]`, 1))
	p, diags, err := pack.Load(f)
	if err != nil || len(diags) > 0 {
		t.Fatalf("Load() = %v, %v", diags, err)
	}
	w := p.Warnings()
	if got, want := at(w), []string{"dotai.yaml:11:29: profile-hint-invalid", "dotai.yaml:11:37: profile-hint-invalid"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("Warnings() = %v, want %v", got, want)
	}
	if !strings.Contains(w[0].Message, `"[bad"`) || !strings.Contains(w[0].Message, "web") || !strings.Contains(w[1].Message, "**") {
		t.Errorf("Warnings() = %v, want each to name the profile and its hint", w)
	}
}
