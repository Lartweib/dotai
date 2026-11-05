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
	"sync"
	"time"

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

// RegistryInvalidError is a packs.json that is not JSON of PacksSchema.
// The registry is left as it is: the user fixes or moves the file.
type RegistryInvalidError struct {
	Path string
	Why  string
}

func (e *RegistryInvalidError) Error() string {
	return fmt.Sprintf("the pack registry %s cannot be read: %s; fix it or move it aside, then try again", e.Path, e.Why)
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
	if dir == "" {
		return PackList{}, &UsageError{msg: "the pack path is empty"}
	}
	// A path that is not absolute can only be an entry of a registry
	// written by hand: it is removed as it is written.
	if filepath.IsAbs(dir) {
		dir = filepath.Clean(dir)
	}
	return editPacks(home, func(doc *packsDoc) bool {
		n := len(doc.Packs)
		doc.Packs = slices.DeleteFunc(doc.Packs, func(p packsItem) bool { return p.Path == dir || samePath(p.Path, dir) })
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
		return packsDoc{}, &RegistryInvalidError{Path: name, Why: err.Error()}
	}
	if doc.Schema != PacksSchema {
		return packsDoc{}, &RegistryInvalidError{Path: name, Why: fmt.Sprintf("its schema %q is not %s", doc.Schema, PacksSchema)}
	}
	if doc.Packs == nil {
		doc.Packs = []packsItem{}
	}
	return doc, nil
}

// identifyPack reads the name and the version of the pack in dir, and
// identifyWait bounds how long the list waits for one pack, such as one on
// a network drive that does not answer.
var (
	identifyPack = func(dir string) (string, string, error) {
		if _, err := packFS(dir); err != nil {
			return "", "", err
		}
		return pack.Identity(os.DirFS(dir))
	}
	identifyWait = 2 * time.Second
)

// describePacks reads the name and the version of each pack of doc, all at
// once, each for at most identifyWait. A pack that cannot be read, or does
// not answer in time, is listed with why.
func describePacks(home string, doc packsDoc) PackList {
	list := PackList{Path: filepath.Join(home, dotaiDir, packsFile), Packs: make([]PackEntry, len(doc.Packs))}
	var wg sync.WaitGroup
	for i, p := range doc.Packs {
		list.Packs[i] = PackEntry{Path: p.Path}
		if !filepath.IsAbs(p.Path) {
			list.Packs[i].Error = fmt.Sprintf("the path %q is not absolute: remove it from the list and import the pack again", p.Path)
			continue
		}
		wg.Add(1)
		go func(e *PackEntry) {
			defer wg.Done()
			type id struct {
				name, version string
				err           error
			}
			done := make(chan id, 1)
			go func() {
				n, v, err := identifyPack(e.Path)
				done <- id{n, v, err}
			}()
			select {
			case r := <-done:
				if r.err != nil {
					e.Error = r.err.Error()
				} else {
					e.Name, e.Version = r.name, r.version
				}
			case <-time.After(identifyWait):
				e.Error = fmt.Sprintf("the pack did not answer within %s", identifyWait)
			}
		}(&list.Packs[i])
	}
	wg.Wait()
	return list
}

// CopyResult is a copy of a pack: where from, where to, and how many
// files it holds.
type CopyResult struct {
	From  string `json:"from"`
	To    string `json:"to"`
	Files int    `json:"files"`
}

// The bounds of a copy of a pack: a pack's own file limit for each file,
// and, for the whole copy, far more than a pack of rules and skills needs,
// so that a copy of the wrong directory stops early.
const (
	maxCopyFiles = 10000
	maxCopyBytes = 64 << 20
)

// copyHooks lets a test stop a copy after a file or act before the copy
// takes its name.
type copyHooks struct {
	afterFile    func(n int) error
	beforeRename func()
}

// packEntryCopy is a directory or a file of a copy, by its path from the pack.
type packEntryCopy struct {
	rel  string
	mode fs.FileMode
}

// CopyPack copies the pack in from, which must have a manifest dotai can
// read, to the new directory to, whose parent must exist and which must not
// be inside the pack, so that the user can edit the copy. It copies regular
// files and directories with their permissions, skips .git, whether a
// directory or the file of a worktree, and refuses a symbolic link or any
// other kind of file, a file larger than a pack may hold, and more than
// maxCopyFiles files or maxCopyBytes bytes, before it writes anything. It
// copies into a new directory next to to and renames it to to at the end,
// so that a copy that stops partway, or whose ctx ends, leaves nothing at
// to; that directory is removed when the copy fails. A to that another
// program creates meanwhile is refused, not replaced (on Linux, an empty
// directory created in the instant before the rename is replaced). It does
// not import the copy.
func CopyPack(ctx context.Context, from, to string) (CopyResult, error) {
	return copyPack(ctx, from, to, copyHooks{})
}

func copyPack(ctx context.Context, from, to string, hooks copyHooks) (CopyResult, error) {
	from, err := packDir(from)
	if err != nil {
		return CopyResult{}, err
	}
	if p, _, err := pack.LoadWith(os.DirFS(from), rulesync.Versions()); err != nil || p == nil {
		return CopyResult{}, &UsageError{msg: fmt.Sprintf("%s is not a pack dotai can read: it needs a dotai.yaml manifest (run dotai validate)", from)}
	}
	if !filepath.IsAbs(to) {
		return CopyResult{}, &UsageError{msg: fmt.Sprintf("the copy path %q is not absolute", to)}
	}
	to = filepath.Clean(to)
	if rel, err := filepath.Rel(from, to); err == nil && (rel == "." || filepath.IsLocal(rel)) {
		return CopyResult{}, &CopyRefusedError{msg: fmt.Sprintf("%s is inside the pack %s: name a directory outside it", to, from)}
	}
	if _, err := os.Lstat(to); err == nil {
		return CopyResult{}, &CopyRefusedError{msg: fmt.Sprintf("%s already exists: name a new directory for the copy", to)}
	}
	parent := filepath.Dir(to)
	if info, err := os.Stat(parent); err != nil || !info.IsDir() {
		return CopyResult{}, &CopyRefusedError{msg: fmt.Sprintf("the parent directory of %s does not exist", to)}
	}
	dirs, files, err := copyList(from)
	if err != nil {
		return CopyResult{}, err
	}
	tmp, err := os.MkdirTemp(parent, "."+filepath.Base(to)+".copy-")
	if err != nil {
		return CopyResult{}, err
	}
	if err := copyInto(ctx, from, tmp, dirs, files, hooks); err != nil {
		return CopyResult{}, errors.Join(err, os.RemoveAll(tmp))
	}
	if hooks.beforeRename != nil {
		hooks.beforeRename()
	}
	refused := &CopyRefusedError{msg: fmt.Sprintf("%s was created while the copy ran: name a new directory for the copy", to)}
	if _, err := os.Lstat(to); err == nil {
		return CopyResult{}, errors.Join(refused, os.RemoveAll(tmp))
	}
	if err := os.Rename(tmp, to); err != nil {
		if _, lerr := os.Lstat(to); lerr == nil {
			return CopyResult{}, errors.Join(refused, os.RemoveAll(tmp))
		}
		return CopyResult{}, errors.Join(err, os.RemoveAll(tmp))
	}
	return CopyResult{From: from, To: to, Files: len(files)}, nil
}

// copyList walks the pack and lists what a copy writes, refusing what a
// pack never holds and what is larger than a copy takes.
func copyList(from string) (dirs, files []packEntryCopy, err error) {
	var total int64
	err = filepath.WalkDir(from, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(from, p)
		if d.Name() == ".git" && rel != "." {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		switch {
		case d.IsDir():
			dirs = append(dirs, packEntryCopy{rel, info.Mode().Perm()})
		case d.Type().IsRegular():
			if info.Size() > pack.MaxFileSize {
				return &CopyRefusedError{msg: fmt.Sprintf("%s is larger than 1 MiB, more than a pack may hold", p)}
			}
			total += info.Size()
			files = append(files, packEntryCopy{rel, info.Mode().Perm()})
			if len(files) > maxCopyFiles || total > maxCopyBytes {
				return &CopyRefusedError{msg: fmt.Sprintf("%s holds more than %d files or %d MiB, more than a copy of a pack takes", from, maxCopyFiles, maxCopyBytes>>20)}
			}
		default:
			return &CopyRefusedError{msg: fmt.Sprintf("%s is not a regular file or a directory: a pack holds no links", p)}
		}
		return nil
	})
	return dirs, files, err
}

// copyInto copies the listed directories and files into tmp, checking ctx
// before each file, then gives each directory its permissions, the
// deepest first, so that none is closed before what goes in it.
func copyInto(ctx context.Context, from, tmp string, dirs, files []packEntryCopy, hooks copyHooks) error {
	for _, d := range dirs {
		if d.rel != "." {
			if err := os.Mkdir(filepath.Join(tmp, d.rel), 0o700); err != nil {
				return err
			}
		}
	}
	for i, f := range files {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := copyPackFile(filepath.Join(from, f.rel), filepath.Join(tmp, f.rel), f.mode); err != nil {
			return err
		}
		if hooks.afterFile != nil {
			if err := hooks.afterFile(i + 1); err != nil {
				return err
			}
		}
	}
	for i := len(dirs) - 1; i >= 0; i-- {
		if err := os.Chmod(filepath.Join(tmp, dirs[i].rel), dirs[i].mode|0o200); err != nil {
			return err
		}
	}
	return ctx.Err()
}

func copyPackFile(src, dst string, mode fs.FileMode) error {
	in, _, err := openRegular(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode|0o200)
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
	// Onboarding is the pack's onboarding agent (decision 0008): the first
	// subagent named onboarding, in layer order; nil when it has none.
	Onboarding *AgentSource `json:"onboarding"`
	// CopyTarget is where a copy of the pack goes unless the user names
	// another directory: <name>-copy next to the pack, or -copy-2 and on
	// when that exists.
	CopyTarget string `json:"copyTarget"`
}

// AgentSource is the source of a subagent: the layer that defines it, its
// path from the pack root and its content.
type AgentSource struct {
	Layer   string `json:"layer"`
	Path    string `json:"path"`
	Content string `json:"content"`
}

// onboardingAgent is the name of the subagent PackContents shows as the
// pack's onboarding agent.
const onboardingAgent = "onboarding"

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
	out := PackContents{Name: p.Manifest.Name, Version: p.Manifest.Version, Layers: []LayerOutline{}, Mods: []string{}, CopyTarget: copyTarget(dir, p.Manifest.Name)}
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
			if out.Onboarding == nil && f.Item == (pack.ItemRef{Kind: pack.ItemSubagent, Name: onboardingAgent}) {
				out.Onboarding = &AgentSource{Layer: l.Name, Path: f.Path, Content: string(f.Data)}
			}
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

// copyTarget is the first of <name>-copy, <name>-copy-2 and on next to the
// pack in dir that does not exist, "" when none of the first hundred is free.
func copyTarget(dir, name string) string {
	if name == "" {
		name = filepath.Base(dir)
	}
	parent := filepath.Dir(filepath.Clean(dir))
	for i := 1; i <= 100; i++ {
		target := filepath.Join(parent, name+"-copy")
		if i > 1 {
			target += fmt.Sprintf("-%d", i)
		}
		if _, err := os.Lstat(target); errors.Is(err, fs.ErrNotExist) {
			return target
		}
	}
	return ""
}
