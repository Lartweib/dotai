package app_test

import (
	"errors"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"testing"

	"github.com/lartweib/dotai/internal/app"
	"github.com/lartweib/dotai/internal/plan"
)

// makeLink is a link at name to dest made by hand, as another tool makes
// one: a symbolic link, or on Windows, where that needs rights a test may
// not have, a junction.
func makeLink(t *testing.T, dest, name string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(name), 0o755); err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS == "windows" {
		if out, err := exec.Command("cmd", "/c", "mklink", "/J", name, dest).CombinedOutput(); err != nil {
			t.Fatalf("cannot create a junction: %v: %s", err, out)
		}
		return
	}
	if err := os.Symlink(dest, name); err != nil {
		t.Fatal(err)
	}
}

// TestLinkInMakesALinkThatLinkReads checks the link the install makes, a
// junction on Windows and a symbolic link elsewhere: Link reads its
// destination without following it, what it points at is reachable
// through it, and RemoveIn removes the link alone.
func TestLinkInMakesALinkThatLinkReads(t *testing.T) {
	d := app.OSInstallFS{}
	root, dest := t.TempDir(), filepath.Join(t.TempDir(), "skill")
	writeFile(t, filepath.Join(dest, "SKILL.md"), "skill\n")
	if err := d.MkdirIn(root, "skills"); err != nil {
		t.Fatal(err)
	}
	if err := d.LinkIn(root, "skills/a", dest); err != nil {
		t.Fatalf("LinkIn() error = %v", err)
	}
	name := filepath.Join(root, "skills", "a")
	info, err := os.Lstat(name)
	if err != nil {
		t.Fatal(err)
	}
	want := fs.ModeSymlink
	if runtime.GOOS == "windows" {
		want = fs.ModeIrregular // a junction
	}
	if info.Mode()&want == 0 {
		t.Errorf("mode = %v, want %v", info.Mode(), want)
	}
	if got, err := d.Link(name); err != nil || got != plan.LinkCurrent(dest) {
		t.Errorf("Link() = %+v, %v, want the link to %s", got, err, dest)
	}
	if data, err := os.ReadFile(filepath.Join(name, "SKILL.md")); err != nil || string(data) != "skill\n" {
		t.Errorf("reading through the link = %q, %v", data, err)
	}
	if err := d.LinkIn(root, "skills/a", dest); err == nil {
		t.Error("LinkIn() over an existing link succeeded, want it refused")
	}
	if _, err := d.Hash(t.Context(), name); !errors.As(err, new(*app.NotRegularError)) {
		t.Errorf("Hash() of a link error = %v, want it not followed", err)
	}
	if err := d.RemoveIn(root, "skills/a"); err != nil {
		t.Fatalf("RemoveIn() error = %v", err)
	}
	if _, err := os.Lstat(name); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("the link is still there: %v", err)
	}
	if data, err := os.ReadFile(filepath.Join(dest, "SKILL.md")); err != nil || string(data) != "skill\n" {
		t.Errorf("what the link pointed at = %q, %v, want it untouched", data, err)
	}
}

// TestLinkReadsALinkMadeByHand checks Link on what another tool may leave:
// a link it made, and what is not a link at all.
func TestLinkReadsALinkMadeByHand(t *testing.T) {
	d := app.OSInstallFS{}
	root, dest := t.TempDir(), t.TempDir()
	makeLink(t, dest, filepath.Join(root, "link"))
	if got, err := d.Link(filepath.Join(root, "link")); err != nil || got != plan.LinkCurrent(dest) {
		t.Errorf("Link() = %+v, %v, want the link to %s", got, err, dest)
	}
	writeFile(t, filepath.Join(root, "file"), "x")
	if err := os.Mkdir(filepath.Join(root, "dir"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"file", "dir"} {
		var nr *app.NotRegularError
		if _, err := d.Link(filepath.Join(root, name)); !errors.As(err, &nr) || !nr.Link {
			t.Errorf("Link() of a %s error = %v, want one that asks for a link", name, err)
		}
	}
	if _, err := d.Link(filepath.Join(root, "missing")); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("Link() of a missing name error = %v, want not exist", err)
	}
}

// TestLinkInStaysInsideTheRoot checks that a link inside a root cannot
// lead the creation of a link outside it.
func TestLinkInStaysInsideTheRoot(t *testing.T) {
	root, outside := t.TempDir(), t.TempDir()
	makeLink(t, outside, filepath.Join(root, "out"))
	if err := (app.OSInstallFS{}).LinkIn(root, "out/a", t.TempDir()); err == nil {
		t.Error("LinkIn() through a link out of the root succeeded")
	}
	if entries, _ := os.ReadDir(outside); len(entries) != 0 {
		t.Errorf("outside = %v, want nothing made", entries)
	}
}

func TestOrderLinks(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "skills")
	at := func(rel string) string { return filepath.Join(dir, filepath.FromSlash(rel)) }
	link := plan.Op{Action: plan.Create, Target: at("a"), Link: "/dest/a"}
	unlink := plan.Op{Action: plan.Remove, Target: at("a"), BeforeLink: "/dest/a"}
	file := func(action plan.Action, rel string) plan.Op { return plan.Op{Action: action, Target: at(rel)} }
	tests := []struct {
		name string
		ops  []plan.Op
		want []int
	}{
		{"nothing below", []plan.Op{link, file(plan.Create, "b/SKILL.md")}, []int{0, 1}},
		{"a link made where files go", []plan.Op{link, file(plan.Remove, "a/SKILL.md"), file(plan.Remove, "a/x/y.md"), file(plan.Create, "b")}, []int{1, 2, 0, 3}},
		{"a link removed where files go", []plan.Op{file(plan.Create, "a/SKILL.md"), file(plan.Create, "a/x.md"), unlink}, []int{2, 0, 1}},
		{"a link removed before them already", []plan.Op{unlink, file(plan.Create, "a/SKILL.md")}, []int{0, 1}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := app.OrderLinks(tt.ops); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("OrderLinks() = %v, want %v", got, tt.want)
			}
		})
	}
}
