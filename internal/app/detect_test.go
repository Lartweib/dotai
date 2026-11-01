package app_test

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/lartweib/dotai/internal/app"
	"github.com/lartweib/dotai/internal/harness"
	"github.com/lartweib/dotai/internal/pack"
	"github.com/lartweib/dotai/internal/state"
)

// found is a lookPath that finds each command of names in bin, and no
// other.
func found(bin string, names ...string) func(string) (string, error) {
	return func(name string) (string, error) {
		if slices.Contains(names, name) {
			return filepath.Join(bin, name), nil
		}
		return "", exec.ErrNotFound
	}
}

// statFS is the real file system with Stat failing with err for the
// paths in fail.
type statFS struct {
	app.OSInstallFS
	fail map[string]error
}

func (s statFS) Stat(name string) (fs.FileMode, error) {
	if err, ok := s.fail[name]; ok {
		return 0, err
	}
	return s.OSInstallFS.Stat(name)
}

// TestDetect checks the two signals of detection, the binary on PATH and
// the configuration directory, kept apart, and that agy needs its binary,
// since Gemini CLI creates ~/.gemini too (decision 0038).
func TestDetect(t *testing.T) {
	home := t.TempDir()
	bin := filepath.Join(home, "bin")
	for _, dir := range []string{".claude", ".gemini"} {
		if err := os.MkdirAll(at(home, dir), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	writeFile(t, at(home, ".config/opencode"), "a file, not a directory")
	tests := []struct {
		id       string
		lookPath func(string) (string, error)
		want     app.Detection
		why      string
	}{
		{"claudecode", found(bin, "claude"), app.Detection{Binary: filepath.Join(bin, "claude"), Config: at(home, ".claude"), ConfigFound: true, Detected: true}, ""},
		{"claudecode", found(bin), app.Detection{Config: at(home, ".claude"), ConfigFound: true, Detected: true}, ""},
		{"antigravity-cli", found(bin), app.Detection{Config: at(home, ".gemini"), ConfigFound: true},
			"agy not found on PATH; " + at(home, ".gemini") + " alone may be Gemini CLI's"},
		{"antigravity-cli", found(bin, "agy"), app.Detection{Binary: filepath.Join(bin, "agy"), Config: at(home, ".gemini"), ConfigFound: true, Detected: true}, ""},
		{"opencode", found(bin), app.Detection{Config: at(home, ".config/opencode")},
			"opencode not found on PATH and no " + at(home, ".config/opencode") + " directory"},
		{"opencode", found(bin, "opencode"), app.Detection{Binary: filepath.Join(bin, "opencode"), Config: at(home, ".config/opencode"), Detected: true}, ""},
		// A command found only through a relative PATH entry is not one.
		{"claudecode", func(string) (string, error) { return filepath.Join("bin", "claude"), exec.ErrDot },
			app.Detection{Config: at(home, ".claude"), ConfigFound: true, Detected: true, Relative: filepath.Join("bin", "claude")}, ""},
		{"antigravity-cli", func(string) (string, error) { return filepath.Join("bin", "agy"), exec.ErrDot },
			app.Detection{Config: at(home, ".gemini"), ConfigFound: true, Relative: filepath.Join("bin", "agy")},
			"agy was found only in the current directory or through a relative PATH entry (" + filepath.Join("bin", "agy") + "), which dotai does not use"},
	}
	for _, tt := range tests {
		got, err := app.Detect(app.OSInstallFS{}, tt.lookPath, home, adapterOf(t, tt.id))
		if err != nil || got != tt.want {
			t.Errorf("Detect(%s) = %+v, %v, want %+v", tt.id, got, err, tt.want)
		}
		if why := got.Why(adapterOf(t, tt.id)); why != tt.why {
			t.Errorf("Why(%s) = %q, want %q", tt.id, why, tt.why)
		}
	}
}

// TestDetectFailsOnAStatError checks that a configuration directory that
// cannot be checked is an error, not an absent harness.
func TestDetectFailsOnAStatError(t *testing.T) {
	home := t.TempDir()
	d := statFS{fail: map[string]error{at(home, ".claude"): fs.ErrPermission}}
	if _, err := app.Detect(d, found(home), home, adapterOf(t, "claudecode")); !errors.Is(err, fs.ErrPermission) || !strings.Contains(err.Error(), at(home, ".claude")) {
		t.Errorf("Detect() error = %v, want the permission error naming the directory", err)
	}
}

func adapterOf(t *testing.T, id string) harness.Adapter {
	t.Helper()
	a, err := harness.Lookup([]string{id})
	if err != nil {
		t.Fatal(err)
	}
	return a[0]
}

// TestSelectHarnesses checks which harnesses an install manages: the
// pack's harnesses that are detected by default, --harness when given, and
// the --harness choice an earlier install of the pack saved on a plain
// install; a required harness that is missing or left out stops it, a
// missing optional one is skipped with a notice (decision 0038). Only an
// explicit choice is saved, and a harness skipped as undetected never
// changes it.
func TestSelectHarnesses(t *testing.T) {
	m := pack.Manifest{Name: "p", Harnesses: []string{"claudecode"}, OptionalHarnesses: []string{"antigravity-cli", "opencode"}}
	det := func(ids ...string) map[string]app.Detection {
		out := map[string]app.Detection{}
		for _, id := range pack.Harnesses {
			out[id] = app.Detection{Config: "/h/" + id, Detected: slices.Contains(ids, id)}
		}
		return out
	}
	saved := func(name string, ids ...string) *state.State {
		return &state.State{Pack: state.Pack{Name: name}, Harnesses: ids, Selection: ids}
	}
	tests := []struct {
		name    string
		asked   []string
		prev    *state.State
		det     map[string]app.Detection
		want    []string
		choice  []string // the choice to save
		notices []string
		usage   bool
		err     string
	}{
		{name: "every detected harness", det: det(pack.Harnesses...), want: []string{"claudecode", "antigravity-cli", "opencode"}},
		{name: "a missing optional harness is skipped", det: det("claudecode", "opencode"), want: []string{"claudecode", "opencode"},
			notices: []string{"the optional harness antigravity-cli is skipped: it is not installed (agy not found on PATH)"}},
		{name: "a missing required harness stops", det: det("antigravity-cli", "opencode"),
			err: "the pack requires claudecode, which is not installed (claude not found on PATH and no /h/claudecode directory)"},
		{name: "--harness selects", asked: []string{"opencode", "claudecode"}, det: det(pack.Harnesses...), want: []string{"claudecode", "opencode"},
			choice:  []string{"claudecode", "opencode"},
			notices: []string{"the optional harness antigravity-cli is not managed: --harness leaves it out"}},
		{name: "--harness leaves out a required harness", asked: []string{"opencode"}, det: det(pack.Harnesses...), usage: true,
			err: "the pack requires claudecode, which --harness leaves out: add it to --harness"},
		{name: "--harness names an unknown harness", asked: []string{"claudecode", "codex"}, det: det(pack.Harnesses...), usage: true,
			err: `--harness names "codex", which the pack does not support: its harnesses are claudecode, antigravity-cli, opencode`},
		{name: "--harness asks for a missing harness", asked: []string{"claudecode", "antigravity-cli"}, det: det("claudecode"),
			err: "--harness asks for antigravity-cli, which is not installed (agy not found on PATH)"},
		{name: "a plain install reuses the saved choice", prev: saved("p", "claudecode", "opencode"), det: det(pack.Harnesses...),
			want: []string{"claudecode", "opencode"}, choice: []string{"claudecode", "opencode"}, notices: []string{"the optional harness antigravity-cli is not managed: the --harness choice an earlier install saved leaves it out; pass --harness to change it"}},
		{name: "a harness of the saved choice that is gone is skipped, and stays in the choice", prev: saved("p", "claudecode", "opencode"), det: det("claudecode"),
			want: []string{"claudecode"}, choice: []string{"claudecode", "opencode"}, notices: []string{
				"the optional harness antigravity-cli is not managed: the --harness choice an earlier install saved leaves it out; pass --harness to change it",
				"the optional harness opencode is skipped: it is not installed (opencode not found on PATH and no /h/opencode directory)"}},
		{name: "another pack's choice is not reused", prev: saved("other", "claudecode"), det: det(pack.Harnesses...),
			want: []string{"claudecode", "antigravity-cli", "opencode"}},
		{name: "--harness wins over the saved choice", asked: []string{"claudecode", "antigravity-cli"}, prev: saved("p", "claudecode"), det: det(pack.Harnesses...),
			want: []string{"claudecode", "antigravity-cli"}, choice: []string{"claudecode", "antigravity-cli"},
			notices: []string{"the optional harness opencode is not managed: --harness leaves it out"}},
		// What an install managed without --harness, or an older state, is
		// no choice: the default applies.
		{name: "the harnesses an install managed are not a choice", prev: &state.State{Pack: state.Pack{Name: "p"}, Harnesses: []string{"claudecode"}},
			det: det(pack.Harnesses...), want: []string{"claudecode", "antigravity-cli", "opencode"}},
		{name: "a required harness the saved choice lacks is managed", prev: saved("p", "opencode"), det: det(pack.Harnesses...),
			want: []string{"claudecode", "opencode"}, choice: []string{"opencode"}, notices: []string{"the optional harness antigravity-cli is not managed: the --harness choice an earlier install saved leaves it out; pass --harness to change it"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, notices, choice, err := app.SelectHarnesses(m, tt.asked, tt.prev, tt.det)
			if tt.err != "" {
				var usage *app.UsageError
				if err == nil || err.Error() != tt.err || errors.As(err, &usage) != tt.usage {
					t.Fatalf("SelectHarnesses() error = %v, want %q (usage %v)", err, tt.err, tt.usage)
				}
				return
			}
			if err != nil || !reflect.DeepEqual(got, tt.want) || !reflect.DeepEqual(notices, tt.notices) || !reflect.DeepEqual(choice, tt.choice) {
				t.Errorf("SelectHarnesses() = %v, %q, choice %v, %v, want %v, %q, choice %v", got, notices, choice, err, tt.want, tt.notices, tt.choice)
			}
		})
	}
}

// TestPlanInstallSkipsAMissingOptionalHarness checks a plan with a missing
// optional harness: nothing is staged or planned for it, it is listed as
// not managed with its detection, and a notice says why; the install
// state saves the selection.
func TestPlanInstallSkipsAMissingOptionalHarness(t *testing.T) {
	home, _ := installHome(t)
	dir := copyPack(t)
	editPack(t, dir, "dotai.yaml", "harnesses: [claudecode, antigravity-cli, opencode]", "harnesses: [claudecode, opencode]\noptional-harnesses: [antigravity-cli]")
	h := app.NewHost(found(at(home, "bin"), "claude", "opencode"), nil, "")
	f := quiet(t)
	p, err := app.PlanInstallHost(context.Background(), app.OSInstallFS{}, h, f.fetch, f.run, dir, home, app.InstallOptions{})
	if err != nil || len(p.Diagnostics) != 0 {
		t.Fatalf("PlanInstall() = %+v, %v", p, err)
	}
	var ids []string
	for _, hp := range p.Harnesses {
		ids = append(ids, hp.ID)
		if hp.Managed != (hp.ID != "antigravity-cli") || hp.Required != (hp.ID != "antigravity-cli") || hp.Config == "" {
			t.Errorf("harness %+v, want claudecode and opencode required and managed, agy optional and not", hp)
		}
	}
	if !reflect.DeepEqual(ids, []string{"claudecode", "opencode", "antigravity-cli"}) {
		t.Errorf("harnesses = %v, want the required ones, then the optional one", ids)
	}
	for _, op := range p.Ops {
		if op.Harness == "antigravity-cli" {
			t.Errorf("op %+v for a harness that is not managed", op)
		}
	}
	if want := []string{"the optional harness antigravity-cli is skipped: it is not installed (agy not found on PATH)"}; !reflect.DeepEqual(p.Notices, want) {
		t.Errorf("notices = %q, want %q", p.Notices, want)
	}
	if _, err := app.InstallHost(context.Background(), newClockFS(t), h, f.fetch, f.run, dir, home, app.InstallOptions{}); err != nil {
		t.Fatal(err)
	}
	s := readJSON(t, at(home, ".dotai/state.json"), state.Decode)
	if !reflect.DeepEqual(s.Harnesses, []string{"claudecode", "opencode"}) || s.Selection != nil {
		t.Errorf("state harnesses = %v, selection %v, want what it manages and no choice", s.Harnesses, s.Selection)
	}

	// agy installed since: the next plain install manages it again, since
	// skipping it as undetected saved no choice.
	h = app.NewHost(found(at(home, "bin"), "claude", "opencode", "agy"), nil, "")
	if _, err := app.InstallHost(context.Background(), newClockFS(t), h, f.fetch, f.run, dir, home, app.InstallOptions{}); err != nil {
		t.Fatal(err)
	}
	s = readJSON(t, at(home, ".dotai/state.json"), state.Decode)
	if !reflect.DeepEqual(s.Harnesses, []string{"claudecode", "opencode", "antigravity-cli"}) || s.Selection != nil {
		t.Errorf("state harnesses = %v, selection %v, want agy managed again", s.Harnesses, s.Selection)
	}

	// An explicit choice is saved, and a plain install reuses it.
	if _, err := app.InstallHost(context.Background(), newClockFS(t), h, f.fetch, f.run, dir, home, app.InstallOptions{Harnesses: []string{"claudecode", "opencode"}}); err != nil {
		t.Fatal(err)
	}
	s = readJSON(t, at(home, ".dotai/state.json"), state.Decode)
	if !reflect.DeepEqual(s.Selection, []string{"claudecode", "opencode"}) {
		t.Errorf("state selection = %v, want the --harness choice", s.Selection)
	}
	p, err = app.PlanInstallHost(context.Background(), app.OSInstallFS{}, h, f.fetch, f.run, dir, home, app.InstallOptions{})
	if err != nil || slices.ContainsFunc(p.Ops, func(op app.InstallOp) bool { return op.Harness == "antigravity-cli" }) {
		t.Errorf("PlanInstall() = %+v, %v, want the saved choice without agy", p.Ops, err)
	}
}

// TestInstallStopsOnAMissingRequiredHarness checks that an install whose
// pack requires a harness that is not installed stops before it writes
// anything, naming the harness and why.
func TestInstallStopsOnAMissingRequiredHarness(t *testing.T) {
	home, _ := installHome(t)
	h := app.NewHost(found(at(home, "bin"), "claude", "opencode"), nil, "")
	f := quiet(t)
	_, err := app.InstallHost(context.Background(), newClockFS(t), h, f.fetch, f.run, examplePack, home, app.InstallOptions{})
	if err == nil || err.Error() != "the pack requires antigravity-cli, which is not installed (agy not found on PATH)" {
		t.Fatalf("Install() error = %v, want the missing agy named", err)
	}
	if got := tree(t, home); len(got) != 0 {
		t.Errorf("home = %v, want nothing written", got)
	}
	if len(f.runs) != 0 {
		t.Errorf("rulesync ran %d times, want the install stopped before rendering", len(f.runs))
	}
}
