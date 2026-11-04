package server

import (
	"errors"
	"net/http"

	"github.com/lartweib/dotai/internal/cli/output"
)

// usageError is a request whose parameters are wrong, as a wrong flag is
// for the command line.
type usageError struct{ msg string }

func (e *usageError) Error() string { return e.msg }

// invalidError is an outcome that rejects its input, such as a pack with
// problems, with the result that explains why, as the command line's.
type invalidError struct {
	msg  string
	data any
}

func (e *invalidError) Error() string { return e.msg }

// writeFailure writes the envelope of err for command, with the codes and
// data the command line gives (output.Classify), and the HTTP status of
// its code.
func writeFailure(w http.ResponseWriter, command string, err error) {
	e := output.FromError(command, err)
	if _, ok := errors.AsType[*usageError](err); ok {
		e.Error.Code, e.Data = output.CodeUsage, nil
	}
	if inv, ok := errors.AsType[*invalidError](err); ok {
		e = output.Invalid(command, err.Error(), inv.data)
	}
	writeEnvelope(w, statusOf(e.Error.Code), e)
}

// statusOf is the HTTP status of an envelope's error code: the request is
// wrong, the input it names is not valid, the operation refuses as the
// command line does, or the server failed.
func statusOf(code string) int {
	switch code {
	case output.CodeUsage:
		return http.StatusBadRequest
	case output.CodeInvalid:
		return http.StatusUnprocessableEntity
	case output.CodeInternal:
		return http.StatusInternalServerError
	case CodeForbidden:
		return http.StatusForbidden
	case CodeUnauthorized:
		return http.StatusUnauthorized
	case CodeNotFound:
		return http.StatusNotFound
	case CodeMethodNotAllowed:
		return http.StatusMethodNotAllowed
	}
	return http.StatusConflict // a refusal, such as consent-required
}

// writeEnvelope writes e as the JSON body of a response of status, never
// cached.
func writeEnvelope(w http.ResponseWriter, status int, e output.Envelope) {
	h := w.Header()
	h.Set("Content-Type", "application/json; charset=utf-8")
	h.Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = output.Write(w, e)
}
