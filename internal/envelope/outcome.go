package envelope

import (
	"fmt"

	"github.com/lartweib/dotai/internal/app"
)

// UsageError rejects a request whose arguments are wrong, as a wrong flag
// is for the command line or a missing query parameter for the web UI.
type UsageError struct{ msg string }

func (e *UsageError) Error() string { return e.msg }

// Usage returns a UsageError with msg.
func Usage(msg string) error { return &UsageError{msg: msg} }

// InvalidError rejects input that dotai read and found not valid, such as
// a pack with problems. Data holds the result that explains why, and the
// envelope carries it.
type InvalidError struct {
	Msg  string
	Data any
}

func (e *InvalidError) Error() string { return e.Msg }

// PackInvalid rejects a pack with the problems diags; data is the result
// that lists them.
func PackInvalid(diags []app.Diagnostic, data any) *InvalidError {
	return &InvalidError{Msg: fmt.Sprintf("pack is invalid: %d problem(s)", len(diags)), Data: data}
}

// DoctorProblems rejects a doctor result that still has problems, and
// returns nil when every finding is fixed or there is none.
func DoctorProblems(res app.DoctorResult) error {
	n := res.Problems()
	if n == 0 {
		return nil
	}
	msg := "doctor found 1 problem"
	if n != 1 {
		msg = fmt.Sprintf("doctor found %d problems", n)
	}
	return &InvalidError{Msg: msg, Data: res}
}
