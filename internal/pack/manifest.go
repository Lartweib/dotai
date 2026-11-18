package pack

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"path"
	"slices"
	"strings"

	"github.com/Masterminds/semver/v3"
	"go.yaml.in/yaml/v3"
)

// ManifestFile is the manifest's path in the pack.
const ManifestFile = "dotai.yaml"

// SchemaV1 is the only manifest schema this engine reads.
const SchemaV1 = "dotai.pack/v1"

// Harnesses are the rulesync target names the engine supports (0019): the
// ids of the harness adapters, in their order. The harness package
// imports this one, so the list cannot derive from it; a test there
// checks that the two agree.
var Harnesses = []string{"claudecode", "antigravity-cli", "opencode"}

// Manifest is the content of dotai.yaml.
type Manifest struct {
	Schema   string
	Name     string
	Version  string
	Engine   string // semver range of supported engine versions
	Rulesync string // exact rulesync version
	// Description and Author describe the pack, "" when the manifest has
	// none; the plugin manifest dotai writes for a mod carries them.
	Description, Author string
	// Harnesses are the harnesses the pack is compatible with, never
	// empty; an install picks among those detected (decision 0047).
	Harnesses []string
	// Requires are the harnesses of Harnesses the pack needs: an install
	// stops when one is missing (decisions 0038 and 0047); nil or empty
	// when it needs none in particular.
	Requires []string
	Profiles []Profile // in declaration order
	Variants []Variant // in declaration order
	// Onboarding is the onboarding block: dotai's onboarding unless it
	// names a custom item (decision 0045).
	Onboarding Onboarding

	hints []hintAt // where each profile's hints are, for Warnings
}

// hintAt is where the manifest gives the hint of a profile.
type hintAt struct {
	profile, hint string
	line, column  int
}

// Profile is a project-level layer. Mods are the mods of the pack's mods/
// it installs in the project, which only a profile names (decision 0011).
type Profile struct {
	Name, Path, Description string
	Hints                   []string
	Mods                    []string
}

// Variant is a user-level layer applied on top of the base. SharedSkills
// makes the user's shared skills folder, ~/.agents/skills, visible in the
// isolated install of the variant (decision 0036).
type Variant struct {
	Name, Path   string
	Default      bool
	Detect       *Detect
	SharedSkills bool
}

// Detect is the mechanical rule that preselects a variant.
type Detect struct {
	Command string // preselect when this command is on PATH
}

func decodeManifest(fsys fs.FS, r reader, root *yaml.Node, rulesyncVersions []string) Manifest {
	o := r.object(nil, root, ManifestFile,
		"schema", "name", "version", "description", "author", "engine", "rulesync", "harnesses", "requires", removedOptionalHarnesses, "profiles", "variants", "onboarding")
	m := decodeIdentity(r, o, rulesyncVersions)
	m.Harnesses = decodeHarnesses(r, o, "harnesses", true, nil)
	m.Requires = decodeHarnesses(r, o, "requires", false, m.Harnesses)
	if k := o.keys[removedOptionalHarnesses]; k != nil {
		*r.diags = append(*r.diags, NewDiagnostic(r.file, k.Line+r.offset, k.Column, CodeFieldUnknown,
			"%s: unknown field %q: decision 0047 removed it", o.what, removedOptionalHarnesses).
			WithHint("list every harness the pack is compatible with in harnesses, and the ones it cannot do without in requires"))
	}
	m.Profiles, m.hints = decodeProfiles(fsys, r, o)
	m.Onboarding = decodeOnboarding(r, o)
	var declared int
	m.Variants, declared = decodeVariants(fsys, r, o)
	if len(m.Variants) > 0 && declared != 1 {
		r.report(o.keys["variants"], CodeVariantDefault, "exactly one variant must set default: true, found %d", declared)
	}
	return m
}

// decodeIdentity reads the scalar fields: schema, identity and versions. A
// rulesync pin outside rulesyncVersions is reported unless the list is nil.
func decodeIdentity(r reader, o object, rulesyncVersions []string) Manifest {
	var m Manifest
	var n *yaml.Node
	if m.Schema, n = o.str("schema", true); n != nil && m.Schema != SchemaV1 {
		r.report(n, CodeSchemaUnknown, "unknown schema %q, want %q", m.Schema, SchemaV1)
	}
	m.Name, _ = o.str("name", true)
	m.Description, _ = o.str("description", false)
	m.Author, _ = o.str("author", false)
	if m.Version, n = o.str("version", true); n != nil && !isExactVersion(m.Version) {
		r.report(n, CodeVersionInvalid, "version %q is not a semantic version", m.Version)
	}
	if m.Engine, n = o.str("engine", true); n != nil {
		if _, err := semver.NewConstraint(m.Engine); err != nil {
			r.report(n, CodeEngineInvalid, "engine %q is not a version range", m.Engine)
		}
	}
	if m.Rulesync, n = o.str("rulesync", true); n != nil && !isExactVersion(m.Rulesync) {
		r.report(n, CodeRulesyncInvalid, "rulesync %q must be an exact version such as 43.9.2", m.Rulesync)
	} else if n != nil && rulesyncVersions != nil && !slices.Contains(rulesyncVersions, m.Rulesync) {
		*r.diags = append(*r.diags, NewDiagnostic(r.file, n.Line+r.offset, n.Column, CodeRulesyncUnsupported,
			"rulesync %s is not supported by this engine", m.Rulesync).
			WithHint("pin one of: %s", strings.Join(rulesyncVersions, ", ")))
	}
	return m
}

func isExactVersion(v string) bool {
	_, err := semver.StrictNewVersion(v)
	return err == nil
}

// removedOptionalHarnesses is the manifest field of decision 0038 that
// decision 0047 removed: a manifest that has it gets a field-unknown
// diagnostic with a hint of what replaces it.
const removedOptionalHarnesses = "optional-harnesses"

// decodeHarnesses reads the list of harnesses at key: known harnesses,
// each once, and, when compatible is not nil, each one of compatible.
func decodeHarnesses(r reader, o object, key string, required bool, compatible []string) []string {
	hs, nodes := o.list(key, required)
	for i, h := range hs {
		switch {
		case !slices.Contains(Harnesses, h):
			r.report(nodes[i], CodeHarnessUnknown, "unknown harness %q, want one of %v", h, Harnesses)
		case slices.Contains(hs[:i], h):
			r.report(nodes[i], CodeHarnessDuplicate, "harness %q is given twice in %s", h, key)
		case compatible != nil && !slices.Contains(compatible, h):
			r.report(nodes[i], CodeHarnessNotCompatible, "harness %q is in %s but not in harnesses, the harnesses the pack is compatible with", h, key)
		}
	}
	return hs
}

func decodeProfiles(fsys fs.FS, r reader, o object) ([]Profile, []hintAt) {
	if !o.has("profiles") {
		return nil, nil
	}
	var out []Profile
	var hints []hintAt
	ps := r.object(o.keys["profiles"], o.vals["profiles"], "profiles")
	for _, name := range ps.order {
		p := r.object(ps.keys[name], ps.vals[name], "profile "+name, "path", "description", "hints", "mods")
		pr := Profile{Name: name, Path: layerPath(fsys, p)}
		pr.Description, _ = p.str("description", true)
		var nodes []*yaml.Node
		pr.Hints, nodes = p.list("hints", false)
		for i, n := range nodes {
			hints = append(hints, hintAt{profile: name, hint: pr.Hints[i], line: n.Line + r.offset, column: n.Column})
		}
		pr.Mods = decodeMods(fsys, p)
		out = append(out, pr)
	}
	return out, hints
}

// CodeProfileHintInvalid is the warning of a profile hint that the
// fingerprint of decision 0045 cannot match as its author means: one that
// path.Match refuses, which matches nothing, or one with **, which
// path.Match reads as * within one path segment, not across directories.
const CodeProfileHintInvalid = "profile-hint-invalid"

// hintWarnings are the warnings of the hints of m.
func hintWarnings(m Manifest) []Diagnostic {
	var out []Diagnostic
	for _, h := range m.hints {
		switch _, err := path.Match(h.hint, ""); {
		case err != nil:
			out = append(out, NewDiagnostic(ManifestFile, h.line, h.column, CodeProfileHintInvalid,
				"profile %s: hint %q is not a valid pattern, so it matches no file", h.profile, h.hint).
				WithHint("write a glob as Go's path.Match reads it, matched against the whole path from the top of the repository, such as package.json or src/*.go"))
		case strings.Contains(h.hint, "**"):
			out = append(out, NewDiagnostic(ManifestFile, h.line, h.column, CodeProfileHintInvalid,
				"profile %s: hint %q holds **, which matches within one path segment only, as *, and never across directories", h.profile, h.hint).
				WithHint("name the path from the top of the repository, one * per segment, such as src/*.rs, or give one hint per depth"))
		}
	}
	return out
}

// decodeVariants returns the variants and how many of them declare a
// default. A default with an invalid value is reported and still counts as
// declared, so it does not also cause a variant-default diagnostic.
func decodeVariants(fsys fs.FS, r reader, o object) (out []Variant, declared int) {
	if !o.has("variants") {
		return nil, 0
	}
	vs := r.object(o.keys["variants"], o.vals["variants"], "variants")
	for _, name := range vs.order {
		v := r.object(vs.keys[name], vs.vals[name], "variant "+name, "path", "default", "detect", "shared-skills", "mods")
		va := Variant{Name: name, Path: layerPath(fsys, v)}
		if v.has("mods") {
			// A variant applies at user level, where a mod would load in every
			// project (decision 0011).
			r.report(v.keys["mods"], CodeModScope, "%s: mods are project-scoped, so only a profile names them", v.what)
		}
		var valid bool
		va.Default, valid = v.boolean("default")
		if va.Default || v.has("default") && !valid {
			declared++
		}
		va.SharedSkills, _ = v.boolean("shared-skills")
		if v.has("detect") {
			d := r.object(v.keys["detect"], v.vals["detect"], "detect", "command")
			va.Detect = &Detect{}
			va.Detect.Command, _ = d.str("command", true)
		}
		out = append(out, va)
	}
	return out, declared
}

// layerPath returns the declared path of a profile or variant, or "" when it
// is missing, invalid or not a directory in fsys.
func layerPath(fsys fs.FS, o object) string {
	p, n := o.str("path", true)
	if n == nil {
		return ""
	}
	clean := path.Clean(p)
	if !fs.ValidPath(clean) || clean == "." {
		o.r.report(n, CodeLayerPathInvalid, "%s: path %q must be relative and inside the pack", o.what, p)
		return ""
	}
	info, link, err := lstat(fsys, clean)
	if err != nil {
		o.r.report(n, CodeLayerMissing, "%s: directory %q does not exist", o.what, p)
		return ""
	}
	if code, msg := entryProblem(clean, link, info.Mode(), true); code != "" {
		o.r.report(n, code, "%s: %s", o.what, msg)
		return ""
	}
	return clean
}

// Identity reads only the name and the version of the manifest of the pack
// in fsys, without loading its layers, for a list of packs.
func Identity(fsys fs.FS) (name, version string, err error) {
	f, err := fsys.Open(ManifestFile)
	if err != nil {
		return "", "", err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, maxFileSize+1))
	if err != nil {
		return "", "", err
	}
	if len(data) > maxFileSize {
		return "", "", fmt.Errorf("%s is larger than 1 MiB", ManifestFile)
	}
	var id struct {
		Name    string `yaml:"name"`
		Version string `yaml:"version"`
	}
	if err := yaml.Unmarshal(data, &id); err != nil {
		return "", "", fmt.Errorf("%s: %w", ManifestFile, err)
	}
	if id.Name == "" {
		return "", "", fmt.Errorf("%s has no name", ManifestFile)
	}
	return id.Name, id.Version, nil
}

// ReadManifest decodes the manifest of the pack in fsys without loading
// its layers, for a reader that needs only the manifest and must be quick,
// such as the session-start hook of decision 0045. It reports what Load
// reports of the manifest alone; the manifest is nil when it cannot be
// used.
func ReadManifest(fsys fs.FS) (*Manifest, []Diagnostic, error) {
	src, d, err := readRegular(fsys, ManifestFile)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return nil, []Diagnostic{NewDiagnostic(ManifestFile, 0, 0, CodeManifestMissing, "the pack has no %s", ManifestFile)}, nil
	case err != nil:
		return nil, nil, err
	case d != nil:
		return nil, []Diagnostic{*d}, nil
	}
	var diags []Diagnostic
	r := reader{file: ManifestFile, diags: &diags}
	root, ok := r.parse(normalize(src), CodeYAMLInvalid)
	if !ok {
		return nil, diags, nil
	}
	m := decodeManifest(fsys, r, root, nil)
	sortFrom(diags, 0)
	return &m, diags, nil
}
