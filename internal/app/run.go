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
)

// RunPlan is how dotai run launches a harness against its isolated install
// (decision 0032): the harness's isolation, which decision 0018 requires to
// show first, the executable found on PATH for Command, and the arguments
// and environment it is given.
type RunPlan struct {
	Harness HarnessPlan
	Command string   // the name looked up on PATH, such as claude
	Program string   // the path it was found at
	Args    []string // the arguments after the program name
	Env     []string // the whole environment of the harness, as KEY=value
}

// PlanRun plans launching the harness id isolated, with args passed
// through after the flags its pointers need, from the install state in the
// absolute home directory home. The install state must record an isolated
// install that includes id and no journal, and the harness's isolated
// directory must be a real directory. The harness's environment is the
// current one without the variables its pointers set, which are then set
// to its isolated directories. It reads the state and takes no lock, and
// writes nothing.
func PlanRun(home, id string, args []string) (RunPlan, error) {
	return planRun(osInstallFS{}, exec.LookPath, home, id, args, os.Environ())
}

func planRun(d installFS, lookPath func(string) (string, error), home, id string, args, environ []string) (RunPlan, error) {
	a, err := launchable(id)
	if err != nil {
		return RunPlan{}, err
	}
	if !filepath.IsAbs(home) {
		return RunPlan{}, fmt.Errorf("home directory %q is not absolute", home)
	}
	home = filepath.Clean(home)
	statePath := filepath.Join(home, dotaiDir, stateFile)
	s, err := readState(d, home)
	switch {
	case err != nil:
		return RunPlan{}, err
	case s == nil:
		return RunPlan{}, fmt.Errorf("there is no install state %s: run dotai install with a pack that has %s first", statePath, id)
	case s.Pending != nil:
		return RunPlan{}, fmt.Errorf("the last install or restore was interrupted, the install state %s still holds its journal: run dotai install to finish it or dotai restore to undo it, then run again", statePath)
	case s.Mode != ModeIsolated:
		return RunPlan{}, fmt.Errorf("the install state %s has mode %q, and dotai run launches an %s install: run dotai install", statePath, s.Mode, ModeIsolated)
	case !slices.Contains(s.Harnesses, id):
		installed := strings.Join(s.Harnesses, ", ")
		if installed == "" {
			installed = "none"
		}
		return RunPlan{}, fmt.Errorf("%s is not installed (installed: %s): add it to the pack's harnesses and run dotai install", id, installed)
	}
	root := filepath.Join(home, filepath.FromSlash(harness.IsolatedDir))
	h := harnessPlan(root, a)
	for _, dir := range []string{root, h.Dir} {
		if err := checkDir(d, dir); errors.Is(err, fs.ErrNotExist) {
			return RunPlan{}, fmt.Errorf("the isolated directory %s is missing: run dotai install", dir)
		} else if err != nil {
			return RunPlan{}, err
		}
	}
	program, err := lookPath(a.Command)
	if err != nil {
		return RunPlan{}, fmt.Errorf("%s was not found on PATH, install it or add its directory to PATH: %w", a.Command, err)
	}
	p := RunPlan{Harness: h, Command: a.Command, Program: program, Args: []string{}, Env: []string{}}
	set := map[string]bool{}
	for _, ptr := range h.Pointers {
		if ptr.Mechanism == string(harness.Flag) {
			p.Args = append(p.Args, ptr.Name, ptr.Dir)
		} else {
			set[envKey(ptr.Name)] = true
		}
	}
	p.Args = append(p.Args, args...)
	for _, kv := range environ {
		name, _, _ := strings.Cut(kv, "=")
		if !set[envKey(name)] {
			p.Env = append(p.Env, kv)
		}
	}
	for _, ptr := range h.Pointers {
		if ptr.Mechanism == string(harness.Env) {
			p.Env = append(p.Env, ptr.Name+"="+ptr.Dir)
		}
	}
	return p, nil
}

// launchable is the adapter of id when dotai run launches it; an unknown
// harness, or one it does not launch yet, is a *UsageError.
func launchable(id string) (harness.Adapter, error) {
	all := harness.All()
	i := slices.IndexFunc(all, func(a harness.Adapter) bool { return a.ID == id })
	var supported, ids []string
	for _, a := range all {
		ids = append(ids, a.ID)
		if a.Command != "" {
			supported = append(supported, a.ID)
		}
	}
	switch {
	case i < 0:
		return harness.Adapter{}, &UsageError{fmt.Sprintf("unknown harness %q: the harnesses are %s", id, strings.Join(ids, ", "))}
	case all[i].Command == "":
		return harness.Adapter{}, &UsageError{fmt.Sprintf("dotai run %s is not supported yet: only %s can be launched isolated for now", id, strings.Join(supported, ", "))}
	}
	return all[i], nil
}

// envKey is the name of an environment variable as the OS compares it:
// without case on Windows.
func envKey(name string) string {
	if runtime.GOOS == "windows" {
		return strings.ToUpper(name)
	}
	return name
}
