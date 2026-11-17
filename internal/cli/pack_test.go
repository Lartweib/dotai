package cli_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lartweib/dotai/internal/app"
	"github.com/lartweib/dotai/internal/cli"
)

// packHome sets a new home directory for the test and returns it.
func packHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	return home
}

// packCalls records what the pack commands asked of the use cases.
type packCalls struct {
	home, url, dir, id string
	opts               app.PackGitOptions
}

// packEnv fakes the pack use cases, recording their calls in c; err, when
// set, is what each one fails with.
func packEnv(stdout, stderr *bytes.Buffer, c *packCalls, err error) cli.Env {
	env := testEnv(stdout, stderr)
	added := app.PackAddResult{
		PackList: app.PackList{Path: "/h/.dotai/packs.json", Packs: []app.PackEntry{}},
		Pack: app.PackEntry{ID: "github.com/acme/pack", Path: "/h/.dotai/packs/github.com/acme/pack", Name: "acme-pack", Version: "1.0.0",
			Source: &app.PackSource{URL: "https://github.com/acme/pack", Ref: "", FullRef: "refs/heads/main", Commit: strings.Repeat("a", 40)}},
		Outcome:  app.PackAdded,
		Warnings: []app.PackWarning{{Code: "ref-branch", Message: "the branch main can move"}},
	}
	env.AddGitPack = func(_ context.Context, home, url string, opts app.PackGitOptions) (app.PackAddResult, error) {
		c.home, c.url, c.opts = home, url, opts
		return added, err
	}
	env.AddLocalPack = func(home, dir string) (app.PackAddResult, error) {
		c.home, c.dir = home, dir
		return app.PackAddResult{Pack: app.PackEntry{ID: "local/mine", Path: dir}, Outcome: app.PackAdded, Warnings: []app.PackWarning{}}, err
	}
	env.RemovePackID = func(home, id string) (app.PackRemoveResult, error) {
		c.home, c.id = home, id
		return app.PackRemoveResult{Removed: []app.PackEntry{{ID: id}}, Deleted: "/h/.dotai/packs/" + id, Warnings: []app.PackWarning{}}, err
	}
	env.ListPacks = func(home string) (app.PackList, error) {
		c.home = home
		return app.PackList{Path: "/h/.dotai/packs.json", Packs: []app.PackEntry{added.Pack}}, err
	}
	return env
}

// envelopeOf decodes the one envelope of stdout.
func envelopeOf(t *testing.T, stdout *bytes.Buffer) map[string]any {
	t.Helper()
	var e map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &e); err != nil {
		t.Fatalf("stdout is not one envelope: %v\n%s", err, stdout)
	}
	return e
}

func TestPackAddOfAURLFetchesWithPromptsOn(t *testing.T) {
	home := packHome(t)
	var stdout, stderr bytes.Buffer
	var c packCalls
	code := cli.Run([]string{"pack", "add", "https://github.com/acme/pack", "--ref", "v1", "--json"}, packEnv(&stdout, &stderr, &c, nil))
	if code != cli.ExitOK {
		t.Fatalf("exit = %d; stdout %s; stderr %s", code, &stdout, &stderr)
	}
	if c.home != home || c.url != "https://github.com/acme/pack" || c.opts.Ref != "v1" || !c.opts.Interactive {
		t.Errorf("calls = %+v, want the URL, the ref and git's prompts on", c)
	}
	e := envelopeOf(t, &stdout)
	data := e["data"].(map[string]any)
	if e["command"] != "pack add" || data["outcome"] != "added" || data["pack"].(map[string]any)["id"] != "github.com/acme/pack" {
		t.Errorf("envelope = %v", e)
	}
}

func TestPackAddPrintsThePackAndItsWarnings(t *testing.T) {
	packHome(t)
	var stdout, stderr bytes.Buffer
	var c packCalls
	if code := cli.Run([]string{"pack", "add", "git@github.com:acme/pack"}, packEnv(&stdout, &stderr, &c, nil)); code != cli.ExitOK {
		t.Fatalf("exit = %d; stderr %s", code, &stderr)
	}
	for _, want := range []string{"added github.com/acme/pack", "acme-pack 1.0.0", "refs/heads/main", strings.Repeat("a", 12)} {
		if !strings.Contains(stdout.String(), want) {
			t.Errorf("stdout lacks %q:\n%s", want, &stdout)
		}
	}
	if !strings.Contains(stderr.String(), "warning: the branch main can move") {
		t.Errorf("stderr = %q, want the warning", &stderr)
	}
}

func TestPackAddDirAddsTheAbsoluteDirectory(t *testing.T) {
	home := packHome(t)
	dir := t.TempDir()
	t.Chdir(dir)
	var stdout, stderr bytes.Buffer
	var c packCalls
	if code := cli.Run([]string{"pack", "add", "--dir", "sub", "--json"}, packEnv(&stdout, &stderr, &c, nil)); code != cli.ExitOK {
		t.Fatalf("exit = %d; stdout %s", code, &stdout)
	}
	if c.home != home || c.dir != filepath.Join(dir, "sub") {
		t.Errorf("calls = %+v, want the directory made absolute", c)
	}
}

func TestPackAddRefusesWhatItCannotTell(t *testing.T) {
	packHome(t)
	for _, args := range [][]string{
		{"pack", "add"},
		{"pack", "add", "--dir", "x", "--ref", "v1"},
		{"pack", "add", "https://github.com/a/b", "--dir", "x"},
		{"pack", "add", "https://github.com/a/b", "https://github.com/a/c"},
		{"pack", "remove"},
	} {
		var stdout, stderr bytes.Buffer
		var c packCalls
		code := cli.Run(append(args, "--json"), packEnv(&stdout, &stderr, &c, nil))
		if code != cli.ExitUsage {
			t.Errorf("%v: exit = %d, want %d", args, code, cli.ExitUsage)
		}
		if e := envelopeOf(t, &stdout); e["error"].(map[string]any)["code"] != "usage" {
			t.Errorf("%v: envelope = %v", args, e)
		}
		if c.url != "" || c.dir != "" {
			t.Errorf("%v: a use case ran: %+v", args, c)
		}
	}
}

func TestPackAddFailuresKeepTheirCodes(t *testing.T) {
	packHome(t)
	for code, err := range map[string]error{
		"host-unsupported":        &app.HostUnsupportedError{URL: "https://gitlab.com/a/b", Host: "gitlab.com"},
		"git-too-old":             &app.GitUnavailableError{Version: "2.40.0", TooOld: true},
		"fetch-by-commit-refused": &app.FetchByCommitRefusedError{URL: "u", Commit: strings.Repeat("1", 40)},
		"pack-tree-refused":       &app.PackTreeRefusedError{Problems: []app.TreeProblem{{Path: "l", Problem: "is a link"}}},
	} {
		var stdout, stderr bytes.Buffer
		var c packCalls
		if exit := cli.Run([]string{"pack", "add", "https://github.com/a/b", "--json"}, packEnv(&stdout, &stderr, &c, err)); exit != cli.ExitInternal {
			t.Errorf("%s: exit = %d, want %d", code, exit, cli.ExitInternal)
		}
		if e := envelopeOf(t, &stdout); e["error"].(map[string]any)["code"] != code {
			t.Errorf("%s: envelope = %v", code, e)
		}
	}
}

func TestPackRemoveRemovesTheIdentity(t *testing.T) {
	home := packHome(t)
	var stdout, stderr bytes.Buffer
	var c packCalls
	if code := cli.Run([]string{"pack", "remove", "github.com/acme/pack"}, packEnv(&stdout, &stderr, &c, nil)); code != cli.ExitOK {
		t.Fatalf("exit = %d; stderr %s", code, &stderr)
	}
	if c.home != home || c.id != "github.com/acme/pack" {
		t.Errorf("calls = %+v", c)
	}
	if !strings.Contains(stdout.String(), "removed github.com/acme/pack") || !strings.Contains(stdout.String(), "deleted") {
		t.Errorf("stdout = %q, want the removal and the deleted clone", &stdout)
	}
}

func TestPackListListsTheIdentities(t *testing.T) {
	packHome(t)
	var stdout, stderr bytes.Buffer
	var c packCalls
	if code := cli.Run([]string{"pack", "list"}, packEnv(&stdout, &stderr, &c, nil)); code != cli.ExitOK {
		t.Fatalf("exit = %d; stderr %s", code, &stderr)
	}
	if !strings.Contains(stdout.String(), "github.com/acme/pack") || !strings.Contains(stdout.String(), "acme-pack 1.0.0") {
		t.Errorf("stdout = %q", &stdout)
	}
	stdout.Reset()
	if code := cli.Run([]string{"pack", "list", "--json"}, packEnv(&stdout, &stderr, &c, errors.New("boom"))); code != cli.ExitInternal {
		t.Errorf("a failed list exits %d", code)
	}
}

func TestPackCommandsWithTheRealUseCases(t *testing.T) {
	home := packHome(t)
	dir := filepath.Join(t.TempDir(), "minimal-pack")
	if err := os.CopyFS(dir, os.DirFS(examplePack)); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	env := testEnv(&stdout, &stderr)
	env.AddLocalPack, env.RemovePackID, env.ListPacks = app.AddLocalPack, app.RemovePackID, app.ListPacks
	if code := cli.Run([]string{"pack", "add", "--dir", dir, "--json"}, env); code != cli.ExitOK {
		t.Fatalf("add exit = %d; %s", code, &stdout)
	}
	stdout.Reset()
	if code := cli.Run([]string{"pack", "remove", "local/minimal-pack", "--json"}, env); code != cli.ExitOK {
		t.Fatalf("remove exit = %d; %s", code, &stdout)
	}
	if _, err := os.Stat(filepath.Join(dir, "dotai.yaml")); err != nil {
		t.Errorf("the removal touched the local pack: %v", err)
	}
	if _, err := os.Stat(filepath.Join(home, ".dotai", "packs.json")); err != nil {
		t.Errorf("no registry in the test home: %v", err)
	}
}

// updateEnv fakes UpdatePack with a plan that changes a tool: it asks the
// consent it is given, as the use case does, and records the answer.
func updateEnv(stdout, stderr *bytes.Buffer, c *packCalls, consented *bool) cli.Env {
	env := packEnv(stdout, stderr, c, nil)
	plan := app.PackUpdatePlan{ID: "github.com/acme/pack", Path: "/h/.dotai/packs/github.com/acme/pack",
		From:    app.PackSource{URL: "https://github.com/acme/pack", FullRef: "refs/heads/main", Commit: strings.Repeat("a", 40)},
		To:      app.PackSource{URL: "https://github.com/acme/pack", FullRef: "refs/heads/main", Commit: strings.Repeat("b", 40)},
		Changes: []app.ExecChange{{Path: "base/dotai/tools/x.sh", Change: app.ChangeAdded, Diff: "--- /dev/null\n+++ b/base/dotai/tools/x.sh\n@@ -0,0 +1 @@\n+rm -rf ~\n"}},
	}
	env.UpdatePack = func(_ context.Context, home, id string, opts app.PackGitOptions) (app.PackAddResult, error) {
		c.home, c.id, c.opts = home, id, opts
		ok := false
		if opts.Consent != nil {
			var err error
			if ok, err = opts.Consent(plan); err != nil {
				return app.PackAddResult{}, err
			}
		}
		*consented = ok
		if !ok {
			return app.PackAddResult{}, &app.PackConsentError{Plan: plan}
		}
		return app.PackAddResult{Pack: app.PackEntry{ID: id, Source: &plan.To}, Outcome: app.PackUpdated, Update: &plan, Warnings: []app.PackWarning{}}, nil
	}
	return env
}

func TestPackUpdateWithYesUpdates(t *testing.T) {
	packHome(t)
	var stdout, stderr bytes.Buffer
	var c packCalls
	var consented bool
	if code := cli.Run([]string{"pack", "update", "github.com/acme/pack", "--yes", "--json"}, updateEnv(&stdout, &stderr, &c, &consented)); code != cli.ExitOK {
		t.Fatalf("exit = %d; %s", code, &stdout)
	}
	if !consented || c.id != "github.com/acme/pack" || !c.opts.Interactive {
		t.Errorf("calls = %+v, consented %v", c, consented)
	}
	if e := envelopeOf(t, &stdout); e["command"] != "pack update" || e["data"].(map[string]any)["outcome"] != "updated" {
		t.Errorf("envelope = %v", e)
	}
}

func TestPackUpdateWithoutYesInJSONNeedsConsentWithThePlan(t *testing.T) {
	packHome(t)
	var stdout, stderr bytes.Buffer
	var c packCalls
	var consented bool
	env := updateEnv(&stdout, &stderr, &c, &consented)
	env.Prompt = func(context.Context, string) (bool, error) { t.Error("--json asked a question"); return true, nil }
	if code := cli.Run([]string{"pack", "update", "github.com/acme/pack", "--json"}, env); code != cli.ExitInternal {
		t.Fatalf("exit = %d; %s", code, &stdout)
	}
	e := envelopeOf(t, &stdout)
	if e["error"].(map[string]any)["code"] != "consent-required" || e["data"].(map[string]any)["changes"] == nil {
		t.Errorf("envelope = %v, want consent-required with the plan", e)
	}
}

func TestPackUpdateShowsTheDiffAndAsks(t *testing.T) {
	packHome(t)
	for answer, wantCode := range map[bool]int{true: cli.ExitOK, false: cli.ExitInternal} {
		var stdout, stderr bytes.Buffer
		var c packCalls
		var consented bool
		var question string
		env := updateEnv(&stdout, &stderr, &c, &consented)
		env.Prompt = func(_ context.Context, q string) (bool, error) { question = q; return answer, nil }
		code := cli.Run([]string{"pack", "update", "github.com/acme/pack"}, env)
		if code != wantCode {
			t.Errorf("answer %v: exit = %d, want %d; stderr %s", answer, code, wantCode, &stderr)
		}
		if !strings.Contains(stdout.String(), "+rm -rf ~") || !strings.Contains(stdout.String(), "added base/dotai/tools/x.sh") {
			t.Errorf("answer %v: stdout lacks the diff:\n%s", answer, &stdout)
		}
		if question == "" {
			t.Errorf("answer %v: nothing was asked", answer)
		}
		if !answer && !strings.Contains(stderr.String(), "cancelled") {
			t.Errorf("a refused update says %q", &stderr)
		}
	}
}

func TestPackAddTakesYesForAnUpdate(t *testing.T) {
	packHome(t)
	var stdout, stderr bytes.Buffer
	var c packCalls
	env := packEnv(&stdout, &stderr, &c, nil)
	if code := cli.Run([]string{"pack", "add", "https://github.com/acme/pack", "--yes", "--json"}, env); code != cli.ExitOK {
		t.Fatalf("exit = %d; %s", code, &stdout)
	}
	if c.opts.Consent == nil {
		t.Fatal("pack add passes no consent")
	}
	if ok, _ := c.opts.Consent(app.PackUpdatePlan{}); !ok {
		t.Error("pack add --yes does not consent")
	}
}

func TestPackUpdateEscapesWhatThePackControls(t *testing.T) {
	packHome(t)
	var stdout, stderr bytes.Buffer
	var c packCalls
	var consented bool
	env := updateEnv(&stdout, &stderr, &c, &consented)
	plan := app.PackUpdatePlan{ID: "github.com/acme/pack",
		From: app.PackSource{FullRef: "refs/heads/m\x1b[2Kain", Commit: strings.Repeat("a", 40)},
		To:   app.PackSource{FullRef: "refs/heads/main", Commit: strings.Repeat("b", 40)},
		Changes: []app.ExecChange{
			{Path: "base/dotai/tools/\x1b[8mx.sh", Change: app.ChangeAdded, Binary: true, Note: "binary: compare the hashes", After: "sha256:00"},
		},
	}
	env.UpdatePack = func(_ context.Context, _, _ string, opts app.PackGitOptions) (app.PackAddResult, error) {
		_, _ = opts.Consent(plan)
		return app.PackAddResult{}, &app.PackConsentError{Plan: plan}
	}
	env.Prompt = func(context.Context, string) (bool, error) { return false, nil }
	cli.Run([]string{"pack", "update", "github.com/acme/pack"}, env)
	if strings.Contains(stdout.String(), "\x1b") || strings.Contains(stderr.String(), "\x1b") {
		t.Errorf("an escape sequence reached the terminal:\n%q", stdout.String())
	}
	if !strings.Contains(stdout.String(), `\x1b[8mx.sh`) || !strings.Contains(stdout.String(), "compare the hashes") {
		t.Errorf("stdout = %q, want the path escaped and the binary note", &stdout)
	}
}

func TestErrorsReachTheTerminalEscaped(t *testing.T) {
	packHome(t)
	var stdout, stderr bytes.Buffer
	var c packCalls
	env := packEnv(&stdout, &stderr, &c, errors.New("git said \x1b]0;owned\x07 here\nsecond line"))
	cli.Run([]string{"pack", "add", "https://github.com/a/b"}, env)
	if strings.Contains(stderr.String(), "\x1b") || strings.Contains(stderr.String(), "\x07") || !strings.Contains(stderr.String(), "\nsecond line") {
		t.Errorf("stderr = %q, want the controls escaped and the lines kept", &stderr)
	}
}
