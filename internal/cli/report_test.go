package cli

import (
	"strings"
	"testing"

	"github.com/lartweib/dotai/internal/app"
)

func TestWriteReport(t *testing.T) {
	dup := app.Diagnostic{File: "base/b.md", Code: "name-duplicate", Message: "rule \"b\" is defined twice"}
	dup.Related = &app.Location{File: "base/a.md"}
	undeclared := app.Diagnostic{File: "variants/v/deploy.md", Code: "override-undeclared", Message: "command \"deploy\" already exists in base", Hint: "add dotai: {overrides: true} to replace it"}
	undeclared.Related = &app.Location{File: "base/deploy.md"}
	version := app.Diagnostic{File: "dotai.yaml", Line: 3, Column: 10, Code: "version-invalid", Message: "bad version"}
	harness := app.Diagnostic{File: "dotai.yaml", Line: 12, Column: 3, Code: "harness-unknown", Message: "unknown harness"}

	tests := []struct {
		name    string
		subject string
		diags   []app.Diagnostic
		compact bool
		want    string
	}{
		{
			name:    "one problem",
			subject: "pack 1.0.0",
			diags:   []app.Diagnostic{dup},
			want: "✗ pack 1.0.0 · 1 problem in 1 file\n\n" +
				"base/b.md\n" +
				"  name-duplicate  rule \"b\" is defined twice\n" +
				"                  first: base/a.md\n",
		},
		{
			name:  "grouped by file in first-seen order, positions only in files that have them",
			diags: []app.Diagnostic{version, undeclared, harness},
			want: "✗ 3 problems in 2 files\n\n" +
				"dotai.yaml\n" +
				"  3:10  version-invalid      bad version\n" +
				"  12:3  harness-unknown      unknown harness\n\n" +
				"variants/v/deploy.md\n" +
				"  override-undeclared  command \"deploy\" already exists in base\n" +
				"                       first: base/deploy.md\n" +
				"                       fix:   add dotai: {overrides: true} to replace it\n",
		},
		{
			name:    "compact",
			diags:   []app.Diagnostic{version, undeclared},
			compact: true,
			want: "dotai.yaml:3:10: version-invalid: bad version\n" +
				"variants/v/deploy.md: override-undeclared: command \"deploy\" already exists in base (first: base/deploy.md) (fix: add dotai: {overrides: true} to replace it)\n",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var b strings.Builder
			if err := writeReport(&b, tt.subject, tt.diags, tt.compact, false); err != nil {
				t.Fatal(err)
			}
			if b.String() != tt.want {
				t.Errorf("report =\n%s\nwant\n%s", b.String(), tt.want)
			}
		})
	}
}
