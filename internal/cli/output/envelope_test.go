package output_test

import (
	"bytes"
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/lartweib/dotai/internal/cli/output"
)

var update = flag.Bool("update", false, "rewrite golden files")

func TestWrite(t *testing.T) {
	tests := []struct {
		name     string
		envelope output.Envelope
		golden   string
	}{
		{
			name:     "success carries data and no error",
			envelope: output.Success("version", map[string]string{"version": "1.2.3"}),
			golden:   "success.json.golden",
		},
		{
			name:     "failure carries error and no data",
			envelope: output.Failure("dotai", output.CodeUsage, `unknown command "nope" for "dotai"`),
			golden:   "failure.json.golden",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer
			if err := output.Write(&buf, tt.envelope); err != nil {
				t.Fatalf("Write() error = %v", err)
			}
			assertGolden(t, filepath.Join("testdata", tt.golden), buf.Bytes())
		})
	}
}

func TestEnvelopeShape(t *testing.T) {
	tests := []struct {
		name     string
		envelope output.Envelope
		want     map[string]any
	}{
		{
			name:     "success without data has an empty data object",
			envelope: output.Success("version", nil),
			want: map[string]any{
				"schema": "dotai.cli/v1", "command": "version", "ok": true,
				"data": map[string]any{},
			},
		},
		{
			name:     "success with data",
			envelope: output.Success("version", map[string]string{"version": "1.2.3"}),
			want: map[string]any{
				"schema": "dotai.cli/v1", "command": "version", "ok": true,
				"data": map[string]any{"version": "1.2.3"},
			},
		},
		{
			name:     "failure with a result carries data and error",
			envelope: output.Invalid("validate", "pack is invalid", map[string]bool{"valid": false}),
			want: map[string]any{
				"schema": "dotai.cli/v1", "command": "validate", "ok": false,
				"data":  map[string]any{"valid": false},
				"error": map[string]any{"code": "invalid", "message": "pack is invalid"},
			},
		},
		{
			name:     "failure has error and no data",
			envelope: output.Failure("version", output.CodeInternal, "boom"),
			want: map[string]any{
				"schema": "dotai.cli/v1", "command": "version", "ok": false,
				"error": map[string]any{"code": "internal", "message": "boom"},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer
			if err := output.Write(&buf, tt.envelope); err != nil {
				t.Fatalf("Write() error = %v", err)
			}
			var got map[string]any
			if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
				t.Fatalf("invalid JSON %q: %v", buf.String(), err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("shape = %v, want %v", got, tt.want)
			}
		})
	}
}

func assertGolden(t *testing.T, path string, got []byte) {
	t.Helper()
	if *update {
		if err := os.WriteFile(path, got, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Errorf("output mismatch for %s\ngot:\n%s\nwant:\n%s", path, got, want)
	}
}
