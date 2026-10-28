package pack

import (
	"cmp"
	"fmt"
	"slices"
)

// Diagnostic is one problem found in a pack. File is relative to the pack
// root, with forward slashes. Line and Column are 1-based; 0 means unknown.
type Diagnostic struct {
	File    string `json:"file"`
	Line    int    `json:"line"`
	Column  int    `json:"column"`
	Code    string `json:"code"`
	Message string `json:"message"`
}

// NewDiagnostic is the only way diagnostics are built, here and in the
// merge stage.
func NewDiagnostic(file string, line, col int, code, format string, args ...any) Diagnostic {
	return Diagnostic{File: file, Line: line, Column: col, Code: code, Message: fmt.Sprintf(format, args...)}
}

// String formats d as file:line:col: code: message, leaving out unknown
// positions.
func (d Diagnostic) String() string {
	pos := d.File
	if d.Line > 0 {
		pos += fmt.Sprintf(":%d", d.Line)
		if d.Column > 0 {
			pos += fmt.Sprintf(":%d", d.Column)
		}
	}
	return fmt.Sprintf("%s: %s: %s", pos, d.Code, d.Message)
}

// sortFrom orders ds[start:], the diagnostics of one file, by position.
func sortFrom(ds []Diagnostic, start int) {
	slices.SortStableFunc(ds[start:], func(a, b Diagnostic) int {
		return cmp.Or(cmp.Compare(a.Line, b.Line), cmp.Compare(a.Column, b.Column))
	})
}
