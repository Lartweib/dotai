package app_test

import (
	"context"
	"errors"
	"testing"

	"github.com/lartweib/dotai/internal/app"
)

func TestAnInstallPlanNamesTheKindOfEachTarget(t *testing.T) {
	home, _ := installHome(t)
	f := &fakeRulesync{t: t}
	p, err := f.planInstall(examplePack, "", home)
	if err != nil {
		t.Fatal(err)
	}
	kinds := map[string]string{}
	for _, op := range p.Ops {
		kinds[op.Harness+" "+op.Source] = op.Kind
	}
	if kinds["claudecode .claude/CLAUDE.md"] != "rules" || kinds[" .dotai/tools/hello.sh"] != "tools" {
		t.Errorf("kinds = %v, want CLAUDE.md a rule and hello.sh a tool", kinds)
	}
	if p.Consent || p.Blocked {
		t.Errorf("a fresh isolated plan: consent %v, blocked %v, want neither", p.Consent, p.Blocked)
	}
}

func TestAReplacePlanSaysItNeedsConsentOrIsRefused(t *testing.T) {
	home, _ := installHome(t)
	writeFile(t, at(home, ".claude/agents/mine.md"), "mine\n")
	h := app.NewHost(found(at(home, "bin"), "claude", "agy", "opencode"), nil, "")
	p, err := app.PlanInstallHost(context.Background(), app.OSInstallFS{}, h, quiet(t).fetch, replaceRun(t), examplePack, home, app.InstallOptions{Mode: app.ModeReplace})
	if err != nil {
		t.Fatal(err)
	}
	if !p.Consent || p.Blocked {
		t.Errorf("replace over a file of the user's: consent %v, blocked %v, want consent only", p.Consent, p.Blocked)
	}
	relocated := app.NewHost(found(at(home, "bin"), "claude", "agy", "opencode"), []string{"CLAUDE_CONFIG_DIR=" + at(home, "elsewhere")}, "")
	p, err = app.PlanInstallHost(context.Background(), app.OSInstallFS{}, relocated, quiet(t).fetch, replaceRun(t), examplePack, home, app.InstallOptions{Mode: app.ModeReplace})
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Refusals) == 0 || !p.Blocked {
		t.Errorf("replace with a relocation variable: refusals %v, blocked %v, want it blocked", p.Refusals, p.Blocked)
	}
}

func TestAProjectPlanSaysItNeedsConsentForAConflict(t *testing.T) {
	project, home, g := applyRepo(t)
	writeFile(t, at(project, ".claude/rules/web.md"), "# the user's\n")
	_, err := applyWith(t, newClockFS(t), g, profileTree, project, home, nil)
	ce, ok := errors.AsType[*app.ApplyConsentError](err)
	if !ok || !ce.Plan.Consent || ce.Plan.Blocked {
		t.Fatalf("Apply() = %v, want a plan that needs consent and is not blocked", err)
	}
}

func TestTheStatusNamesTheHomeAndItsBackups(t *testing.T) {
	home, _ := installHome(t)
	st, err := app.ReadStatus(home)
	if err != nil || st.Home != home || st.BackupsPath != at(home, ".dotai/backups") {
		t.Errorf("status = %+v, %v, want the home and its backups directory", st, err)
	}
}

func TestHarnessesNamesEveryHarness(t *testing.T) {
	hs := app.Harnesses()
	if len(hs) != 3 || hs[0].ID != "claudecode" || hs[0].Name != "Claude Code" {
		t.Errorf("Harnesses() = %+v, want the three harnesses with their names", hs)
	}
}
