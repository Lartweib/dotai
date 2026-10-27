package output_test

import (
	"bytes"
	"flag"
	"os"
	"path/filepath"
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

func TestEnvelopeSchema(t *testing.T) {
	for _, e := range []output.Envelope{
		output.Success("version", nil),
		output.Failure("version", output.CodeInternal, "boom"),
	} {
		if e.Schema != "dotai.cli/v1" {
			t.Errorf("Schema = %q, want dotai.cli/v1", e.Schema)
		}
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
