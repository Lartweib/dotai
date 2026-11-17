// Package envelope defines the versioned JSON envelope that every command
// prints with --json. See docs/CLI-JSON.md.
package envelope

import (
	"bytes"
	"encoding/json"
	"io"
)

// Schema identifies the envelope version. Changing the envelope shape
// requires a new schema value.
const Schema = "dotai.cli/v1"

// Error codes carried in Error.Code.
const (
	CodeUsage    = "usage"    // invalid command, argument or flag
	CodeInternal = "internal" // the command failed while running
	CodeInvalid  = "invalid"  // the input was read and is not valid
	// A replace install, or its restore, refused before it wrote anything
	// (decision 0038): it needs consent (--yes) to wipe what dotai does not
	// own, its plan has refusals that no flag overrides, or a harness it
	// writes into is running.
	CodeConsent = "consent-required"
	CodeRefused = "replace-refused"
	CodeRunning = "harness-running"
	// A project apply refused in the home directory or at a root, where
	// the project scope would overlap the user scope (decision 0041).
	CodeProjectRefused = "project-refused"
	// A project apply whose plan has refusals, such as an MCP server of
	// the name of one dotai does not own, which no flag overrides.
	CodeApplyRefused = "apply-refused"
	// A project's record under the home directory that names what an apply
	// of the project never writes, refused before anything was written.
	CodeRecordInvalid = "record-invalid"
	// A copy of a pack refused before it wrote anything: its target exists
	// or has no parent, or the pack holds a link.
	CodeCopyRefused = "copy-refused"
	// The pack registry, ~/.dotai/packs.json, is not JSON of its schema;
	// it is left as it is.
	CodeRegistryInvalid = "registry-invalid"
	// A local pack whose name another directory of the registry already
	// has (decision 0044).
	CodePackNameTaken = "pack-name-taken"
	// A git URL of a host other than github.com (decision 0044).
	CodeHostUnsupported = "host-unsupported"
)

// Codes lists every error code above, so that an adapter that maps each
// one, such as the web UI to an HTTP status, can check that it maps them
// all.
var Codes = []string{
	CodeUsage, CodeInternal, CodeInvalid, CodeConsent, CodeRefused, CodeRunning,
	CodeProjectRefused, CodeApplyRefused, CodeRecordInvalid, CodeCopyRefused, CodeRegistryInvalid,
	CodePackNameTaken, CodeHostUnsupported,
}

// Envelope is the single JSON document a command prints with --json.
// A success always carries "data" (an empty object when there is no
// result) and never "error"; a failure carries "error", and "data" only
// when the command produced a result that explains it (see Invalid).
type Envelope struct {
	Schema  string
	Command string
	OK      bool
	Data    any
	Error   *Error
}

// MarshalJSON encodes the envelope with exactly the fields of its outcome.
func (e Envelope) MarshalJSON() ([]byte, error) {
	if e.OK {
		data := e.Data
		if data == nil {
			data = struct{}{}
		}
		return marshal(struct {
			Schema  string `json:"schema"`
			Command string `json:"command"`
			OK      bool   `json:"ok"`
			Data    any    `json:"data"`
		}{e.Schema, e.Command, true, data})
	}
	return marshal(struct {
		Schema  string `json:"schema"`
		Command string `json:"command"`
		OK      bool   `json:"ok"`
		Data    any    `json:"data,omitempty"`
		Error   *Error `json:"error"`
	}{e.Schema, e.Command, false, e.Data, e.Error})
}

func marshal(v any) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return bytes.TrimSuffix(buf.Bytes(), []byte("\n")), nil
}

// Error describes why a command failed.
type Error struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// Success wraps the result of a command.
func Success(command string, data any) Envelope {
	return Envelope{Schema: Schema, Command: command, OK: true, Data: data}
}

// Failure wraps a command error.
func Failure(command, code, message string) Envelope {
	return Envelope{Schema: Schema, Command: command, Error: &Error{Code: code, Message: message}}
}

// Invalid reports input that was read and is not valid, such as a pack with
// errors. data is the command result that lists the problems.
func Invalid(command, message string, data any) Envelope {
	e := Failure(command, CodeInvalid, message)
	e.Data = data
	return e
}

// Write prints the envelope as indented JSON followed by a newline.
func Write(w io.Writer, e Envelope) error {
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	return enc.Encode(e)
}
