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
	boom := errors.New("boom")
	if _, err := merge.ExpandTools("${DOTAI_TOOL:x}", func(string) (string, error) { return "", boom }); !errors.Is(err, boom) {
		t.Errorf("ExpandTools() error = %v, want the command's", err)
	}
}
