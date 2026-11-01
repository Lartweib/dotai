package app_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"slices"
	"sort"
	"strings"
	"testing"

	"github.com/lartweib/dotai/internal/app"
	"github.com/lartweib/dotai/internal/plan"
	"github.com/lartweib/dotai/internal/rulesync"
	"github.com/lartweib/dotai/internal/state"
)

// realConfig is a fake of the real user-level configuration of all three
// harnesses, as the replace inventory of HARNESS-NOTES found it: logins,
// history, caches and state, the user's own files, other tools' files, the
// files only Gemini CLI reads, and the generic paths under the home
// directory.
var realConfig = map[string]string{
	".claude/.credentials.json":             `{"token":"secret"}`,
	".claude/history.jsonl":                 "{}\n",
	".claude/projects/p/session.jsonl":      "{}\n",
	".claude/cache/model-catalog/a.json":    "{}",
	".claude/backups/.claude.json.backup.1": "{}",
	".claude/settings.json":                 `{"model":"opus","theme":"dark"}`,
	".claude/CLAUDE.md":                     "# my rules\n",
	".claude/agents/mine.md":                "mine\n",
	".claude/skills/mine/SKILL.md":          "mine\n",
	".claude/plugins/synced/.bucket-1":      "x",
	".claude/from-another-tool/x":           "x",
	".claude.json": `{"firstStartTime":"2026-01-01","oauthAccount":{"email":"a@b"},"numStartups":3,` +
		`"mcpServers":{"mine":{"command":"x"}},"projects":{"C:/work":{"mcpServers":{"p":{}},"allowedTools":["Bash"],` +
		`"enabledMcpjsonServers":[],"hasTrustDialogAccepted":true},"C:/other":{"hasTrustDialogAccepted":true}}}`,
	"CLAUDE.md":                                  "# home rules\n",
	"CLAUDE.local.md":                            "# local\n",
	".gemini/settings.json":                      `{"theme":"x"}`,
	".gemini/oauth_creds.json":                   "{}",
	".gemini/google_accounts.json":               "{}",
	".gemini/commands/c.toml":                    "x",
	".gemini/extensions/e/x":                     "x",
	".gemini/antigravity/conversations/c":        "x",
	".gemini/GEMINI.md":                          "# gemini rules\n",
	".gemini/AGENTS.md":                          "# agents rules\n",
	".gemini/unknown-to-agy":                     "x",
	".gemini/config/.migrated":                   "",
	".gemini/config/mcp_config.json":             "{}",
	".gemini/config/skills/old/SKILL.md":         "old\n",
	".gemini/config/projects/default.json":       "{}",
	".gemini/antigravity-cli/settings.json":      `{"x":1}`,
	".gemini/antigravity-cli/conversations/c.db": "db",
	".gemini/antigravity-cli/brain/b/steps":      "x",
	".gemini/antigravity-cli/cache/x":            "x",
	".gemini/antigravity-cli/installation_id":    "id",
	".config/opencode/opencode.json":             "{}",
	".config/opencode/AGENTS.md":                 "# oc rules\n",
	".config/opencode/service.json":              `{"password":"p"}`,
	".config/opencode/agents/a.md":               "a\n",
	".config/other-tool/x":                       "x",
	".local/share/opencode/opencode.db":          "db",
	".local/share/opencode/auth.json":            "{}",
	"AGENTS.md":                                  "# home agents\n",
	".opencode/agents/x.md":                      "x\n",
	".agents/skills/shared-one/SKILL.md":         "---\nname: shared-one\ndescription: Shared.\n---\n",
	".agents/rules/r.md":                         "r\n",
	"Documents/notes.txt":                        "mine\n",
}

// realHome is a temporary home seeded with realConfig and an empty
// temporary directory, with every harness's command on PATH.
func realHome(t *testing.T) (string, app.Host) {
	t.Helper()
	home, _ := installHome(t)
	for rel, data := range realConfig {
		writeFile(t, at(home, rel), data)
	}
	return home, app.NewHost(found(at(home, "bin"), "claude", "agy", "opencode"), nil, "")
}

// hashes is every entry under home, files with the SHA-256 of their
// content, so that a test sees any write.
func hashes(t *testing.T, home string) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.WalkDir(home, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(home, p)
		if d.IsDir() {
			out[filepath.ToSlash(rel)+"/"] = "dir"
			return nil
		}
		data, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		h := sha256.Sum256(data)
		out[filepath.ToSlash(rel)] = hex.EncodeToString(h[:])
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// replaceRun is rulesync writing each harness's rules file in the user
// scope, for the targets it is run for.
func replaceRun(t *testing.T) func(context.Context, rulesync.Result, []string, ...rulesync.Job) error {
	files := map[string]string{"claudecode": ".claude/CLAUDE.md", "antigravity-cli": ".gemini/GEMINI.md", "opencode": ".config/opencode/AGENTS.md"}
	return func(_ context.Context, _ rulesync.Result, targets []string, jobs ...rulesync.Job) error {
		for _, j := range jobs {
			for _, id := range targets {
				if j.Global {
					writeFile(t, filepath.Join(j.Dir, filepath.FromSlash(files[id])), "# pack rules\n")
				}
			}
		}
		return nil
	}
}

func planReplace(t *testing.T, home string, h app.Host, variant string) (app.InstallPlan, error) {
	t.Helper()
	f := quiet(t)
	return app.PlanInstallHost(context.Background(), app.OSInstallFS{}, h, f.fetch, replaceRun(t), examplePack, home, app.InstallOptions{Variant: variant, Mode: app.ModeReplace})
}

func rels(home string, paths []string) []string {
	out := []string{}
	for _, p := range paths {
		rel, _ := filepath.Rel(home, p)
		out = append(out, filepath.ToSlash(rel))
	}
	sort.Strings(out)
	return out
}

// TestPlanReplaceListsWipesKeepsAndInstalls checks the dry run of a
// replace install over the real configuration of all three harnesses
// (decision 0038): what moves to the backup, with the warnings for other
// tools, what stays, the keys of ~/.claude.json it wipes and the pack's
// installs in the harnesses' own directories, and that nothing on disk
// changed.
func TestPlanReplaceListsWipesKeepsAndInstalls(t *testing.T) {
	home, h := realHome(t)
	before := hashes(t, home)
	p, err := planReplace(t, home, h, "")
	if err != nil || len(p.Diagnostics) != 0 {
		t.Fatalf("PlanInstall(replace) = %+v, %v", p, err)
	}
	if after := hashes(t, home); !reflect.DeepEqual(after, before) {
		for k := range after {
			if before[k] != after[k] {
				t.Errorf("%s changed: %q -> %q", k, before[k], after[k])
			}
		}
		for k := range before {
			if _, ok := after[k]; !ok {
				t.Errorf("%s is gone", k)
			}
		}
		t.Fatal("the dry run wrote to the home directory")
	}
	if p.Mode != app.ModeReplace || p.Root != home {
		t.Errorf("mode %q, root %q, want replace in the home directory", p.Mode, p.Root)
	}

	var wiped []string
	collateral := map[string]string{}
	for _, w := range p.Wipes {
		wiped = append(wiped, w.Path)
		collateral[w.Path] = w.Collateral
	}
	wantWiped := []string{
		".agents/rules", ".agents/skills",
		".claude/CLAUDE.md", ".claude/agents", ".claude/from-another-tool", ".claude/plugins", ".claude/settings.json", ".claude/skills",
		".config/opencode/AGENTS.md", ".config/opencode/agents", ".config/opencode/opencode.json",
		".gemini/AGENTS.md", ".gemini/GEMINI.md",
		".gemini/antigravity-cli/settings.json",
		".gemini/config/mcp_config.json", ".gemini/config/projects", ".gemini/config/skills",
		".opencode", "AGENTS.md", "CLAUDE.md",
	}
	if got := rels(home, wiped); !reflect.DeepEqual(got, wantWiped) {
		t.Errorf("wipes =\n%q\nwant\n%q", got, wantWiped)
	}
	for _, rel := range []string{".gemini/GEMINI.md", ".gemini/AGENTS.md", "CLAUDE.md", "AGENTS.md", ".opencode", ".agents/skills", ".agents/rules"} {
		if collateral[at(home, rel)] == "" {
			t.Errorf("the wipe of %s warns about no other tool", rel)
		}
	}
	if !strings.Contains(collateral[at(home, ".gemini/GEMINI.md")], "Gemini CLI") {
		t.Errorf("collateral of ~/.gemini/GEMINI.md = %q, want it to name Gemini CLI", collateral[at(home, ".gemini/GEMINI.md")])
	}
	for _, w := range p.Wipes {
		if collateral[w.Path] != "" && strings.HasPrefix(w.Path, at(home, ".claude")+string(filepath.Separator)) {
			t.Errorf("the wipe of %s warns about other tools, want none inside a harness directory", w.Path)
		}
	}

	var kept []string
	kinds := map[string]string{}
	for _, k := range p.Keeps {
		kept = append(kept, k.Path)
		kinds[k.Path] = k.Kind
	}
	wantKept := []string{
		".claude/.credentials.json", ".claude/backups", ".claude/cache", ".claude/history.jsonl", ".claude/projects",
		".config/opencode/service.json",
		".gemini/antigravity-cli/brain", ".gemini/antigravity-cli/cache", ".gemini/antigravity-cli/conversations", ".gemini/antigravity-cli/installation_id",
		".gemini/config/.migrated",
	}
	if got := rels(home, kept); !reflect.DeepEqual(got, wantKept) {
		t.Errorf("keeps =\n%q\nwant\n%q", got, wantKept)
	}
	if kinds[at(home, ".claude/.credentials.json")] != "login" || kinds[at(home, ".claude/projects")] != "history" || kinds[at(home, ".gemini/config/.migrated")] != "state" {
		t.Errorf("keep kinds = %v", kinds)
	}

	type key struct{ harness, file, key string }
	var keys []key
	for _, k := range p.WipeKeys {
		keys = append(keys, key{k.Harness, k.File, strings.Join(k.Key, " ")})
	}
	wantKeys := []key{
		{"claudecode", at(home, ".claude.json"), "mcpServers"},
		{"claudecode", at(home, ".claude.json"), "projects C:/work mcpServers"},
		{"claudecode", at(home, ".claude.json"), "projects C:/work allowedTools"},
	}
	if !reflect.DeepEqual(keys, wantKeys) {
		t.Errorf("key wipes = %+v, want %+v (an empty value holds no configuration)", keys, wantKeys)
	}
	if len(p.Refusals) != 0 {
		t.Errorf("refusals = %+v, want none", p.Refusals)
	}

	ops := map[string]app.InstallOp{}
	for _, op := range p.Ops {
		rel, _ := filepath.Rel(home, op.Target)
		ops[filepath.ToSlash(rel)] = op
	}
	for rel, harness := range map[string]string{
		".claude/CLAUDE.md": "claudecode", ".claude/settings.json": "claudecode",
		".gemini/config/GEMINI.md": "antigravity-cli", ".gemini/antigravity-cli/settings.json": "antigravity-cli",
		".config/opencode/AGENTS.md": "opencode", ".config/opencode/opencode.jsonc": "opencode",
	} {
		// A file of the user's at a target is wiped first, so the pack's is
		// created on a clean base.
		if op, ok := ops[rel]; !ok || op.Action != "create" || op.Harness != harness || op.Before != "" {
			t.Errorf("op at %s = %+v, want a create for %s", rel, op, harness)
		}
	}
	if _, ok := ops[".gemini/GEMINI.md"]; ok {
		t.Error("the pack's rules file for agy installs at the root of ~/.gemini, which Gemini CLI reads")
	}
	for _, op := range p.Ops {
		if strings.HasPrefix(op.Target, at(home, ".dotai/isolated")) {
			t.Errorf("op %+v installs in the isolated layout", op)
		}
	}
	for _, hp := range p.Harnesses {
		if !hp.Managed || hp.Dir != hp.Config || hp.Relogin != "no" || len(hp.Pointers) != 0 || len(hp.Leaks) != 0 || hp.Level != "" {
			t.Errorf("harness %+v, want it managed in its own directory, keeping the login, without pointers or leaks", hp)
		}
	}
}

// TestPlanReplaceKeepsTheSharedSkills checks that a variant that shares
// skills keeps ~/.agents/skills, and links its skills into Claude Code's
// own skills directory (decision 0036).
func TestPlanReplaceKeepsTheSharedSkills(t *testing.T) {
	home, h := realHome(t)
	p, err := planReplace(t, home, h, "shared")
	if err != nil || len(p.Diagnostics) != 0 {
		t.Fatalf("PlanInstall(replace, shared) = %+v, %v", p, err)
	}
	if slices.ContainsFunc(p.Wipes, func(w app.WipePlan) bool { return w.Path == at(home, ".agents/skills") }) {
		t.Error("~/.agents/skills is wiped for a variant that shares it")
	}
	if !slices.ContainsFunc(p.Keeps, func(k app.KeepPlan) bool { return k.Path == at(home, ".agents/skills") && k.Kind == "shared" }) {
		t.Errorf("keeps = %+v, want ~/.agents/skills kept as shared", p.Keeps)
	}
	if !slices.ContainsFunc(p.Ops, func(op app.InstallOp) bool {
		return op.Target == at(home, ".claude/skills/shared-one") && op.Link == at(home, ".agents/skills/shared-one") && op.Action == "create"
	}) {
		t.Errorf("ops = %+v, want the shared skill linked into ~/.claude/skills", p.Ops)
	}
	if !slices.ContainsFunc(p.Ops, func(op app.InstallOp) bool { return op.Target == at(home, ".gemini/config/skills/shared-one/SKILL.md") }) {
		t.Errorf("ops = %+v, want the shared skill copied into agy's config/skills", p.Ops)
	}
}

// TestPlanReplaceRefusals checks what makes a replace refuse: a variable
// that points a selected harness at another configuration, an entry no
// keep-list classifies whose name looks like a login or history, and a
// ~/.claude.json that is not a JSON object. The dry run still shows the
// plan, and writes nothing.
func TestPlanReplaceRefusals(t *testing.T) {
	home, _ := realHome(t)
	writeFile(t, at(home, ".claude/oauth-backup.json"), "{}")
	writeFile(t, at(home, ".config/opencode/sessions.db"), "db")
	writeFile(t, at(home, ".claude.json"), "[1, 2]")
	// Inside a directory it would wipe, and inside a leak entry.
	writeFile(t, at(home, ".claude/agents/sub/api_token.txt"), "x")
	writeFile(t, at(home, ".opencode/secrets.json"), "x")
	h := app.NewHost(found(at(home, "bin"), "claude", "agy", "opencode"),
		[]string{"CLAUDE_CONFIG_DIR=/x", "XDG_STATE_HOME=/z", "XDG_CONFIG_HOME=/y", "XDG_RUNTIME_DIR=/run", "PATH=/bin"}, "linux")
	before := hashes(t, home)
	p, err := planReplace(t, home, h, "")
	if err != nil {
		t.Fatalf("PlanInstall(replace) error = %v", err)
	}
	type refusal struct{ code, harness, subject string }
	var got []refusal
	for _, r := range p.Refusals {
		got = append(got, refusal{r.Code, r.Harness, r.Variable + r.Path})
		if r.Message == "" {
			t.Errorf("refusal %+v has no message", r)
		}
	}
	want := []refusal{
		{app.RefuseRelocation, "claudecode", "CLAUDE_CONFIG_DIR"},
		{app.RefuseUnclassified, "claudecode", at(home, ".claude/agents/sub/api_token.txt")},
		{app.RefuseUnclassified, "claudecode", at(home, ".claude/oauth-backup.json")},
		{app.RefuseSplitFile, "claudecode", at(home, ".claude.json")},
		{app.RefuseRelocation, "opencode", "XDG_CONFIG_HOME"},
		{app.RefuseRelocation, "opencode", "XDG_STATE_HOME"},
		{app.RefuseUnclassified, "opencode", at(home, ".config/opencode/sessions.db")},
		{app.RefuseUnclassified, "opencode", at(home, ".opencode/secrets.json")},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("refusals =\n%+v\nwant\n%+v", got, want)
	}
	for _, w := range p.Wipes {
		if strings.HasSuffix(w.Path, "oauth-backup.json") || strings.HasSuffix(w.Path, "sessions.db") ||
			w.Path == at(home, ".claude/agents") || w.Path == at(home, ".opencode") {
			t.Errorf("%s is wiped, want it refused unclassified", w.Path)
		}
	}
	if !reflect.DeepEqual(hashes(t, home), before) {
		t.Error("the dry run wrote to the home directory")
	}
}

// TestPlanReplaceOverAnIsolatedInstall checks that a replace planned over
// an isolated install removes what that install owns.
func TestPlanReplaceOverAnIsolatedInstall(t *testing.T) {
	home, h := realHome(t)
	f := quiet(t)
	if _, err := app.InstallHost(context.Background(), newClockFS(t), h, f.fetch, replaceRun(t), examplePack, home, app.InstallOptions{}); err != nil {
		t.Fatal(err)
	}
	p, err := planReplace(t, home, h, "")
	if err != nil {
		t.Fatal(err)
	}
	removed := 0
	for _, op := range p.Ops {
		if strings.HasPrefix(op.Target, at(home, ".dotai/isolated")) {
			if op.Action != "remove" {
				t.Errorf("op %+v in the isolated install, want a remove", op)
			}
			removed++
		}
	}
	if removed == 0 {
		t.Errorf("ops = %+v, want the isolated install removed", p.Ops)
	}
}

// TestInstallInReplaceModeIsNotAvailableYet checks that an install that
// would write in the replace mode, asked for or recorded in the state, is
// refused as a usage error before anything is written, and that install
// never switches a replace install to isolated.
func TestInstallInReplaceModeIsNotAvailableYet(t *testing.T) {
	home, h := realHome(t)
	f := quiet(t)
	before := hashes(t, home)
	_, err := app.InstallHost(context.Background(), newClockFS(t), h, f.fetch, replaceRun(t), examplePack, home, app.InstallOptions{Mode: app.ModeReplace})
	var usage *app.UsageError
	if !errors.As(err, &usage) || !strings.Contains(err.Error(), "not available yet") || !strings.Contains(err.Error(), "--dry-run") {
		t.Errorf("Install(replace) error = %v, want a usage error that it is not available yet", err)
	}
	if !reflect.DeepEqual(hashes(t, home), before) {
		t.Fatal("Install(replace) wrote to the home directory")
	}

	writeState(t, at(home, ".dotai/state.json"), state.State{Schema: state.Schema, Pack: state.Pack{Name: "minimal-pack"}, Mode: app.ModeReplace,
		Harnesses: []string{"claudecode", "antigravity-cli", "opencode"}, Files: []state.File{}})
	before = hashes(t, home)
	if _, err := app.InstallHost(context.Background(), newClockFS(t), h, f.fetch, replaceRun(t), examplePack, home, app.InstallOptions{}); !errors.As(err, &usage) || !strings.Contains(err.Error(), "not available yet") {
		t.Errorf("Install() over a replace state error = %v, want the replace mode not available yet", err)
	}
	p, err := app.PlanInstallHost(context.Background(), app.OSInstallFS{}, h, f.fetch, replaceRun(t), examplePack, home, app.InstallOptions{})
	if err != nil || p.Mode != app.ModeReplace {
		t.Errorf("PlanInstall() over a replace state = %q, %v, want the mode the state records", p.Mode, err)
	}
	for _, write := range []bool{false, true} {
		var err error
		if write {
			_, err = app.InstallHost(context.Background(), newClockFS(t), h, f.fetch, replaceRun(t), examplePack, home, app.InstallOptions{Mode: app.ModeIsolated})
		} else {
			_, err = app.PlanInstallHost(context.Background(), app.OSInstallFS{}, h, f.fetch, replaceRun(t), examplePack, home, app.InstallOptions{Mode: app.ModeIsolated})
		}
		if !errors.As(err, &usage) || !strings.Contains(err.Error(), "dotai restore") {
			t.Errorf("install --mode isolated over a replace state (write %v) error = %v, want a usage error pointing to dotai restore", write, err)
		}
	}
	if _, err := app.PlanInstallHost(context.Background(), app.OSInstallFS{}, h, f.fetch, replaceRun(t), examplePack, home, app.InstallOptions{Mode: "merge"}); !errors.As(err, &usage) {
		t.Errorf("PlanInstall(merge) error = %v, want a usage error", err)
	}
	if !reflect.DeepEqual(hashes(t, home), before) {
		t.Error("a refused install wrote to the home directory")
	}
}

// TestPlanReplaceLeavesWhatDotaiOwns checks a replace planned over an
// earlier replace install: an entry with a file dotai owns below it is
// looked into, so that only what dotai does not own there is wiped, and
// the owned file is planned as the plan plans it, removed here since the
// pack no longer stages it.
func TestPlanReplaceLeavesWhatDotaiOwns(t *testing.T) {
	home, h := realHome(t)
	owned := at(home, ".claude/skills/pack/SKILL.md")
	writeFile(t, owned, "pack\n")
	writeFile(t, at(home, ".claude/skills/pack/notes.md"), "the user's\n")
	writeState(t, at(home, ".dotai/state.json"), state.State{Schema: state.Schema, Pack: state.Pack{Name: "minimal-pack"}, Mode: app.ModeReplace,
		Harnesses: []string{"claudecode", "antigravity-cli", "opencode"}, Files: []state.File{{Target: owned, Harness: "claudecode", SHA256: sum("pack\n")}}})
	f := quiet(t)
	p, err := app.PlanInstallHost(context.Background(), app.OSInstallFS{}, h, f.fetch, replaceRun(t), examplePack, home, app.InstallOptions{})
	if err != nil || p.Mode != app.ModeReplace {
		t.Fatalf("PlanInstall() = %+v, %v, want the replace mode of the state", p, err)
	}
	var wiped []string
	for _, w := range p.Wipes {
		if strings.HasPrefix(w.Path, at(home, ".claude/skills")) {
			wiped = append(wiped, w.Path)
		}
	}
	if want := []string{at(home, ".claude/skills/mine"), at(home, ".claude/skills/pack/notes.md")}; !reflect.DeepEqual(wiped, want) {
		t.Errorf("wipes under ~/.claude/skills = %q, want %q", wiped, want)
	}
	if !slices.ContainsFunc(p.Ops, func(op app.InstallOp) bool { return op.Target == owned && op.Action == "remove" }) {
		t.Errorf("ops = %+v, want the owned skill removed", p.Ops)
	}
}

// TestPlanInstallOfAnInvalidPackHasEmptyLists checks that the plan of a
// pack with problems, which the JSON error carries, has every list empty,
// never null.
func TestPlanInstallOfAnInvalidPackHasEmptyLists(t *testing.T) {
	home, _ := installHome(t)
	dir := copyPack(t)
	editPack(t, dir, "dotai.yaml", "harnesses: [claudecode, antigravity-cli, opencode]", "harnesses: [claudecode, vscode]")
	p, err := quiet(t).planInstall(dir, "", home)
	if err != nil || len(p.Diagnostics) == 0 {
		t.Fatalf("PlanInstall() = %+v, %v, want diagnostics", p, err)
	}
	if p.Wipes == nil || p.Keeps == nil || p.WipeKeys == nil || p.Refusals == nil || p.Notices == nil || p.Harnesses == nil || p.Ops == nil {
		t.Errorf("plan = %+v, want every list empty, not nil", p)
	}
}

// readFailFS is the real file system with Read failing with err for the
// file name.
type readFailFS struct {
	app.OSInstallFS
	name string
	err  error
}

func (r readFailFS) Read(ctx context.Context, name string) ([]byte, plan.Current, error) {
	if name == r.name {
		return nil, plan.Current{}, r.err
	}
	return r.OSInstallFS.Read(ctx, name)
}

// TestPlanReplaceRefusesAnUnreadableSplitFile checks that a ~/.claude.json
// that cannot be read is a refusal of Claude Code, not a failed dry run.
func TestPlanReplaceRefusesAnUnreadableSplitFile(t *testing.T) {
	home, h := realHome(t)
	f := quiet(t)
	d := readFailFS{name: at(home, ".claude.json"), err: fs.ErrPermission}
	p, err := app.PlanInstallHost(context.Background(), d, h, f.fetch, replaceRun(t), examplePack, home, app.InstallOptions{Mode: app.ModeReplace})
	if err != nil {
		t.Fatalf("PlanInstall(replace) error = %v, want a refusal", err)
	}
	if len(p.Refusals) != 1 || p.Refusals[0].Code != app.RefuseSplitFile || p.Refusals[0].Path != d.name || !strings.Contains(p.Refusals[0].Message, "cannot be read") {
		t.Errorf("refusals = %+v, want ~/.claude.json refused as unreadable", p.Refusals)
	}
}

// TestPlanReplaceRefusesALinkedHarnessDirectory checks that a harness
// directory that is a link is a refusal of that harness only: the other
// harnesses are still planned, and nothing is read through the link.
func TestPlanReplaceRefusesALinkedHarnessDirectory(t *testing.T) {
	home, h := realHome(t)
	elsewhere := t.TempDir()
	writeFile(t, filepath.Join(elsewhere, "settings.json"), "{}")
	if err := os.RemoveAll(at(home, ".gemini")); err != nil {
		t.Fatal(err)
	}
	makeLink(t, elsewhere, at(home, ".gemini"))
	p, err := planReplace(t, home, h, "")
	if err != nil {
		t.Fatalf("PlanInstall(replace) error = %v, want a refusal", err)
	}
	var linked []app.Refusal
	for _, r := range p.Refusals {
		if r.Code == app.RefuseLinkedDir {
			linked = append(linked, r)
		}
	}
	if len(linked) != 1 || linked[0].Harness != "antigravity-cli" || linked[0].Path != at(home, ".gemini") {
		t.Errorf("refusals = %+v, want one for agy's linked ~/.gemini", p.Refusals)
	}
	for _, w := range p.Wipes {
		if strings.HasPrefix(w.Path, at(home, ".gemini")) {
			t.Errorf("%s is wiped through the link", w.Path)
		}
	}
	if !slices.ContainsFunc(p.Wipes, func(w app.WipePlan) bool { return w.Path == at(home, ".claude/settings.json") }) ||
		!slices.ContainsFunc(p.Wipes, func(w app.WipePlan) bool { return w.Path == at(home, ".config/opencode/opencode.json") }) {
		t.Errorf("wipes = %+v, want Claude Code and OpenCode still planned", p.Wipes)
	}
}

// TestPlanReplaceFindsOwnedTargetsWithoutCase checks that, where the file
// system ignores case, a target dotai owns is found under a wiped entry
// whatever the case its record has.
func TestPlanReplaceFindsOwnedTargetsWithoutCase(t *testing.T) {
	if runtime.GOOS != "windows" && runtime.GOOS != "darwin" {
		t.Skip("case is folded on Windows and macOS only")
	}
	home, h := realHome(t)
	writeFile(t, at(home, ".claude/skills/pack/SKILL.md"), "pack\n")
	owned := at(home, ".claude/SKILLS/Pack/SKILL.md")
	writeState(t, at(home, ".dotai/state.json"), state.State{Schema: state.Schema, Pack: state.Pack{Name: "minimal-pack"}, Mode: app.ModeReplace,
		Harnesses: []string{"claudecode", "antigravity-cli", "opencode"}, Files: []state.File{{Target: owned, Harness: "claudecode", SHA256: sum("pack\n")}}})
	f := quiet(t)
	p, err := app.PlanInstallHost(context.Background(), app.OSInstallFS{}, h, f.fetch, replaceRun(t), examplePack, home, app.InstallOptions{})
	if err != nil {
		t.Fatal(err)
	}
	for _, w := range p.Wipes {
		if strings.EqualFold(w.Path, at(home, ".claude/skills")) || strings.EqualFold(w.Path, at(home, ".claude/skills/pack")) {
			t.Errorf("%s is wiped, want it looked into, since dotai owns a file below it", w.Path)
		}
	}
	if !slices.ContainsFunc(p.Wipes, func(w app.WipePlan) bool { return w.Path == at(home, ".claude/skills/mine") }) {
		t.Errorf("wipes = %+v, want the user's skill wiped", p.Wipes)
	}
}

// TestPlanInstallOfAnInvalidPackReportsItsMode checks that the plan of a
// pack with problems has the mode asked for, or the one the install state
// records, with its root, and that an unknown mode is a usage error even
// then.
func TestPlanInstallOfAnInvalidPackReportsItsMode(t *testing.T) {
	home, h := realHome(t)
	dir := copyPack(t)
	editPack(t, dir, "dotai.yaml", "harnesses: [claudecode, antigravity-cli, opencode]", "harnesses: [claudecode, vscode]")
	f := quiet(t)
	plan := func(opts app.InstallOptions) (app.InstallPlan, error) {
		return app.PlanInstallHost(context.Background(), app.OSInstallFS{}, h, f.fetch, f.run, dir, home, opts)
	}
	if p, err := plan(app.InstallOptions{Mode: app.ModeReplace}); err != nil || len(p.Diagnostics) == 0 || p.Mode != app.ModeReplace || p.Root != home {
		t.Errorf("PlanInstall(replace) = %q, %q, %v, want the diagnostics in replace mode at the home directory", p.Mode, p.Root, err)
	}
	var usage *app.UsageError
	if _, err := plan(app.InstallOptions{Mode: "bogus"}); !errors.As(err, &usage) {
		t.Errorf("PlanInstall(bogus) error = %v, want a usage error", err)
	}
	writeState(t, at(home, ".dotai/state.json"), state.State{Schema: state.Schema, Pack: state.Pack{Name: "minimal-pack"}, Mode: app.ModeReplace,
		Harnesses: []string{"claudecode"}, Files: []state.File{}})
	if p, err := plan(app.InstallOptions{}); err != nil || p.Mode != app.ModeReplace || p.Root != home {
		t.Errorf("PlanInstall() over a replace state = %q, %q, %v, want the recorded replace mode", p.Mode, p.Root, err)
	}
}
