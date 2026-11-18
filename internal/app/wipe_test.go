package app_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/lartweib/dotai/internal/app"
	"github.com/lartweib/dotai/internal/backup"
	"github.com/lartweib/dotai/internal/state"
)

// entries is every path under dir, by its slash path relative to dir: a
// directory as "dir", a file as "file", its exec bit where the file system
// keeps one and the SHA-256 of its content, and a link or a junction as
// "link" and its destination, never followed. Paths that skip says to
// leave out are not walked.
func entries(t *testing.T, dir string, skip ...string) map[string]string {
	t.Helper()
	out := map[string]string{}
	var walk func(name, rel string)
	walk = func(name, rel string) {
		if slices.Contains(skip, rel) {
			return
		}
		info, err := os.Lstat(name)
		if err != nil {
			t.Fatal(err)
		}
		switch mode := info.Mode(); {
		case mode&(fs.ModeSymlink|fs.ModeIrregular) != 0:
			dest, _ := os.Readlink(name)
			out[rel] = "link " + dest
		case mode.IsDir():
			out[rel] = "dir"
			names, err := os.ReadDir(name)
			if err != nil {
				t.Fatal(err)
			}
			for _, e := range names {
				walk(filepath.Join(name, e.Name()), path.Join(rel, e.Name()))
			}
		default:
			data, err := os.ReadFile(name)
			if err != nil {
				t.Fatal(err)
			}
			h := sha256.Sum256(data)
			out[rel] = fmt.Sprintf("file exec=%v %s", execBit(mode), hex.EncodeToString(h[:]))
		}
	}
	walk(dir, "")
	return out
}

func execBit(mode fs.FileMode) bool { return os.PathSeparator == '/' && mode&0o111 != 0 }

// home is the whole seeded home directory outside dotai's own.
func homeEntries(t *testing.T, home string) map[string]string {
	t.Helper()
	return entries(t, home, ".dotai")
}

// within is the part of e at or below the slash path rel, relative to rel,
// the entry itself as "".
func within(e map[string]string, rel string) map[string]string {
	out := map[string]string{}
	for k, v := range e {
		if k == rel {
			out[""] = v
		} else if inner, ok := strings.CutPrefix(k, rel+"/"); ok {
			out[inner] = v
		}
	}
	return out
}

// diff reports what differs between got and want, path by path.
func diff(t *testing.T, what string, got, want map[string]string) {
	t.Helper()
	if reflect.DeepEqual(got, want) {
		return
	}
	var lines []string
	for k, v := range want {
		if got[k] != v {
			lines = append(lines, fmt.Sprintf("  %s: %q, want %q", k, got[k], v))
		}
	}
	for k, v := range got {
		if _, ok := want[k]; !ok {
			lines = append(lines, fmt.Sprintf("  %s: %q, want nothing", k, v))
		}
	}
	slices.Sort(lines)
	t.Errorf("%s differs:\n%s", what, strings.Join(lines, "\n"))
}

// replaceHome is realHome with links in the areas a replace wipes: a
// link that is a wiped entry itself, and one inside a wiped directory,
// both to a directory outside the home directory, which must never be
// followed. It gives that directory too.
func replaceHome(t *testing.T) (home string, h app.Host, outside string) {
	t.Helper()
	home, h = realHome(t)
	outside = t.TempDir()
	writeFile(t, filepath.Join(outside, "SKILL.md"), "outside\n")
	makeLink(t, outside, at(home, ".claude/skills/linked"))
	makeLink(t, outside, at(home, ".claude/agents/linked"))
	return home, h, outside
}

func installReplace(t *testing.T, d app.ExecuteFS, h app.Host, home string) (app.InstallResult, error) {
	t.Helper()
	f := quiet(t)
	return app.InstallReplace(context.Background(), d, h, f.fetch, replaceRun(t), examplePack, home, app.InstallOptions{})
}

func installAgain(t *testing.T, d app.ExecuteFS, h app.Host, home string) (app.InstallResult, error) {
	t.Helper()
	f := quiet(t)
	return app.InstallAgainReplace(context.Background(), d, h, f.fetch, replaceRun(t), examplePack, home, app.InstallOptions{})
}

// claudeJSON is the ~/.claude.json of realConfig without the keys a
// replace wipes, as the JSONC edit layer deletes them.
const claudeJSON = `{"firstStartTime":"2026-01-01","oauthAccount":{"email":"a@b"},"numStartups":3,` +
	`"projects":{"C:/work":{` +
	`"enabledMcpjsonServers":[],"hasTrustDialogAccepted":true},"C:/other":{"hasTrustDialogAccepted":true}}}`

// TestInstallReplaceWipesByMove checks a replace install over the real
// configuration of all three harnesses (decision 0038): each wiped entry
// is moved into the backup, byte for byte, links moved without being
// followed; ~/.claude.json loses only its configuration keys, its prior
// bytes kept in the backup; every kept entry stays; the pack installs on
// the clean base; and the backup, the first of a replace, is pinned.
func TestInstallReplaceWipesByMove(t *testing.T) {
	home, h, outside := replaceHome(t)
	before, outsideBefore := homeEntries(t, home), entries(t, outside)
	claudeBefore, err := os.ReadFile(at(home, ".claude.json"))
	if err != nil {
		t.Fatal(err)
	}
	res, err := installReplace(t, newClockFS(t), h, home)
	if err != nil {
		t.Fatalf("Install(replace) error = %v", err)
	}
	if res.Backup == nil || len(res.Wipes) == 0 {
		t.Fatalf("Install(replace) = %+v, want a backup and wipes", res)
	}
	after := homeEntries(t, home)
	backupDir := res.Backup.Path
	wiped := map[string]bool{}
	for _, w := range res.Wipes {
		rel, _ := filepath.Rel(home, w.Path)
		rel = filepath.ToSlash(rel)
		wiped[rel] = true
		diff(t, "the backup of "+rel, entries(t, filepath.Join(backupDir, "files", filepath.FromSlash(rel))), within(before, rel))
		if want := before[rel]; strings.HasPrefix(want, "link") && after[rel] != "" {
			t.Errorf("%s = %q after the install, want the link moved away", rel, after[rel])
		}
	}
	for _, rel := range []string{".claude/skills/linked", ".claude/agents"} {
		if !wiped[rel] && !wiped[path.Dir(rel)] {
			t.Errorf("%s is not wiped, wipes = %v", rel, wiped)
		}
	}
	diff(t, "the directory the links point at", entries(t, outside), outsideBefore)
	for _, k := range res.Keeps {
		rel, _ := filepath.Rel(home, k.Path)
		rel = filepath.ToSlash(rel)
		diff(t, "the kept "+rel, within(after, rel), within(before, rel))
	}
	for rel, v := range before {
		under := strings.HasPrefix(rel, ".claude/") || strings.HasPrefix(rel, ".gemini/config/") || strings.HasPrefix(rel, ".gemini/antigravity-cli/") ||
			strings.HasPrefix(rel, ".config/opencode/") || strings.HasPrefix(rel, ".agents/") || strings.HasPrefix(rel, ".opencode") ||
			rel == "CLAUDE.md" || rel == "AGENTS.md" || rel == ".gemini/GEMINI.md" || rel == ".gemini/AGENTS.md" || rel == ".claude.json"
		if !under && after[rel] != v {
			t.Errorf("%s = %q, want it untouched %q", rel, after[rel], v)
		}
	}
	if got, _ := os.ReadFile(at(home, ".claude.json")); string(got) != claudeJSON {
		t.Errorf("~/.claude.json =\n%s\nwant only its configuration keys deleted\n%s", got, claudeJSON)
	}
	if got, _ := os.ReadFile(filepath.Join(backupDir, "files", ".claude.json")); string(got) != string(claudeBefore) {
		t.Errorf("the backup of ~/.claude.json = %s, want its prior bytes", got)
	}
	for _, rel := range []string{".claude/CLAUDE.md", ".gemini/config/GEMINI.md", ".config/opencode/AGENTS.md"} {
		if got, _ := os.ReadFile(at(home, rel)); string(got) != "# pack rules\n" {
			t.Errorf("%s = %q, want the pack's rules", rel, got)
		}
	}
	m := manifest(t, home, res.Backup.ID)
	if app.PinnedBackup(t, home) != res.Backup.ID || m.Mode != app.ModeReplace || len(m.Wipes) != len(res.Wipes) || len(m.Splits) != 1 {
		t.Errorf("manifest = %+v, want the pinned replace backup with every wipe and ~/.claude.json", m)
	}
	for _, w := range m.Wipes {
		if w.File != "files/"+strings.TrimPrefix(filepath.ToSlash(strings.TrimPrefix(w.Path, home)), "/") || len(w.Items) == 0 {
			t.Errorf("manifest wipe %+v, want it at its path under files/ with its items", w)
		}
		for _, it := range w.Items {
			if it.Kind == backup.KindFile && it.SHA256 == "" {
				t.Errorf("manifest item %+v of %s has no SHA-256", it, w.Path)
			}
		}
	}
	if s := m.Splits[0]; s.Target != at(home, ".claude.json") || s.Before.SHA256 != sum(string(claudeBefore)) || s.After.SHA256 != sum(claudeJSON) || len(s.Keys) != 3 {
		t.Errorf("manifest split = %+v, want ~/.claude.json with its three keys", s)
	}
	s := readJSON(t, at(home, ".dotai/state.json"), state.Decode)
	if s.Mode != app.ModeReplace || s.Pending != nil || s.Backup != res.Backup.ID {
		t.Errorf("state = %+v, want the replace install without a journal", s)
	}
	for _, f := range s.Files {
		if f.Target == at(home, ".claude.json") {
			t.Error("the state owns ~/.claude.json, which dotai never owns")
		}
	}
}

// TestInstallReplaceRefusesWithoutWriting checks that the refusals of the
// plan stop the replace install before anything is written.
func TestInstallReplaceRefusesWithoutWriting(t *testing.T) {
	home, h := realHome(t)
	writeFile(t, at(home, ".claude/oauth-backup.json"), "{}")
	before := homeEntries(t, home)
	_, err := installReplace(t, newClockFS(t), h, home)
	var refused *app.RefusedError
	if !errors.As(err, &refused) || len(refused.Refusals) != 1 {
		t.Fatalf("Install(replace) error = %v, want it refused", err)
	}
	diff(t, "home", homeEntries(t, home), before)
	if _, err := os.Stat(at(home, ".dotai")); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("~/.dotai = %v, want nothing written", err)
	}
}

// stepFS fails, or with kill set panics as a killed process stops, at the
// write number at of an operation: each entry moved, each file written
// other than the state, each link made and each file removed. With state
// set it fails the write number at of the state instead. Every write
// after that one works, so that the rollback can run.
type stepFS struct {
	*clockFS
	at, n int
	state bool
	kill  bool
}

var errInjected = errors.New("injected failure")

func (f *stepFS) step(isState bool) error {
	if isState != f.state {
		return nil
	}
	f.n++
	if f.n != f.at {
		return nil
	}
	if f.kill {
		panic(errCrash)
	}
	return errInjected
}

func (f *stepFS) MoveIn(root, from, to string) error {
	if err := f.step(false); err != nil {
		return err
	}
	return f.clockFS.MoveIn(root, from, to)
}

func (f *stepFS) WriteIn(root, name string, data []byte, mode os.FileMode) error {
	if err := f.step(name == "state.json" && filepath.Base(root) == ".dotai"); err != nil {
		return err
	}
	return f.clockFS.WriteIn(root, name, data, mode)
}

func (f *stepFS) LinkIn(root, name, dest string) error {
	if err := f.step(false); err != nil {
		return err
	}
	return f.clockFS.LinkIn(root, name, dest)
}

func (f *stepFS) RemoveIn(root, name string) error {
	if err := f.step(name == "state.json" && filepath.Base(root) == ".dotai"); err != nil {
		return err
	}
	return f.clockFS.RemoveIn(root, name)
}

// steps is how many writes a replace install over replaceHome makes,
// besides the state: the moves, the write of ~/.claude.json and the pack's
// files.
func steps(t *testing.T) int {
	t.Helper()
	home, h, _ := replaceHome(t)
	d := &stepFS{clockFS: newClockFS(t)}
	if _, err := installReplace(t, d, h, home); err != nil {
		t.Fatal(err)
	}
	return d.n
}

// TestInstallReplaceRollsBackExactly checks that a failure at any point
// of a replace install, while it moves entries, when it rewrites
// ~/.claude.json, while it installs the pack, or when it writes its final
// state, moves every entry back to its exact path and puts back the bytes
// of ~/.claude.json, so that the home directory is as it was.
func TestInstallReplaceRollsBackExactly(t *testing.T) {
	n := steps(t)
	type point struct {
		name  string
		at    int
		state bool
	}
	points := []point{{"first move", 1, false}, {"a later move", 5, false}, {"last write", n, false}, {"midway", n - 3, false}, {"final state", 2, true}}
	for i := 6; i < n-3; i += 4 {
		points = append(points, point{fmt.Sprintf("write %d", i), i, false})
	}
	for i, p := range points {
		t.Run(p.name, func(t *testing.T) {
			heavy(t, i)
			home, h, outside := replaceHome(t)
			before, outsideBefore := homeEntries(t, home), entries(t, outside)
			_, err := installReplace(t, &stepFS{clockFS: newClockFS(t), at: p.at, state: p.state}, h, home)
			var ie *app.InstallError
			if !errors.As(err, &ie) || !errors.Is(err, errInjected) || ie.Rollback != nil {
				t.Fatalf("Install(replace) error = %v, want the injected failure rolled back", err)
			}
			diff(t, "home after the rollback", homeEntries(t, home), before)
			diff(t, "the directory the links point at", entries(t, outside), outsideBefore)
			if _, err := os.Stat(at(home, ".dotai/state.json")); !errors.Is(err, fs.ErrNotExist) {
				t.Errorf("state = %v, want none, as before", err)
			}
		})
	}
}

// removeSplitFS fails the first write of the pack's files after it removed
// ~/.claude.json, as another program could.
type removeSplitFS struct {
	*clockFS
	home   string
	splits int
}

func (f *removeSplitFS) WriteIn(root, name string, data []byte, mode os.FileMode) error {
	switch {
	case name == ".claude.json":
		f.splits++
	case f.splits > 0 && name != "state.json" && name != "manifest.json":
		if err := os.Remove(at(f.home, ".claude.json")); err != nil {
			return err
		}
		f.splits = -1
		return errInjected
	}
	return f.clockFS.WriteIn(root, name, data, mode)
}

// TestInstallReplaceRollbackReportsARemovedSplitFile checks that a
// rollback that finds ~/.claude.json removed since the install rewrote it
// says so, and that its copy is in the backup.
func TestInstallReplaceRollbackReportsARemovedSplitFile(t *testing.T) {
	home, h, _ := replaceHome(t)
	_, err := installReplace(t, &removeSplitFS{clockFS: newClockFS(t), home: home}, h, home)
	var ie *app.InstallError
	if !errors.As(err, &ie) || ie.Rollback == nil || !strings.Contains(ie.Rollback.Error(), at(home, ".claude.json")+" is missing") ||
		!strings.Contains(ie.Rollback.Error(), "in the backup") {
		t.Fatalf("Install(replace) error = %v, want the rollback to report ~/.claude.json missing", err)
	}
}
