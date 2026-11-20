package app_test

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/lartweib/dotai/internal/app"
)

func TestUnifiedDiff(t *testing.T) {
	for name, tt := range map[string]struct{ old, new, want string }{
		"a changed line": {
			"a\nb\nc\n", "a\nB\nc\n",
			"--- a/f\n+++ b/f\n@@ -1,3 +1,3 @@\n a\n-b\n+B\n c\n",
		},
		"an added file": {
			"", "x\ny\n",
			"--- /dev/null\n+++ b/f\n@@ -0,0 +1,2 @@\n+x\n+y\n",
		},
		"a removed file": {
			"x\n", "",
			"--- a/f\n+++ /dev/null\n@@ -1 +0,0 @@\n-x\n",
		},
		"no newline at the end": {
			"a\n", "a\nb",
			"--- a/f\n+++ b/f\n@@ -1 +1,2 @@\n a\n+b\n\\ No newline at end of file\n",
		},
		"two hunks with context": {
			"1\n2\n3\n4\n5\n6\n7\n8\n9\n10\n11\n12\n", "x\n2\n3\n4\n5\n6\n7\n8\n9\n10\n11\ny\n",
			"--- a/f\n+++ b/f\n@@ -1,4 +1,4 @@\n-1\n+x\n 2\n 3\n 4\n@@ -9,4 +9,4 @@\n 9\n 10\n 11\n-12\n+y\n",
		},
	} {
		oldFile, newFile := "a/f", "b/f"
		if tt.old == "" {
			oldFile = ""
		}
		if tt.new == "" {
			newFile = ""
		}
		if got := app.UnifiedDiff(oldFile, newFile, tt.old, tt.new); got != tt.want {
			t.Errorf("%s: UnifiedDiff() =\n%s\nwant\n%s", name, got, tt.want)
		}
	}
}

func TestUnifiedDiffOfLargeInputsIsStillComplete(t *testing.T) {
	var a, b strings.Builder
	for i := range 3000 {
		a.WriteString("old " + string(rune('a'+i%26)) + "\n")
		b.WriteString("new " + string(rune('a'+i%26)) + "\n")
	}
	got := app.UnifiedDiff("a/f", "b/f", a.String(), b.String())
	if strings.Count(got, "\n-old ") != 3000 || strings.Count(got, "\n+new ") != 3000 {
		t.Errorf("the diff of 3000 changed lines does not show them all")
	}
}

// writeTree writes files, slash paths to content, under dir.
func writeTree(t *testing.T, dir string, files map[string]string) {
	t.Helper()
	for name, content := range files {
		p := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func TestExecChangesListsOnlyExecutableContent(t *testing.T) {
	old, new := t.TempDir(), t.TempDir()
	same := map[string]string{"dotai.yaml": "name: x\n", "base/.rulesync/rules/r.md": "rule\n", "base/.rulesync/hooks.jsonc": "{}\n"}
	writeTree(t, old, same)
	writeTree(t, new, same)
	writeTree(t, old, map[string]string{
		"base/.rulesync/mcp.jsonc":          "{\"a\":1}\n",
		"base/.rulesync/rules/r.md":         "rule\n",
		"profiles/web/dotai/settings.yaml":  "claudecode: {}\n",
		"base/dotai/tools/gone.sh":          "echo gone\n",
		"mods/hello/hooks/register.js":      "old()\n",
		".git/hooks/pre-commit":             "x\n",
		"base/.rulesync/skills/s/SKILL.md":  "skill\n",
		"variants/tmux/.rulesync/hooks.txt": "not a hooks file\n",
	})
	writeTree(t, new, map[string]string{
		"base/.rulesync/mcp.jsonc":          "{\"a\":2}\n",
		"base/.rulesync/rules/r.md":         "changed rule\n",
		"profiles/web/dotai/settings.yaml":  "claudecode: {}\n",
		"variants/tmux/dotai/tools/new.ps1": "Write-Host new\n",
		"mods/hello/hooks/register.js":      "new()\n",
		".git/hooks/pre-commit":             "y\n",
		"base/.rulesync/skills/s/SKILL.md":  "skill changed\n",
		"variants/tmux/.rulesync/hooks.txt": "changed\n",
	})
	changes, err := app.ExecChanges(old, new, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, c := range changes {
		got = append(got, c.Change+" "+c.Path)
		if c.Diff == "" {
			t.Errorf("%s has no diff", c.Path)
		}
	}
	want := []string{
		"modified base/.rulesync/mcp.jsonc",
		"removed base/dotai/tools/gone.sh",
		"modified mods/hello/hooks/register.js",
		"added variants/tmux/dotai/tools/new.ps1",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("changes =\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

func TestExecChangesOfABinaryFileHasNoTextDiff(t *testing.T) {
	old, new := t.TempDir(), t.TempDir()
	writeTree(t, old, map[string]string{"base/dotai/tools/bin": "a\x00b"})
	writeTree(t, new, map[string]string{"base/dotai/tools/bin": "a\x00c"})
	changes, err := app.ExecChanges(old, new, nil, nil)
	if err != nil || len(changes) != 1 || changes[0].Diff != "" || !strings.Contains(changes[0].Note, "binary") ||
		changes[0].Before == changes[0].After || !strings.HasPrefix(changes[0].After, "sha256:") {
		t.Errorf("changes = %+v, %v, want a binary change with its hashes", changes, err)
	}
}

func TestExecChangesCoverPermissionsScriptsTheManifestAndAnyCase(t *testing.T) {
	old, new := t.TempDir(), t.TempDir()
	files := []string{
		"base/.rulesync/permissions.jsonc",
		"base/.rulesync/skills/s/run.py",
		"base/.rulesync/commands/c/helper.js",
		"base/.rulesync/subagents/a/tool.sh",
		"dotai.yaml",
		"Profiles/Web/.Rulesync/Hooks.jsonc",
		"MODS/x/index.js",
		"Variants/X/Dotai/Tools/t.ps1",
	}
	for _, f := range files {
		writeTree(t, old, map[string]string{f: "old\n"})
		writeTree(t, new, map[string]string{f: "new\n"})
	}
	writeTree(t, old, map[string]string{"base/.rulesync/skills/s/SKILL.md": "a\n", "base/.rulesync/rules/r.txt": "a\n"})
	writeTree(t, new, map[string]string{"base/.rulesync/skills/s/SKILL.md": "b\n", "base/.rulesync/rules/r.txt": "b\n"})
	changes, err := app.ExecChanges(old, new, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{}
	for _, c := range changes {
		got[c.Path] = true
	}
	for _, f := range files {
		if !got[f] {
			t.Errorf("%s is not shown as executable content", f)
		}
	}
	for _, f := range []string{"base/.rulesync/skills/s/SKILL.md", "base/.rulesync/rules/r.txt"} {
		if got[f] {
			t.Errorf("%s is shown as executable content", f)
		}
	}
}

func TestExecChangesShowAnExecutableBitAndAModeChange(t *testing.T) {
	old, new := t.TempDir(), t.TempDir()
	same := map[string]string{"base/.rulesync/rules/run": "#!/bin/sh\n", "base/.rulesync/rules/new": "#!/bin/sh\n"}
	writeTree(t, old, same)
	writeTree(t, new, same)
	oldModes := map[string]string{"base/.rulesync/rules/run": "100644", "base/.rulesync/rules/new": "100644"}
	newModes := map[string]string{"base/.rulesync/rules/run": "100755", "base/.rulesync/rules/new": "100644"}
	changes, err := app.ExecChanges(old, new, oldModes, newModes)
	if err != nil {
		t.Fatal(err)
	}
	if len(changes) != 1 || changes[0].Path != "base/.rulesync/rules/run" || changes[0].Change != app.ChangeModified ||
		changes[0].ModeBefore != "100644" || changes[0].ModeAfter != "100755" || !strings.Contains(changes[0].Note, "100755") {
		t.Errorf("changes = %+v, want the mode change of the same bytes", changes)
	}
}

func TestExecChangesShowControlCharactersAsBinary(t *testing.T) {
	for name, content := range map[string]string{
		"an erase of the line":  "ok\n\x1b[2K\x1b[1A" + "hidden()\n",
		"a carriage return":     "visible\rhidden()\n",
		"concealed text":        "\x1b[8mhidden()\x1b[0m\n",
		"a C1 control":          "a\u009bb\n",
		"a bidi override":       "a\u202eb\n",
		"a zero-width space":    "a\u200bb\n",
		"a right-to-left mark":  "a\u200fb\n",
		"a line separator":      "a\u2028b\n",
		"a paragraph separator": "a\u2029b\n",
		"a word joiner":         "a\u2060b\n",
		"an Arabic letter mark": "a\u061cb\n",
		"a byte order mark":     "a\ufeffb\n",
	} {
		old, new := t.TempDir(), t.TempDir()
		writeTree(t, old, map[string]string{"base/.rulesync/hooks.jsonc": "{}\n"})
		writeTree(t, new, map[string]string{"base/.rulesync/hooks.jsonc": content})
		changes, err := app.ExecChanges(old, new, nil, nil)
		if err != nil || len(changes) != 1 || !changes[0].Binary || changes[0].Diff != "" || changes[0].After == "" {
			t.Errorf("%s: changes = %+v, %v, want a binary change with its hash and no diff", name, changes, err)
		}
	}
	old, new := t.TempDir(), t.TempDir()
	writeTree(t, new, map[string]string{"base/dotai/tools/crlf.ps1": "Write-Host a\r\n\tindented\r\n"})
	if changes, err := app.ExecChanges(old, new, nil, nil); err != nil || len(changes) != 1 || changes[0].Binary || changes[0].Diff == "" {
		t.Errorf("a CRLF file = %+v, %v, want a text diff", changes, err)
	}
}

func TestExecChangesKeepTheDiffOfAFileThatStartsWithAByteOrderMark(t *testing.T) {
	old, new := t.TempDir(), t.TempDir()
	writeTree(t, new, map[string]string{"base/dotai/tools/bom.ps1": "\ufeffWrite-Host a\r\n"})
	if changes, err := app.ExecChanges(old, new, nil, nil); err != nil || len(changes) != 1 || changes[0].Binary || changes[0].Diff == "" {
		t.Errorf("a file with a leading BOM = %+v, %v, want a text diff", changes, err)
	}
	writeTree(t, new, map[string]string{"base/dotai/tools/bom.ps1": "\ufeffa\ufeffb\n"})
	if changes, err := app.ExecChanges(old, new, nil, nil); err != nil || len(changes) != 1 || !changes[0].Binary || changes[0].Diff != "" {
		t.Errorf("a BOM past the start = %+v, %v, want a binary change", changes, err)
	}
	if !app.UnsafePath("\ufeffbase/x.sh") {
		t.Error("a path that starts with a BOM is safe")
	}
}

// TestExecChangesOfAnUnsafePathHasNoDiff checks that a path with a control
// character or a line feed gets no diff, which would print the path raw or
// forge the diff's headers.
func TestExecChangesOfAnUnsafePathHasNoDiff(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows refuses control characters and line feeds in file names")
	}
	for _, tt := range []struct{ name, path, want string }{
		{"a control character", "base/dotai/tools/a\x1b[2Kb.sh", "no diff that would print the path raw"},
		{"a newline", "base/.rulesync/mcp.jsonc\n+++ b/x/dotai/tools/t.sh", "no diff that would forge its headers"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			old, new := t.TempDir(), t.TempDir()
			writeTree(t, new, map[string]string{tt.path: "echo\n"})
			changes, err := app.ExecChanges(old, new, nil, nil)
			if err != nil || len(changes) != 1 || !changes[0].Binary || changes[0].Diff != "" {
				t.Errorf("changes = %+v, %v, want %s", changes, err, tt.want)
			}
		})
	}
}

func TestAPathIsSafeOnlyWithoutAnyControlCharacter(t *testing.T) {
	for _, p := range []string{"base/.rulesync/mcp.jsonc\n+++ b/x", "a\rb", "a\tb", "a\x1bb", "a\u202eb", "a\u0085b",
		"a\u200bb", "a\u200eb", "a\u2028b", "a\u2029b", "a\u2060b", "a\u061cb", "a\ufeffb"} {
		if !app.UnsafePath(p) {
			t.Errorf("UnsafePath(%q) = false, want true", p)
		}
	}
	if app.UnsafePath("base/dotai/tools/a b.sh") {
		t.Error("a plain path is unsafe")
	}
}
