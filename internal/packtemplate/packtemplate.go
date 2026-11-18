// Package packtemplate is the pack template of decision 0046: one pack,
// embedded in the engine and versioned with it, that shows the files and
// folders dotai expects, each a minimal, commented and harmless example.
// Fill writes it for a new pack's answers, and Unchanged tells whether a
// file of a pack is still one of the template's own, as any released
// version of the template wrote it.
package packtemplate

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/fs"
	"path"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"

	"github.com/lartweib/dotai/internal/pack"
)

// Version is the version of the embedded template. A change to the
// template is a new version, whose hashes are appended to hashes.txt; the
// hashes of the versions before it stay, so that a pack made from any
// release of the template is still recognized.
const Version = 1

//go:embed all:files
var files embed.FS

//go:embed hashes.txt
var hashesFile []byte

// The names of the template's own profile, mod and variant, which Fill
// replaces with the answers'.
const (
	exampleProfile = "example"
	exampleMod     = "example-mod"
	settingsFile   = "base/dotai/settings.yaml"
	manifestFile   = "dotai.yaml"
)

// FS is the embedded template, rooted at the pack's top.
func FS() fs.FS {
	sub, err := fs.Sub(files, "files")
	if err != nil {
		panic(err) // the embedded directory exists
	}
	return sub
}

// Profile is a profile of a new pack: its name, its description and hints,
// which the profiling skill of decision 0045 lists, and the mods it
// installs in a project.
type Profile struct {
	Name        string   `json:"name" yaml:"name"`
	Description string   `json:"description" yaml:"description"`
	Hints       []string `json:"hints" yaml:"hints"`
	Mods        []string `json:"mods" yaml:"mods"`
}

// Answers are what a new pack is made of: its name, the harnesses it
// requires and its profiles, in order.
type Answers struct {
	Name      string    `json:"name" yaml:"name"`
	Harnesses []string  `json:"harnesses" yaml:"harnesses"`
	Profiles  []Profile `json:"profiles" yaml:"profiles"`
}

// File is a file Fill writes: its path from the pack's top, with forward
// slashes, its content and its permissions.
type File struct {
	Path string
	Data []byte
	Mode fs.FileMode
}

// Fill is the template filled in with a: the manifest written from a, the
// template's files as they are, the settings without the blocks of
// harnesses a does not name, and the example profile and mod copied for
// each profile and each mod a names. The pack name, the profile names
// and the mod names follow the rule for mod names, so that each is safe
// as a folder; a name that does not is an error. Files are in path order.
func Fill(a Answers) ([]File, error) {
	if err := names(a); err != nil {
		return nil, err
	}
	var out []File
	err := fs.WalkDir(FS(), ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || p == manifestFile {
			return err
		}
		data, err := fs.ReadFile(FS(), p)
		if err != nil {
			return err
		}
		mode := fileMode(p)
		switch {
		case strings.HasPrefix(p, "profiles/"+exampleProfile+"/"):
			for _, pr := range a.Profiles {
				target := "profiles/" + pr.Name + strings.TrimPrefix(p, "profiles/"+exampleProfile)
				if target == "profiles/"+pr.Name+"/.rulesync/rules/"+exampleProfile+".md" {
					target = "profiles/" + pr.Name + "/.rulesync/rules/" + pr.Name + ".md"
				}
				out = append(out, File{Path: target, Data: data, Mode: mode})
			}
		case strings.HasPrefix(p, "mods/"+exampleMod+"/"):
			for _, m := range mods(a) {
				out = append(out, File{Path: "mods/" + m + strings.TrimPrefix(p, "mods/"+exampleMod), Data: data, Mode: mode})
			}
		case p == settingsFile:
			out = append(out, File{Path: p, Data: keepBlocks(data, a.Harnesses), Mode: mode})
		default:
			out = append(out, File{Path: p, Data: data, Mode: mode})
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	out = append(out, File{Path: manifestFile, Data: manifest(a), Mode: 0o644})
	slices.SortFunc(out, func(x, y File) int { return strings.Compare(x.Path, y.Path) })
	return out, nil
}

// names refuses a name of a that is not safe as a folder.
func names(a Answers) error {
	check := func(what, name string) error {
		if p := pack.ModNameProblem(name); p != "" {
			return fmt.Errorf("the %s name %q %s", what, name, p)
		}
		return nil
	}
	if err := check("pack", a.Name); err != nil {
		return err
	}
	for _, pr := range a.Profiles {
		if err := check("profile", pr.Name); err != nil {
			return err
		}
		for _, m := range pr.Mods {
			if err := check("mod", m); err != nil {
				return err
			}
		}
	}
	return nil
}

// mods are the mods the profiles of a name, each once, in order.
func mods(a Answers) []string {
	var out []string
	for _, pr := range a.Profiles {
		for _, m := range pr.Mods {
			if !slices.Contains(out, m) {
				out = append(out, m)
			}
		}
	}
	return out
}

// fileMode is the permissions of the template file p: a shell script is
// executable.
func fileMode(p string) fs.FileMode {
	if path.Ext(p) == ".sh" {
		return 0o755
	}
	return 0o644
}

// topKey is a line that starts a top-level block of a YAML mapping.
var topKey = regexp.MustCompile(`^([A-Za-z0-9_-]+):`)

// keepBlocks is the settings data without the top-level blocks whose key
// is not one of keep. The comment lines before the first block stay.
func keepBlocks(data []byte, keep []string) []byte {
	var b bytes.Buffer
	kept := true
	for _, line := range strings.SplitAfter(string(data), "\n") {
		if m := topKey.FindStringSubmatch(line); m != nil {
			kept = slices.Contains(keep, m[1])
		}
		if kept {
			b.WriteString(line)
		}
	}
	return b.Bytes()
}

// blockKeys are the keys of the top-level blocks of the settings data.
func blockKeys(data []byte) []string {
	var keys []string
	for _, line := range strings.Split(string(data), "\n") {
		if m := topKey.FindStringSubmatch(line); m != nil {
			keys = append(keys, m[1])
		}
	}
	return keys
}

// manifest is the dotai.yaml of a: the template's own, with its comments,
// whose name, harnesses and profiles are a's. Without profiles, the
// paragraph of the profiles, with its comment, is left out.
func manifest(a Answers) []byte {
	text, err := fs.ReadFile(FS(), manifestFile)
	if err != nil {
		panic(err) // the embedded manifest reads
	}
	var paras []string
	// A checkout that turned LF into CRLF still writes LF.
	text = bytes.ReplaceAll(text, []byte("\r\n"), []byte("\n"))
	for _, para := range strings.Split(strings.TrimSuffix(string(text), "\n"), "\n\n") {
		var lines []string
		profiles, inProfiles := false, false
		for _, line := range strings.Split(para, "\n") {
			if inProfiles && strings.HasPrefix(line, "  ") {
				continue // a line of the template's own profiles
			}
			inProfiles = false
			switch {
			case strings.HasPrefix(line, "name: "):
				line = "name: " + scalar(a.Name)
			case strings.HasPrefix(line, "harnesses: "):
				line = "harnesses: " + flow(a.Harnesses)
			case line == "profiles:":
				profiles, inProfiles = true, true
				lines = append(lines, line)
				lines = append(lines, profileLines(a.Profiles)...)
				continue
			}
			lines = append(lines, line)
		}
		if !profiles || len(a.Profiles) > 0 {
			paras = append(paras, strings.Join(lines, "\n"))
		}
	}
	return []byte(strings.Join(paras, "\n\n") + "\n")
}

// profileLines are the lines of the manifest's profiles for ps.
func profileLines(ps []Profile) []string {
	var out []string
	for _, pr := range ps {
		out = append(out, "  "+pr.Name+":", "    path: profiles/"+pr.Name, "    description: "+quoted(pr.Description))
		if len(pr.Hints) > 0 {
			out = append(out, "    hints: "+flow(pr.Hints))
		}
		if len(pr.Mods) > 0 {
			out = append(out, "    mods: "+flow(pr.Mods))
		}
	}
	return out
}

// plain is a YAML scalar that reads as the same string without quotes.
var plain = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9_./-]*$`)

// scalar is s as a YAML scalar: plain when that reads as s, quoted
// otherwise.
func scalar(s string) string {
	if plain.MatchString(s) && !slices.Contains([]string{"true", "false", "null", "yes", "no", "on", "off", "y", "n"}, strings.ToLower(s)) {
		if _, err := strconv.ParseFloat(s, 64); err != nil {
			return s
		}
	}
	return quoted(s)
}

// quoted is s as a double-quoted YAML scalar: a JSON string, which YAML
// reads as written.
func quoted(s string) string {
	var b strings.Builder
	e := json.NewEncoder(&b)
	e.SetEscapeHTML(false)
	if err := e.Encode(s); err != nil {
		panic(err) // a string always encodes
	}
	return strings.TrimSuffix(b.String(), "\n")
}

// flow is vs as a YAML flow sequence.
func flow(vs []string) string {
	out := make([]string, len(vs))
	for i, v := range vs {
		out[i] = scalar(v)
	}
	return "[" + strings.Join(out, ", ") + "]"
}

// Hash is the SHA-256 in hex of data with its line endings normalized, so
// that a checkout that turned LF into CRLF still matches.
func Hash(data []byte) string {
	sum := sha256.Sum256(bytes.ReplaceAll(data, []byte("\r\n"), []byte("\n")))
	return hex.EncodeToString(sum[:])
}

// HashLine is one line of hashes.txt: the template version, the hash of a
// file that version writes, and the file's path in the template.
type HashLine struct {
	Version int
	Hash    string
	Path    string
}

// ParseHashes reads hashes.txt: one "<version> <hash> <path>" per line,
// with # comments and blank lines.
func ParseHashes(data []byte) []HashLine {
	var out []HashLine
	sc := bufio.NewScanner(bytes.NewReader(data))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		f := strings.Fields(line)
		if len(f) != 3 {
			continue
		}
		v, err := strconv.Atoi(f[0])
		if err != nil {
			continue
		}
		out = append(out, HashLine{Version: v, Hash: f[1], Path: f[2]})
	}
	return out
}

// CurrentHashes are the lines hashes.txt holds for Version, "<hash>
// <path>" in path order: every file Fill can write below the pack's top,
// the settings once for each choice of its blocks. The files at the top,
// the manifest and the README, are not installed, so they are left out.
func CurrentHashes() []string {
	var out []string
	err := fs.WalkDir(FS(), ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.Contains(p, "/") {
			return err
		}
		data, err := fs.ReadFile(FS(), p)
		if err != nil {
			return err
		}
		if p != settingsFile {
			out = append(out, Hash(data)+" "+p)
			return nil
		}
		keys := blockKeys(data)
		seen := map[string]bool{}
		for mask := 0; mask < 1<<len(keys); mask++ {
			var keep []string
			for i, k := range keys {
				if mask&(1<<i) != 0 {
					keep = append(keep, k)
				}
			}
			if h := Hash(keepBlocks(data, keep)); !seen[h] {
				seen[h] = true
				out = append(out, h+" "+p)
			}
		}
		return nil
	})
	if err != nil {
		panic(err) // the embedded template reads
	}
	slices.SortFunc(out, func(x, y string) int {
		xs, ys := strings.Fields(x), strings.Fields(y)
		return strings.Compare(xs[1]+" "+xs[0], ys[1]+" "+ys[0])
	})
	return out
}

var known = sync.OnceValue(func() map[string]bool {
	m := map[string]bool{}
	for _, l := range ParseHashes(hashesFile) {
		m[l.Hash] = true
	}
	return m
})

// Unchanged reports whether data, its line endings normalized, is a file
// some released version of the template writes below a pack's top.
func Unchanged(data []byte) bool { return known()[Hash(data)] }
