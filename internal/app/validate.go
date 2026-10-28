package app

import (
	"fmt"
	"io/fs"
	"os"

	"github.com/lartweib/dotai/internal/merge"
	"github.com/lartweib/dotai/internal/pack"
)

// Diagnostic is one problem found in a pack.
type Diagnostic = pack.Diagnostic

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
	p, diags, err := pack.Load(fsys)
	if err != nil {
		return ValidateResult{}, err
	}
	res := ValidateResult{Valid: len(diags) == 0, Diagnostics: append([]pack.Diagnostic{}, diags...)}
	if p == nil {
		return res, nil
	}
	m := p.Manifest
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
	return res, nil
}
