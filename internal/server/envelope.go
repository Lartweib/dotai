package server

import (
	"errors"
	"net/http"

	"github.com/lartweib/dotai/internal/envelope"
)

// writeFailure writes the envelope of err for command, with the codes and
// data the command line gives (envelope.Classify), and the HTTP status of
// its code. A failure of the API itself has its own code and status.
func writeFailure(w http.ResponseWriter, command string, err error) {
	if ae, ok := errors.AsType[*apiError](err); ok {
		e := envelope.Failure(command, ae.code, ae.msg)
		e.Data = ae.data
		writeEnvelope(w, ae.status, e)
		return
	}
	e := envelope.FromError(command, err)
	if re, ok := errors.AsType[*resultError](err); ok {
		e.Data = re.data
	}
	writeEnvelope(w, statusOf(e.Error.Code), e)
}

// resultError is an error of a use case that comes with the result it got
// to before it failed, which the envelope carries as its data.
type resultError struct {
	err  error
	data any
}

func (e *resultError) Error() string { return e.err.Error() }
func (e *resultError) Unwrap() error { return e.err }

// statusOf gives the HTTP status of an envelope's error code: 400 for a
// wrong request, 422 for input that is not valid, 409 for a refusal of the
// command line, the status of each code of the server, and 500 for a
// failure or a code it does not know.
func statusOf(code string) int {
	switch code {
	case envelope.CodeUsage:
		return http.StatusBadRequest
	case envelope.CodeInvalid:
		return http.StatusUnprocessableEntity
	case envelope.CodeConsent, envelope.CodeRefused, envelope.CodeRunning,
		envelope.CodeProjectRefused, envelope.CodeApplyRefused, envelope.CodeRecordInvalid, envelope.CodeCopyRefused, envelope.CodeNewRefused, envelope.CodeRegistryInvalid,
		envelope.CodePackNameTaken, envelope.CodeHostUnsupported, envelope.CodeFetchByCommit, envelope.CodeTreeRefused:
		return http.StatusConflict
	case envelope.CodeFetchFailed:
		return http.StatusBadGateway
	case CodeForbidden:
		return http.StatusForbidden
	case CodeUnauthorized:
		return http.StatusUnauthorized
	case CodeNotFound:
		return http.StatusNotFound
	case CodeMethodNotAllowed:
		return http.StatusMethodNotAllowed
	case CodeBusy:
		return http.StatusTooManyRequests
	case CodePlanChanged:
		return http.StatusConflict
	}
	return http.StatusInternalServerError
}

// writeEnvelope writes e as the JSON body of a response of status, never
// cached.
func writeEnvelope(w http.ResponseWriter, status int, e envelope.Envelope) {
	h := w.Header()
	h.Set("Content-Type", "application/json; charset=utf-8")
	h.Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = envelope.Write(w, e)
}
