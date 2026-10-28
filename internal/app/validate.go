package app

import (
	"fmt"
	"io/fs"
	"os"
	"slices"
	"strings"

	"github.com/lartweib/dotai/internal/merge"
	"github.com/lartweib/dotai/internal/pack"
	"github.com/lartweib/dotai/internal/rulesync"
)

// Diagnostic is one problem found in a pack.
type Diagnostic = pack.Diagnostic

// / Location is a place in a pack, such as the earlier definition a
// diagnostic points at.
type Location = pack.Location

// CodeRulesyncUnsupported reports a rulesync version the engine has no
// checksums for.
const CodeRulesyncUnsupported = "rulesync-unsupported"

// Selection names the variant and profiles a command works on.
type Selection = merge.Selection

// ValidateResult is the outcome of validating a pack.
type ValidateResult struct {
	Valid       bool              `json:"valid"`
	Pack        *PackSummary      `json:"pack"` // nil when the manifest cannot be read
	Diagnostics []pack.Diagnostic `json:"diagnostics"`
}

// PackSummary describes a loaded pack. Lists are never nil.
type PackSummary struct {
	Name           string         `json:"name"`
	Version        string         `json:"version"`
	Engine         string         `json:"engine"`
	Rulesync       string         `json:"rulesync"`
	Harnesses      []string       `json:"harnesses"`
	Profiles       []string       `json:"profiles"`
	Variants       []string       `json:"variants"`
	DefaultVariant string         `json:"defaultVariant"`
	Layers         []LayerSummary `json:"layers"`
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
// pack with no structural diagnostics, over the layers sel names, and append
// their diagnostics after the structural ones.
func ValidateFS(fsys fs.FS, sel Selection) (ValidateResult, error) {
	res, _, err := validate(fsys, sel)
	return res, err
}

// validate is ValidateFS that also returns the loaded pack, nil when the
// manifest cannot be read.
func validate(fsys fs.FS, sel Selection) (ValidateResult, *pack.Pack, error) {
	p, diags, err := pack.Load(fsys)
	if err != nil {
		return ValidateResult{}, nil, err
	}
	res := ValidateResult{Diagnostics: append([]pack.Diagnostic{}, diags...)}
	if p == nil {
		return res, p, nil
	}
	m := p.Manifest
	res.Diagnostics = append(res.Diagnostics, checkRulesync(m, diags)...)
	res.Valid = len(res.Diagnostics) == 0
	s := &PackSummary{
		Name: m.Name, Version: m.Version, Engine: m.Engine, Rulesync: m.Rulesync,
		Harnesses: append([]string{}, m.Harnesses...),
		Profiles:  []string{}, Variants: []string{}, Layers: []LayerSummary{},
	}
	for _, pr := range m.Profiles {
		s.Profiles = append(s.Profiles, pr.Name)
	}
	for _, v := range m.Variants {
		s.Variants = append(s.Variants, v.Name)
		if v.Default {
			s.DefaultVariant = v.Name
		}
	}
	for _, l := range p.Layers {
		s.Layers = append(s.Layers, LayerSummary{Kind: string(l.Kind), Name: l.Name, Path: l.Path, Items: len(l.Items)})
	}
	res.Pack = s
	if res.Valid {
		res.Diagnostics = append(res.Diagnostics, merge.Check(p, sel)...)
		res.Valid = len(res.Diagnostics) == 0
	}
	return res, p, nil
}

// checkRulesync reports a pinned rulesync version whose checksums the
// engine does not embed (0029). A version that is not exact is already
// reported by pack.Load.
func checkRulesync(m pack.Manifest, diags []pack.Diagnostic) []pack.Diagnostic {
	invalid := slices.ContainsFunc(diags, func(d pack.Diagnostic) bool { return d.Code == pack.CodeRulesyncInvalid })
	if m.Rulesync == "" || invalid || rulesync.Supported(m.Rulesync) {
		return nil
	}
	at := m.RulesyncAt
	return []pack.Diagnostic{pack.NewDiagnostic(at.File, at.Line, at.Column, CodeRulesyncUnsupported,
		"rulesync %s is not supported by this engine", m.Rulesync).
		WithHint("pin one of: %s", strings.Join(rulesync.Versions(), ", "))}
}
