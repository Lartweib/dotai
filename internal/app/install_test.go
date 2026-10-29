package app_test

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/lartweib/dotai/internal/app"
	"github.com/lartweib/dotai/internal/harness"
	"github.com/lartweib/dotai/internal/plan"
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
	return app.PlanInstallWith(app.OSInstallFS{}, f.fetch, f.run, dir, variant, home)
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
		if h.ID != a.ID || h.Level != string(a.Isolation.Level) || h.Dir != dir || h.Relogin != a.Isolation.Relogin ||
			!reflect.DeepEqual(h.Leaks, a.Isolation.Leaks) || len(h.Pointers) != len(a.Isolation.Pointers) {
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
