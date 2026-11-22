package app_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/lartweib/dotai/internal/app"
	"github.com/lartweib/dotai/internal/proc"
	"github.com/lartweib/dotai/internal/rulesync"
)

// liveCase is a file of one harness that a later update rewrites, and
// whether the harness itself rewrites it while it runs (decision 0053).
type liveCase struct {
	name    string
	harness string
	process string
	rel     string // the file the update changes, relative to the home directory
	staged  string // the same file in the staging tree, "" when it is rel
	live    bool
}

var liveCases = []liveCase{
	{"claude agent", "claudecode", "claude", ".claude/agents/worker.md", "", false},
	{"claude skill", "claudecode", "claude", ".claude/skills/delegate/SKILL.md", "", false},
	{"claude settings", "claudecode", "claude", ".claude/settings.json", "", true},
	{"claude home file", "claudecode", "claude", ".claude.json", "", true},
	{"agy skill", "antigravity-cli", "agy", ".gemini/config/skills/delegate/SKILL.md", "", false},
	{"agy settings", "antigravity-cli", "agy", ".gemini/antigravity-cli/settings.json", "", true},
	{"agy hooks", "antigravity-cli", "agy", ".gemini/config/hooks.json", "", true},
	{"opencode agent", "opencode", "opencode", ".config/opencode/agents/worker.md", "", false},
	{"opencode config", "opencode", "opencode", ".config/opencode/opencode.jsonc", "", true},
}

// stagedAs is the content of the file of c in version v.
func (c liveCase) stagedAs(v string) string {
	if filepath.Ext(c.rel) == ".md" {
		return "# " + v + "\n"
	}
	return `{"version":"` + v + `"}`
}

// versioned is rulesync writing each harness's rules file, as replaceRun
// does, and the file of c as the version *v holds.
func versioned(t *testing.T, c liveCase, v *string) func(context.Context, rulesync.Result, []string, ...rulesync.Job) error {
	rules := replaceRun(t)
	return func(ctx context.Context, bin rulesync.Result, targets []string, jobs ...rulesync.Job) error {
		if err := rules(ctx, bin, targets, jobs...); err != nil {
			return err
		}
		for _, j := range jobs {
			if j.Global {
				writeFile(t, filepath.Join(j.Dir, filepath.FromSlash(cmpStaged(c))), c.stagedAs(*v))
			}
		}
		return nil
	}
}

func cmpStaged(c liveCase) string {
	if c.staged != "" {
		return c.staged
	}
	return c.rel
}

// hostListing is a host that lists procs, dotai's own process being 99.
func hostListing(procs []proc.Process, err error) app.Host {
	return app.NewHostListing(found("bin", "claude", "agy", "opencode"), nil, func() ([]proc.Process, error) { return procs, err }, 99)
}

// settledFor is a home with a first replace install of c's harness, whose
// file is as version v1 holds it, and a function that installs version v2
// over it as a later install on the host h.
func settledFor(t *testing.T, c liveCase) (home string, later func(h app.Host, opts app.InstallOptions) (app.InstallResult, error)) {
	t.Helper()
	home, _ = installHome(t)
	version := "v1"
	run := versioned(t, c, &version)
	fetch := quiet(t).fetch
	opts := func(o app.InstallOptions) app.InstallOptions {
		o.Harnesses = []string{c.harness}
		return o
	}
	if _, err := app.InstallReplace(context.Background(), newClockFS(t), hostListing(nil, nil), fetch, run, examplePack, home, opts(app.InstallOptions{})); err != nil {
		t.Fatalf("first replace install: %v", err)
	}
	version = "v2"
	return home, func(h app.Host, o app.InstallOptions) (app.InstallResult, error) {
		return app.InstallAgainReplace(context.Background(), newClockFS(t), h, fetch, run, examplePack, home, opts(o))
	}
}

// TestALaterUpdateRefusesOnlyForTheFilesARunningHarnessRewrites checks
// decision 0053: a later replace install that does not reset goes on while
// a harness runs and warns that it picks the update up in new sessions,
// unless it writes a file that harness rewrites while it runs, which it
// refuses naming the harness and the file, writing nothing. --ignore-running
// overrides both.
func TestALaterUpdateRefusesOnlyForTheFilesARunningHarnessRewrites(t *testing.T) {
	for _, c := range liveCases {
		t.Run(c.name, func(t *testing.T) {
			home, later := settledFor(t, c)
			before := homeEntries(t, home)
			running := hostListing([]proc.Process{{PID: 10, Name: c.process}, {PID: 99, Name: "claude"}}, nil)

			res, err := later(running, app.InstallOptions{})
			var re *app.RunningError
			if c.live {
				if !errors.As(err, &re) || len(re.Files) != 1 || re.Files[0].Harness != c.harness || re.Files[0].Path != at(home, c.rel) ||
					len(re.Running) != 1 || re.Running[0].Harness != c.harness {
					t.Fatalf("update with %s running: error = %v, want a *RunningError naming %s and %s", c.harness, err, c.harness, c.rel)
				}
				diff(t, "home after the refusal", homeEntries(t, home), before)
				if res, err = later(running, app.InstallOptions{IgnoreRunning: true}); err != nil {
					t.Fatalf("update with --ignore-running: %v", err)
				}
				for _, w := range res.InstallPlan.Warnings {
					if w.Code == app.CodeHarnessRunningUpdate {
						t.Errorf("--ignore-running warned %+v, want it to skip the check", w)
					}
				}
			} else {
				if err != nil {
					t.Fatalf("update with %s running: %v, want it to go on", c.harness, err)
				}
				var warned []string
				for _, w := range res.InstallPlan.Warnings {
					if w.Code == app.CodeHarnessRunningUpdate {
						warned = append(warned, w.Harness)
					}
				}
				if !slices.Equal(warned, []string{c.harness}) {
					t.Errorf("%s warnings = %v, want one for %s", app.CodeHarnessRunningUpdate, warned, c.harness)
				}
			}
			got, err := os.ReadFile(at(home, c.rel))
			if err != nil || string(got) == c.stagedAs("v1") {
				t.Errorf("%s = %q, %v, want the update written", c.rel, got, err)
			}
		})
	}
}

// TestAResetStillRefusesWhileTheHarnessRuns checks that wiping a harness,
// as --reset does for a later install, is refused for the whole harness
// even when it writes no file the harness rewrites.
func TestAResetStillRefusesWhileTheHarnessRuns(t *testing.T) {
	c := liveCases[0]
	home, later := settledFor(t, c)
	before := homeEntries(t, home)
	_, err := later(hostListing([]proc.Process{{PID: 10, Name: "claude"}}, nil), app.InstallOptions{Reset: true})
	var re *app.RunningError
	if !errors.As(err, &re) || len(re.Running) != 1 || len(re.Files) != 0 {
		t.Fatalf("reset with claude running: error = %v, want a *RunningError for the wipe", err)
	}
	diff(t, "home after the refusal", homeEntries(t, home), before)
	if _, err := later(hostListing([]proc.Process{{PID: 10, Name: "claude"}}, nil), app.InstallOptions{Reset: true, IgnoreRunning: true}); err != nil {
		t.Errorf("reset with --ignore-running: %v, want it to go on", err)
	}
}

// TestAnUpdateThatCannotListProcessesGoesOnUnlessItWritesALiveFile checks
// that when the process list fails, an update that touches no file the
// harness rewrites goes on, and one that does is refused as before,
// naming why.
func TestAnUpdateThatCannotListProcessesGoesOnUnlessItWritesALiveFile(t *testing.T) {
	failing := hostListing(nil, errors.New("no access"))
	home, later := settledFor(t, liveCases[0])
	if _, err := later(failing, app.InstallOptions{}); err != nil {
		t.Errorf("update of an agent without a process list: %v, want it to go on", err)
	}
	if !exists(at(home, ".claude/agents/worker.md")) {
		t.Error("the agent is not there")
	}
	_, later = settledFor(t, liveCases[2])
	var re *app.RunningError
	if _, err := later(failing, app.InstallOptions{}); !errors.As(err, &re) || re.Err == nil {
		t.Errorf("update of settings.json without a process list: %v, want a *RunningError with the listing's failure", err)
	}
}

func exists(name string) bool { _, err := os.Stat(name); return err == nil }

// TestARestoreRefusesOnlyForTheFilesARunningHarnessRewrites checks that the
// restore of a later update follows the rule of the install: it goes on
// while the harness runs when it puts back no file the harness rewrites, is
// refused naming the file when it does, and --ignore-running overrides it.
func TestARestoreRefusesOnlyForTheFilesARunningHarnessRewrites(t *testing.T) {
	for _, c := range []liveCase{liveCases[0], liveCases[2], liveCases[8]} {
		t.Run(c.name, func(t *testing.T) {
			home, later := settledFor(t, c)
			if _, err := later(hostListing(nil, nil), app.InstallOptions{}); err != nil {
				t.Fatalf("update: %v", err)
			}
			running := hostListing([]proc.Process{{PID: 10, Name: c.process}}, nil)
			restore := func(opts app.RestoreOptions) (app.RestoreResult, error) {
				return app.RestoreHost(context.Background(), newClockFS(t), running, home, "", opts, func(app.RestoreResult) (bool, error) { return true, nil })
			}
			before := homeEntries(t, home)
			_, err := restore(app.RestoreOptions{})
			var re *app.RunningError
			if c.live {
				if !errors.As(err, &re) || len(re.Files) != 1 || re.Files[0].Path != at(home, c.rel) || re.Operation != "restore" {
					t.Fatalf("restore with %s running: error = %v, want a *RunningError naming %s", c.harness, err, c.rel)
				}
				diff(t, "home after the refusal", homeEntries(t, home), before)
				if _, err := restore(app.RestoreOptions{IgnoreRunning: true}); err != nil {
					t.Errorf("restore with --ignore-running: %v", err)
				}
				return
			}
			if err != nil {
				t.Errorf("restore with %s running: %v, want it to go on", c.harness, err)
			}
		})
	}
}
