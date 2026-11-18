package app

import (
	"fmt"
	"io/fs"
	"os"
	"slices"

	"github.com/lartweib/dotai/internal/merge"
	"github.com/lartweib/dotai/internal/pack"
	"github.com/lartweib/dotai/internal/rulesync"
)

// Diagnostic is one problem found in a pack.
type Diagnostic = pack.Diagnostic

// / Location is a place in a pack, such as the earlier definition a
// diagnostic points at.
type Location = pack.Location

// Selection names the variant and profiles a command works on.
type Selection = merge.Selection

// ValidateResult is the outcome of validating a pack.
type ValidateResult struct {
	Valid       bool              `json:"valid"`
	Pack        *PackSummary      `json:"pack"` // nil when the manifest cannot be read
	Diagnostics []pack.Diagnostic `json:"diagnostics"`
	// Warnings are what may not do what the pack means but is no error,
	// such as a hook event that a tool-scoped block of hooks.jsonc takes
	// from the shared hooks. They leave the pack valid.
	Warnings []pack.Diagnostic `json:"warnings"`
}

// PackSummary describes a loaded pack. Lists are never nil.
type PackSummary struct {
	Name      string   `json:"name"`
	Version   string   `json:"version"`
	Engine    string   `json:"engine"`
	Rulesync  string   `json:"rulesync"`
	Harnesses []string `json:"harnesses"`
	// OptionalHarnesses are the harnesses an install skips when they are
	// not installed (decision 0038).
	OptionalHarnesses []string         `json:"optionalHarnesses"`
	Profiles          []ProfileSummary `json:"profiles"` // in declaration order
	Variants          []string         `json:"variants"`
	DefaultVariant    string           `json:"defaultVariant"`
	SharedSkills      []string         `json:"sharedSkills"` // the variants that share the user's skills (decision 0036)
	// Onboarding is the pack's onboarding (decision 0045): dotai's, or the
	// custom item that replaces it.
	Onboarding OnboardingSummary `json:"onboarding"`
	Layers     []LayerSummary    `json:"layers"`
}

// OnboardingSummary is the onboarding a pack declares: Mode, dotai or
// custom, and Path, the custom item as the manifest names it, nil in the
// dotai mode.
type OnboardingSummary struct {
	Mode string  `json:"mode"`
	Path *string `json:"path"`
}

// ProfileSummary describes one profile as the manifest declares it, for
// the onboarding agent that picks profiles. Path is empty when the
// manifest's path is invalid; Hints and Mods, the mods the profile
// installs in a project, are never nil.
type ProfileSummary struct {
	Name        string   `json:"name"`
	Path        string   `json:"path"`
	Description string   `json:"description"`
	Hints       []string `json:"hints"`
	Mods        []string `json:"mods"`
}

// LayerSummary describes one layer and how many items it defines.
type LayerSummary struct {
	Kind  string `json:"kind"`
	Name  string `json:"name"`
	Path  string `json:"path"`
	Items int    `json:"items"`
}

// Validate checks the pack in directory dir for the layers sel names. The
// error is set only when dir or the pack cannot be read; problems in the
// pack are diagnostics.
func Validate(dir string, sel Selection) (ValidateResult, error) {
	fsys, err := packFS(dir)
	if err != nil {
		return ValidateResult{}, err
	}
	return ValidateFS(fsys, sel)
}

// packFS returns the pack in directory dir.
func packFS(dir string) (fs.FS, error) {
	info, err := os.Stat(dir)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("%s is not a directory", dir)
	}
	return os.DirFS(dir), nil
}

// ValidateFS checks the pack whose root is fsys.
//
// Validation has two stages. Structural checks (pack.Load) come first. The
// cross-layer merge rules of 0023 (duplicate names, overrides) run only on a
// pack with no structural diagnostics other than an unsupported rulesync
// pin, over the layers sel names, and append
// their diagnostics after the structural ones, and their warnings.
func ValidateFS(fsys fs.FS, sel Selection) (ValidateResult, error) {
	res, _, err := validate(fsys, sel)
	return res, err
}

// validate is ValidateFS that also returns the loaded pack, nil when the
// manifest cannot be read.
func validate(fsys fs.FS, sel Selection) (ValidateResult, *pack.Pack, error) {
	p, diags, err := pack.LoadWith(fsys, rulesync.Versions())
	if err != nil {
		return ValidateResult{}, nil, err
	}
	res := ValidateResult{Valid: len(diags) == 0, Diagnostics: append([]pack.Diagnostic{}, diags...), Warnings: []pack.Diagnostic{}}
	if p == nil {
		return res, p, nil
	}
	m := p.Manifest
	s := &PackSummary{
		Name: m.Name, Version: m.Version, Engine: m.Engine, Rulesync: m.Rulesync,
		Harnesses: append([]string{}, m.Harnesses...), OptionalHarnesses: append([]string{}, m.OptionalHarnesses...),
		Profiles: []ProfileSummary{}, Variants: []string{}, SharedSkills: []string{}, Layers: []LayerSummary{},
		Onboarding: OnboardingSummary{Mode: m.Onboarding.Mode()},
	}
	if c := m.Onboarding.Custom; c != "" {
		s.Onboarding.Path = &c
	}
	for _, pr := range m.Profiles {
		s.Profiles = append(s.Profiles, ProfileSummary{Name: pr.Name, Path: pr.Path, Description: pr.Description, Hints: append([]string{}, pr.Hints...), Mods: append([]string{}, pr.Mods...)})
	}
	for _, v := range m.Variants {
		s.Variants = append(s.Variants, v.Name)
		if v.Default {
			s.DefaultVariant = v.Name
		}
		if v.SharedSkills {
			s.SharedSkills = append(s.SharedSkills, v.Name)
		}
	}
	for _, l := range p.Layers {
		s.Layers = append(s.Layers, LayerSummary{Kind: string(l.Kind), Name: l.Name, Path: l.Path, Items: len(l.Items)})
	}
	res.Pack = s
	res.Warnings = append(res.Warnings, p.Warnings()...)
	// An unsupported rulesync pin does not affect the layers, so the merge
	// rules still run.
	if !slices.ContainsFunc(diags, func(d pack.Diagnostic) bool { return d.Code != pack.CodeRulesyncUnsupported }) {
		res.Diagnostics = append(res.Diagnostics, merge.Check(p, sel)...)
		res.Warnings = append(res.Warnings, merge.Warnings(p, sel)...)
		res.Valid = len(res.Diagnostics) == 0
	}
	return res, p, nil
}
