package pack

import (
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

// Harnesses are the rulesync target names the engine supports (0019).
var Harnesses = []string{"claudecode", "antigravity-cli", "opencode"}

// Manifest is the content of dotai.yaml.
type Manifest struct {
	Schema    string
	Name      string
	Version   string
	Engine    string // semver range of supported engine versions
	Rulesync  string // exact rulesync version
	Harnesses []string
	Profiles  []Profile // in declaration order
	Variants  []Variant // in declaration order
}

// Profile is a project-level layer.
type Profile struct {
	Name, Path, Description string
	Hints                   []string
}

// Variant is a user-level layer applied on top of the base.
type Variant struct {
	Name, Path string
	Default    bool
	Detect     *Detect
}

// Detect is the mechanical rule that preselects a variant.
type Detect struct {
	Command string // preselect when this command is on PATH
}

func decodeManifest(fsys fs.FS, r reader, root *yaml.Node, rulesyncVersions []string) Manifest {
	o := r.object(nil, root, ManifestFile,
		"schema", "name", "version", "engine", "rulesync", "harnesses", "profiles", "variants")
	m := decodeIdentity(r, o, rulesyncVersions)
	m.Harnesses = decodeHarnesses(r, o)
	m.Profiles = decodeProfiles(fsys, r, o)
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
	if m.Version, n = o.str("version", true); n != nil && !isExactVersion(m.Version) {
		r.report(n, CodeVersionInvalid, "version %q is not a semantic version", m.Version)
	}
	if m.Engine, n = o.str("engine", true); n != nil {
		if _, err := semver.NewConstraint(m.Engine); err != nil {
			r.report(n, CodeEngineInvalid, "engine %q is not a version range", m.Engine)
		}
	}
	if m.Rulesync, n = o.str("rulesync", true); n != nil && !isExactVersion(m.Rulesync) {
		r.report(n, CodeRulesyncInvalid, "rulesync %q must be an exact version such as 41.0.0", m.Rulesync)
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

func decodeHarnesses(r reader, o object) []string {
	hs, nodes := o.list("harnesses", true)
	for i, h := range hs {
		if !slices.Contains(Harnesses, h) {
			r.report(nodes[i], CodeHarnessUnknown, "unknown harness %q, want one of %v", h, Harnesses)
		}
	}
	return hs
}

func decodeProfiles(fsys fs.FS, r reader, o object) []Profile {
	if !o.has("profiles") {
		return nil
	}
	var out []Profile
	ps := r.object(o.keys["profiles"], o.vals["profiles"], "profiles")
	for _, name := range ps.order {
		p := r.object(ps.keys[name], ps.vals[name], "profile "+name, "path", "description", "hints")
		pr := Profile{Name: name, Path: layerPath(fsys, p)}
		pr.Description, _ = p.str("description", true)
		pr.Hints, _ = p.list("hints", false)
		out = append(out, pr)
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
		v := r.object(vs.keys[name], vs.vals[name], "variant "+name, "path", "default", "detect")
		va := Variant{Name: name, Path: layerPath(fsys, v)}
		var valid bool
		va.Default, valid = v.boolean("default")
		if va.Default || v.has("default") && !valid {
			declared++
		}
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
