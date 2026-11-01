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
	"github.com/lartweib/dotai/internal/state"
)

// host is what an install reads of the machine besides the disk: how a
// command is found on PATH, the environment and the OS.
type host struct {
	lookPath func(string) (string, error)
	environ  []string
	goos     string
}

// osHost is the host dotai runs on.
func osHost() host { return host{lookPath: exec.LookPath, environ: os.Environ(), goos: runtime.GOOS} }

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

// detectAll detects every harness of m in home.
func detectAll(d installFS, h host, home string, m pack.Manifest) (map[string]Detection, error) {
	adapters, err := harness.Lookup(m.AllHarnesses())
	if err != nil {
		return nil, err
	}
	out := map[string]Detection{}
	for _, a := range adapters {
		if out[a.ID], err = detect(d, h.lookPath, home, a); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// selectHarnesses chooses the harnesses an install manages, in the order
// of m's harnesses, required then optional (decision 0038), and the
// --harness choice to save in the install state, nil for none. With asked,
// the --harness list, not nil, it manages those and saves them. Otherwise,
// when the previous install prev was of the same pack and saved a choice,
// it reuses that choice, with every required harness, and saves it again
// as it was; otherwise it manages every harness of m that det detects and
// saves no choice. So a harness skipped as undetected never changes the
// saved choice. A required harness that is not detected stops the install,
// and so does one asked for that is not; a required harness asked leaves
// out, and a name asked that the pack does not support, are a *UsageError.
// An optional harness that is not managed gets a notice, text for people.
func selectHarnesses(m pack.Manifest, asked []string, prev *state.State, det map[string]Detection) (selected, notices, choice []string, err error) {
	all := m.AllHarnesses()
	adapters, err := harness.Lookup(all)
	if err != nil {
		return nil, nil, nil, err
	}
	for _, id := range asked {
		if !slices.Contains(all, id) {
			return nil, nil, nil, &UsageError{fmt.Sprintf("--harness names %q, which the pack does not support: its harnesses are %s", id, strings.Join(all, ", "))}
		}
	}
	var saved []string
	switch {
	case asked != nil:
		for _, id := range all {
			if slices.Contains(asked, id) {
				choice = append(choice, id)
			}
		}
	case prev != nil && prev.Pack.Name == m.Name && prev.Selection != nil:
		saved, choice = prev.Selection, slices.Clone(prev.Selection)
	}
	var missing []string
	for _, a := range adapters {
		required := slices.Contains(m.Harnesses, a.ID)
		d := det[a.ID]
		switch {
		case asked != nil && !slices.Contains(asked, a.ID) && required:
			return nil, nil, nil, &UsageError{fmt.Sprintf("the pack requires %s, which --harness leaves out: add it to --harness", a.ID)}
		case asked != nil && !slices.Contains(asked, a.ID):
			notices = append(notices, fmt.Sprintf("the optional harness %s is not managed: --harness leaves it out", a.ID))
		case saved != nil && !required && !slices.Contains(saved, a.ID):
			notices = append(notices, fmt.Sprintf("the optional harness %s is not managed: the --harness choice an earlier install saved leaves it out; pass --harness to change it", a.ID))
		case d.Detected:
			selected = append(selected, a.ID)
		case asked != nil:
			missing = append(missing, fmt.Sprintf("--harness asks for %s, which is not installed (%s)", a.ID, d.Why(a)))
		case required:
			missing = append(missing, fmt.Sprintf("the pack requires %s, which is not installed (%s)", a.ID, d.Why(a)))
		default:
			notices = append(notices, fmt.Sprintf("the optional harness %s is skipped: it is not installed (%s)", a.ID, d.Why(a)))
		}
	}
	if len(missing) > 0 {
		return nil, nil, nil, errors.New(strings.Join(missing, "; "))
	}
	return selected, notices, choice, nil
}
