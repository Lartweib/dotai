package app

import (
	"context"
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
// show first, the executable found on PATH for Command, the arguments and
// environment it is given, and notes about how it is launched.
type RunPlan struct {
	Harness HarnessPlan
	Command string   // the name looked up on PATH, such as claude
	Program string   // the path it was found at
	Args    []string // the arguments after the program name
	Env     []string // the whole environment of the harness, as KEY=value
	Vars    []string // the variables set to a fixed value, as KEY=value, which Env ends with
	Notes   []string // what the user should know about the launch, text for people
	// Warnings are what may go wrong in the launch, text for people.
	Warnings []string
}

// batchUnsafe are the characters cmd.exe reads in a batch file's command
// line even inside the quotes the arguments get, so that they cannot be
// passed to a batch file safely.
const batchUnsafe = "&|<>^%\"!\r\n"

// PlanRun plans launching the harness id isolated, with args passed
// through after the flags its pointers need, from the install state in the
// absolute home directory home. An argument that sets one of those flags
// is a *UsageError, and each flag the harness adds goes into args where
// the command they run takes it. The install state must record an isolated
// install that includes id and no journal, and the harness's isolated
// directory must be a real directory. While it checks them, it holds
// ~/.dotai/lock, which it takes without waiting, so that it refuses to run
// while an install or a restore writes, and releases it before returning:
// the harness runs without it. The harness's environment is the current one
// without the variables its pointers and its fixed variables set, which
// are then set to its isolated directories and their values. On Windows, a
// batch file (.cmd or .bat) found first on PATH gives way to the
// executable it runs, when the harness names it, or to the harness's .exe
// further on PATH; launched on its own, it gets a note, and an argument
// that cmd.exe would read is refused. Apart from the lock and the empty
// directory of a pointer that must exist, it writes nothing.
func PlanRun(home, id string, args []string) (RunPlan, error) {
	return planRun(osInstallFS{}, exec.LookPath, runtime.GOOS, home, id, args, os.Environ())
}

func planRun(d executeFS, lookPath func(string) (string, error), goos, home, id string, args, environ []string) (p RunPlan, err error) {
	a, err := launchable(id)
	if err != nil {
		return RunPlan{}, err
	}
	if err := checkFlags(a, args); err != nil {
		return RunPlan{}, err
	}
	if !filepath.IsAbs(home) {
		return RunPlan{}, fmt.Errorf("home directory %q is not absolute", home)
	}
	home = filepath.Clean(home)
	statePath := filepath.Join(home, dotaiDir, stateFile)
	// Read once without the lock, so that a home without an install gets
	// no lock either.
	if s, err := readState(d, home); err != nil {
		return RunPlan{}, err
	} else if s == nil {
		return RunPlan{}, fmt.Errorf("there is no install state %s: run dotai install with a pack that has %s first", statePath, id)
	}
	unlock, err := runner{ctx: context.Background(), d: d, home: home}.lock()
	if locked, ok := errors.AsType[*LockedError](err); ok {
		return RunPlan{}, fmt.Errorf("an install or restore is running, run again when it is done: %w", locked)
	} else if err != nil {
		return RunPlan{}, err
	}
	defer func() {
		if uErr := unlock(); uErr != nil {
			p, err = RunPlan{}, errors.Join(err, fmt.Errorf("the lock %s could not be removed: %w; remove it before dotai runs again", filepath.Join(home, dotaiDir, lockFile), uErr))
		}
	}()
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
	h := harnessPlan(root, a, goos)
	dirs := []string{root, h.Dir}
	if a.Install != "" {
		dir := h.Dir
		for seg := range strings.SplitSeq(a.Install, "/") {
			dir = filepath.Join(dir, seg)
			dirs = append(dirs, dir)
		}
	}
	for _, dir := range dirs {
		if err := checkDir(d, dir); errors.Is(err, fs.ErrNotExist) {
			return RunPlan{}, fmt.Errorf("the isolated directory %s is missing: run dotai install", dir)
		} else if err != nil {
			return RunPlan{}, err
		}
	}
	program, err := findProgram(d, lookPath, goos, a)
	if err != nil {
		return RunPlan{}, err
	}
	p = RunPlan{Harness: h, Command: a.Command, Program: program, Args: []string{}, Env: []string{}, Vars: []string{}, Notes: []string{}, Warnings: []string{}}
	set, drop := map[string]bool{}, map[string]bool{}
	for _, name := range a.Isolation.Drop {
		drop[envKey(goos, name)] = true
	}
	for _, ptr := range h.Pointers {
		if ptr.Mechanism == string(harness.Flag) {
			p.Args = append(p.Args, ptr.Name+"="+ptr.Dir)
		} else {
			set[envKey(goos, ptr.Name)] = true
		}
	}
	for _, v := range a.Isolation.Vars {
		set[envKey(goos, v.Name)] = true
		p.Vars = append(p.Vars, v.Name+"="+v.Value)
	}
	isDir := func(name string) bool {
		mode, err := d.Lstat(name)
		return err == nil && mode.IsDir()
	}
	args, p.Notes, p.Warnings = addFlags(a.Isolation.Flags, args, isDir)
	p.Args = append(p.Args, args...)
	var dropped []string
	for _, kv := range environ {
		name, _, _ := strings.Cut(kv, "=")
		switch key := envKey(goos, name); {
		case drop[key]:
			dropped = append(dropped, name)
		case !set[key]:
			p.Env = append(p.Env, kv)
		}
	}
	if len(dropped) > 0 {
		p.Notes = append(p.Notes, fmt.Sprintf("removed %s from the environment, since each points %s at another configuration", strings.Join(dropped, ", "), a.ID))
	}
	for _, ptr := range h.Pointers {
		if ptr.Mechanism == string(harness.Env) {
			p.Env = append(p.Env, ptr.Name+"="+ptr.Dir)
		}
	}
	p.Env = append(p.Env, p.Vars...)
	if isBatch(goos, program) {
		for _, arg := range p.Args {
			if strings.ContainsAny(arg, batchUnsafe) {
				return RunPlan{}, fmt.Errorf("%s resolves to the batch file %s, which Windows runs through cmd.exe, and the argument %q cannot be passed to it safely: cmd.exe reads & | < > ^ %% \" ! and line breaks even in quotes. Install the native %s.exe, or leave those characters out", a.Command, program, arg, a.Command)
			}
		}
		p.Notes = append(p.Notes, fmt.Sprintf("%s resolves to the batch file %s: Ctrl-C may show cmd's \"Terminate batch job (Y/N)?\" prompt after the harness exits; the native %s.exe avoids it", a.Command, program, a.Command))
	}
	for _, ptr := range a.Isolation.Pointers {
		if ptr.Make {
			if err := makeDir(d, h.Dir, ptr.Path); err != nil {
				return RunPlan{}, err
			}
		}
	}
	return p, nil
}

// checkFlags refuses an argument that sets a flag pointer of a, which
// dotai run sets itself: the flag with one or two dashes, alone or with
// =value, wherever it is, since a harness may read it past a --.
func checkFlags(a harness.Adapter, args []string) error {
	for _, ptr := range a.Isolation.Pointers {
		if ptr.Mechanism != harness.Flag {
			continue
		}
		bare := strings.TrimLeft(ptr.Name, "-")
		for _, arg := range args {
			name, _, _ := strings.Cut(arg, "=")
			if strings.HasPrefix(name, "-") && strings.TrimPrefix(strings.TrimPrefix(name, "-"), "-") == bare {
				return &UsageError{fmt.Sprintf("dotai run %s sets %s itself, to the isolated install: leave out %q", a.ID, ptr.Name, arg)}
			}
		}
	}
	return nil
}

// makeDir creates the directory rel, a slash path, in dir when it is
// missing, and refuses it when it is not a real directory.
func makeDir(d executeFS, dir, rel string) error {
	name := filepath.Join(dir, filepath.FromSlash(rel))
	err := checkDir(d, name)
	if !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	if err := d.MkdirIn(dir, filepath.FromSlash(rel)); err != nil {
		return err
	}
	return checkDir(d, name)
}

// addFlags adds each flag to args right after the global flags and the
// subcommands they start with, when the command they run takes it and
// args do not have it or a flag that takes its place before a --; a note
// says why for each flag it adds. A word that is neither a subcommand nor,
// first, a directory for the default command, makes the command unknown:
// the flag is not added and a warning says what may happen. isDir reports
// a directory. Notes and warnings are never nil.
func addFlags(flags []harness.AddedFlag, args []string, isDir func(string) bool) ([]string, []string, []string) {
	notes, warnings := []string{}, []string{}
	for _, f := range flags {
		end := slices.Index(args, "--")
		if end < 0 {
			end = len(args)
		}
		if slices.ContainsFunc(args[:end], func(arg string) bool {
			name, _, _ := strings.Cut(arg, "=")
			return name == f.Name || slices.Contains(f.Unless, name)
		}) {
			continue
		}
		command, i, unknown := "", 0, ""
		for i < end {
			arg := args[i]
			if name, _, hasValue := strings.Cut(arg, "="); strings.HasPrefix(arg, "-") {
				value, global := f.Globals[name]
				if !global {
					break
				}
				i++
				if value && !hasValue {
					i++
				}
				continue
			}
			next := strings.TrimSpace(command + " " + arg)
			if slices.Contains(f.Subcommands, next) {
				command, i = next, i+1
				continue
			}
			parent := command != "" && slices.ContainsFunc(f.Subcommands, func(c string) bool { return strings.HasPrefix(c, command+" ") })
			if command == "" && !isDir(arg) || parent {
				unknown = next
			}
			break
		}
		i = min(i, end)
		switch {
		case unknown != "":
			warnings = append(warnings, fmt.Sprintf("%q is not a command known to take %s, so it is not added: %s", unknown, f.Name, f.Warning))
		case slices.Contains(f.Commands, command):
			args = slices.Insert(slices.Clone(args), i, f.Name)
			notes = append(notes, fmt.Sprintf("%s added: %s", f.Name, f.Note))
		}
	}
	return args, notes, warnings
}

// findProgram looks a's command up on PATH. A command found only in the
// current directory or through a relative PATH entry is refused, as
// exec.LookPath reports it. On Windows, a batch file gives way to the
// executable it runs, a's BatchTarget next to it, when it is there, or
// else to command.exe when PATH has one further on.
func findProgram(d installFS, lookPath func(string) (string, error), goos string, a harness.Adapter) (string, error) {
	command := a.Command
	program, err := lookPath(command)
	switch {
	case errors.Is(err, exec.ErrDot):
		return "", fmt.Errorf("%s was found only in the current directory or through a relative PATH entry (%s), which dotai does not run: remove it from PATH, or make that entry absolute", command, program)
	case err != nil:
		return "", fmt.Errorf("%s was not found on PATH, install it or add its directory to PATH: %w", command, err)
	}
	if isBatch(goos, program) {
		if a.BatchTarget != "" {
			exe := filepath.Join(filepath.Dir(program), filepath.FromSlash(a.BatchTarget))
			if mode, err := d.Lstat(exe); err == nil && mode.IsRegular() {
				return exe, nil
			}
		}
		if exe, err := lookPath(command + ".exe"); err == nil {
			return exe, nil
		}
	}
	return program, nil
}

// isBatch reports a Windows batch file, which Windows runs through cmd.exe.
func isBatch(goos, program string) bool {
	if goos != "windows" {
		return false
	}
	// Windows paths whatever the OS that runs the tests.
	ext := strings.ToLower(program[strings.LastIndexAny(program, `.\/`)+1:])
	return ext == "cmd" || ext == "bat"
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

// envKey is the name of an environment variable as goos compares it:
// without case on Windows.
func envKey(goos, name string) string {
	if goos == "windows" {
		return strings.ToUpper(name)
	}
	return name
}
