package app_test

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/lartweib/dotai/internal/app"
	"github.com/lartweib/dotai/internal/proc"
)

// installConsent is a replace install on the host h that asks consent.
func installConsent(t *testing.T, h app.Host, home string, opts app.InstallOptions, consent app.Consent) (app.InstallResult, error) {
	t.Helper()
	opts.Mode = app.ModeReplace
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
	_, err = installConsent(t, h, home, app.InstallOptions{}, func(p app.InstallPlan) (bool, error) {
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
