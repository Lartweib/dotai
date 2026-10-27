// Package output defines the versioned JSON envelope that every command
// prints with --json. See docs/CLI-JSON.md.
package output

import (
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
)

// Envelope is the single JSON document a command prints with --json.
type Envelope struct {
	Schema  string `json:"schema"`
	Command string `json:"command"`
	OK      bool   `json:"ok"`
	Data    any    `json:"data,omitempty"`
	Error   *Error `json:"error,omitempty"`
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

// Write prints the envelope as indented JSON followed by a newline.
func Write(w io.Writer, e Envelope) error {
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	return enc.Encode(e)
}
