package server

import (
	"net/http"
	"testing"

	"github.com/lartweib/dotai/internal/envelope"
)

func TestEveryCodeHasItsIntendedStatus(t *testing.T) {
	want := map[string]int{
		envelope.CodeUsage:           http.StatusBadRequest,
		envelope.CodeInvalid:         http.StatusUnprocessableEntity,
		envelope.CodeInternal:        http.StatusInternalServerError,
		envelope.CodeConsent:         http.StatusConflict,
		envelope.CodeRefused:         http.StatusConflict,
		envelope.CodeRunning:         http.StatusConflict,
		envelope.CodeProjectRefused:  http.StatusConflict,
		envelope.CodeApplyRefused:    http.StatusConflict,
		envelope.CodeRecordInvalid:   http.StatusConflict,
		envelope.CodeCopyRefused:     http.StatusConflict,
		envelope.CodeRegistryInvalid: http.StatusConflict,
		CodeUnauthorized:             http.StatusUnauthorized,
		CodeForbidden:                http.StatusForbidden,
		CodeNotFound:                 http.StatusNotFound,
		CodeMethodNotAllowed:         http.StatusMethodNotAllowed,
		CodeBusy:                     http.StatusTooManyRequests,
		CodePlanChanged:              http.StatusConflict,
	}
	for _, code := range append(append([]string{}, envelope.Codes...), serverCodes...) {
		status, ok := want[code]
		if !ok {
			t.Errorf("code %q has no intended status in this test", code)
			continue
		}
		if got := statusOf(code); got != status {
			t.Errorf("statusOf(%q) = %d, want %d", code, got, status)
		}
	}
	if len(want) != len(envelope.Codes)+len(serverCodes) {
		t.Errorf("this test names %d codes, the envelope and the server declare %d", len(want), len(envelope.Codes)+len(serverCodes))
	}
	if got := statusOf("no-such-code"); got != http.StatusInternalServerError {
		t.Errorf("statusOf(an unknown code) = %d, want 500", got)
	}
}
