package envelope

import (
	"errors"

	"github.com/lartweib/dotai/internal/app"
)

// Classify maps err, an error of the use cases in internal/app, to the
// error code of its envelope and the data that explains it, such as the
// plan a refusal carries, nil when there is none. An error it does not
// know is CodeInternal. The command line and the local web UI both report
// errors through it, so that they give the same codes.
func Classify(err error) (code string, data any) {
	code = CodeInternal
	var (
		usage        *app.UsageError
		consent      *app.ConsentError
		refused      *app.RefusedError
		running      *app.RunningError
		project      *app.ProjectRefusedError
		applyConsent *app.ApplyConsentError
		applyRefused *app.ApplyRefusedError
		invalid      *InvalidError
		packInvalid  *app.PackInvalidError
		registry     *app.RegistryInvalidError
	)
	switch {
	case errors.As(err, &usage), errors.As(err, new(*UsageError)):
		code = CodeUsage
	case errors.As(err, &invalid):
		code, data = CodeInvalid, invalid.Data
	case errors.As(err, &consent):
		code, data = CodeConsent, consent.Plan
	case errors.As(err, &refused):
		code, data = CodeRefused, refused.Plan
	case errors.As(err, &running):
		code, data = CodeRunning, map[string]any{"harnesses": running.Harnesses, "running": runningList(running.Running)}
	case errors.As(err, &project):
		code = CodeProjectRefused
	case errors.As(err, &applyConsent):
		code, data = CodeConsent, applyConsent.Plan
	case errors.As(err, &applyRefused):
		code, data = CodeApplyRefused, applyRefused.Plan
	case errors.As(err, new(*app.RecordInvalidError)):
		code = CodeRecordInvalid
	case errors.As(err, &packInvalid):
		code, data = CodeInvalid, packInvalid.Result
	case errors.As(err, new(*app.CopyRefusedError)):
		code = CodeCopyRefused
	case errors.As(err, &registry):
		code, data = CodeRegistryInvalid, map[string]any{"path": registry.Path}
	}
	// A restore refused over conflicts lists them, whatever its code.
	if conflict, ok := errors.AsType[*app.ConflictError](err); ok {
		data = map[string]any{"conflicts": conflict.Conflicts}
	}
	return code, data
}

// FromError returns the failure envelope of command for err, with the
// code and the data that Classify gives.
func FromError(command string, err error) Envelope {
	code, data := Classify(err)
	e := Failure(command, code, err.Error())
	e.Data = data
	return e
}

// runningList is never nil, so that it is [] in an envelope.
func runningList(r []app.RunningHarness) []app.RunningHarness {
	if r == nil {
		return []app.RunningHarness{}
	}
	return r
}
