package pack

import (
	"cmp"
	"fmt"
	"slices"
)

// Location is a place in a pack. File is relative to the pack root, with
// forward slashes. Line and Column are 1-based; Line 0 means the whole
// file, and Column 0 the whole line.
type Location struct {
	File   string `json:"file"`
	Line   int    `json:"line"`
	Column int    `json:"column"`
}

// String formats l as file[:line[:col]].
func (l Location) String() string {
	pos := l.File
	if l.Line > 0 {
		pos += fmt.Sprintf(":%d", l.Line)
		if l.Column > 0 {
			pos += fmt.Sprintf(":%d", l.Column)
		}
	}
	return pos
}

// Diagnostic is one problem found in a pack, located like a Location.
// Related points at the earlier definition the problem clashes with, and
// Hint says how to fix it; both are optional.
type Diagnostic struct {
	File    string    `json:"file"`
	Line    int       `json:"line"`
	Column  int       `json:"column"`
	Code    string    `json:"code"`
	Message string    `json:"message"`
	Related *Location `json:"related,omitempty"`
	Hint    string    `json:"hint,omitempty"`
}

// NewDiagnostic is the only way diagnostics are built, here and in the
// merge stage.
func NewDiagnostic(file string, line, col int, code, format string, args ...any) Diagnostic {
	return Diagnostic{File: file, Line: line, Column: col, Code: code, Message: fmt.Sprintf(format, args...)}
}

// WithRelated returns d pointing at the definition it clashes with.
func (d Diagnostic) WithRelated(file string, line int) Diagnostic {
	d.Related = &Location{File: file, Line: line}
	return d
}

// WithHint returns d with a hint on how to fix it.
func (d Diagnostic) WithHint(format string, args ...any) Diagnostic {
	d.Hint = fmt.Sprintf(format, args...)
	return d
}

// Location is where d is.
func (d Diagnostic) Location() Location {
	return Location{File: d.File, Line: d.Line, Column: d.Column}
}

// String formats d on one line as file[:line[:col]]: code: message, then
// (first: related) and (fix: hint) when present.
func (d Diagnostic) String() string {
	s := fmt.Sprintf("%s: %s: %s", d.Location(), d.Code, d.Message)
	if d.Related != nil {
		s += fmt.Sprintf(" (first: %s)", d.Related)
	}
	if d.Hint != "" {
		s += fmt.Sprintf(" (fix: %s)", d.Hint)
	}
	return s
}

// sortFrom orders ds[start:], the diagnostics of one file, by position.
func sortFrom(ds []Diagnostic, start int) {
	slices.SortStableFunc(ds[start:], func(a, b Diagnostic) int {
		return cmp.Or(cmp.Compare(a.Line, b.Line), cmp.Compare(a.Column, b.Column))
	})
}
