package cli

import (
	"fmt"
	"io"
	"strings"

	"github.com/lartweib/dotai/internal/app"
)

// Diagnostic formats accepted by --format.
const (
	formatPretty  = "pretty"  // grouped by file under a summary header
	formatCompact = "compact" // one file:line:col: code: message line each
)

// formatValue is the --format flag; Set rejects unknown formats, so they
// are usage errors.
type formatValue string

func (f *formatValue) String() string { return string(*f) }
func (f *formatValue) Type() string   { return "format" }
func (f *formatValue) Set(s string) error {
	if s != formatPretty && s != formatCompact {
		return fmt.Errorf("want %s or %s", formatPretty, formatCompact)
	}
	*f = formatValue(s)
	return nil
}

// writeReport prints diags, compact or grouped by file in the order files
// first appear, colored when color is true. subject, such as the pack name
// and version, may be empty.
func writeReport(w io.Writer, subject string, diags []app.Diagnostic, compact, color bool) error {
	var b strings.Builder
	if compact {
		for _, d := range diags {
			fmt.Fprintln(&b, d)
		}
		_, err := io.WriteString(w, b.String())
		return err
	}

	var files []string
	byFile := map[string][]app.Diagnostic{}
	codeW := 0
	for _, d := range diags {
		if _, ok := byFile[d.File]; !ok {
			files = append(files, d.File)
		}
		byFile[d.File] = append(byFile[d.File], d)
		codeW = max(codeW, len(d.Code))
	}

	fmt.Fprintf(&b, "%s %s%s in %s\n", paint(color, styleRed, "✗"), prefix(subject), plural(len(diags), "problem"), plural(len(files), "file"))
	for _, f := range files {
		fmt.Fprintf(&b, "\n%s\n", paint(color, styleBold, f))
		posW := 0 // positions take a column only in files that have them
		for _, d := range byFile[f] {
			if p := position(d); p != "" {
				posW = max(posW, len(p)+2)
			}
		}
		indent := strings.Repeat(" ", 2+posW+codeW+2)
		for _, d := range byFile[f] {
			code := paint(color, styleRed, d.Code) + strings.Repeat(" ", codeW-len(d.Code))
			fmt.Fprintf(&b, "  %-*s%s  %s\n", posW, position(d), code, d.Message)
			if d.Related != nil {
				fmt.Fprintf(&b, "%s%s\n", indent, paint(color, styleDim, "first: "+d.Related.String()))
			}
			if d.Hint != "" {
				fmt.Fprintf(&b, "%s%s\n", indent, paint(color, styleDim, "fix:   "+d.Hint))
			}
		}
	}
	_, err := io.WriteString(w, b.String())
	return err
}

// writeValid prints the success line of validate.
func writeValid(w io.Writer, p *app.PackSummary, color bool) error {
	_, err := fmt.Fprintf(w, "%s %s %s is valid · %s\n", paint(color, styleGreen, "✓"), p.Name, p.Version, plural(len(p.Layers), "layer"))
	return err
}

// position is line[:col] of d, empty for a problem with the whole file.
func position(d app.Diagnostic) string {
	return strings.TrimPrefix(strings.TrimPrefix(d.Location().String(), d.File), ":")
}

func prefix(subject string) string {
	if subject == "" {
		return ""
	}
	return subject + " · "
}

func plural(n int, noun string) string {
	if n == 1 {
		return fmt.Sprintf("1 %s", noun)
	}
	return fmt.Sprintf("%d %ss", n, noun)
}
