package app

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"

	"github.com/lartweib/dotai/internal/harness"
	"github.com/lartweib/dotai/internal/pack"
	"github.com/lartweib/dotai/internal/plan"
	"github.com/lartweib/dotai/internal/proc"
	"github.com/lartweib/dotai/internal/state"
)

// host is what an install reads of the machine besides the disk: how a
// command is found on PATH, the environment, the OS, and the processes
// running, besides self, dotai's own; nil processes lists none.
type host struct {
	lookPath  func(string) (string, error)
	environ   []string
	goos      string
	processes processList
	self      int
	// exe gives the dotai binary, which an install names in the
	// session-start hook of decision 0045, DotaiBinary; nil installs no
	// onboarding, for a test that does not stage one.
	exe func() (string, error)
}

// osHost is the host dotai runs on.
func osHost() host {
	return host{lookPath: exec.LookPath, environ: os.Environ(), goos: runtime.GOOS, processes: proc.List, self: os.Getpid(), exe: DotaiBinary}
}

// DotaiBinary is the dotai binary the session-start hook of decision 0045
// names: the dotai on PATH when it is the very file that runs, as a
// package manager's link to its versioned directory is, whose path
// outlives an upgrade, and otherwise the binary that runs, as
// os.Executable gives it.
func DotaiBinary() (string, error) { return dotaiBinary(exec.LookPath, os.Executable) }

func dotaiBinary(lookPath func(string) (string, error), executable func() (string, error)) (string, error) {
	exe, err := executable()
	if err != nil {
		return "", err
	}
	found, err := lookPath("dotai")
	if err != nil || !filepath.IsAbs(found) {
		return exe, nil
	}
	a, errA := os.Stat(found)
	b, errB := os.Stat(exe)
	if errA == nil && errB == nil && os.SameFile(a, b) {
		return found, nil
	}
	return exe, nil
}

// Detection is whether a harness is installed, from two separate signals
// (decision 0038): Binary, its command as found on PATH, "" when it is
// not, and whether its configuration directory Config exists. Detected is
// either signal, or only the binary for a harness whose directory another
// program creates too, such as agy's ~/.gemini.
//
// Relative is the command when it was found only in the current directory
// or through a relative PATH entry, which does not count, so that Why can
// say so.
type Detection struct {
	Detected    bool   `json:"detected"`
	Binary      string `json:"binary"`
	Config      string `json:"config"`
	ConfigFound bool   `json:"configFound"`
	Relative    string `json:"-"`
}

// detect detects the harness a in home: its command through lookPath,
// which a command found only through a relative PATH entry does not count
// for, and its configuration directory through d.Stat. A directory that
// cannot be checked, for any reason but that it is missing, is an error,
// not an absent harness.
func detect(d installFS, lookPath func(string) (string, error), home string, a harness.Adapter) (Detection, error) {
	det := Detection{Config: filepath.Join(home, filepath.FromSlash(a.Real.Dir))}
	switch p, err := lookPath(a.Command); {
	case err == nil && p != "":
		det.Binary = p
	case errors.Is(err, exec.ErrDot):
		det.Relative = p
	}
	mode, err := d.Stat(det.Config)
	switch {
	case err == nil:
		det.ConfigFound = mode.IsDir()
	case !errors.Is(err, fs.ErrNotExist):
		return Detection{}, fmt.Errorf("dotai cannot tell whether %s is installed: %s: %w", a.ID, det.Config, err)
	}
	det.Detected = det.Binary != "" || det.ConfigFound && !a.Real.NeedsBinary
	return det, nil
}

// Why is why d is not detected, text for people; "" when it is.
func (d Detection) Why(a harness.Adapter) string {
	switch {
	case d.Detected:
		return ""
	case d.Relative != "":
		return fmt.Sprintf("%s was found only in the current directory or through a relative PATH entry (%s), which dotai does not use", a.Command, d.Relative)
	case a.Real.NeedsBinary && d.ConfigFound:
		return fmt.Sprintf("%s not found on PATH; %s alone may be Gemini CLI's", a.Command, d.Config)
	case a.Real.NeedsBinary:
		return fmt.Sprintf("%s not found on PATH", a.Command)
	}
	return fmt.Sprintf("%s not found on PATH and no %s directory", a.Command, d.Config)
}

// detectAll detects every harness dotai knows in home. A harness m is not
// compatible with is detected for the plan to show only: a configuration
// directory of it that cannot be checked counts as not found, and does not
// stop the install.
func detectAll(d installFS, h host, home string, m pack.Manifest) (map[string]Detection, error) {
	out := map[string]Detection{}
	for _, a := range harness.All() {
		det, err := detect(d, h.lookPath, home, a)
		switch {
		case err != nil && slices.Contains(m.Harnesses, a.ID):
			return nil, err
		case err != nil:
			det = Detection{Config: filepath.Join(home, filepath.FromSlash(a.Real.Dir))}
		}
		out[a.ID] = det
	}
	return out, nil
}

// Codes of the warning of a preview harness an install manages, and of the
// refusals of a harness selection (decision 0047).
const (
	// CodeHarnessPreview warns that the install manages a preview harness,
	// with the reason it is one; it blocks nothing and needs no consent.
	CodeHarnessPreview = "harness-preview"
	// A name the harness choice gives that dotai does not know, or that the
	// pack is not compatible with.
	CodeHarnessUnknown       = "harness-unknown"
	CodeHarnessNotCompatible = "harness-not-compatible"
	// A required harness the harness choice leaves out, one that is not
	// installed, and one that is a preview no choice names; a required
	// preview harness that is not installed gets both of the last two.
	CodeHarnessRequired        = "harness-required"
	CodeHarnessMissing         = "harness-missing"
	CodeHarnessPreviewRequired = "harness-preview-required"
	// A harness the choice names that is not installed.
	CodeHarnessNotInstalled = "harness-not-installed"
	// No harness the pack is compatible with is selected.
	CodeHarnessNoneSelected = "harness-none-selected"
)

// HarnessState is a harness dotai knows, as a selection sees it (decision
// 0047): whether the pack is compatible with it and requires it, its
// status and why it is a preview, its detection, and whether the install
// manages it.
type HarnessState struct {
	ID           string `json:"id"`
	Compatible   bool   `json:"compatible"`
	Required     bool   `json:"required"`
	Status       string `json:"status"`
	StatusReason string `json:"statusReason"`
	Managed      bool   `json:"managed"`
	Detection
}

// IsPreview reports whether h is a preview harness: any status but
// supported counts as one, as the selection counts it (decision 0047).
func (h HarnessState) IsPreview() bool { return h.Status != string(harness.Supported) }

// HarnessRefusal is why a selection of harnesses is refused: its code, the
// harness, "" for none, and the message, text for people.
type HarnessRefusal struct {
	Code    string `json:"code"`
	Harness string `json:"harness"`
	Message string `json:"message"`
}

// HarnessRefusedError is a harness selection that an install or an apply
// refuses before it stages anything (decision 0047), with each refusal and
// the state of every harness, so that a caller can show the choice again.
type HarnessRefusedError struct {
	Refusals  []HarnessRefusal
	Harnesses []HarnessState
}

func (e *HarnessRefusedError) Error() string {
	msgs := make([]string, 0, len(e.Refusals))
	for _, r := range e.Refusals {
		msgs = append(msgs, r.Message)
	}
	return strings.Join(msgs, "; ")
}

// harnessSelection is what selectHarnesses chooses: Selected, the
// harnesses an install manages, in the manifest's order; Notices, why a
// compatible harness is not managed, text for people; Choice, the
// --harness choice to save in the install state, nil for none; Warnings,
// a harness-preview warning per preview harness it manages; and States,
// every harness dotai knows, the compatible ones in the manifest's order,
// then the others in dotai's.
type harnessSelection struct {
	Selected, Notices, Choice []string
	Warnings                  []plan.Warning
	States                    []HarnessState
}

// chooser is whose selection selectHarnesses makes, which decides how its
// texts tell the user to choose: an install takes --harness, and an apply
// follows the install's choice, so it points at dotai install --harness.
type chooser bool

const (
	byInstall chooser = false
	byApply   chooser = true
)

// optIn tells the user how to choose the preview harness id.
func (c chooser) optIn(id string) string {
	if c == byApply {
		return "choose it with dotai install --harness, then apply again"
	}
	return fmt.Sprintf("pass --harness %s to manage it", id)
}

// change tells the user how to change the choice.
func (c chooser) change() string {
	if c == byApply {
		return "choose again with dotai install --harness to change it"
	}
	return "pass --harness to change it"
}

// selectHarnesses chooses the harnesses an install, or with c byApply an
// apply, manages among those m is compatible with (decisions 0038 and
// 0047). With asked, the --harness list, not nil, it manages those and
// saves them as the choice. Otherwise, when the previous install prev was
// of the same pack and saved a choice, it reuses that choice, with every
// required harness, and saves it again as it was; otherwise it manages the
// compatible harnesses that det detects and that are supported, and saves
// no choice. So a harness skipped as undetected never changes the saved
// choice, and a harness that is not supported, a preview one, is managed
// only when a choice names it, or when, without a choice, the previous
// install of the same pack managed it, as one from before decision 0047
// did. A selection that cannot stand is a *HarnessRefusedError, with every
// refusal: a name asked that dotai does not know or the pack is not
// compatible with, a required harness asked leaves out, one that is not
// installed or is a preview no choice names, a harness asked that is not
// installed, and a selection with no harness at all.
func selectHarnesses(m pack.Manifest, asked []string, prev *state.State, det map[string]Detection, c chooser) (harnessSelection, error) {
	var sel harnessSelection
	var refusals []HarnessRefusal
	refuse := func(code, id, format string, args ...any) {
		refusals = append(refusals, HarnessRefusal{Code: code, Harness: id, Message: fmt.Sprintf(format, args...)})
	}
	for _, id := range asked {
		switch _, err := harness.Lookup([]string{id}); {
		case err != nil:
			refuse(CodeHarnessUnknown, id, "--harness names %q, which dotai does not know: it knows %s", id, strings.Join(pack.Harnesses, ", "))
		case !slices.Contains(m.Harnesses, id):
			refuse(CodeHarnessNotCompatible, id, "--harness names %q, which the pack is not compatible with: its harnesses are %s", id, strings.Join(m.Harnesses, ", "))
		}
	}
	adapters, err := harness.Lookup(m.Harnesses)
	if err != nil {
		return harnessSelection{}, err
	}
	samePack := prev != nil && prev.Pack.Name == m.Name
	var saved []string
	switch {
	case asked != nil:
		for _, id := range m.Harnesses {
			if slices.Contains(asked, id) {
				sel.Choice = append(sel.Choice, id)
			}
		}
	case samePack && prev.Selection != nil:
		saved, sel.Choice = prev.Selection, slices.Clone(prev.Selection)
	}
	// kept is a preview harness that the previous install of the pack
	// managed without a choice: it stays opted in.
	kept := func(id string) bool {
		return asked == nil && saved == nil && samePack && slices.Contains(prev.Harnesses, id)
	}
	var why []string // why each compatible harness is not selected, for a selection of none
	for _, a := range adapters {
		required, preview := slices.Contains(m.Requires, a.ID), a.Status != harness.Supported
		chosen := slices.Contains(sel.Choice, a.ID) || kept(a.ID)
		d := det[a.ID]
		switch {
		case asked != nil && !slices.Contains(asked, a.ID) && required:
			refuse(CodeHarnessRequired, a.ID, "the pack requires %s, which --harness leaves out: add it to --harness", a.ID)
		case asked != nil && !slices.Contains(asked, a.ID):
			sel.Notices = append(sel.Notices, fmt.Sprintf("the harness %s is not managed: --harness leaves it out", a.ID))
			why = append(why, fmt.Sprintf("%s is left out of --harness", a.ID))
		case saved != nil && !required && !slices.Contains(saved, a.ID):
			sel.Notices = append(sel.Notices, fmt.Sprintf("the harness %s is not managed: the --harness choice an earlier install saved leaves it out; %s", a.ID, c.change()))
			why = append(why, fmt.Sprintf("%s is left out of the --harness choice an earlier install saved", a.ID))
		case preview && required && !chosen:
			refuse(CodeHarnessPreviewRequired, a.ID, "the pack requires %s, a preview harness that is managed only when you choose it: %s; %s", a.ID, a.StatusReason, c.optIn(a.ID))
			if !d.Detected {
				refuse(CodeHarnessMissing, a.ID, "the pack requires %s, which is not installed (%s)", a.ID, d.Why(a))
			}
		case d.Detected && preview && !chosen:
			sel.Notices = append(sel.Notices, fmt.Sprintf("the preview harness %s is not managed unless you choose it: %s; %s", a.ID, a.StatusReason, c.optIn(a.ID)))
			why = append(why, fmt.Sprintf("%s is a preview harness: %s", a.ID, c.optIn(a.ID)))
		case d.Detected:
			sel.Selected = append(sel.Selected, a.ID)
			if preview {
				sel.Warnings = append(sel.Warnings, plan.Warning{Code: CodeHarnessPreview, Harness: a.ID, Message: fmt.Sprintf("%s is a preview harness: %s", a.ID, a.StatusReason)})
			}
			if preview && kept(a.ID) {
				sel.Notices = append(sel.Notices, fmt.Sprintf("the preview harness %s stays managed: the earlier install of the pack managed it; %s", a.ID, c.change()))
			}
		case asked != nil:
			refuse(CodeHarnessNotInstalled, a.ID, "--harness asks for %s, which is not installed (%s)", a.ID, d.Why(a))
		case required:
			refuse(CodeHarnessMissing, a.ID, "the pack requires %s, which is not installed (%s)", a.ID, d.Why(a))
		default:
			sel.Notices = append(sel.Notices, fmt.Sprintf("the harness %s is skipped: it is not installed (%s)", a.ID, d.Why(a)))
			why = append(why, fmt.Sprintf("%s is not installed (%s)", a.ID, d.Why(a)))
		}
	}
	if len(refusals) == 0 && len(sel.Selected) == 0 {
		if asked != nil && len(asked) == 0 {
			why = []string{"--harness names none"}
		}
		refuse(CodeHarnessNoneSelected, "", "no harness the pack is compatible with is selected: %s", strings.Join(why, "; "))
	}
	sel.States = harnessStates(m, sel.Selected, det)
	if len(refusals) > 0 {
		return harnessSelection{}, &HarnessRefusedError{Refusals: refusals, Harnesses: sel.States}
	}
	return sel, nil
}

// harnessStates is the state of every harness dotai knows for m, with
// selected managed: the compatible ones in the manifest's order, then the
// others in dotai's.
func harnessStates(m pack.Manifest, selected []string, det map[string]Detection) []HarnessState {
	order := slices.Clone(m.Harnesses)
	for _, id := range pack.Harnesses {
		if !slices.Contains(order, id) {
			order = append(order, id)
		}
	}
	out := []HarnessState{}
	for _, id := range order {
		found, err := harness.Lookup([]string{id})
		if err != nil {
			continue
		}
		a := found[0]
		out = append(out, HarnessState{
			ID: id, Compatible: slices.Contains(m.Harnesses, id), Required: slices.Contains(m.Requires, id),
			Status: string(a.Status), StatusReason: a.StatusReason, Managed: slices.Contains(selected, id), Detection: det[id],
		})
	}
	return out
}
