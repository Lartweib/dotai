package server

import (
	"net/http"

	"github.com/lartweib/dotai/internal/envelope"
)

// writeFailure writes the envelope of err for command, with the codes and
// data the command line gives (envelope.Classify), and the HTTP status of
// its code.
func writeFailure(w http.ResponseWriter, command string, err error) {
	e := envelope.FromError(command, err)
	writeEnvelope(w, statusOf(e.Error.Code), e)
}

// statusOf is the HTTP status of an envelope's error code: the request is
// wrong, the input it names is not valid, the operation refuses as the
// command line does, or the server failed.
func statusOf(code string) int {
	switch code {
	case envelope.CodeUsage:
		return http.StatusBadRequest
	case envelope.CodeInvalid:
		return http.StatusUnprocessableEntity
	case envelope.CodeInternal:
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
func writeEnvelope(w http.ResponseWriter, status int, e envelope.Envelope) {
	h := w.Header()
	h.Set("Content-Type", "application/json; charset=utf-8")
	h.Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = envelope.Write(w, e)
}
