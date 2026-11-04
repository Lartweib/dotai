package cli_test

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/lartweib/dotai/internal/app"
	"github.com/lartweib/dotai/internal/backup"
	"github.com/lartweib/dotai/internal/cli"
	"github.com/lartweib/dotai/internal/envelope"
	"github.com/lartweib/dotai/internal/plan"
)

// restoreResult is a restore of two installs with fixed paths, so that
// its output reads the same on every OS.
var restoreResult = app.RestoreResult{
	Undone: []app.UndoneInstall{
		{ID: "20261029T103000.000Z_install", Path: "/home/u/.dotai/backups/20261029T103000.000Z_install", CreatedAt: time.Date(2026, 10, 29, 10, 30, 0, 0, time.UTC),
			Pack: backup.Pack{Name: "minimal-pack", Version: "0.1.0", Path: "/home/u/packs/minimal-pack"}, Variant: "tmux", Harnesses: []string{"claudecode", "opencode"}},
		{ID: "20261029T101500.000Z_install", Path: "/home/u/.dotai/backups/20261029T101500.000Z_install", CreatedAt: time.Date(2026, 10, 29, 10, 15, 0, 0, time.UTC),
			Pack: backup.Pack{Name: "minimal-pack", Version: "0.1.0", Path: "/home/u/packs/minimal-pack"}, Variant: "standalone", Harnesses: []string{"claudecode", "opencode"}},
	},
	Ops: []app.RestoreOp{
		{Action: plan.Replace, Harness: "claudecode", Target: "/home/u/.dotai/isolated/claude/settings.json", From: "20261029T101500.000Z_install", Before: "aa", After: "bb",
			Keys: []plan.KeyChange{{Key: "env.DOTAI_TERMINAL", Action: plan.Remove}, {Key: "model", Action: plan.Replace}}},
		{Action: plan.Remove, Harness: "claudecode", Target: "/home/u/.dotai/isolated/claude/agents/reviewer.md", Before: "cc", Conflict: true, Keys: []plan.KeyChange{}},
		{Action: plan.Create, Harness: "opencode", Target: "/home/u/.dotai/isolated/opencode/config/opencode/opencode.jsonc", From: "20261029T103000.000Z_install", After: "dd", Keys: []plan.KeyChange{}},
		{Action: plan.Remove, Target: "/home/u/.dotai/tools/hello.sh", Before: "ee", BeforeExec: true, Keys: []plan.KeyChange{}},
	},
	Wipes:     []app.RestoreWipe{},
	WipeKeys:  []app.WipeKey{},
	Conflicts: []app.RestoreConflict{{Target: "/home/u/.dotai/isolated/claude/agents/reviewer.md", Harness: "claudecode", Backup: "20261029T103000.000Z_install", Expected: "c0", Current: "cc"}},
	Harnesses: []string{"claudecode", "opencode"},
	Backup:    &app.BackupRef{ID: "20261029T110000.000Z_restore", Path: "/home/u/.dotai/backups/20261029T110000.000Z_restore"},
	State:     "/home/u/.dotai/state.json",
	Warnings:  []string{"close claudecode, opencode before restoring: a harness that is running may rewrite its files while they are put back"},
}

// fakeRestore is a restore that asks overwrite about res's conflicts as
// the real one does, and records how it was called.
type fakeRestore struct {
	t     *testing.T
	res   app.RestoreResult
	home  string
	id    string
	asked bool
}

func (f *fakeRestore) restore(ctx context.Context, home, id string, _ app.RestoreOptions, warn app.Warn, overwrite func(app.RestoreResult) (bool, error)) (app.RestoreResult, error) {
	if ctx == nil || overwrite == nil {
		f.t.Error("Restore called without a context or an overwrite function")
	}
	f.home, f.id = home, id
	if warn != nil { // as the real one does, before it asks or writes
		for _, w := range f.res.Warnings {
			warn(w)
		}
	}
	if len(f.res.Conflicts) > 0 {
		f.asked = true
		ok, err := overwrite(f.res)
		if err != nil {
			return app.RestoreResult{}, err
		}
		if !ok {
			return app.RestoreResult{}, &app.ConflictError{Conflicts: f.res.Conflicts}
		}
	}
	return f.res, nil
}

// prompter answers a question as answer, and records it.
type prompter struct {
	answer   bool
	question string
}

func (p *prompter) prompt(_ context.Context, q string) (bool, error) {
	p.question = q
	return p.answer, nil
}

func restoreHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	return home
}

func TestRunRestore(t *testing.T) {
	home := restoreHome(t)
	for _, tt := range []struct {
		name, golden, id string
		args             []string
	}{
		{name: "text", args: []string{"restore", "20261029T101500.000Z_install", "--yes"}, golden: "restore.txt.golden", id: "20261029T101500.000Z_install"},
		{name: "json", args: []string{"restore", "--yes", "--json"}, golden: "restore.json.golden"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			env := testEnv(&stdout, &stderr)
			f := &fakeRestore{t: t, res: restoreResult}
			env.Restore = f.restore
			p := &prompter{}
			env.Prompt = p.prompt
			code := cli.Run(tt.args, env)
			if code != cli.ExitOK || f.home != home || f.id != tt.id || p.question != "" {
				t.Errorf("exit code = %d, Restore(%q, %q), asked %q, want 0, the home and %q without a question", code, f.home, f.id, p.question, tt.id)
			}
			if tt.name == "text" && stderr.String() != "warning: "+restoreResult.Warnings[0]+"\n" {
				t.Errorf("stderr = %q, want the warning", stderr.String())
			}
			if tt.name == "json" && stderr.Len() != 0 {
				t.Errorf("stderr = %q, want nothing besides the envelope", stderr.String())
			}
			assertGolden(t, filepath.Join("testdata", tt.golden), stdout.Bytes())
		})
	}
}

// TestRunRestoreOfAReplaceInstall checks the text of a restore that moves
// back what a replace install wiped and puts back the keys of
// ~/.claude.json (decision 0038).
func TestRunRestoreOfAReplaceInstall(t *testing.T) {
	restoreHome(t)
	res := restoreResult
	res.Undone = res.Undone[1:]
	res.Ops = []app.RestoreOp{{Action: plan.Remove, Harness: "claudecode", Target: "/home/u/.claude/CLAUDE.md", Before: "cc", Keys: []plan.KeyChange{}}}
	res.Wipes = []app.RestoreWipe{
		{Harness: "claudecode", Path: "/home/u/.claude/CLAUDE.md", Kind: "file", From: res.Undone[0].ID},
		{Harness: "claudecode", Path: "/home/u/.claude/agents", Kind: "dir", From: res.Undone[0].ID, Conflict: true},
		{Harness: "opencode", Path: "/home/u/AGENTS.md", Kind: "file", From: res.Undone[0].ID},
	}
	res.WipeKeys = []app.WipeKey{
		{Harness: "claudecode", File: "/home/u/.claude.json", Key: []string{"mcpServers"}},
		{Harness: "claudecode", File: "/home/u/.claude.json", Key: []string{"projects", "/home/u/work", "allowedTools"}},
	}
	res.Conflicts = []app.RestoreConflict{{Target: "/home/u/.claude/agents", Harness: "claudecode", Backup: res.Undone[0].ID}}
	res.Uninstalled = true
	var stdout, stderr bytes.Buffer
	env := testEnv(&stdout, &stderr)
	env.Restore = (&fakeRestore{t: t, res: res}).restore
	if code := cli.Run([]string{"restore", "--yes"}, env); code != cli.ExitOK {
		t.Fatalf("exit code = %d, stderr %s", code, stderr.String())
	}
	assertGolden(t, filepath.Join("testdata", "restore-replace.txt.golden"), stdout.Bytes())
}

func TestRunRestoreJSONShape(t *testing.T) {
	restoreHome(t)
	var stdout bytes.Buffer
	env := testEnv(&stdout, &bytes.Buffer{})
	env.Restore = (&fakeRestore{t: t, res: restoreResult}).restore
	if code := cli.Run([]string{"restore", "--yes", "--json"}, env); code != cli.ExitOK {
		t.Fatalf("exit code = %d", code)
	}
	var e struct {
		Command string                     `json:"command"`
		Data    map[string]json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &e); err != nil {
		t.Fatal(err)
	}
	if e.Command != "restore" {
		t.Errorf("command = %q, want restore", e.Command)
	}
	for _, key := range []string{"undone", "ops", "conflicts", "harnesses", "backup", "state", "uninstalled", "warnings"} {
		if _, ok := e.Data[key]; !ok {
			t.Errorf("data has no %q: %v", key, e.Data)
		}
	}
	var ops []map[string]json.RawMessage
	if err := json.Unmarshal(e.Data["ops"], &ops); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"action", "harness", "target", "from", "exec", "before", "beforeExec", "after", "conflict"} {
		if _, ok := ops[0][key]; !ok {
			t.Errorf("op has no %q: %v", key, ops[0])
		}
	}
}

// TestRunRestoreConflicts checks who decides about files changed after the
// install: --yes overwrites, a terminal is asked once, and without one, or
// with --json, the restore is refused.
func TestRunRestoreConflicts(t *testing.T) {
	restoreHome(t)
	conflict := restoreResult.Conflicts[0].Target
	for _, tt := range []struct {
		name     string
		args     []string
		terminal bool
		answer   bool
		wantCode int
		wantAsk  bool
		wantErr  string
	}{
		{name: "terminal, yes", args: []string{"restore"}, terminal: true, answer: true, wantCode: cli.ExitOK, wantAsk: true},
		{name: "terminal, no", args: []string{"restore"}, terminal: true, wantCode: cli.ExitInternal, wantAsk: true, wantErr: "the restore was cancelled: nothing was written"},
		{name: "no terminal", args: []string{"restore"}, wantCode: cli.ExitInternal, wantErr: "the file " + conflict + " was changed"},
		{name: "json on a terminal", args: []string{"restore", "--json"}, terminal: true, wantCode: cli.ExitInternal, wantErr: "the file " + conflict + " was changed"},
		{name: "yes", args: []string{"restore", "--yes"}, wantCode: cli.ExitOK},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			env := testEnv(&stdout, &stderr)
			env.Restore = (&fakeRestore{t: t, res: restoreResult}).restore
			p := &prompter{answer: tt.answer}
			if tt.terminal {
				env.Prompt = p.prompt
			}
			code := cli.Run(tt.args, env)
			if code != tt.wantCode || (p.question != "") != tt.wantAsk {
				t.Fatalf("exit code = %d, asked %q, want %d and asked %v", code, p.question, tt.wantCode, tt.wantAsk)
			}
			if tt.wantAsk && (!strings.Contains(stdout.String(), "1 file was changed after dotai installed it") || !strings.Contains(stdout.String(), conflict) ||
				p.question != "Overwrite it? [y/N] " || !strings.Contains(stderr.String(), "warning: close claudecode, opencode")) {
				t.Errorf("stdout = %q, stderr = %q, question %q, want the conflict, the warning and one question", stdout.String(), stderr.String(), p.question)
			}
			if tt.wantErr == "" {
				if strings.Count(stderr.String(), "warning: close") != 1 {
					t.Errorf("stderr = %q, want the warning once", stderr.String())
				}
				return
			}
			if strings.Contains(tt.args[len(tt.args)-1], "json") {
				if e := decodeOne(t, stdout.Bytes()); e.OK || e.Error == nil || e.Error.Code != envelope.CodeInternal || !strings.Contains(e.Error.Message, tt.wantErr) {
					t.Errorf("envelope = %+v, want an internal error with %q", e, tt.wantErr)
				}
				var e struct {
					Data struct {
						Conflicts []map[string]string `json:"conflicts"`
					} `json:"data"`
				}
				if err := json.Unmarshal(stdout.Bytes(), &e); err != nil {
					t.Fatal(err)
				}
				want := map[string]string{"target": conflict, "harness": "claudecode", "backup": "20261029T103000.000Z_install", "key": "", "expected": "c0", "current": "cc"}
				if len(e.Data.Conflicts) != 1 || !reflect.DeepEqual(e.Data.Conflicts[0], want) {
					t.Errorf("data conflicts = %v, want %v", e.Data.Conflicts, want)
				}
				return
			}
			if !strings.Contains(stderr.String(), "Error: "+tt.wantErr) {
				t.Errorf("stderr = %q, want %q", stderr.String(), tt.wantErr)
			}
		})
	}
}

// TestRunRestoreWarnsBeforeItWrites checks that the warning to close the
// harnesses is printed when the restore gives it, before it writes and so
// before the summary, once.
func TestRunRestoreWarnsBeforeItWrites(t *testing.T) {
	restoreHome(t)
	var out bytes.Buffer // stdout and stderr, in the order they are written
	env := testEnv(&out, &out)
	res := restoreResult
	res.Conflicts = []app.RestoreConflict{}
	env.Restore = (&fakeRestore{t: t, res: res}).restore
	if code := cli.Run([]string{"restore"}, env); code != cli.ExitOK {
		t.Fatalf("exit code = %d", code)
	}
	w := "warning: " + restoreResult.Warnings[0] + "\n"
	if got := out.String(); !strings.HasPrefix(got, w+"Restore · ") || strings.Count(got, w) != 1 {
		t.Errorf("output = %q, want the warning first and once", got)
	}
}

// TestRunRestoreAsksAboutAChangedKey checks that a key of a settings file
// that changed after the install is listed by name before the question.
func TestRunRestoreAsksAboutAChangedKey(t *testing.T) {
	restoreHome(t)
	settings := "/home/u/.dotai/isolated/claude/settings.json"
	res := restoreResult
	res.Conflicts = []app.RestoreConflict{
		restoreResult.Conflicts[0],
		{Target: settings, Harness: "claudecode", Backup: "20261029T103000.000Z_install", Key: "model", Expected: "m0", Current: "m1"},
	}
	var stdout, stderr bytes.Buffer
	env := testEnv(&stdout, &stderr)
	env.Restore = (&fakeRestore{t: t, res: res}).restore
	p := &prompter{}
	env.Prompt = p.prompt
	cli.Run([]string{"restore"}, env)
	want := "2 files or keys were changed after dotai installed them; the restore overwrites or deletes them, after copying their files into a new backup:\n" +
		"  " + restoreResult.Conflicts[0].Target + "\n" +
		"  key model in " + settings + "\n"
	if stdout.String() != want || p.question != "Overwrite them? [y/N] " {
		t.Errorf("stdout = %q, question %q, want %q", stdout.String(), p.question, want)
	}
}

func TestRunRestoreThatWroteNothing(t *testing.T) {
	restoreHome(t)
	var stdout bytes.Buffer
	env := testEnv(&stdout, &bytes.Buffer{})
	res := app.RestoreResult{Undone: restoreResult.Undone[1:], Ops: []app.RestoreOp{}, Conflicts: []app.RestoreConflict{}, Harnesses: []string{},
		State: "/home/u/.dotai/state.json", Uninstalled: true, Warnings: []string{}}
	env.Restore = (&fakeRestore{t: t, res: res}).restore
	if code := cli.Run([]string{"restore"}, env); code != cli.ExitOK ||
		!strings.HasSuffix(stdout.String(), "0 restore, 0 delete · restored\nbackup: none, no file was written\nstate:  removed, nothing dotai installed is left (/home/u/.dotai/state.json)\n") {
		t.Errorf("exit code = %d, stdout = %q, want no backup and the state removed", code, stdout.String())
	}
}

func TestRunRestoreUsageErrors(t *testing.T) {
	restoreHome(t)
	var stdout bytes.Buffer
	env := testEnv(&stdout, &bytes.Buffer{})
	env.Restore = func(context.Context, string, string, app.RestoreOptions, app.Warn, func(app.RestoreResult) (bool, error)) (app.RestoreResult, error) {
		t.Error("Restore called on a usage error")
		return app.RestoreResult{}, nil
	}
	if code := cli.Run([]string{"restore", "a", "b", "--json"}, env); code != cli.ExitUsage {
		t.Errorf("exit code = %d, want %d", code, cli.ExitUsage)
	}
	if e := decodeOne(t, stdout.Bytes()); e.OK || e.Command != "restore" || e.Error == nil || e.Error.Code != envelope.CodeUsage {
		t.Errorf("envelope = %+v, want a usage error", e)
	}
}

// TestRunRestoreReal runs the real restore in a home with no install.
func TestRunRestoreReal(t *testing.T) {
	home := restoreHome(t)
	var stderr bytes.Buffer
	env := testEnv(&bytes.Buffer{}, &stderr)
	env.Restore = app.Restore
	if code := cli.Run([]string{"restore"}, env); code != cli.ExitInternal || !strings.Contains(stderr.String(), "nothing to restore") {
		t.Errorf("exit code = %d, stderr = %q, want nothing to restore", code, stderr.String())
	}
	if code := cli.Run([]string{"restore", "latest"}, env); code != cli.ExitUsage {
		t.Errorf("exit code = %d, want a usage error for an id that is not one", code)
	}
	if got := filepath.Join(home, ".dotai"); dirExists(got) {
		t.Errorf("%s exists, want nothing created", got)
	}
}

func dirExists(name string) bool {
	_, err := os.Stat(name)
	return err == nil
}

// projectRestore is a restore of a project with fixed paths.
var projectRestore = app.ProjectRestoreResult{
	Project: "/work/site", Record: "/home/u/.dotai/projects/0123456789abcdef/record.json",
	Ops: []app.ProjectRestoreOp{
		{Action: plan.Remove, Harness: "claudecode", Kind: "rules", Target: "/work/site/.claude/rules/web.md", Keys: []plan.ProjectKey{}},
		{Action: plan.Replace, Harness: "antigravity-cli", Kind: "rules", Target: "/work/site/.agents/rules/web.md", From: "20261102T101500.000Z_apply", Keys: []plan.ProjectKey{}},
		{Action: plan.Merge, Harness: "claudecode", Kind: "settings", Target: "/work/site/.claude/settings.local.json", Conflict: true,
			Keys: []plan.ProjectKey{{Path: []string{"model"}, Action: plan.Conflict}, {Path: []string{"env", "A"}, Action: plan.Remove}}},
		{Action: plan.Merge, Harness: "claudecode", Kind: "mcp", Target: "/home/u/.claude.json",
			Keys: []plan.ProjectKey{{Path: []string{"projects", "/work/site", "mcpServers", "browser"}, Action: plan.Remove}}},
	},
	Exclude:   app.ExcludeRestore{File: "/work/site/.git/info/exclude", Removed: true},
	Conflicts: []app.RestoreConflict{{Target: "/work/site/.claude/settings.local.json", Harness: "claudecode", Key: "model"}},
	Backup:    &app.BackupRef{ID: "20261102T111500.000Z_restore", Path: "/home/u/.dotai/projects/0123456789abcdef/backups/20261102T111500.000Z_restore"},
	Warnings:  []string{},
}

func TestRunRestoreProject(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for _, tt := range []struct {
		name, golden string
		args         []string
	}{
		{"text", "restore-project.txt.golden", []string{"restore", "--project", "--yes"}},
		{"json", "restore-project.json.golden", []string{"restore", "--project", "--yes", "--json"}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			env := testEnv(&stdout, &stderr)
			env.RestoreProject = func(_ context.Context, gotCwd, gotHome string, overwrite func(app.ProjectRestoreResult) (bool, error)) (app.ProjectRestoreResult, error) {
				if gotCwd != cwd || gotHome != home {
					t.Errorf("RestoreProject(%q, %q), want the current directory and the home", gotCwd, gotHome)
				}
				if ok, _ := overwrite(projectRestore); !ok {
					t.Error("overwrite with --yes = false")
				}
				return projectRestore, nil
			}
			if code := cli.Run(tt.args, env); code != cli.ExitOK || stderr.Len() != 0 {
				t.Errorf("exit code = %d, stderr = %q", code, stderr.String())
			}
			assertGolden(t, filepath.Join("testdata", tt.golden), stdout.Bytes())
		})
	}
}

func TestRunRestoreProjectUsageAndConflicts(t *testing.T) {
	for _, args := range [][]string{{"restore", "--project", "20261029T101500.000Z_install", "--json"}, {"restore", "--project", "--ignore-running", "--json"}} {
		var stdout bytes.Buffer
		env := testEnv(&stdout, &bytes.Buffer{})
		if code := cli.Run(args, env); code != cli.ExitUsage {
			t.Errorf("%q: exit code = %d, want %d", args, code, cli.ExitUsage)
		}
	}
	var stdout bytes.Buffer
	env := testEnv(&stdout, &bytes.Buffer{})
	env.RestoreProject = func(_ context.Context, _, _ string, overwrite func(app.ProjectRestoreResult) (bool, error)) (app.ProjectRestoreResult, error) {
		if ok, _ := overwrite(projectRestore); !ok {
			return app.ProjectRestoreResult{}, &app.ConflictError{Conflicts: projectRestore.Conflicts}
		}
		return projectRestore, nil
	}
	if code := cli.Run([]string{"restore", "--project", "--json"}, env); code != cli.ExitInternal || !strings.Contains(stdout.String(), `"conflicts"`) {
		t.Errorf("exit code = %d, stdout = %q, want the conflicts refused", code, stdout.String())
	}
}
