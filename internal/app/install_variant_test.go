package app_test

import (
	"testing"
	"time"

	"github.com/lartweib/dotai/internal/app"
	"github.com/lartweib/dotai/internal/pack"
	"github.com/lartweib/dotai/internal/state"
)

// recordInstall saves in home the install state of the example pack, or of
// another pack named name, installed with the variant variant.
func recordInstall(t *testing.T, home, name, variant string) {
	t.Helper()
	s := state.New(state.Pack{Name: name, Version: "0.1.0"}, variant, app.ModeIsolated, time.Date(2026, 10, 29, 10, 0, 0, 0, time.UTC), pack.Harnesses, "", nil)
	data, err := state.Encode(s)
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, at(home, ".dotai/state.json"), string(data))
}

func variantWarnings(p app.InstallPlan) (n int) {
	for _, w := range p.Warnings {
		if w.Code == app.CodeRecordedVariantMissing {
			n++
		}
	}
	return n
}

// TestInstallKeepsTheRecordedVariant checks that, without --variant, a
// plan uses the variant the install state records for the pack, not the
// pack's default, as it does with the mode.
func TestInstallKeepsTheRecordedVariant(t *testing.T) {
	home, _ := installHome(t)
	recordInstall(t, home, "minimal-pack", "tmux")
	p, err := quiet(t).planInstall(examplePack, "", home)
	if err != nil || len(p.Diagnostics) != 0 {
		t.Fatalf("PlanInstall() = %+v, %v", p, err)
	}
	if p.Variant != "tmux" || variantWarnings(p) != 0 {
		t.Errorf("variant = %q, warnings = %+v, want the recorded tmux and no warning", p.Variant, p.Warnings)
	}
}

func TestAnExplicitVariantOverridesTheRecordedOne(t *testing.T) {
	home, _ := installHome(t)
	recordInstall(t, home, "minimal-pack", "tmux")
	p, err := quiet(t).planInstall(examplePack, "shared", home)
	if err != nil || len(p.Diagnostics) != 0 {
		t.Fatalf("PlanInstall() = %+v, %v", p, err)
	}
	if p.Variant != "shared" || variantWarnings(p) != 0 {
		t.Errorf("variant = %q, warnings = %+v, want the one asked for and no warning", p.Variant, p.Warnings)
	}
}

// TestARecordedVariantThePackDroppedFallsBackAndWarns checks that a
// variant the pack no longer declares does not stop the install: it warns
// and takes the default.
func TestARecordedVariantThePackDroppedFallsBackAndWarns(t *testing.T) {
	home, _ := installHome(t)
	recordInstall(t, home, "minimal-pack", "orca")
	p, err := quiet(t).planInstall(examplePack, "", home)
	if err != nil || len(p.Diagnostics) != 0 {
		t.Fatalf("PlanInstall() = %+v, %v", p, err)
	}
	if p.Variant != "standalone" || variantWarnings(p) != 1 {
		t.Errorf("variant = %q, warnings = %+v, want the default and one %s warning", p.Variant, p.Warnings, app.CodeRecordedVariantMissing)
	}
}

func TestTheFirstInstallTakesTheDefaultVariant(t *testing.T) {
	home, _ := installHome(t)
	p, err := quiet(t).planInstall(examplePack, "", home)
	if err != nil || len(p.Diagnostics) != 0 {
		t.Fatalf("PlanInstall() = %+v, %v", p, err)
	}
	if p.Variant != "standalone" || variantWarnings(p) != 0 {
		t.Errorf("variant = %q, warnings = %+v, want the default and no warning", p.Variant, p.Warnings)
	}
}

// TestAnotherPacksRecordedVariantIsNotUsed checks that the record of a
// pack with another name decides nothing.
func TestAnotherPacksRecordedVariantIsNotUsed(t *testing.T) {
	home, _ := installHome(t)
	recordInstall(t, home, "other-pack", "tmux")
	p, err := quiet(t).planInstall(examplePack, "", home)
	if err != nil || len(p.Diagnostics) != 0 {
		t.Fatalf("PlanInstall() = %+v, %v", p, err)
	}
	if p.Variant != "standalone" || variantWarnings(p) != 0 {
		t.Errorf("variant = %q, warnings = %+v, want the default and no warning", p.Variant, p.Warnings)
	}
}
