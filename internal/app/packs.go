package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/lartweib/dotai/internal/merge"
	"github.com/lartweib/dotai/internal/pack"
	"github.com/lartweib/dotai/internal/rulesync"
)

// The pack registry (decision 0043): ~/.dotai/packs.json lists the
// absolute paths of the packs the user imported, so that the web UI shows
// the same packs in every browser. It holds paths only; the name and the
// version of each pack are read from its manifest when the list is read.

const (
	packsFile = "packs.json"
	// PacksSchema is the schema of ~/.dotai/packs.json.
	PacksSchema = "dotai.packs/v1"
)

// packsDoc is the content of packs.json.
type packsDoc struct {
	Schema string      `json:"schema"`
	Packs  []packsItem `json:"packs"`
}

type packsItem struct {
	Path string `json:"path"`
}

// PackList is the registry as it reads now: Path is packs.json, and each
// pack is listed in import order.
type PackList struct {
	Path  string      `json:"path"`
	Packs []PackEntry `json:"packs"`
}

// PackEntry is one pack of the registry. Name and Version come from its
// manifest; Error says why it could not be read, such as a directory that
// is gone, and the entry stays listed.
type PackEntry struct {
	Path    string `json:"path"`
	Name    string `json:"name"`
	Version string `json:"version"`
	Error   string `json:"error"`
}

// PackInvalidError refuses the import of a pack that does not validate,
// with the result of the validation.
type PackInvalidError struct {
	Path   string
	Result ValidateResult
}

func (e *PackInvalidError) Error() string {
	return fmt.Sprintf("%s is not a valid pack: dotai validate reports %d problems", e.Path, len(e.Result.Diagnostics))
}

// CopyRefusedError refuses a copy of a pack before it writes anything.
type CopyRefusedError struct{ msg string }

func (e *CopyRefusedError) Error() string { return e.msg }

// ListPacks reads the registry of home. A home without one has no packs,
// and nothing is written.
func ListPacks(home string) (PackList, error) {
	home, err := requireAbs(home)
	if err != nil {
		return PackList{}, err
	}
	doc, err := readPacks(home)
	if err != nil {
		return PackList{}, err
	}
	return describePacks(home, doc), nil
}

// ImportPack adds the pack in dir to the registry of home once it
// validates. A pack already listed is left as it is.
func ImportPack(home, dir string) (PackList, error) {
	home, err := requireAbs(home)
	if err != nil {
		return PackList{}, err
	}
	dir, err = packDir(dir)
	if err != nil {
		return PackList{}, err
	}
	res, err := Validate(dir, Selection{})
	if err != nil {
		return PackList{}, err
	}
	if !res.Valid {
		return PackList{}, &PackInvalidError{Path: dir, Result: res}
	}
	return editPacks(home, func(doc *packsDoc) bool {
		if slices.ContainsFunc(doc.Packs, func(p packsItem) bool { return samePath(p.Path, dir) }) {
			return false
		}
		doc.Packs = append(doc.Packs, packsItem{Path: dir})
		return true
	})
}

// RemovePack forgets the pack at dir in the registry of home. It never
// touches the pack's files; a path that is not listed is no error.
func RemovePack(home, dir string) (PackList, error) {
	home, err := requireAbs(home)
	if err != nil {
		return PackList{}, err
	}
	if !filepath.IsAbs(dir) {
		return PackList{}, &UsageError{msg: fmt.Sprintf("the pack path %q is not absolute", dir)}
	}
	dir = filepath.Clean(dir)
	return editPacks(home, func(doc *packsDoc) bool {
		n := len(doc.Packs)
		doc.Packs = slices.DeleteFunc(doc.Packs, func(p packsItem) bool { return samePath(p.Path, dir) })
		return len(doc.Packs) != n
	})
}

// packDir checks that dir is the absolute path of a directory.
func packDir(dir string) (string, error) {
	if !filepath.IsAbs(dir) {
		return "", &UsageError{msg: fmt.Sprintf("the pack path %q is not absolute", dir)}
	}
	dir = filepath.Clean(dir)
	info, err := os.Stat(dir)
	if err != nil || !info.IsDir() {
		return "", &UsageError{msg: fmt.Sprintf("there is no pack directory at %s", dir)}
	}
	return dir, nil
}

// editPacks changes the registry with edit under ~/.dotai/lock, and
// writes it, atomically, only when edit reports a change.
func editPacks(home string, edit func(*packsDoc) bool) (PackList, error) {
	r := runner{ctx: context.Background(), d: osInstallFS{}, home: home}
	var warnings []string
	var doc packsDoc
	err := r.locked(&warnings, func() error {
		var err error
		if doc, err = readPacks(home); err != nil {
			return err
		}
		if !edit(&doc) {
			return nil
		}
		data, err := json.MarshalIndent(doc, "", "  ")
		if err != nil {
			return err
		}
		return r.d.WriteIn(filepath.Join(home, dotaiDir), packsFile, append(data, '\n'), 0o600)
	})
	if err != nil {
		return PackList{}, err
	}
	return describePacks(home, doc), nil
}

// readPacks reads packs.json, empty when it is missing.
func readPacks(home string) (packsDoc, error) {
	name := filepath.Join(home, dotaiDir, packsFile)
	doc := packsDoc{Schema: PacksSchema, Packs: []packsItem{}}
	data, _, ok, err := readTarget(context.Background(), osInstallFS{}, home, name)
	if err != nil || !ok {
		return doc, err
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		return packsDoc{}, fmt.Errorf("%s: %w", name, err)
	}
	if doc.Schema != PacksSchema {
		return packsDoc{}, fmt.Errorf("%s: schema %q is not %s", name, doc.Schema, PacksSchema)
	}
	for _, p := range doc.Packs {
		if !filepath.IsAbs(p.Path) {
			return packsDoc{}, fmt.Errorf("%s: the pack path %q is not absolute", name, p.Path)
		}
	}
	if doc.Packs == nil {
		doc.Packs = []packsItem{}
	}
	return doc, nil
}

// describePacks reads the name and the version of each pack of doc.
func describePacks(home string, doc packsDoc) PackList {
	list := PackList{Path: filepath.Join(home, dotaiDir, packsFile), Packs: []PackEntry{}}
	for _, p := range doc.Packs {
		e := PackEntry{Path: p.Path}
		fsys, err := packFS(p.Path)
		if err == nil {
			var pk *pack.Pack
			if pk, _, err = pack.LoadWith(fsys, rulesync.Versions()); err == nil && pk == nil {
				err = errors.New("its manifest dotai.yaml cannot be read")
			}
			if err == nil {
				e.Name, e.Version = pk.Manifest.Name, pk.Manifest.Version
			}
		}
		if err != nil {
			e.Error = err.Error()
		}
		list.Packs = append(list.Packs, e)
	}
	return list
}

// CopyResult is a copy of a pack: where from, where to, and how many
// files it holds.
type CopyResult struct {
	From  string `json:"from"`
	To    string `json:"to"`
	Files int    `json:"files"`
}

// CopyPack copies the pack in from to the new directory to, whose parent
// must exist, so that the user can edit the copy. It copies regular files
// and directories, skips .git, and refuses a symbolic link or any other
// kind of file before it writes anything, as a pack holds none. A copy
// that fails partway removes what it created. It does not import the copy.
func CopyPack(from, to string) (CopyResult, error) {
	from, err := packDir(from)
	if err != nil {
		return CopyResult{}, err
	}
	if !filepath.IsAbs(to) {
		return CopyResult{}, &UsageError{msg: fmt.Sprintf("the copy path %q is not absolute", to)}
	}
	to = filepath.Clean(to)
	if _, err := os.Lstat(to); err == nil {
		return CopyResult{}, &CopyRefusedError{msg: fmt.Sprintf("%s already exists: name a new directory for the copy", to)}
	}
	if info, err := os.Stat(filepath.Dir(to)); err != nil || !info.IsDir() {
		return CopyResult{}, &CopyRefusedError{msg: fmt.Sprintf("the parent directory of %s does not exist", to)}
	}
	if rel, err := filepath.Rel(from, to); err == nil && filepath.IsLocal(rel) {
		return CopyResult{}, &CopyRefusedError{msg: fmt.Sprintf("%s is inside the pack %s", to, from)}
	}
	var dirs, files []string
	err = filepath.WalkDir(from, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(from, p)
		switch {
		case d.IsDir() && d.Name() == ".git" && rel != ".":
			return filepath.SkipDir
		case d.IsDir():
			dirs = append(dirs, rel)
		case d.Type().IsRegular():
			files = append(files, rel)
		default:
			return &CopyRefusedError{msg: fmt.Sprintf("%s is not a regular file or a directory: a pack holds no links", p)}
		}
		return nil
	})
	if err != nil {
		return CopyResult{}, err
	}
	if err := os.Mkdir(to, 0o755); err != nil {
		return CopyResult{}, err
	}
	if err := copyTree(from, to, dirs, files); err != nil {
		return CopyResult{}, errors.Join(err, os.RemoveAll(to))
	}
	return CopyResult{From: from, To: to, Files: len(files)}, nil
}

func copyTree(from, to string, dirs, files []string) error {
	for _, d := range dirs {
		if d != "." {
			if err := os.Mkdir(filepath.Join(to, d), 0o755); err != nil {
				return err
			}
		}
	}
	for _, f := range files {
		if err := copyPackFile(filepath.Join(from, f), filepath.Join(to, f)); err != nil {
			return err
		}
	}
	return nil
}

func copyPackFile(src, dst string) error {
	in, info, err := openRegular(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, info.Mode().Perm()|0o200)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

// PackContents is what each layer of a pack holds, by type, for people
// to read: the web UI's pack detail.
type PackContents struct {
	Name    string         `json:"name"`
	Version string         `json:"version"`
	Layers  []LayerOutline `json:"layers"`
	Mods    []string       `json:"mods"` // the directories of mods/
}

// LayerOutline is one layer and its contents.
type LayerOutline struct {
	Kind     string        `json:"kind"`
	Name     string        `json:"name"`
	Path     string        `json:"path"`
	Contents LayerContents `json:"contents"`
}

// LayerContents lists the items of a layer by type, by name: the rulesync
// sources, the MCP servers of mcp.jsonc, whether it has hooks and
// permissions, the harnesses its settings.yaml has a block for, its tools
// and, for a profile, the mods it names. No list is nil.
type LayerContents struct {
	Rules       []string `json:"rules"`
	Skills      []string `json:"skills"`
	Subagents   []string `json:"subagents"`
	Commands    []string `json:"commands"`
	MCPServers  []string `json:"mcpServers"`
	Hooks       bool     `json:"hooks"`
	Permissions bool     `json:"permissions"`
	Settings    []string `json:"settings"`
	Tools       []string `json:"tools"`
	Mods        []string `json:"mods"`
}

// SummarizePack reads what each layer of the pack in dir holds. A pack
// whose manifest cannot be read is a usage error; Validate reports what
// is wrong with it.
func SummarizePack(dir string) (PackContents, error) {
	fsys, err := packFS(dir)
	if err != nil {
		return PackContents{}, &UsageError{msg: fmt.Sprintf("there is no pack directory at %s", dir)}
	}
	p, _, err := pack.LoadWith(fsys, rulesync.Versions())
	if err != nil {
		return PackContents{}, err
	}
	if p == nil {
		return PackContents{}, &UsageError{msg: fmt.Sprintf("the manifest of %s cannot be read: run dotai validate", dir)}
	}
	out := PackContents{Name: p.Manifest.Name, Version: p.Manifest.Version, Layers: []LayerOutline{}, Mods: []string{}}
	for _, m := range p.Mods {
		out.Mods = append(out.Mods, m.Name)
	}
	profileMods := map[string][]string{}
	for _, pr := range p.Manifest.Profiles {
		profileMods[pr.Name] = pr.Mods
	}
	for _, l := range p.Layers {
		c := LayerContents{Rules: []string{}, Skills: []string{}, Subagents: []string{}, Commands: []string{},
			MCPServers: []string{}, Settings: []string{}, Tools: []string{}, Mods: []string{}}
		for _, it := range l.Items {
			switch it.Kind {
			case pack.ItemRule:
				c.Rules = append(c.Rules, it.Name)
			case pack.ItemSkill:
				c.Skills = append(c.Skills, it.Name)
			case pack.ItemSubagent:
				c.Subagents = append(c.Subagents, it.Name)
			case pack.ItemCommand:
				c.Commands = append(c.Commands, it.Name)
			}
		}
		for _, f := range l.Files {
			switch {
			case f.Rel == ".rulesync/mcp.jsonc":
				if names, err := merge.MCPServerNames(f.Data); err == nil {
					c.MCPServers = names
				}
			case f.Rel == ".rulesync/hooks.jsonc":
				c.Hooks = true
			case f.Rel == ".rulesync/permissions.jsonc":
				c.Permissions = true
			case strings.HasPrefix(f.Rel, "dotai/tools/"):
				c.Tools = append(c.Tools, strings.TrimPrefix(f.Rel, "dotai/tools/"))
			}
		}
		if l.Settings != nil {
			for _, b := range l.Settings.Blocks {
				c.Settings = append(c.Settings, b.Harness)
			}
		}
		if l.Kind == pack.LayerProfile {
			c.Mods = append(c.Mods, profileMods[l.Name]...)
		}
		out.Layers = append(out.Layers, LayerOutline{Kind: string(l.Kind), Name: l.Name, Path: l.Path, Contents: c})
	}
	return out, nil
}
