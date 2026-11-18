package pack

import (
	"io/fs"
	"path"
	"strings"
)

// The onboarding of a pack (decision 0045): dotai's own, the profiling
// skill and the session-start hook an install generates, or the pack's
// custom item that replaces both.
const (
	OnboardingDotai  = "dotai"
	OnboardingCustom = "custom"
)

// ProfilesSkill is the name of the profiling skill dotai generates, which
// no item of a pack may take unless the pack replaces dotai's onboarding.
const ProfilesSkill = "dotai-profiles"

// legacyOnboarding is the name of the onboarding subagent the example pack
// shipped before decision 0045.
const legacyOnboarding = "onboarding"

// Diagnostic codes of the onboarding.
const (
	CodeOnboardingInvalid      = "onboarding-invalid"
	CodeOnboardingNameReserved = "onboarding-name-reserved"
)

// Warning codes of the onboarding.
const (
	CodeOnboardingReplaced  = "onboarding-replaced"
	CodeOnboardingDuplicate = "onboarding-duplicate"
)

// Onboarding is the onboarding block of the manifest. Custom is the path,
// relative to the pack root, of the subagent file or skill directory that
// replaces dotai's onboarding, "" for none; Line and Column are where the
// manifest gives it.
type Onboarding struct {
	Custom       string
	Line, Column int
}

// Mode is the onboarding the manifest declares: OnboardingCustom when it
// names a custom item, valid or not, and OnboardingDotai otherwise.
func (o Onboarding) Mode() string {
	if o.Custom != "" {
		return OnboardingCustom
	}
	return OnboardingDotai
}

// decodeOnboarding reads the optional onboarding block of the manifest.
func decodeOnboarding(r reader, o object) Onboarding {
	if !o.has("onboarding") {
		return Onboarding{}
	}
	b := r.object(o.keys["onboarding"], o.vals["onboarding"], "onboarding", "custom")
	custom, n := b.str("custom", true)
	if n == nil {
		return Onboarding{}
	}
	return Onboarding{Custom: custom, Line: n.Line, Column: n.Column}
}

// customOnboarding is where the custom item of the onboarding is in the
// pack's layers.
type customOnboarding struct {
	layer, item int
}

// checkOnboarding resolves the custom item of the onboarding, or reports
// why it cannot be one, and reports each skill or subagent that takes the
// name of dotai's profiling skill while dotai's onboarding is kept.
func checkOnboarding(p *Pack, diags *[]Diagnostic) {
	o := p.Manifest.Onboarding
	if o.Custom == "" {
		for _, l := range p.Layers {
			for _, it := range l.Items {
				if (it.Kind == ItemSkill || it.Kind == ItemSubagent) && strings.EqualFold(it.Name, ProfilesSkill) {
					*diags = append(*diags, NewDiagnostic(it.Path, 0, 0, CodeOnboardingNameReserved, "%s %q takes the name of dotai's profiling skill", it.Kind, it.Name).
						WithHint("rename it, or declare it as onboarding.custom in %s to replace dotai's onboarding", ManifestFile))
				}
			}
		}
		return
	}
	invalid := func(format string, args ...any) {
		*diags = append(*diags, NewDiagnostic(ManifestFile, o.Line, o.Column, CodeOnboardingInvalid, format, args...).
			WithHint("name a file under <layer>/.rulesync/subagents/ or a directory under <layer>/.rulesync/skills/, where <layer> is base/ or a variant's directory"))
	}
	clean := path.Clean(o.Custom)
	if !fs.ValidPath(clean) || clean != o.Custom {
		invalid("onboarding.custom %q must be a clean path relative to the pack root", o.Custom)
		return
	}
	for li, l := range p.Layers {
		if l.Kind == LayerProfile {
			continue
		}
		rest, ok := strings.CutPrefix(clean, l.Path+"/"+rulesyncDir+"/")
		if !ok {
			continue
		}
		for ii, it := range l.Items {
			if !(it.Kind == ItemSubagent && it.Path == clean || it.Kind == ItemSkill && rest == "skills/"+it.Name) {
				continue
			}
			if strings.EqualFold(it.Name, ProfilesSkill) {
				invalid("onboarding.custom %s is named %s, the name of dotai's profiling skill", o.Custom, ProfilesSkill)
				return
			}
			p.custom = &customOnboarding{layer: li, item: ii}
			return
		}
	}
	invalid("onboarding.custom %s is not a subagent or a skill of the base or a variant", o.Custom)
}

// CustomOnboarding is the item that replaces dotai's onboarding and its
// layer, the base or a variant, when the manifest names one that is
// valid.
func (p *Pack) CustomOnboarding() (Layer, Item, bool) {
	if p.custom == nil {
		return Layer{}, Item{}, false
	}
	l := p.Layers[p.custom.layer]
	return l, l.Items[p.custom.item], true
}

// Warnings are what the pack's manifest and layers may not do as its
// author means but is no error: a custom item that replaces dotai's
// onboarding, at the pack author's own risk, and an onboarding subagent
// shipped besides dotai's onboarding, as packs did before decision 0045.
func (p *Pack) Warnings() []Diagnostic {
	out := []Diagnostic{}
	if _, it, ok := p.CustomOnboarding(); ok {
		o := p.Manifest.Onboarding
		out = append(out, NewDiagnostic(ManifestFile, o.Line, o.Column, CodeOnboardingReplaced,
			"%s %q replaces dotai's onboarding: dotai generates neither its profiling skill %s nor its session-start hook. dotai's onboarding proposes profiles from the manifest and applies them only with the user's approval; a replacement is at the pack author's own risk", it.Kind, it.Path, ProfilesSkill))
		return out
	}
	if p.Manifest.Onboarding.Custom != "" {
		return out
	}
	for _, l := range p.Layers {
		for _, it := range l.Items {
			if it.Kind == ItemSubagent && strings.EqualFold(it.Name, legacyOnboarding) {
				out = append(out, NewDiagnostic(it.Path, 0, 0, CodeOnboardingDuplicate, "subagent %q is an onboarding of the pack's own besides dotai's, which the install generates from the manifest's profiles", it.Name).
					WithHint("remove it, or declare it as onboarding.custom in %s to replace dotai's onboarding", ManifestFile))
			}
		}
	}
	return out
}
