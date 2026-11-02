package merge_test

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/lartweib/dotai/internal/merge"
	"github.com/lartweib/dotai/internal/pack"
)

// TestToolCommandsNeedBothScripts checks the tool command variable of
// decision 0037: a setting that runs a tool needs its shell script and its
// PowerShell script in the tools of its scope, a name that is not a tool
// name is refused, and each problem is reported once, by Check over every
// variant and by Stage.
func TestToolCommandsNeedBothScripts(t *testing.T) {
	statusLine := "antigravity-cli:\n  statusLine: {type: command, command: '${DOTAI_TOOL:hello} --short'}\n"
	both := []src{plain("dotai/tools/hello.sh"), plain("dotai/tools/hello.ps1")}
	tests := []struct {
		name string
		pack *pack.Pack
		want []string
	}{
		{"both scripts", newPack(base(append(both, settings(statusLine))...)), nil},
		{"both scripts from another layer", newPack(base(both...), variant("v", settings(statusLine))), nil},
		{
			"the PowerShell script missing, with two variants",
			newPack(base(settings(statusLine), plain("dotai/tools/hello.sh")), variant("a"), variant("b")),
			[]string{"base/dotai/settings.yaml:2:40: tool-missing: setting antigravity-cli.statusLine runs tool hello, but no layer of its scope provides dotai/tools/hello.ps1"},
		},
		{
			"both missing",
			newPack(base(settings(statusLine))),
			[]string{
				"base/dotai/settings.yaml:2:40: tool-missing: setting antigravity-cli.statusLine runs tool hello, but no layer of its scope provides dotai/tools/hello.sh",
				"base/dotai/settings.yaml:2:40: tool-missing: setting antigravity-cli.statusLine runs tool hello, but no layer of its scope provides dotai/tools/hello.ps1",
			},
		},
		{
			"the scripts in the other scope",
			newPack(base(both...), profile("p", settings("claudecode:\n  statusLine: '${DOTAI_TOOL:hello}'\n"))),
			[]string{
				"profiles/p/dotai/settings.yaml:2:15: tool-missing: setting claudecode.statusLine runs tool hello, but no layer of its scope provides dotai/tools/hello.sh",
				"profiles/p/dotai/settings.yaml:2:15: tool-missing: setting claudecode.statusLine runs tool hello, but no layer of its scope provides dotai/tools/hello.ps1",
			},
		},
		{
			"not a tool name",
			newPack(base(settings("claudecode:\n  statusLine: '${DOTAI_TOOL:hello.sh/x}'\n"))),
			[]string{"base/dotai/settings.yaml:2:15: tool-invalid: setting claudecode.statusLine uses ${DOTAI_TOOL:hello.sh/x}, which does not name a tool"},
		},
		{
			"no closing brace, after a complete one",
			newPack(base(append(both, settings("claudecode:\n  statusLine: '${DOTAI_TOOL:hello} ${DOTAI_TOOL:foo --short'\n"))...)),
			[]string{"base/dotai/settings.yaml:2:15: tool-invalid: setting claudecode.statusLine uses ${DOTAI_TOOL:foo --short, which has no closing brace"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := withoutHints(merge.Check(tt.pack, merge.Selection{})); !reflect.DeepEqual(got, append([]string{}, tt.want...)) {
				t.Errorf("Check() = %q, want %q", got, tt.want)
			}
			sel := merge.Selection{}
			if len(tt.pack.Manifest.Profiles) > 0 {
				sel.Profiles = []string{tt.pack.Manifest.Profiles[0].Name}
			}
			if _, diags := merge.Stage(tt.pack, sel); !reflect.DeepEqual(withoutHints(diags), append([]string{}, tt.want...)) {
				t.Errorf("Stage() = %q, want %q", format(diags), tt.want)
			}
		})
	}
}

// withoutHints is format of ds with no hint, which tests check apart.
func withoutHints(ds []pack.Diagnostic) []string {
	for i := range ds {
		if ds[i].Code == merge.CodeToolMissing && !strings.Contains(ds[i].Hint, "ship both dotai/tools/") {
			ds[i].Message += " (unexpected hint)"
		}
		ds[i].Hint = ""
	}
	return format(ds)
}

func TestExpandTools(t *testing.T) {
	got, err := merge.ExpandTools("a ${DOTAI_TOOL:x} b ${DOTAI_TOOL:y.v2} ${DOTAI_TOOLS}/z", func(name string) (string, error) { return "<" + name + ">", nil })
	if want := "a <x> b <y.v2> ${DOTAI_TOOLS}/z"; err != nil || got != want {
		t.Errorf("ExpandTools() = %q, %v, want %q", got, err, want)
	}
	var e *merge.ToolNameError
	if _, err := merge.ExpandTools("${DOTAI_TOOL:}", nil); !errors.As(err, &e) {
		t.Errorf("ExpandTools() of an empty name error = %v, want a ToolNameError", err)
	}
	called := false
	if _, err := merge.ExpandTools("${DOTAI_TOOL:x} ${DOTAI_TOOL:foo", func(string) (string, error) { called = true; return "", nil }); !errors.As(err, &e) || !e.Unclosed || e.Name != "foo" || called {
		t.Errorf("ExpandTools() of an unclosed variable error = %v, called %t, want an unclosed ToolNameError before any command", err, called)
	}
	boom := errors.New("boom")
	if _, err := merge.ExpandTools("${DOTAI_TOOL:x}", func(string) (string, error) { return "", boom }); !errors.Is(err, boom) {
		t.Errorf("ExpandTools() error = %v, want the command's", err)
	}
}

// TestHookToolCommandsNeedBothScripts checks the tool command variable in
// the command of a hook, shared or in a tool-scoped block: like a setting,
// a hook that runs a tool needs both its scripts in the tools of its
// scope, and a name that is not a tool name is refused.
func TestHookToolCommandsNeedBothScripts(t *testing.T) {
	both := []src{plain("dotai/tools/hello.sh"), plain("dotai/tools/hello.ps1")}
	shared := hooks("{\n  \"hooks\": {\n    \"sessionStart\": [{\"command\": \"${DOTAI_TOOL:hello} start\"}]\n  }\n}\n")
	tests := []struct {
		name string
		pack *pack.Pack
		want []string
	}{
		{"both scripts", newPack(base(append(both, shared)...)), nil},
		{"a hook without the variable", newPack(base(hooks(`{"hooks": {"stop": [{"command": "${DOTAI_TOOLS}/x.sh"}]}}`))), nil},
		{
			"the PowerShell script missing",
			newPack(base(shared, plain("dotai/tools/hello.sh"))),
			[]string{"base/.rulesync/hooks.jsonc:3: tool-missing: hook hooks.sessionStart runs tool hello, but no layer of its scope provides dotai/tools/hello.ps1"},
		},
		{
			"in a tool-scoped block of a profile",
			newPack(base(both...), profile("p", hooks(`{"antigravity-cli": {"hooks": {"stop": [{"matcher": "", "command": "${DOTAI_TOOL:hello}"}]}}}`))),
			[]string{
				"profiles/p/.rulesync/hooks.jsonc:1: tool-missing: hook antigravity-cli.hooks.stop runs tool hello, but no layer of its scope provides dotai/tools/hello.sh",
				"profiles/p/.rulesync/hooks.jsonc:1: tool-missing: hook antigravity-cli.hooks.stop runs tool hello, but no layer of its scope provides dotai/tools/hello.ps1",
			},
		},
		{
			"not a tool name",
			newPack(base(hooks(`{"hooks": {"stop": [{"command": "${DOTAI_TOOL:a b}"}]}}`))),
			[]string{"base/.rulesync/hooks.jsonc:1: tool-invalid: hook hooks.stop uses ${DOTAI_TOOL:a b}, which does not name a tool"},
		},
		{
			"no closing brace",
			newPack(base(hooks(`{"hooks": {"stop": [{"command": "${DOTAI_TOOL:hello"}]}}`))),
			[]string{"base/.rulesync/hooks.jsonc:1: tool-invalid: hook hooks.stop uses ${DOTAI_TOOL:hello, which has no closing brace"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := withoutHints(merge.Check(tt.pack, merge.Selection{})); !reflect.DeepEqual(got, append([]string{}, tt.want...)) {
				t.Errorf("Check() = %q, want %q", got, tt.want)
			}
		})
	}
}

// TestExpandHookTools checks that the tool command variables of every hook
// command of a merged hooks file are expanded for the hook's block, shared
// or tool-scoped, that nothing else changes, and that an error stops the
// expansion.
func TestExpandHookTools(t *testing.T) {
	s, diags := merge.Stage(newPack(base(hooks(`{"version": 1, "hooks": {"stop": [{"command": "${DOTAI_TOOL:a} && x", "matcher": "${DOTAI_TOOL:a}"}, {"command": "plain"}]},`+
		` "claudecode": {"hooks": {"pre/tool": [{"command": "${DOTAI_TOOL:a}"}]}}, "other": {"command": "${DOTAI_TOOL:a}"}}`),
		plain("dotai/tools/a.sh"), plain("dotai/tools/a.ps1"))), merge.Selection{})
	if len(diags) > 0 {
		t.Fatal(format(diags))
	}
	file := staged(t, s.User, merge.HooksFile)
	var calls []string
	got, err := merge.ExpandHookTools(file.Data, func(h merge.Hook, name string) (string, error) {
		calls = append(calls, h.Block+" "+h.Event+" "+h.Pointer()+" "+h.String())
		return "<" + h.Block + ":" + name + ">", nil
	})
	if err != nil {
		t.Fatal(err)
	}
	// The first plain variable is claudecode's: "other" is no block.
	want := strings.Replace(string(file.Data), `"command": "${DOTAI_TOOL:a} && x"`, `"command": "<:a> && x"`, 1)
	want = strings.Replace(want, `"command": "${DOTAI_TOOL:a}"`, `"command": "<claudecode:a>"`, 1)
	if string(got) != want {
		t.Errorf("ExpandHookTools() =\n%s\nwant\n%s", got, want)
	}
	if wantCalls := []string{" stop /hooks/stop/0 hook hooks.stop", "claudecode pre/tool /claudecode/hooks/pre~1tool/0 hook claudecode.hooks.pre/tool"}; !reflect.DeepEqual(calls, wantCalls) {
		t.Errorf("calls = %q, want %q", calls, wantCalls)
	}
	boom := errors.New("boom")
	if _, err := merge.ExpandHookTools(file.Data, func(merge.Hook, string) (string, error) { return "", boom }); !errors.Is(err, boom) {
		t.Errorf("ExpandHookTools() error = %v, want the command's", err)
	}
}
