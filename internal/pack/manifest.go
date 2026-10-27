package pack

import (
	"io/fs"
	"path"
	"slices"

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

func decodeManifest(fsys fs.FS, r reader, root *yaml.Node) Manifest {
	o := r.object(nil, root, ManifestFile,
		"schema", "name", "version", "engine", "rulesync", "harnesses", "profiles", "variants")
	var m Manifest
	var n *yaml.Node
	if m.Schema, n = o.str("schema", true); n != nil && m.Schema != SchemaV1 {
		r.report(n, CodeSchemaUnknown, "unknown schema %q, want %q", m.Schema, SchemaV1)
	}
	m.Name, _ = o.str("name", true)
	if m.Version, n = o.str("version", true); n != nil {
		if _, err := semver.StrictNewVersion(m.Version); err != nil {
			r.report(n, CodeVersionInvalid, "version %q is not a semantic version", m.Version)
		}
	}
	if m.Engine, n = o.str("engine", true); n != nil {
		if _, err := semver.NewConstraint(m.Engine); err != nil {
			r.report(n, CodeEngineInvalid, "engine %q is not a version range: %v", m.Engine, err)
		}
	}
	if m.Rulesync, n = o.str("rulesync", true); n != nil {
		if _, err := semver.StrictNewVersion(m.Rulesync); err != nil {
			r.report(n, CodeRulesyncInvalid, "rulesync %q must be an exact version such as 41.0.0", m.Rulesync)
		}
	}
	var nodes []*yaml.Node
	m.Harnesses, nodes = o.list("harnesses", true)
	for i, h := range m.Harnesses {
		if !slices.Contains(Harnesses, h) {
			r.report(nodes[i], CodeHarnessUnknown, "unknown harness %q, want one of %v", h, Harnesses)
		}
	}

	if o.vals["profiles"] != nil {
		ps := r.object(o.keys["profiles"], o.vals["profiles"], "profiles")
		for _, name := range ps.order {
			p := r.object(ps.keys[name], ps.vals[name], "profile "+name, "path", "description", "hints")
			pr := Profile{Name: name, Path: layerPath(fsys, p)}
			pr.Description, _ = p.str("description", true)
			pr.Hints, _ = p.list("hints", false)
			m.Profiles = append(m.Profiles, pr)
		}
	}
	if o.vals["variants"] != nil {
		vs := r.object(o.keys["variants"], o.vals["variants"], "variants")
		defaults := 0
		for _, name := range vs.order {
			v := r.object(vs.keys[name], vs.vals[name], "variant "+name, "path", "default", "detect")
			va := Variant{Name: name, Path: layerPath(fsys, v), Default: v.boolean("default")}
			if v.vals["detect"] != nil {
				d := r.object(v.keys["detect"], v.vals["detect"], "detect", "command")
				va.Detect = &Detect{}
				va.Detect.Command, _ = d.str("command", true)
			}
			// A default that is not a boolean was reported; count it as meant.
			if dn := v.vals["default"]; va.Default || dn != nil && dn.Tag != "!!bool" {
				defaults++
			}
			m.Variants = append(m.Variants, va)
		}
		if len(vs.order) > 0 && defaults != 1 {
			r.report(o.keys["variants"], CodeVariantDefault, "exactly one variant must set default: true, found %d", defaults)
		}
	}
	return m
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
	if info, err := fs.Stat(fsys, clean); err != nil || !info.IsDir() {
		o.r.report(n, CodeLayerMissing, "%s: directory %q does not exist", o.what, p)
		return ""
	}
	return clean
}
