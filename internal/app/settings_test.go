package app_test

import (
	"errors"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"

	"github.com/lartweib/dotai/internal/app"
	"github.com/lartweib/dotai/internal/merge"
)

// settings returns the top-level keys of a YAML mapping as merged settings.
func settings(t *testing.T, src string) []merge.Setting {
	t.Helper()
	var doc yaml.Node
	if err := yaml.Unmarshal([]byte(src), &doc); err != nil {
		t.Fatal(err)
	}
	var out []merge.Setting
	m := doc.Content[0]
	for i := 0; i+1 < len(m.Content); i += 2 {
		out = append(out, merge.Setting{Key: m.Content[i].Value, Value: m.Content[i+1], Layer: "base"})
	}
	return out
}

func TestEncodeSettings(t *testing.T) {
	tools := app.ToolPaths{User: `C:\home\.dotai\tools`, Project: "p/.dotai/tools"}
	for _, tt := range []struct {
		name, rulesync, settings, want string
	}{
		{"keeps the key order", "", "z: 1\na: 2\nm: {y: 1, b: [2, 1]}", "{\n  \"z\": 1,\n  \"a\": 2,\n  \"m\": {\n    \"y\": 1,\n    \"b\": [\n      2,\n      1\n    ]\n  }\n}\n"},
		{"gives each scalar its JSON type", "", "i: 0x1F\nf: 1.50\nb: true\ns: '42'\nd: 2026-10-29\ne: []", `{"i":31,"f":1.5,"b":true,"s":"42","d":"2026-10-29","e":[]}`},
		{"escapes no HTML", "", `s: "<a> & \"b\"\\c"`, `{"s":"<a> & \"b\"\\c"}`},
		{"expands the path variables in strings", "", "${DOTAI_TOOLS}: [\"${DOTAI_TOOLS}/x\", \"${DOTAI_PROJECT_TOOLS}\"]", `{"${DOTAI_TOOLS}":["C:/home/.dotai/tools/x","p/.dotai/tools"]}`},
		{"appends to the keys rulesync wrote", "// generated\n{\"hooks\": {\"a\": [1]}, \"$schema\": \"s\",}", "model: opus", `{"hooks":{"a":[1]},"$schema":"s","model":"opus"}`},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var existing []byte
			if tt.rulesync != "" {
				existing = []byte(tt.rulesync)
			}
			got, err := app.EncodeSettings(existing, settings(t, tt.settings), tools)
			want := tt.want
			if !strings.Contains(want, "\n") {
				want = indent(t, want)
			}
			if err != nil || string(got) != want {
				t.Errorf("EncodeSettings() = %q, %v, want %q", got, err, want)
			}
		})
	}
}

// indent writes compact JSON as EncodeSettings does.
func indent(t *testing.T, compact string) string {
	t.Helper()
	got, err := app.EncodeSettings([]byte(compact), nil, app.ToolPaths{})
	if err != nil {
		t.Fatal(err)
	}
	return string(got)
}

func TestEncodeSettingsFails(t *testing.T) {
	errHome := errors.New("no home")
	for _, tt := range []struct {
		name, rulesync, settings string
		tools                    app.ToolPaths
		want                     error
		wantText                 string
	}{
		{"a key rulesync also wrote", `{"$schema": "s", "hooks": {}}`, "model: m\nhooks: {}", app.ToolPaths{}, app.ErrSettingsConflict, `"hooks"`},
		{"a rulesync file that is not an object", `[]`, "model: m", app.ToolPaths{}, nil, "not a JSON object"},
		{"a path variable without its directory", "", "s: ${DOTAI_TOOLS}", app.ToolPaths{UserErr: errHome}, errHome, "${DOTAI_TOOLS}"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var existing []byte
			if tt.rulesync != "" {
				existing = []byte(tt.rulesync)
			}
			got, err := app.EncodeSettings(existing, settings(t, tt.settings), tt.tools)
			if err == nil || got != nil || tt.want != nil && !errors.Is(err, tt.want) || !strings.Contains(err.Error(), tt.wantText) {
				t.Errorf("EncodeSettings() = %q, %v, want an error with %v and %s", got, err, tt.want, tt.wantText)
			}
		})
	}
}

func TestEncodeSettingsLeavesUnusedVariablesAlone(t *testing.T) {
	got, err := app.EncodeSettings(nil, settings(t, "s: plain"), app.ToolPaths{UserErr: errors.New("no home"), ProjectErr: errors.New("no project")})
	if want := "{\n  \"s\": \"plain\"\n}\n"; err != nil || string(got) != want {
		t.Errorf("EncodeSettings() = %q, %v, want %q", got, err, want)
	}
}
