package app_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/lartweib/dotai/internal/app"
)

// sibling is the path of a temporary sibling of the clone of
// github.com/acme/pack, as a swap names them.
func sibling(home, kind string) string {
	return filepath.Join(home, ".dotai", "packs", "github.com", "acme", ".pack."+kind+"-123")
}

func TestACrashBetweenMovingAsideAndRenamingIsRecovered(t *testing.T) {
	gitHome(t)
	home := t.TempDir()
	addedRemote(t, home, "https://github.com/acme/pack", "v1")
	clone := clonePath(home, "github.com/acme/pack")
	// The clone moved aside, the new one never renamed into place.
	if err := os.Rename(clone, sibling(home, "old")); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(sibling(home, "fetch"), 0o755); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-2 * time.Hour)
	if err := os.Chtimes(sibling(home, "fetch"), old, old); err != nil {
		t.Fatal(err)
	}
	list, err := app.ListPacks(home)
	if err != nil {
		t.Fatalf("ListPacks() error = %v", err)
	}
	if !slices.Contains(list.Recovered, clone) {
		t.Errorf("recovered = %v, want %s", list.Recovered, clone)
	}
	if _, err := os.Stat(filepath.Join(clone, "dotai.yaml")); err != nil {
		t.Errorf("the clone was not moved back: %v", err)
	}
	if got := packLeftovers(t, home, "acme"); len(got) != 0 {
		t.Errorf("the sweep left %v", got)
	}
}

func TestACrashAfterTheRenameBeforeTheRegistryPutsTheRegisteredCloneBack(t *testing.T) {
	gitHome(t)
	home := t.TempDir()
	addedRemote(t, home, "https://github.com/acme/pack", "v1")
	clone := clonePath(home, "github.com/acme/pack")
	if err := os.Rename(clone, sibling(home, "old")); err != nil {
		t.Fatal(err)
	}
	// A new clone at another commit, which the registry does not name.
	writeTree(t, clone, map[string]string{".git/HEAD": strings.Repeat("b", 40) + "\n", "dotai.yaml": "name: other\n"})
	res, err := app.RemovePackID(home, "github.com/acme/none")
	if !errors.As(err, new(*app.UsageError)) {
		t.Fatalf("RemovePackID() error = %v, want a usage error after the sweep", err)
	}
	_ = res
	if data, err := os.ReadFile(filepath.Join(clone, "dotai.yaml")); err != nil || strings.Contains(string(data), "other") {
		t.Errorf("the clone the registry names was not put back: %q, %v", data, err)
	}
	if got := packLeftovers(t, home, "acme"); len(got) != 0 {
		t.Errorf("the sweep left %v", got)
	}
}

func TestTheSweepKeepsAFreshFetchAndDropsWhatNoEntryNeeds(t *testing.T) {
	gitHome(t)
	home := t.TempDir()
	addedRemote(t, home, "https://github.com/acme/pack", "v1")
	for _, kind := range []string{"fetch", "remove", "old"} {
		if err := os.MkdirAll(sibling(home, kind), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	// An owner with nothing left in it, as a failed addition leaves it.
	empty := filepath.Join(home, ".dotai", "packs", "github.com", "gone")
	if err := os.MkdirAll(empty, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := app.UpdatePack(context.Background(), home, "github.com/acme/pack", app.PackGitOptions{}); err != nil {
		t.Fatalf("UpdatePack() error = %v", err)
	}
	if got := packLeftovers(t, home, "acme"); !slices.Equal(got, []string{".pack.fetch-123"}) {
		t.Errorf("leftovers = %v, want the fresh fetch alone, which may be another fetch running", got)
	}
	if _, err := os.Stat(empty); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("the empty owner directory stays: %v", err)
	}
	if _, err := os.Stat(filepath.Join(clonePath(home, "github.com/acme/pack"), "dotai.yaml")); err != nil {
		t.Errorf("the clone was touched: %v", err)
	}
}
