package app_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/lartweib/dotai/internal/app"
	"github.com/lartweib/dotai/internal/harness"
	"github.com/lartweib/dotai/internal/plan"
	"github.com/lartweib/dotai/internal/rulesync"
)

// installHome is an empty home directory, with the temporary directories
// pointed at an empty directory too, so that a test sees anything the
// plan leaves behind.
func installHome(t *testing.T) (home, tmp string) {
	t.Helper()
	home, tmp = t.TempDir(), t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	for _, v := range []string{"TMPDIR", "TMP", "TEMP"} {
		t.Setenv(v, tmp)
	}
	return home, tmp
}

func (f *fakeRulesync) planInstall(dir, variant, home string) (app.InstallPlan, error) {
	return app.PlanInstallWith(context.Background(), app.OSInstallFS{}, f.fetch, f.run, dir, variant, home)
}

func sum(data string) string {
	h := sha256.Sum256([]byte(data))
	return hex.EncodeToString(h[:])
}

// at is the absolute target of a slash path under home.
func at(home, rel string) string { return filepath.Join(home, filepath.FromSlash(rel)) }

func TestPlanInstallInAFreshHomeCreatesEveryFile(t *testing.T) {
	home, tmp := installHome(t)
	f := &fakeRulesync{t: t}
	p, err := f.planInstall(examplePack, "", home)
	if err != nil || len(p.Diagnostics) != 0 {
		t.Fatalf("PlanInstall() = %+v, %v", p, err)
	}
	if p.Pack == nil || p.Pack.Name != "minimal-pack" || p.Variant != "standalone" || p.Mode != app.ModeIsolated || p.Root != at(home, ".dotai/isolated") {
		t.Errorf("header = %+v, %q, %q, %q, want minimal-pack, the default variant, isolated and the isolated root", p.Pack, p.Variant, p.Mode, p.Root)
	}
	// In target order.
	want := []struct {
		harness, source, target string
		exec                    bool
	}{
		{"claudecode", ".claude/CLAUDE.md", ".dotai/isolated/claude/CLAUDE.md", false},
		{"claudecode", ".claude/settings.json", ".dotai/isolated/claude/settings.json", false},
		{"antigravity-cli", ".gemini/antigravity-cli/settings.json", ".dotai/isolated/gemini/antigravity-cli/settings.json", false},
		{"opencode", ".config/opencode/opencode.jsonc", ".dotai/isolated/opencode/config/opencode/opencode.jsonc", false},
		// A pack read on Windows has no exec bit (pack.File.Exec).
		{"", ".dotai/tools/hello.sh", ".dotai/tools/hello.sh", runtime.GOOS != "windows"},
	}
	if len(p.Ops) != len(want) {
		t.Fatalf("ops = %+v, want %d", p.Ops, len(want))
	}
	for i, w := range want {
		op := p.Ops[i]
		if op.Action != plan.Create || op.Harness != w.harness || op.Source != w.source || op.Target != at(home, w.target) || op.Exec != w.exec ||
			op.Before != "" || op.BeforeExec || len(op.After) != 64 {
			t.Errorf("op %d = %+v, want create %s from %s to %s, exec %v", i, op, w.harness, w.source, w.target, w.exec)
		}
	}
	if p.Ops[0].After != sum("# generated\n") {
		t.Errorf("after = %s, want the hash of the staged CLAUDE.md", p.Ops[0].After)
	}
	if got := tree(t, home); len(got) != 0 {
		t.Errorf("home = %v, want it untouched", got)
	}
	if entries, _ := os.ReadDir(tmp); len(entries) != 0 {
		t.Errorf("temporary directory holds %v, want the staging removed", entries)
	}
}

func TestPlanInstallDescribesTheIsolationOfEachHarness(t *testing.T) {
	home, _ := installHome(t)
	p, err := quiet(t).planInstall(examplePack, "", home)
	if err != nil {
		t.Fatal(err)
	}
	adapters, err := harness.Lookup([]string{"claudecode", "antigravity-cli", "opencode"})
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Harnesses) != len(adapters) {
		t.Fatalf("harnesses = %+v, want one per harness of the manifest", p.Harnesses)
	}
	for i, a := range adapters {
		h := p.Harnesses[i]
		dir := at(home, ".dotai/isolated/"+a.Dir)
		if h.ID != a.ID || h.Level != string(a.Isolation.Level) || h.Dir != dir || h.Relogin != string(a.Isolation.ReloginOn(runtime.GOOS)) ||
			!reflect.DeepEqual(h.Leaks, a.Isolation.LeaksOn(runtime.GOOS, false)) || len(h.Pointers) != len(a.Isolation.Pointers) {
			t.Errorf("harness %d = %+v, want %s from its adapter in %s", i, h, a.ID, dir)
			continue
		}
		for j, ptr := range a.Isolation.Pointers {
			want := app.PointerPlan{Mechanism: string(ptr.Mechanism), Name: ptr.Name, Dir: filepath.Join(dir, filepath.FromSlash(ptr.Path))}
			if h.Pointers[j] != want {
				t.Errorf("%s pointer %d = %+v, want %+v", a.ID, j, h.Pointers[j], want)
			}
		}
	}
	if got := p.Harnesses[2].Pointers[0].Dir; got != at(home, ".dotai/isolated/opencode/config") {
		t.Errorf("XDG_CONFIG_HOME = %s, want the config directory", got)
	}
}

func TestPlanInstallComparesTheCurrentTargets(t *testing.T) {
	home, _ := installHome(t)
	writeFile(t, at(home, ".dotai/isolated/claude/CLAUDE.md"), "# generated\n")
	writeFile(t, at(home, ".dotai/tools/hello.sh"), "old\n")
	p, err := (&fakeRulesync{t: t}).planInstall(examplePack, "", home)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]app.InstallOp{}
	for _, op := range p.Ops {
		got[op.Source] = op
	}
	if op := got[".claude/CLAUDE.md"]; op.Action != plan.Unchanged || op.Before != sum("# generated\n") || op.After != op.Before {
		t.Errorf("CLAUDE.md = %+v, want unchanged with its hash before and after", op)
	}
	if op := got[".dotai/tools/hello.sh"]; op.Action != plan.Replace || op.Before != sum("old\n") || op.After == op.Before || op.BeforeExec {
		t.Errorf("hello.sh = %+v, want replace from the old content", op)
	}
	if op := got[".claude/settings.json"]; op.Action != plan.Create {
		t.Errorf("settings.json = %+v, want create", op)
	}
}

func TestPlanInstallRefusesATargetThatIsNotARegularFile(t *testing.T) {
	home, tmp := installHome(t)
	target := at(home, ".dotai/isolated/claude/settings.json")
	if err := os.MkdirAll(target, 0o755); err != nil {
		t.Fatal(err)
	}
	_, err := quiet(t).planInstall(examplePack, "", home)
	var nr *app.NotRegularError
	if !errors.As(err, &nr) || nr.Path != target || !strings.Contains(err.Error(), target) {
		t.Errorf("PlanInstall() error = %v, want a not-regular error naming %s", err, target)
	}
	if entries, _ := os.ReadDir(tmp); len(entries) != 0 {
		t.Errorf("temporary directory holds %v, want the staging removed", entries)
	}
}

func TestPlanInstallDoesNotFollowALinkAtATarget(t *testing.T) {
	home, _ := installHome(t)
	real := filepath.Join(t.TempDir(), "CLAUDE.md")
	writeFile(t, real, "# generated\n")
	link := at(home, ".dotai/isolated/claude/CLAUDE.md")
	if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(real, link); err != nil {
		t.Skipf("symbolic links not supported here: %v", err)
	}
	_, err := (&fakeRulesync{t: t}).planInstall(examplePack, "", home)
	var nr *app.NotRegularError
	if !errors.As(err, &nr) || nr.Path != link {
		t.Errorf("PlanInstall() error = %v, want a not-regular error naming %s", err, link)
	}
}

func TestPlanInstallInvalidPackPlansNothing(t *testing.T) {
	home, tmp := installHome(t)
	p, err := quiet(t).planInstall(examplePack, "nope", home)
	if err != nil {
		t.Fatalf("PlanInstall() error = %v", err)
	}
	if len(p.Diagnostics) != 1 || p.Pack == nil || len(p.Ops) != 0 || p.Ops == nil || p.Harnesses == nil {
		t.Errorf("PlanInstall() = %+v, want one diagnostic, the pack and empty lists", p)
	}
	if entries, _ := os.ReadDir(tmp); len(entries) != 0 {
		t.Errorf("temporary directory holds %v, want nothing", entries)
	}
}

func TestPlanInstallUsesTheVariant(t *testing.T) {
	home, _ := installHome(t)
	p, err := quiet(t).planInstall(examplePack, "tmux", home)
	if err != nil || p.Variant != "tmux" {
		t.Fatalf("PlanInstall() = %+v, %v, want the tmux variant", p, err)
	}
}

func TestPlanInstallComparesTheExecBitOutsideWindows(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows files have no exec bit")
	}
	home, _ := installHome(t)
	staged := filepath.Join(t.TempDir(), "s")
	fr := quiet(t)
	fr.tools = app.ToolsIn(home, t.TempDir())
	if _, err := fr.stage(examplePack, staged, app.Selection{}, false); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(staged, "user", ".dotai", "tools", "hello.sh"))
	if err != nil {
		t.Fatal(err)
	}
	target := at(home, ".dotai/tools/hello.sh")
	writeFile(t, target, string(data))
	p, err := quiet(t).planInstall(examplePack, "", home)
	if err != nil {
		t.Fatal(err)
	}
	for _, op := range p.Ops {
		if op.Target == target && (op.Action != plan.Replace || op.BeforeExec || !op.Exec) {
			t.Errorf("hello.sh = %+v, want replace to set the exec bit", op)
		}
	}
}

// spyInstallFS is the real file system port that counts its reads of the
// disk, can add names to the listing of the user staging tree and can
// fail to remove the temporary staging.
type spyInstallFS struct {
	app.OSInstallFS
	extra     []string
	reads     int
	removeErr error
	staging   string
}

func (s *spyInstallFS) StagingDir() (string, error) {
	dir, err := s.OSInstallFS.StagingDir()
	s.staging = dir
	return dir, err
}

// Files adds extra to the user tree of the staging, not to the trees the
// stage itself lists while it renders.
func (s *spyInstallFS) Files(dir string) ([]string, error) {
	names, err := s.OSInstallFS.Files(dir)
	if filepath.Base(dir) == app.ScopeUser && filepath.Base(filepath.Dir(dir)) == "staging" {
		names = append(names, s.extra...)
	}
	return names, err
}

func (s *spyInstallFS) Lstat(name string) (os.FileMode, error) {
	s.reads++
	return s.OSInstallFS.Lstat(name)
}

func (s *spyInstallFS) Hash(ctx context.Context, name string) (plan.Current, error) {
	s.reads++
	return s.OSInstallFS.Hash(ctx, name)
}

func (s *spyInstallFS) Read(ctx context.Context, name string) ([]byte, plan.Current, error) {
	s.reads++
	return s.OSInstallFS.Read(ctx, name)
}

func (s *spyInstallFS) RemoveAll(path string) error {
	if path == s.staging && s.removeErr != nil {
		_ = s.OSInstallFS.RemoveAll(path)
		return s.removeErr
	}
	return s.OSInstallFS.RemoveAll(path)
}

func assertNoStaging(t *testing.T, tmp string) {
	t.Helper()
	if entries, _ := os.ReadDir(tmp); len(entries) != 0 {
		t.Errorf("temporary directory holds %v, want the staging removed", entries)
	}
}

func TestPlanInstallRefusesALinkAboveATarget(t *testing.T) {
	home, _ := installHome(t)
	real := t.TempDir()
	link := at(home, ".dotai")
	if err := os.Symlink(real, link); err != nil {
		t.Skipf("symbolic links not supported here: %v", err)
	}
	_, err := quiet(t).planInstall(examplePack, "", home)
	var nr *app.NotRegularError
	if !errors.As(err, &nr) || nr.Path != link || !nr.Dir || !strings.Contains(err.Error(), "move or remove it") {
		t.Errorf("PlanInstall() error = %v, want a not-regular error naming %s and the fix", err, link)
	}
}

func TestPlanInstallRefusesAJunctionAboveATarget(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("junctions are a Windows file system feature")
	}
	home, _ := installHome(t)
	link := at(home, ".dotai")
	if out, err := exec.Command("cmd", "/c", "mklink", "/J", link, t.TempDir()).CombinedOutput(); err != nil {
		t.Skipf("cannot create a junction: %v: %s", err, out)
	}
	_, err := quiet(t).planInstall(examplePack, "", home)
	var nr *app.NotRegularError
	if !errors.As(err, &nr) || nr.Path != link || !nr.Dir {
		t.Errorf("PlanInstall() error = %v, want a not-regular error naming %s", err, link)
	}
}

func TestPlanInstallRefusesAFileAboveATarget(t *testing.T) {
	home, tmp := installHome(t)
	file := at(home, ".dotai/isolated/claude")
	writeFile(t, file, "not a directory")
	_, err := quiet(t).planInstall(examplePack, "", home)
	var nr *app.NotRegularError
	if !errors.As(err, &nr) || nr.Path != file || !nr.Dir || !strings.Contains(err.Error(), "is a file where the install needs a directory") {
		t.Errorf("PlanInstall() error = %v, want a not-regular error naming %s", err, file)
	}
	assertNoStaging(t, tmp)
}

func TestPlanInstallChecksStagedNamesBeforeReadingTheDisk(t *testing.T) {
	for _, name := range []string{".dotai/tools/a:b", ".dotai/tools/a\x00b"} {
		t.Run(fmt.Sprintf("%q", name), func(t *testing.T) {
			home, tmp := installHome(t)
			d := &spyInstallFS{extra: []string{name}}
			_, err := app.PlanInstallWith(context.Background(), d, quiet(t).fetch, quiet(t).run, examplePack, "", home)
			if err == nil || !strings.Contains(err.Error(), "is not a valid file name on Windows") || d.reads != 0 {
				t.Errorf("PlanInstall() error = %v after %d reads, want the plan's path error before any read", err, d.reads)
			}
			assertNoStaging(t, tmp)
		})
	}
}

func TestPlanInstallReportsUnmappedFilesAsDiagnostics(t *testing.T) {
	home, tmp := installHome(t)
	d := &spyInstallFS{extra: []string{".cursor/rules.md", ".aider.conf"}}
	p, err := app.PlanInstallWith(context.Background(), d, quiet(t).fetch, quiet(t).run, examplePack, "", home)
	if err != nil || len(p.Diagnostics) != 2 || len(p.Ops) != 0 || d.reads != 0 {
		t.Fatalf("PlanInstall() = %+v, %v after %d reads, want two diagnostics and no read", p, err, d.reads)
	}
	for i, want := range []string{"user/.aider.conf", "user/.cursor/rules.md"} {
		if g := p.Diagnostics[i]; g.Code != app.CodePathUnmapped || g.File != "dotai.yaml" || !strings.Contains(g.Message, want) || g.Hint == "" {
			t.Errorf("diagnostic %d = %+v, want %s at dotai.yaml naming %s with a hint", i, g, app.CodePathUnmapped, want)
		}
	}
	assertNoStaging(t, tmp)
}

func TestPlanInstallRemovesTheStagingOnFailure(t *testing.T) {
	t.Run("stage error", func(t *testing.T) {
		home, tmp := installHome(t)
		f := &fakeRulesync{t: t, quiet: true, fetchErr: errors.New("offline")}
		if _, err := f.planInstall(examplePack, "", home); err == nil {
			t.Error("PlanInstall() error = nil, want the fetch failure")
		}
		assertNoStaging(t, tmp)
	})
	t.Run("cancelled", func(t *testing.T) {
		home, tmp := installHome(t)
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		f := quiet(t)
		if _, err := app.PlanInstallWith(ctx, app.OSInstallFS{}, f.fetch, f.run, examplePack, "", home); !errors.Is(err, context.Canceled) {
			t.Errorf("PlanInstall() error = %v, want the cancellation", err)
		}
		assertNoStaging(t, tmp)
	})
	t.Run("panic", func(t *testing.T) {
		home, tmp := installHome(t)
		panics := func(context.Context, rulesync.Result, []string, ...rulesync.Job) error { panic("rulesync fake") }
		func() {
			defer func() {
				if r := recover(); r != "rulesync fake" {
					t.Errorf("recovered %v, want the fake's panic", r)
				}
			}()
			_, _ = app.PlanInstallWith(context.Background(), app.OSInstallFS{}, quiet(t).fetch, panics, examplePack, "", home)
		}()
		assertNoStaging(t, tmp)
	})
}

func TestPlanInstallReportsAStagingItCannotRemove(t *testing.T) {
	home, _ := installHome(t)
	d := &spyInstallFS{removeErr: errors.New("busy")}
	p, err := app.PlanInstallWith(context.Background(), d, quiet(t).fetch, quiet(t).run, examplePack, "", home)
	if err == nil || !strings.Contains(err.Error(), "could not be removed") || !strings.Contains(err.Error(), d.staging) || p.Ops != nil {
		t.Errorf("PlanInstall() = %+v, %v, want no plan and an error naming %s", p, err, d.staging)
	}
}
