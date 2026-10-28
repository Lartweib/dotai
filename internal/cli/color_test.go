package cli

import (
	"bytes"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/lartweib/dotai/internal/app"
)

func TestUseColor(t *testing.T) {
	tests := []struct {
		name     string
		w        io.Writer
		terminal bool
		noColor  string
		want     bool
	}{
		{name: "terminal", w: os.Stdout, terminal: true, want: true},
		{name: "terminal with NO_COLOR", w: os.Stdout, terminal: true, noColor: "1", want: false},
		{name: "file that is not a terminal", w: os.Stdout, terminal: false, want: false},
		{name: "not a file", w: &bytes.Buffer{}, terminal: true, want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("NO_COLOR", tt.noColor)
			old := isTerminal
			isTerminal = func(*os.File) bool { return tt.terminal }
			t.Cleanup(func() { isTerminal = old })

			if got := useColor(tt.w); got != tt.want {
				t.Errorf("useColor() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestWriteReportColor(t *testing.T) {
	d := app.Diagnostic{File: "a.md", Code: "name-duplicate", Message: "twice", Related: &app.Location{File: "b.md"}}
	var b strings.Builder
	if err := writeReport(&b, "", []app.Diagnostic{d}, false, true); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"\x1b[1ma.md\x1b[0m", "\x1b[31mname-duplicate\x1b[0m", "\x1b[2mfirst: b.md\x1b[0m"} {
		if !strings.Contains(b.String(), want) {
			t.Errorf("report = %q, want it to contain %q", b.String(), want)
		}
	}
}
