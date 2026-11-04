package server

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
)

// The confirm of an action (decision 0043): a plan or a preview carries
// the digest of what it shows, and the action that carries it out takes
// that digest back. The use case asks Confirm with the plan it is about
// to carry out, before it writes; an action whose plan has another digest
// stops there with CodePlanChanged and the new plan, so that the user
// carries out only a plan they saw.

// canonical is the JSON of v as the API sends it, without HTML escaping.
func canonical(v any) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return bytes.TrimSuffix(buf.Bytes(), []byte("\n")), nil
}

// Digest returns the digest of v: "sha256:" and the SHA-256 in hex of its
// JSON. Two plans with the same JSON have the same digest.
func Digest(v any) (string, error) {
	b, err := canonical(v)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(b)
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}

// withDigest returns v, which encodes as a JSON object, with its digest
// added as the field digest.
func withDigest(v any) (json.RawMessage, error) {
	b, err := canonical(v)
	if err != nil {
		return nil, err
	}
	d, err := Digest(v)
	if err != nil {
		return nil, err
	}
	field := `"digest":"` + d + `"}`
	if len(b) > 2 {
		field = "," + field
	}
	return append(b[:len(b)-1:len(b)-1], field...), nil
}

// confirm returns the Confirm of an action whose user saw the plan of the
// digest want: it refuses any other plan with CodePlanChanged, the new
// plan and its digest in the envelope's data.
func confirm[T any](want string) func(T) error {
	return func(v T) error {
		got, err := Digest(v)
		if err != nil {
			return err
		}
		if got == want {
			return nil
		}
		data, err := withDigest(v)
		if err != nil {
			return err
		}
		return &apiError{code: CodePlanChanged, status: http.StatusConflict, data: data,
			msg: "the plan changed since it was shown, so the action stopped before writing it: review the new plan and confirm it again"}
	}
}
