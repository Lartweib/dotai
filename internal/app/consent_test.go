package app_test

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/lartweib/dotai/internal/app"
	"github.com/lartweib/dotai/internal/pack"
	"github.com/lartweib/dotai/internal/proc"
)

// installConsent is a replace install on the host h that asks consent.
func installConsent(t *testing.T, h app.Host, home string, opts app.InstallOptions, consent app.Consent) (app.InstallResult, error) {
	t.Helper()
	opts.Mode = app.ModeReplace
	if opts.Harnesses == nil {
		opts.Harnesses = pack.Harnesses // OpenCode, a preview harness, too
	}
	return app.InstallConsent(context.Background(), newClockFS(t), h, quiet(t).fetch, replaceRun(t), examplePack, home, opts, consent)
}

// TestReplaceConsent checks when a replace install needs consent
// (decision 0038): not when everything it wipes is an empty directory,
// such as on a fresh install; when it wipes a file of the user's, consent
// is asked once with the plan, and without it the install writes nothing.
func TestReplaceConsent(t *testing.T) {
	home, _ := installHome(t)
	for _, rel := range []string{".claude/agents", ".claude/plugins/cache", ".config/opencode"} {
		if err := os.MkdirAll(at(home, rel), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	h := app.NewHost(found(at(home, "bin"), "claude", "agy", "opencode"), nil, "")
	res, err := installConsent(t, h, home, app.InstallOptions{}, nil)
	if err != nil || len(res.Wipes) == 0 {
		t.Fatalf("Install(replace) over empty directories = %+v, %v, want it to wipe them without consent", res.Wipes, err)
	}

	writeFile(t, at(home, ".claude/agents/mine.md"), "mine\n")
	before := homeEntries(t, home)
	asked := 0
	_, err = installConsent(t, h, home, app.InstallOptions{Reset: true}, func(p app.InstallPlan) (bool, error) {
		asked++
		if len(p.Wipes) != 1 || !strings.HasSuffix(p.Wipes[0].Path, "agents") {
			t.Errorf("consent asked with wipes %+v, want ~/.claude/agents", p.Wipes)
		}
		return false, nil
	})
	var ce *app.ConsentError
	if !errors.As(err, &ce) || asked != 1 || ce.Entries != 1 || len(ce.Plan.Wipes) != 1 {
		t.Fatalf("Install(replace) answered no: %v, asked %d times, want a *ConsentError for 1 entry", err, asked)
	}
	diff(t, "home after the refusal", homeEntries(t, home), before)
}

// TestReplaceRunningCheck checks that a replace install is refused while a
// process named as a harness it manages runs, but not for dotai's own
// process, an unmanaged harness or --ignore-running, and that a listing
// that fails refuses too.
func TestReplaceRunningCheck(t *testing.T) {
	for _, tt := range []struct {
		name    string
		procs   []proc.Process
		listErr error
		ignore  bool
		want    string // the harness refused, "" for none
	}{
		{name: "claude running", procs: []proc.Process{{PID: 10, Name: "claude"}}, want: "claudecode"},
		{name: "agy running", procs: []proc.Process{{PID: 10, Name: "agy"}}, want: "antigravity-cli"},
		{name: "claude started through a link to its versioned binary", procs: []proc.Process{{PID: 10, Name: "2.1.30", Argv0: "claude"}}, want: "claudecode"},
		{name: "claude installed through npm", procs: []proc.Process{{PID: 10, Name: "node", Argv0: "node", Args: "node /usr/lib/node_modules/@anthropic-ai/claude-code/cli.js"}}, want: "claudecode"},
		{name: "opencode installed through npm", procs: []proc.Process{{PID: 10, Name: "node", Args: "node /usr/lib/node_modules/opencode-ai/bin/opencode"}}, want: "opencode"},
		{name: "another node program", procs: []proc.Process{{PID: 10, Name: "node", Args: "node /srv/server.js"}}},
		{name: "a program that names the package but is not node", procs: []proc.Process{{PID: 10, Name: "less", Args: "less /usr/lib/node_modules/@anthropic-ai/claude-code/README.md"}}},
		{name: "dotai's own process", procs: []proc.Process{{PID: 99, Name: "claude"}}},
		{name: "another program", procs: []proc.Process{{PID: 10, Name: "claudette"}}},
		{name: "ignored", procs: []proc.Process{{PID: 10, Name: "opencode"}}, ignore: true},
		{name: "listing fails", listErr: errors.New("no access"), want: "listing"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			home, _ := realHome(t)
			before := homeEntries(t, home)
			h := app.NewHostListing(found(at(home, "bin"), "claude", "agy", "opencode"), nil,
				func() ([]proc.Process, error) { return tt.procs, tt.listErr }, 99)
			_, err := installConsent(t, h, home, app.InstallOptions{IgnoreRunning: tt.ignore}, func(app.InstallPlan) (bool, error) { return true, nil })
			var re *app.RunningError
			switch {
			case tt.want == "":
				if err != nil {
					t.Fatalf("Install(replace) error = %v, want it installed", err)
				}
				return
			case !errors.As(err, &re):
				t.Fatalf("Install(replace) error = %v, want a *RunningError", err)
			case tt.want == "listing" && (re.Err == nil || !strings.Contains(err.Error(), "--ignore-running")):
				t.Errorf("error = %v, want the listing's failure and --ignore-running", err)
			case tt.want != "listing" && (len(re.Running) != 1 || re.Running[0].Harness != tt.want):
				t.Errorf("running = %+v, want %s", re.Running, tt.want)
			}
			diff(t, "home after the refusal", homeEntries(t, home), before)
		})
	}
}

// TestRunningErrorReads checks the message of a refusal for one harness
// and for several, of an install and of a restore, and the message of a
// consent refusal, which names every kind of change consent covers.
func TestRunningErrorReads(t *testing.T) {
	one := []app.RunningHarness{{Harness: "opencode", PID: 7, Name: "opencode"}}
	two := append([]app.RunningHarness{{Harness: "claudecode", PID: 5, Name: "claude"}}, one...)
	for _, tt := range []struct {
		err  error
		want string
	}{
		{&app.RunningError{Operation: "install", Running: one, Harnesses: []string{"opencode"}},
			"opencode (opencode 7) is running, and the replace install would rewrite its configuration under it; the install wrote nothing: close it, then run again;"},
		{&app.RunningError{Operation: "restore", Running: two, Harnesses: []string{"claudecode", "opencode"}},
			"claudecode (claude 5), opencode (opencode 7) are running, and the restore would put back their configuration under them; the restore wrote nothing: close them, then run again;"},
		{&app.RunningError{Operation: "restore", Harnesses: []string{"claudecode", "opencode"}, Err: errors.New("no access")},
			"dotai could not check whether claudecode, opencode are running (no access), and the restore would put back their configuration while they run: the restore wrote nothing; close them, then run again with --ignore-running"},
		{&app.ConsentError{Entries: 2, Files: 1, Keys: 3},
			"the replace install would move into its backup what dotai does not own: 2 entries it wipes, 1 file it overwrites and 3 keys of ~/.claude.json it deletes; the install wrote nothing: run it again with --yes"},
	} {
		if got := tt.err.Error(); !strings.HasPrefix(got, tt.want) {
			t.Errorf("Error() = %q, want it to start with %q", got, tt.want)
		}
	}
}
