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
		envelope.CodeProjectRefused, envelope.CodeApplyRefused, envelope.CodeRecordInvalid:
		return http.StatusConflict
	case CodeForbidden:
		return http.StatusForbidden
	case CodeUnauthorized:
		return http.StatusUnauthorized
	case CodeNotFound:
		return http.StatusNotFound
	case CodeMethodNotAllowed:
		return http.StatusMethodNotAllowed
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
