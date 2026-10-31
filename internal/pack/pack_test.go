package pack_test

import (
	"fmt"
	"io/fs"
	"reflect"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/lartweib/dotai/internal/pack"
)

const manifest = `schema: dotai.pack/v1
name: test-pack
version: 0.1.0
engine: ">=0.1.0"
rulesync: 41.0.0
harnesses: [claudecode, opencode]
profiles:
  web:
    path: profiles/web
    description: Web projects.
    hints: [astro.config.*]
variants:
  standalone:
    path: variants/standalone
    default: true
  orca:
    path: variants/orca
    detect: { command: orca }
`

const subagent = `---
name: reviewer
description: Reviews a diff.
claudecode:
  model: sonnet
dotai:
  capabilities: [git]
---
Body.
`

// validPack is a small pack with no problems. Each test case mutates a
// fresh copy.
func validPack() fstest.MapFS {
	return fstest.MapFS{
		"dotai.yaml":                                  file(manifest),
		"base/.rulesync/rules/overview.md":            file("---\nroot: true\n---\n# Overview\n"),
		"base/.rulesync/skills/commit/SKILL.md":       file("---\nname: commit\ndescription: Commits.\n---\n"),
		"base/.rulesync/skills/commit/notes.md":       file("Not an item.\n"),
		"base/.rulesync/subagents/review/reviewer.md": file(subagent),
		"base/dotai/settings.yaml":                    file("claudecode:\n  model: sonnet\n"),
		"profiles/web/.rulesync/commands/deploy.md":   file("No frontmatter.\n"),
		"variants/standalone/.rulesync/rules/solo.md": file("---\ndotai:\n  overrides: true\n---\n"),
		"variants/orca":                               {Mode: fs.ModeDir},
	}
}

func file(s string) *fstest.MapFile { return &fstest.MapFile{Data: []byte(s)} }

func TestLoadSharedSkills(t *testing.T) {
	f := validPack()
	editManifest("    default: true", "    default: true\n    shared-skills: true")(f)
	p, diags, err := pack.Load(f)
	if err != nil || len(diags) != 0 {
		t.Fatalf("Load() = %v, %v, want no diagnostics", diags, err)
	}
	if got := []bool{p.Manifest.Variants[0].SharedSkills, p.Manifest.Variants[1].SharedSkills}; !reflect.DeepEqual(got, []bool{true, false}) {
		t.Errorf("shared skills = %v, want only standalone's (absent is false)", got)
	}
}

func TestLoadValidPack(t *testing.T) {
	p, diags, err := pack.Load(validPack())
	if err != nil || len(diags) != 0 {
		t.Fatalf("Load() = %v, %v, want no diagnostics", diags, err)
	}

	m := p.Manifest
	if m.Name != "test-pack" || m.Version != "0.1.0" || m.Engine != ">=0.1.0" || m.Rulesync != "41.0.0" {
		t.Errorf("identity = %+v", m)
	}
	if !reflect.DeepEqual(m.Harnesses, []string{"claudecode", "opencode"}) {
		t.Errorf("harnesses = %v", m.Harnesses)
	}
	wantProfiles := []pack.Profile{{Name: "web", Path: "profiles/web", Description: "Web projects.", Hints: []string{"astro.config.*"}}}
	if !reflect.DeepEqual(m.Profiles, wantProfiles) {
		t.Errorf("profiles = %+v", m.Profiles)
	}
	wantVariants := []pack.Variant{
		{Name: "standalone", Path: "variants/standalone", Default: true},
		{Name: "orca", Path: "variants/orca", Detect: &pack.Detect{Command: "orca"}},
	}
	if !reflect.DeepEqual(m.Variants, wantVariants) {
		t.Errorf("variants = %+v", m.Variants)
	}

	var got []string
	for _, l := range p.Layers {
		got = append(got, fmt.Sprintf("%s %s %s", l.Kind, l.Name, l.Path))
		for _, it := range l.Items {
			got = append(got, fmt.Sprintf("  %s %s %s %v %v root=%v", it.Kind, it.Name, it.Path, it.Dotai.Capabilities, it.Dotai.Overrides, it.Root))
		}
	}
	want := []string{
		"base base base",
		"  rule overview base/.rulesync/rules/overview.md [] false root=true",
		"  skill commit base/.rulesync/skills/commit/SKILL.md [] false root=false",
		"  subagent reviewer base/.rulesync/subagents/review/reviewer.md [git] false root=false",
		"profile web profiles/web",
		"  command deploy profiles/web/.rulesync/commands/deploy.md [] false root=false",
		"variant standalone variants/standalone",
		"  rule solo variants/standalone/.rulesync/rules/solo.md [] true root=false",
		"variant orca variants/orca",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("layers =\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

func TestLoadDiagnostics(t *testing.T) {
	const rule = "base/.rulesync/rules/overview.md"
	const links = "; packs must not contain links"
	tests := []struct {
		name   string
		mutate func(fstest.MapFS)
		want   []string // "file:line:col: code: message"
	}{
		{
			name:   "missing manifest",
			mutate: func(f fstest.MapFS) { delete(f, "dotai.yaml") },
			want:   []string{"dotai.yaml:0:0: manifest-missing: the pack has no dotai.yaml"},
		},
		{
			name:   "manifest is a symbolic link",
			mutate: symlink("dotai.yaml", "other.yaml"),
			want:   []string{`dotai.yaml:0:0: symlink-not-allowed: "dotai.yaml" is a symbolic link` + links},
		},
		{
			name:   "manifest is not a regular file",
			mutate: func(f fstest.MapFS) { f["dotai.yaml"] = &fstest.MapFile{Mode: fs.ModeNamedPipe} },
			want:   []string{`dotai.yaml:0:0: not-regular-file: "dotai.yaml" is not a regular file`},
		},
		{
			name:   "manifest too large",
			mutate: setManifest("name: x\n" + strings.Repeat("#", 1<<20)),
			want:   []string{`dotai.yaml:0:0: file-too-large: "dotai.yaml" is larger than 1 MiB`},
		},
		{
			name:   "manifest syntax error",
			mutate: setManifest("schema: dotai.pack/v1\nname: a: b\n"),
			want:   []string{"dotai.yaml:2:0: yaml-invalid: mapping values are not allowed in this context"},
		},
		{
			name:   "manifest tab indentation",
			mutate: setManifest("schema: dotai.pack/v1\nprofiles:\n\tweb: {}\n"),
			want:   []string{"dotai.yaml:3:0: yaml-invalid: found character that cannot start any token"},
		},
		{
			// go-yaml v3 reports the line before the one that opens the
			// flow context; it exposes no structured position to do better.
			name:   "manifest unclosed flow sequence",
			mutate: setManifest("schema: dotai.pack/v1\nharnesses: [claudecode\nname: x\n"),
			want:   []string{"dotai.yaml:1:0: yaml-invalid: did not find expected ',' or ']'"},
		},
		{
			name:   "manifest is not a mapping",
			mutate: setManifest("- a\n"),
			want:   []string{"dotai.yaml:1:1: field-type: dotai.yaml must be a mapping"},
		},
		{
			name:   "manifest with two documents",
			mutate: setManifest(manifest + "---\nname: other\n"),
			want:   []string{"dotai.yaml:19:1: yaml-invalid: only one YAML document is allowed"},
		},
		{
			name:   "unknown schema",
			mutate: editManifest("dotai.pack/v1", "dotai.pack/v9"),
			want:   []string{`dotai.yaml:1:9: schema-unknown: unknown schema "dotai.pack/v9", want "dotai.pack/v1"`},
		},
		{
			name:   "missing required fields are all reported",
			mutate: editManifest("name: test-pack\nversion: 0.1.0\n", ""),
			want: []string{
				`dotai.yaml:1:1: field-required: dotai.yaml: missing required field "name"`,
				`dotai.yaml:1:1: field-required: dotai.yaml: missing required field "version"`,
			},
		},
		{
			name:   "null and empty required values",
			mutate: editManifest("name: test-pack\nversion: 0.1.0\n", "name: ~\nversion: \"\"\n"),
			want: []string{
				`dotai.yaml:2:7: field-required: dotai.yaml: "name" must not be empty`,
				`dotai.yaml:3:10: field-required: dotai.yaml: "version" must not be empty`,
			},
		},
		{
			name:   "null required list",
			mutate: editManifest("[claudecode, opencode]", "null"),
			want:   []string{`dotai.yaml:6:12: field-required: dotai.yaml: "harnesses" must not be empty`},
		},
		{
			name:   "unknown and duplicate fields",
			mutate: editManifest("name: test-pack\n", "name: test-pack\nname: again\nowner: me\n"),
			want: []string{
				`dotai.yaml:3:1: field-duplicate: dotai.yaml: duplicate field "name"`,
				`dotai.yaml:4:1: field-unknown: dotai.yaml: unknown field "owner"`,
			},
		},
		{
			name:   "invalid semver version",
			mutate: editManifest("version: 0.1.0", "version: 1.0"),
			want:   []string{`dotai.yaml:3:10: version-invalid: version "1.0" is not a semantic version`},
		},
		{
			name:   "invalid engine range",
			mutate: editManifest(`">=0.1.0"`, `">=banana"`),
			want:   []string{`dotai.yaml:4:9: engine-invalid: engine ">=banana" is not a version range`},
		},
		{
			name:   "rulesync is a range, not an exact version",
			mutate: editManifest("rulesync: 41.0.0", `rulesync: "^41.0.0"`),
			want:   []string{`dotai.yaml:5:11: rulesync-invalid: rulesync "^41.0.0" must be an exact version such as 41.0.0`},
		},
		{
			name:   "unknown harness",
			mutate: editManifest("[claudecode, opencode]", "[claudecode, vscode]"),
			want:   []string{`dotai.yaml:6:25: harness-unknown: unknown harness "vscode", want one of [claudecode antigravity-cli opencode]`},
		},
		{
			name:   "empty harness list",
			mutate: editManifest("[claudecode, opencode]", "[]"),
			want:   []string{`dotai.yaml:6:12: field-required: dotai.yaml: "harnesses" must not be empty`},
		},
		{
			name:   "harnesses is not a list",
			mutate: editManifest("[claudecode, opencode]", "claudecode"),
			want:   []string{`dotai.yaml:6:12: field-type: dotai.yaml: "harnesses" must be a list`},
		},
		{
			name:   "profile without description",
			mutate: editManifest("    description: Web projects.\n", ""),
			want:   []string{`dotai.yaml:8:3: field-required: profile web: missing required field "description"`},
		},
		{
			name:   "declared layer path does not exist",
			mutate: editManifest("path: profiles/web", "path: profiles/nope"),
			want:   []string{`dotai.yaml:9:11: layer-missing: profile web: directory "profiles/nope" does not exist`},
		},
		{
			name:   "declared layer path is a file",
			mutate: editManifest("path: profiles/web", "path: base/dotai/settings.yaml"),
			want:   []string{`dotai.yaml:9:11: layer-not-directory: profile web: "base/dotai/settings.yaml" is not a directory`},
		},
		{
			name: "declared layer path is a symbolic link",
			mutate: func(f fstest.MapFS) {
				dropTree(f, "profiles/web")
				symlink("profiles/web", "../base")(f)
			},
			want: []string{`dotai.yaml:9:11: symlink-not-allowed: profile web: "profiles/web" is a symbolic link` + links},
		},
		{
			name: "declared layer path goes through a symbolic link",
			mutate: func(f fstest.MapFS) {
				dropTree(f, "profiles")
				symlink("profiles", "elsewhere")(f)
				f["elsewhere/web/.rulesync/rules/a.md"] = file("# A\n")
			},
			want: []string{`dotai.yaml:9:11: symlink-not-allowed: profile web: "profiles" is a symbolic link` + links},
		},
		{
			name:   "layer path outside the pack",
			mutate: editManifest("path: profiles/web", "path: ../web"),
			want:   []string{`dotai.yaml:9:11: layer-path-invalid: profile web: path "../web" must be relative and inside the pack`},
		},
		{
			name:   "default is not a boolean",
			mutate: editManifest("default: true", "default: yes"),
			want:   []string{`dotai.yaml:15:14: field-type: variant standalone: "default" must be true or false`},
		},
		{
			name:   "shared-skills is not a boolean",
			mutate: editManifest("    default: true", "    default: true\n    shared-skills: [all]"),
			want:   []string{`dotai.yaml:16:20: field-type: variant standalone: "shared-skills" must be true or false`},
		},
		{
			name:   "shared-skills on a profile",
			mutate: editManifest("    hints: [astro.config.*]\n", "    hints: [astro.config.*]\n    shared-skills: true\n"),
			want:   []string{`dotai.yaml:12:5: field-unknown: profile web: unknown field "shared-skills"`},
		},
		{
			name:   "two default variants",
			mutate: editManifest("    detect: { command: orca }", "    default: true"),
			want:   []string{"dotai.yaml:12:1: variant-default: exactly one variant must set default: true, found 2"},
		},
		{
			name:   "no default variant",
			mutate: editManifest("    default: true\n", ""),
			want:   []string{"dotai.yaml:12:1: variant-default: exactly one variant must set default: true, found 0"},
		},
		{
			name:   "unknown detect rule",
			mutate: editManifest("{ command: orca }", "{ env: ORCA }"),
			want: []string{
				`dotai.yaml:18:5: field-required: detect: missing required field "command"`,
				`dotai.yaml:18:15: field-unknown: detect: unknown field "env"`,
			},
		},
		{
			name: "base is a file",
			mutate: func(f fstest.MapFS) {
				dropTree(f, "base")
				f["base"] = file("not a directory\n")
			},
			want: []string{`base:0:0: layer-not-directory: "base" is not a directory`},
		},
		{
			name: "base is a symbolic link",
			mutate: func(f fstest.MapFS) {
				dropTree(f, "base")
				symlink("base", "variants/orca")(f)
			},
			want: []string{`base:0:0: symlink-not-allowed: "base" is a symbolic link` + links},
		},
		{
			name:   "symbolic link inside a layer",
			mutate: symlink("base/.rulesync/rules/link.md", "../../../dotai.yaml"),
			want:   []string{`base/.rulesync/rules/link.md:0:0: symlink-not-allowed: "base/.rulesync/rules/link.md" is a symbolic link` + links},
		},
		{
			name:   "symbolic link as a layer's .rulesync",
			mutate: symlink("variants/orca/.rulesync", "../standalone/.rulesync"),
			want:   []string{`variants/orca/.rulesync:0:0: symlink-not-allowed: "variants/orca/.rulesync" is a symbolic link` + links},
		},
		{
			name:   "symbolic link in a layer's dotai directory",
			mutate: symlink("base/dotai/tools/run", "/usr/bin/env"),
			want:   []string{`base/dotai/tools/run:0:0: symlink-not-allowed: "base/dotai/tools/run" is a symbolic link` + links},
		},
		{
			name:   "named pipe inside a layer",
			mutate: func(f fstest.MapFS) { f["base/.rulesync/rules/pipe.md"] = &fstest.MapFile{Mode: fs.ModeNamedPipe} },
			want:   []string{`base/.rulesync/rules/pipe.md:0:0: not-regular-file: "base/.rulesync/rules/pipe.md" is not a regular file`},
		},
		{
			name:   "source too large",
			mutate: setFile(rule, strings.Repeat("x", 1<<20+1)),
			want:   []string{rule + `:0:0: file-too-large: "` + rule + `" is larger than 1 MiB`},
		},
		{
			name:   "unterminated frontmatter",
			mutate: setFile(rule, "---\nroot: true\n# Overview\n"),
			want:   []string{rule + ":1:1: frontmatter-invalid: frontmatter has no closing ---"},
		},
		{
			name:   "frontmatter syntax error",
			mutate: setFile(rule, "---\nroot: true\nname: a: b\n---\n"),
			want:   []string{rule + ":3:0: frontmatter-invalid: mapping values are not allowed in this context"},
		},
		{
			name:   "malformed dotai block",
			mutate: setFile(rule, "---\ndotai:\n  capabilities: git\n  overrides: \"true\"\n  model_tier: fast\n---\n"),
			want: []string{
				rule + `:3:17: field-type: dotai: "capabilities" must be a list`,
				rule + `:4:14: field-type: dotai: "overrides" must be true or false`,
				rule + `:5:3: field-unknown: dotai: unknown field "model_tier"`,
			},
		},
		{
			name:   "dotai block is not a mapping",
			mutate: setFile(rule, "---\ndotai: [git]\n---\n"),
			want:   []string{rule + ":2:8: field-type: dotai must be a mapping"},
		},
		{
			name: "problems in several files are all collected",
			mutate: func(f fstest.MapFS) {
				editManifest("dotai.pack/v1", "dotai.pack/v2")(f)
				setFile(rule, "---\ndotai: true\n---\n")(f)
			},
			want: []string{
				`dotai.yaml:1:9: schema-unknown: unknown schema "dotai.pack/v2", want "dotai.pack/v1"`,
				rule + ":2:8: field-type: dotai must be a mapping",
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fsys := validPack()
			tt.mutate(fsys)
			_, diags, err := pack.Load(fsys)
			if err != nil {
				t.Fatalf("Load() error = %v", err)
			}
			if got := format(diags); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("diagnostics =\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(tt.want, "\n"))
			}
		})
	}
}

func TestLoadLineEndingsBOMAndFences(t *testing.T) {
	const rule = "base/.rulesync/rules/overview.md"
	fsys := validPack()
	crlf := func(s string) *fstest.MapFile {
		return &fstest.MapFile{Data: []byte("\uFEFF" + strings.ReplaceAll(s, "\n", "\r\n"))}
	}
	fsys["dotai.yaml"] = crlf(strings.Replace(manifest, "version: 0.1.0", "version: 1.0", 1))
	fsys[rule] = crlf("---  \ndotai:\n  overrides: maybe\n--- \t\n# Overview\n")
	fsys["variants/standalone/.rulesync/rules/solo.md"] = crlf("--- \ndotai:\n  overrides: true\n---\t\n")

	p, diags, err := pack.Load(fsys)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	want := []string{
		`dotai.yaml:3:10: version-invalid: version "1.0" is not a semantic version`,
		rule + `:3:14: field-type: dotai: "overrides" must be true or false`,
	}
	if got := format(diags); !reflect.DeepEqual(got, want) {
		t.Errorf("diagnostics =\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	if p.Manifest.Schema != pack.SchemaV1 {
		t.Errorf("schema = %q, want %q", p.Manifest.Schema, pack.SchemaV1)
	}
	if solo := p.Layers[2].Items[0]; solo.Name != "solo" || !solo.Dotai.Overrides {
		t.Errorf("item = %+v, want solo with overrides", solo)
	}
}

func TestLoadLayerFiles(t *testing.T) {
	fsys := validPack()
	fsys["base/README.md"] = file("Outside the layer trees.")
	fsys["base/dotai/tools/run.sh"] = &fstest.MapFile{Data: []byte("echo run"), Mode: 0o755}
	fsys["base/.rulesync/skills/orphan/notes.md"] = file("A skill directory without SKILL.md.")

	p, diags, err := pack.Load(fsys)
	if err != nil || len(diags) != 0 {
		t.Fatalf("Load() = %v, %v, want no diagnostics", diags, err)
	}
	var got []string
	for _, f := range p.Layers[0].Files {
		got = append(got, fmt.Sprintf("%s %s %s/%s exec=%v", f.Path, f.Rel, f.Item.Kind, f.Item.Name, f.Exec))
	}
	want := []string{
		"base/.rulesync/rules/overview.md .rulesync/rules/overview.md rule/overview exec=false",
		"base/.rulesync/skills/commit/SKILL.md .rulesync/skills/commit/SKILL.md skill/commit exec=false",
		"base/.rulesync/skills/commit/notes.md .rulesync/skills/commit/notes.md skill/commit exec=false",
		"base/.rulesync/skills/orphan/notes.md .rulesync/skills/orphan/notes.md / exec=false",
		"base/.rulesync/subagents/review/reviewer.md .rulesync/subagents/review/reviewer.md subagent/reviewer exec=false",
		"base/dotai/settings.yaml dotai/settings.yaml / exec=false",
		"base/dotai/tools/run.sh dotai/tools/run.sh / exec=true",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("files = %q\nwant %q", got, want)
	}
	if data := string(p.Layers[0].Files[6].Data); data != "echo run" {
		t.Errorf("tools/run.sh = %q, want its content", data)
	}
}

// errFS fails to open one file with an error that is not about the pack.
type errFS struct {
	fstest.MapFS
	name string
	err  error
}

func (f errFS) Open(name string) (fs.File, error) {
	if name == f.name {
		return nil, &fs.PathError{Op: "open", Path: name, Err: f.err}
	}
	return f.MapFS.Open(name)
}

func TestLoadErrors(t *testing.T) {
	tests := []struct {
		name string
		fsys fs.FS
	}{
		{"manifest is a directory", fstest.MapFS{"dotai.yaml/x": file("")}},
		{"manifest permission denied", errFS{validPack(), "dotai.yaml", fs.ErrPermission}},
		{"source permission denied", errFS{validPack(), "base/.rulesync/rules/overview.md", fs.ErrPermission}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p, diags, err := pack.Load(tt.fsys)
			if err == nil {
				t.Fatalf("Load() error = nil, want error; diagnostics = %v", diags)
			}
			if p != nil || diags != nil {
				t.Errorf("Load() = %v, %v, want no pack and no diagnostics with an error", p, diags)
			}
		})
	}
}

func format(diags []pack.Diagnostic) []string {
	var out []string
	for _, d := range diags {
		out = append(out, fmt.Sprintf("%s:%d:%d: %s: %s", d.File, d.Line, d.Column, d.Code, d.Message))
	}
	return out
}

func symlink(name, target string) func(fstest.MapFS) {
	return func(f fstest.MapFS) { f[name] = &fstest.MapFile{Mode: fs.ModeSymlink, Data: []byte(target)} }
}

// dropTree removes dir and everything below it.
func dropTree(f fstest.MapFS, dir string) {
	for name := range f {
		if name == dir || strings.HasPrefix(name, dir+"/") {
			delete(f, name)
		}
	}
}

func TestDiagnosticString(t *testing.T) {
	tests := []struct {
		d    pack.Diagnostic
		want string
	}{
		{pack.Diagnostic{File: "dotai.yaml", Line: 3, Column: 10, Code: "version-invalid", Message: "bad"}, "dotai.yaml:3:10: version-invalid: bad"},
		{pack.Diagnostic{File: "dotai.yaml", Line: 2, Code: "yaml-invalid", Message: "bad"}, "dotai.yaml:2: yaml-invalid: bad"},
		{pack.Diagnostic{File: "dotai.yaml", Code: "manifest-missing", Message: "bad"}, "dotai.yaml: manifest-missing: bad"},
		{pack.Diagnostic{File: "a.md", Code: "name-duplicate", Message: "bad", Related: &pack.Location{File: "b.md"}}, "a.md: name-duplicate: bad (first: b.md)"},
		{pack.Diagnostic{File: "a.md", Code: "mcp-server-conflict", Message: "bad", Related: &pack.Location{File: "b.json", Line: 4, Column: 2}, Hint: "rename it"}, "a.md: mcp-server-conflict: bad (first: b.json:4:2) (fix: rename it)"},
		{pack.Diagnostic{File: "a.md", Code: "layer-selection", Message: "bad", Hint: "use one of [a]"}, "a.md: layer-selection: bad (fix: use one of [a])"},
	}
	for _, tt := range tests {
		if got := tt.d.String(); got != tt.want {
			t.Errorf("String() = %q, want %q", got, tt.want)
		}
	}
}

func setManifest(s string) func(fstest.MapFS) { return setFile("dotai.yaml", s) }

func setFile(name, s string) func(fstest.MapFS) {
	return func(f fstest.MapFS) { f[name] = file(s) }
}

func editManifest(old, new string) func(fstest.MapFS) {
	return func(f fstest.MapFS) {
		s := string(f["dotai.yaml"].Data)
		if !strings.Contains(s, old) {
			panic("fixture does not contain " + old)
		}
		f["dotai.yaml"] = file(strings.Replace(s, old, new, 1))
	}
}

func TestLoadWithRulesyncVersions(t *testing.T) {
	tests := []struct{ pin, want string }{
		{pin: "41.0.0"},
		{pin: "40.0.0", want: "dotai.yaml:5:11: rulesync-unsupported: rulesync 40.0.0 is not supported by this engine (fix: pin one of: 41.0.0, 42.0.0)"},
		{pin: "latest", want: `dotai.yaml:5:11: rulesync-invalid: rulesync "latest" must be an exact version such as 41.0.0`},
	}
	for _, tt := range tests {
		t.Run(tt.pin, func(t *testing.T) {
			fsys := validPack()
			fsys["dotai.yaml"].Data = []byte(strings.Replace(manifest, "41.0.0", tt.pin, 1))
			_, diags, err := pack.LoadWith(fsys, []string{"41.0.0", "42.0.0"})
			var got []string
			for _, d := range diags {
				got = append(got, d.String())
			}
			if err != nil || strings.Join(got, "\n") != tt.want {
				t.Errorf("diagnostics = %q, %v, want %q", got, err, tt.want)
			}
		})
	}
}
