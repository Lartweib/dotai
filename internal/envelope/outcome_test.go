package envelope_test

import (
	"errors"
	"reflect"
	"testing"

	"github.com/lartweib/dotai/internal/app"
	"github.com/lartweib/dotai/internal/envelope"
)

func TestTheSharedOutcomesClassify(t *testing.T) {
	plan := app.InstallPlan{Diagnostics: []app.Diagnostic{{Code: "a"}, {Code: "b"}}}
	tests := []struct {
		name     string
		err      error
		wantCode string
		wantMsg  string
		wantData any
	}{
		{"usage", envelope.Usage("the query parameter pack is required"), envelope.CodeUsage, "the query parameter pack is required", nil},
		{"invalid pack", envelope.PackInvalid(plan.Diagnostics, plan), envelope.CodeInvalid, "pack is invalid: 2 problem(s)", plan},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			code, data := envelope.Classify(tt.err)
			if code != tt.wantCode || tt.err.Error() != tt.wantMsg || !reflect.DeepEqual(data, tt.wantData) {
				t.Errorf("Classify(%q) = %q, %#v, want %q, %q, %#v", tt.err, code, data, tt.wantMsg, tt.wantCode, tt.wantData)
			}
		})
	}
}

func TestDoctorProblems(t *testing.T) {
	clean := app.DoctorResult{Findings: []app.Finding{{Code: app.CodeExcludeMissing, Fixed: true}}}
	if err := envelope.DoctorProblems(clean); err != nil {
		t.Errorf("DoctorProblems() of a fixed finding = %v, want nil", err)
	}
	for n, want := range map[int]string{1: "doctor found 1 problem", 3: "doctor found 3 problems"} {
		res := app.DoctorResult{}
		for range n {
			res.Findings = append(res.Findings, app.Finding{Code: app.CodeFileChanged})
		}
		err := envelope.DoctorProblems(res)
		var inv *envelope.InvalidError
		if !errors.As(err, &inv) || err.Error() != want || !reflect.DeepEqual(inv.Data, res) {
			t.Errorf("DoctorProblems() of %d = %v, want %q with the result", n, err, want)
		}
	}
}
