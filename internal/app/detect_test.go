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
	"github.com/lartweib/dotai/internal/plan"
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

// / TestSelectHarnesses checks which harnesses an install manages (decisions
// 0038 and 0047): by default the pack's compatible harnesses that are
// detected and supported, --harness when given, and the --harness choice
// an earlier install of the pack saved on a plain install. A preview
// harness is managed only when the choice names it, with a harness-preview
// warning. A required harness that is missing or left out, a name that is
// unknown or not compatible, and no harness at all stop it, each with its
// code. Only an explicit choice is saved, and a harness skipped as
// undetected never changes it.
func TestSelectHarnesses(t *testing.T) {
	m := pack.Manifest{Name: "p", Harnesses: []string{"claudecode", "antigravity-cli", "opencode"}, Requires: []string{"claudecode"}}
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
	oc := adapterOf(t, "opencode")
	previewNotice := "the preview harness opencode is not managed unless you choose it: " + oc.StatusReason + "; pass --harness opencode to manage it"
	applyNotice := "the preview harness opencode is not managed unless you choose it: " + oc.StatusReason + "; choose it with dotai install --harness, then apply again"
	keptNotice := "the preview harness opencode stays managed: the earlier install of the pack managed it; pass --harness to change it"
	managed := func(name string, ids ...string) *state.State {
		return &state.State{Pack: state.Pack{Name: name}, Harnesses: ids}
	}
	previewWarning := []plan.Warning{{Code: app.CodeHarnessPreview, Harness: "opencode", Message: "opencode is a preview harness: " + oc.StatusReason}}
	tests := []struct {
		name     string
		m        *pack.Manifest
		apply    bool // an apply's selection, which follows the install's
		asked    []string
		prev     *state.State
		det      map[string]app.Detection
		want     []string
		choice   []string // the choice to save
		notices  []string
		warnings []plan.Warning
		codes    []string // the codes of the refusals
		err      string
	}{
		{name: "the detected supported harnesses, without the preview one", det: det(pack.Harnesses...), want: []string{"claudecode", "antigravity-cli"},
			notices: []string{previewNotice}},
		{name: "a missing harness that is not required is skipped", det: det("claudecode"), want: []string{"claudecode"},
			notices: []string{"the harness antigravity-cli is skipped: it is not installed (agy not found on PATH)",
				"the harness opencode is skipped: it is not installed (opencode not found on PATH and no /h/opencode directory)"}},
		{name: "a missing required harness stops", det: det("antigravity-cli", "opencode"), codes: []string{app.CodeHarnessMissing},
			err: "the pack requires claudecode, which is not installed (claude not found on PATH and no /h/claudecode directory)"},
		{name: "--harness opts in to the preview harness", asked: []string{"opencode", "claudecode"}, det: det(pack.Harnesses...), want: []string{"claudecode", "opencode"},
			choice: []string{"claudecode", "opencode"}, warnings: previewWarning,
			notices: []string{"the harness antigravity-cli is not managed: --harness leaves it out"}},
		{name: "--harness leaves out a required harness", asked: []string{"opencode"}, det: det(pack.Harnesses...), codes: []string{app.CodeHarnessRequired},
			err: "the pack requires claudecode, which --harness leaves out: add it to --harness"},
		{name: "--harness names an unknown harness", asked: []string{"claudecode", "codex"}, det: det(pack.Harnesses...), codes: []string{app.CodeHarnessUnknown},
			err: `--harness names "codex", which dotai does not know: it knows claudecode, antigravity-cli, opencode`},
		{name: "--harness names a harness the pack is not compatible with", m: &pack.Manifest{Name: "p", Harnesses: []string{"claudecode"}},
			asked: []string{"claudecode", "opencode"}, det: det(pack.Harnesses...), codes: []string{app.CodeHarnessNotCompatible},
			err: `--harness names "opencode", which the pack is not compatible with: its harnesses are claudecode`},
		{name: "--harness asks for a missing harness", asked: []string{"claudecode", "antigravity-cli"}, det: det("claudecode"), codes: []string{app.CodeHarnessNotInstalled},
			err: "--harness asks for antigravity-cli, which is not installed (agy not found on PATH)"},
		{name: "a plain install reuses the saved choice, the preview harness in it", prev: saved("p", "claudecode", "opencode"), det: det(pack.Harnesses...),
			want: []string{"claudecode", "opencode"}, choice: []string{"claudecode", "opencode"}, warnings: previewWarning,
			notices: []string{"the harness antigravity-cli is not managed: the --harness choice an earlier install saved leaves it out; pass --harness to change it"}},
		{name: "a harness of the saved choice that is gone is skipped, and stays in the choice", prev: saved("p", "claudecode", "antigravity-cli"), det: det("claudecode", "opencode"),
			want: []string{"claudecode"}, choice: []string{"claudecode", "antigravity-cli"}, notices: []string{
				"the harness antigravity-cli is skipped: it is not installed (agy not found on PATH)",
				"the harness opencode is not managed: the --harness choice an earlier install saved leaves it out; pass --harness to change it"}},
		{name: "another pack's choice is not reused", prev: saved("other", "claudecode"), det: det(pack.Harnesses...),
			want: []string{"claudecode", "antigravity-cli"}, notices: []string{previewNotice}},
		{name: "--harness wins over the saved choice", asked: []string{"claudecode", "antigravity-cli"}, prev: saved("p", "claudecode"), det: det(pack.Harnesses...),
			want: []string{"claudecode", "antigravity-cli"}, choice: []string{"claudecode", "antigravity-cli"},
			notices: []string{"the harness opencode is not managed: --harness leaves it out"}},
		// What an install managed without --harness, or an older state, is
		// no choice: the default applies, but a preview harness it managed
		// stays opted in, so that an install from before decision 0047 does
		// not drop it.
		{name: "the harnesses an install managed are not a choice, but a preview one it managed stays", prev: managed("p", "claudecode", "opencode"),
			det: det(pack.Harnesses...), want: []string{"claudecode", "antigravity-cli", "opencode"}, warnings: previewWarning, notices: []string{keptNotice}},
		{name: "a preview harness an install did not manage stays out", prev: managed("p", "claudecode"),
			det: det(pack.Harnesses...), want: []string{"claudecode", "antigravity-cli"}, notices: []string{previewNotice}},
		{name: "a preview harness another pack's install managed stays out", prev: managed("other", "claudecode", "opencode"),
			det: det(pack.Harnesses...), want: []string{"claudecode", "antigravity-cli"}, notices: []string{previewNotice}},
		{name: "an apply leaves out a detected preview harness by default, and says to choose it at install", apply: true,
			det: det(pack.Harnesses...), want: []string{"claudecode", "antigravity-cli"}, notices: []string{applyNotice}},
		{name: "an apply follows a saved choice that names the preview harness", apply: true, prev: saved("p", "claudecode", "opencode"), det: det(pack.Harnesses...),
			want: []string{"claudecode", "opencode"}, choice: []string{"claudecode", "opencode"}, warnings: previewWarning,
			notices: []string{"the harness antigravity-cli is not managed: the --harness choice an earlier install saved leaves it out; choose again with dotai install --harness to change it"}},
		{name: "an apply's refusal says to choose with dotai install", apply: true, m: &pack.Manifest{Name: "p", Harnesses: []string{"claudecode", "opencode"}, Requires: []string{"opencode"}},
			det: det(pack.Harnesses...), codes: []string{app.CodeHarnessPreviewRequired},
			err: "the pack requires opencode, a preview harness that is managed only when you choose it: " + oc.StatusReason + "; choose it with dotai install --harness, then apply again"},
		{name: "an apply with only a preview harness says to choose it at install", apply: true, det: det("opencode"), m: &pack.Manifest{Name: "p", Harnesses: []string{"antigravity-cli", "opencode"}},
			codes: []string{app.CodeHarnessNoneSelected},
			err:   "no harness the pack is compatible with is selected: antigravity-cli is not installed (agy not found on PATH); opencode is a preview harness: choose it with dotai install --harness, then apply again"},
		{name: "a required harness the saved choice lacks is managed", prev: saved("p", "antigravity-cli"), det: det(pack.Harnesses...),
			want: []string{"claudecode", "antigravity-cli"}, choice: []string{"antigravity-cli"},
			notices: []string{"the harness opencode is not managed: the --harness choice an earlier install saved leaves it out; pass --harness to change it"}},
		{name: "a required preview harness is managed only when named", m: &pack.Manifest{Name: "p", Harnesses: []string{"claudecode", "opencode"}, Requires: []string{"opencode"}},
			det: det(pack.Harnesses...), codes: []string{app.CodeHarnessPreviewRequired},
			err: "the pack requires opencode, a preview harness that is managed only when you choose it: " + oc.StatusReason + "; pass --harness opencode to manage it"},
		{name: "a required preview harness that is missing too gets both refusals", m: &pack.Manifest{Name: "p", Harnesses: []string{"claudecode", "opencode"}, Requires: []string{"opencode"}},
			det: det("claudecode"), codes: []string{app.CodeHarnessPreviewRequired, app.CodeHarnessMissing},
			err: "the pack requires opencode, a preview harness that is managed only when you choose it: " + oc.StatusReason + "; pass --harness opencode to manage it; " +
				"the pack requires opencode, which is not installed (opencode not found on PATH and no /h/opencode directory)"},
		{name: "a required preview harness the earlier install managed stays", m: &pack.Manifest{Name: "p", Harnesses: []string{"claudecode", "opencode"}, Requires: []string{"opencode"}},
			prev: managed("p", "claudecode", "opencode"), det: det(pack.Harnesses...), want: []string{"claudecode", "opencode"}, warnings: previewWarning, notices: []string{keptNotice}},
		{name: "no compatible harness detected stops", det: det(), m: &pack.Manifest{Name: "p", Harnesses: []string{"antigravity-cli", "opencode"}},
			codes: []string{app.CodeHarnessNoneSelected},
			err:   "no harness the pack is compatible with is selected: antigravity-cli is not installed (agy not found on PATH); opencode is not installed (opencode not found on PATH and no /h/opencode directory)"},
		{name: "only the preview harness detected stops, naming the opt-in", det: det("opencode"), m: &pack.Manifest{Name: "p", Harnesses: []string{"antigravity-cli", "opencode"}},
			codes: []string{app.CodeHarnessNoneSelected},
			err:   "no harness the pack is compatible with is selected: antigravity-cli is not installed (agy not found on PATH); opencode is a preview harness: pass --harness opencode to manage it"},
		{name: "an empty --harness stops", asked: []string{}, m: &pack.Manifest{Name: "p", Harnesses: []string{"claudecode"}}, det: det(pack.Harnesses...),
			codes: []string{app.CodeHarnessNoneSelected}, err: "no harness the pack is compatible with is selected: --harness names none"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mm := m
			if tt.m != nil {
				mm = *tt.m
			}
			sel := app.SelectHarnesses
			if tt.apply {
				sel = app.SelectHarnessesForApply
			}
			got, err := sel(mm, tt.asked, tt.prev, tt.det)
			if tt.err != "" {
				refused, ok := errors.AsType[*app.HarnessRefusedError](err)
				if !ok || err.Error() != tt.err {
					t.Fatalf("SelectHarnesses() error = %v, want %q", err, tt.err)
				}
				var codes []string
				for _, r := range refused.Refusals {
					codes = append(codes, r.Code)
				}
				if !reflect.DeepEqual(codes, tt.codes) || len(refused.Harnesses) != len(pack.Harnesses) {
					t.Errorf("refusals %+v, harnesses %+v, want codes %v and every harness", refused.Refusals, refused.Harnesses, tt.codes)
				}
				return
			}
			if err != nil || !reflect.DeepEqual(got.Selected, tt.want) || !reflect.DeepEqual(got.Notices, tt.notices) ||
				!reflect.DeepEqual(got.Choice, tt.choice) || !reflect.DeepEqual(got.Warnings, tt.warnings) {
				t.Errorf("SelectHarnesses() = %v, %q, choice %v, warnings %+v, %v, want %v, %q, choice %v, warnings %+v",
					got.Selected, got.Notices, got.Choice, got.Warnings, err, tt.want, tt.notices, tt.choice, tt.warnings)
			}
		})
	}
}

// TestSelectHarnessesStates checks the state of every harness dotai knows
// that a selection reports (decision 0047): the compatible ones in the
// manifest's order, then the others; whether each is compatible, required,
// a preview with its reason, detected, and managed.
func TestSelectHarnessesStates(t *testing.T) {
	m := pack.Manifest{Name: "p", Harnesses: []string{"opencode", "claudecode"}, Requires: []string{"claudecode"}}
	det := map[string]app.Detection{
		"claudecode":      {Config: "/h/claude", Detected: true},
		"antigravity-cli": {Config: "/h/gemini", Detected: true, Binary: "/bin/agy"},
		"opencode":        {Config: "/h/opencode", Detected: true},
	}
	got, err := app.SelectHarnesses(m, nil, nil, det)
	if err != nil {
		t.Fatal(err)
	}
	reason := adapterOf(t, "opencode").StatusReason
	want := []app.HarnessState{
		{ID: "opencode", Compatible: true, Status: "preview", StatusReason: reason, Detection: det["opencode"]},
		{ID: "claudecode", Compatible: true, Required: true, Status: "supported", Managed: true, Detection: det["claudecode"]},
		{ID: "antigravity-cli", Status: "supported", Detection: det["antigravity-cli"]},
	}
	if !reflect.DeepEqual(got.States, want) {
		t.Errorf("states =\n%+v\nwant\n%+v", got.States, want)
	}
}

// TestPlanInstallSelectsHarnesses checks the selection of a plan (decision
// 0047): of a pack compatible with every harness and requiring none, the
// plan manages the detected supported ones, lists every harness with its
// state, skips the missing one and the preview one with a notice each, and
// stages nothing for them. A plain install saves no choice, so a harness
// installed since is managed by the next one; --harness opts in to the
// preview harness, with a harness-preview warning, and is saved and
// reused.
func TestPlanInstallSelectsHarnesses(t *testing.T) {
	home, _ := installHome(t)
	h := app.NewHost(found(at(home, "bin"), "claude", "opencode"), nil, "")
	f := quiet(t)
	p, err := app.PlanInstallHost(context.Background(), app.OSInstallFS{}, h, f.fetch, f.run, examplePack, home, app.InstallOptions{})
	if err != nil || len(p.Diagnostics) != 0 {
		t.Fatalf("PlanInstall() = %+v, %v", p, err)
	}
	type st struct {
		id                                      string
		compatible, required, detected, managed bool
		status                                  string
	}
	var got []st
	for _, hp := range p.Harnesses {
		got = append(got, st{hp.ID, hp.Compatible, hp.Required, hp.Detected, hp.Managed, hp.Status})
		if hp.Config == "" || (hp.Status == "preview") != (hp.StatusReason != "") {
			t.Errorf("harness %+v, want its config and a reason for a preview only", hp)
		}
	}
	want := []st{
		{"claudecode", true, false, true, true, "supported"},
		{"antigravity-cli", true, false, false, false, "supported"},
		{"opencode", true, false, true, false, "preview"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("harnesses =\n%+v\nwant\n%+v", got, want)
	}
	if slices.ContainsFunc(p.Ops, func(op app.InstallOp) bool { return op.Harness != "claudecode" && op.Harness != "" }) {
		t.Errorf("ops %+v, want only claudecode's and the tools", p.Ops)
	}
	reason := adapterOf(t, "opencode").StatusReason
	if want := []string{"the harness antigravity-cli is skipped: it is not installed (agy not found on PATH)",
		"the preview harness opencode is not managed unless you choose it: " + reason + "; pass --harness opencode to manage it"}; !reflect.DeepEqual(p.Notices, want) {
		t.Errorf("notices = %q, want %q", p.Notices, want)
	}
	if slices.ContainsFunc(p.Warnings, func(w plan.Warning) bool { return w.Code == app.CodeHarnessPreview }) {
		t.Errorf("warnings %+v, want no harness-preview for a harness not managed", p.Warnings)
	}
	if _, err := app.InstallHost(context.Background(), newClockFS(t), h, f.fetch, f.run, examplePack, home, app.InstallOptions{}); err != nil {
		t.Fatal(err)
	}
	s := readJSON(t, at(home, ".dotai/state.json"), state.Decode)
	if !reflect.DeepEqual(s.Harnesses, []string{"claudecode"}) || s.Selection != nil {
		t.Errorf("state harnesses = %v, selection %v, want what it manages and no choice", s.Harnesses, s.Selection)
	}

	// agy installed since: the next plain install manages it, since
	// skipping it as undetected saved no choice.
	h = app.NewHost(found(at(home, "bin"), "claude", "opencode", "agy"), nil, "")
	if _, err := app.InstallHost(context.Background(), newClockFS(t), h, f.fetch, f.run, examplePack, home, app.InstallOptions{}); err != nil {
		t.Fatal(err)
	}
	s = readJSON(t, at(home, ".dotai/state.json"), state.Decode)
	if !reflect.DeepEqual(s.Harnesses, []string{"claudecode", "antigravity-cli"}) || s.Selection != nil {
		t.Errorf("state harnesses = %v, selection %v, want agy managed too", s.Harnesses, s.Selection)
	}

	// --harness opts in to the preview harness; the choice is saved, and a
	// plain install reuses it, with the warning.
	res, err := app.InstallHost(context.Background(), newClockFS(t), h, f.fetch, f.run, examplePack, home, app.InstallOptions{Harnesses: []string{"claudecode", "opencode"}})
	if err != nil {
		t.Fatal(err)
	}
	s = readJSON(t, at(home, ".dotai/state.json"), state.Decode)
	if !reflect.DeepEqual(s.Selection, []string{"claudecode", "opencode"}) {
		t.Errorf("state selection = %v, want the --harness choice", s.Selection)
	}
	wantWarning := plan.Warning{Code: app.CodeHarnessPreview, Harness: "opencode", Message: "opencode is a preview harness: " + reason}
	if !slices.Contains(res.InstallPlan.Warnings, wantWarning) {
		t.Errorf("install warnings %+v, want %+v", res.InstallPlan.Warnings, wantWarning)
	}
	p, err = app.PlanInstallHost(context.Background(), app.OSInstallFS{}, h, f.fetch, f.run, examplePack, home, app.InstallOptions{})
	if err != nil || slices.ContainsFunc(p.Ops, func(op app.InstallOp) bool { return op.Harness == "antigravity-cli" }) ||
		!slices.ContainsFunc(p.Ops, func(op app.InstallOp) bool { return op.Harness == "opencode" }) || !slices.Contains(p.Warnings, wantWarning) {
		t.Errorf("PlanInstall() = %+v, warnings %+v, %v, want the saved choice with opencode and its warning", p.Ops, p.Warnings, err)
	}
}

// TestPlanInstallListsHarnessesThePackIsNotCompatibleWith checks that a
// plan lists every harness dotai knows (decision 0047): one the pack is not
// compatible with comes after the compatible ones, not managed, with its
// detection and status.
func TestPlanInstallListsHarnessesThePackIsNotCompatibleWith(t *testing.T) {
	home, _ := installHome(t)
	dir := copyPack(t)
	editPack(t, dir, "dotai.yaml", "harnesses: [claudecode, antigravity-cli, opencode]", "harnesses: [claudecode, opencode]")
	editPack(t, dir, "base/dotai/settings.yaml", "antigravity-cli:", "unused:")
	editPack(t, dir, "base/dotai/settings.yaml", "unused:\n  statusLine: { type: command, command: \"${DOTAI_TOOL:hello}\" }\n", "")
	h := app.NewHost(found(at(home, "bin"), "claude", "agy"), nil, "")
	f := quiet(t)
	p, err := app.PlanInstallHost(context.Background(), app.OSInstallFS{}, h, f.fetch, f.run, dir, home, app.InstallOptions{})
	if err != nil || len(p.Diagnostics) != 0 {
		t.Fatalf("PlanInstall() = %+v, %v", p, err)
	}
	var ids []string
	for _, hp := range p.Harnesses {
		ids = append(ids, hp.ID)
		if hp.ID == "antigravity-cli" && (hp.Compatible || hp.Managed || hp.Status != "supported" || hp.Dir != "" || !hp.Detected) {
			t.Errorf("harness %+v, want agy not compatible, not managed, supported and detected", hp)
		}
	}
	if !reflect.DeepEqual(ids, []string{"claudecode", "opencode", "antigravity-cli"}) {
		t.Errorf("harnesses = %v, want the compatible ones, then the other", ids)
	}
	if slices.ContainsFunc(p.Notices, func(n string) bool { return strings.Contains(n, "antigravity-cli") }) {
		t.Errorf("notices = %q, want none for a harness the pack is not compatible with", p.Notices)
	}
}

// TestInstallStopsOnAMissingRequiredHarness checks that an install whose
// pack requires a harness that is not installed stops before it writes
// anything, with the code harness-missing, naming the harness and why.
func TestInstallStopsOnAMissingRequiredHarness(t *testing.T) {
	home, _ := installHome(t)
	h := app.NewHost(found(at(home, "bin"), "claude", "opencode"), nil, "")
	f := quiet(t)
	dir := copyPack(t)
	editPack(t, dir, "dotai.yaml", "harnesses: [claudecode, antigravity-cli, opencode]", "harnesses: [claudecode, antigravity-cli, opencode]\nrequires: [antigravity-cli]")
	_, err := app.InstallHost(context.Background(), newClockFS(t), h, f.fetch, f.run, dir, home, app.InstallOptions{})
	refused, ok := errors.AsType[*app.HarnessRefusedError](err)
	if !ok || err.Error() != "the pack requires antigravity-cli, which is not installed (agy not found on PATH)" ||
		len(refused.Refusals) != 1 || refused.Refusals[0].Code != app.CodeHarnessMissing || refused.Refusals[0].Harness != "antigravity-cli" {
		t.Fatalf("Install() error = %v, want harness-missing naming agy", err)
	}
	if got := tree(t, home); len(got) != 0 {
		t.Errorf("home = %v, want nothing written", got)
	}
	if len(f.runs) != 0 {
		t.Errorf("rulesync ran %d times, want the install stopped before rendering", len(f.runs))
	}
}

// TestAnInstallFromBefore0047KeepsItsPreviewHarness checks the upgrade of
// an install that managed OpenCode without a saved choice, as every
// install from before decision 0047 did: the next plain install keeps
// managing it, with the harness-preview warning, and removes none of its
// files, rather than dropping it without consent.
func TestAnInstallFromBefore0047KeepsItsPreviewHarness(t *testing.T) {
	home, _ := installHome(t)
	h := app.NewHost(found(at(home, "bin"), "claude", "agy", "opencode"), nil, "")
	f := quiet(t)
	if _, err := app.InstallHost(context.Background(), newClockFS(t), h, f.fetch, f.run, examplePack, home, app.InstallOptions{Harnesses: pack.Harnesses}); err != nil {
		t.Fatal(err)
	}
	// An install from before decision 0047 saved no choice.
	s := readJSON(t, at(home, ".dotai/state.json"), state.Decode)
	s.Selection = nil
	data, err := state.Encode(s)
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, at(home, ".dotai/state.json"), string(data))

	p, err := app.PlanInstallHost(context.Background(), app.OSInstallFS{}, h, f.fetch, f.run, examplePack, home, app.InstallOptions{})
	if err != nil || len(p.Diagnostics) != 0 {
		t.Fatalf("PlanInstall() = %+v, %v", p, err)
	}
	for _, op := range p.Ops {
		if op.Action == plan.Remove {
			t.Errorf("op %+v removes a file, want OpenCode kept", op)
		}
	}
	if !slices.ContainsFunc(p.Harnesses, func(hp app.HarnessPlan) bool { return hp.ID == "opencode" && hp.Managed }) ||
		!slices.ContainsFunc(p.Warnings, func(w plan.Warning) bool { return w.Code == app.CodeHarnessPreview && w.Harness == "opencode" }) {
		t.Errorf("harnesses %+v, warnings %+v, want OpenCode managed with harness-preview", p.Harnesses, p.Warnings)
	}
}
