package app_test

import (
	"errors"
	"path/filepath"
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
	// A native separator, so Windows shows that it becomes a forward slash.
	tools := app.ToolPaths{User: filepath.FromSlash("C:/home/.dotai/tools"), Project: "p/.dotai/tools"}
	for _, tt := range []struct {
		name, rulesync, settings, want string
	}{
		{"keeps the key order", "", "z: 1\na: 2\nm: {y: 1, b: [2, 1]}", "{\n  \"z\": 1,\n  \"a\": 2,\n  \"m\": {\n    \"y\": 1,\n    \"b\": [\n      2,\n      1\n    ]\n  }\n}\n"},
		{"gives each scalar its JSON type", "", "i: 0x1F\nf: 1.50\nb: true\ns: '42'\nd: 2026-10-29\ne: []\nm: {}", "{\n  \"i\": 31,\n  \"f\": 1.5,\n  \"b\": true,\n  \"s\": \"42\",\n  \"d\": \"2026-10-29\",\n  \"e\": [],\n  \"m\": {}\n}\n"},
		{"escapes no HTML", "", `s: "<a> & \"b\"\\c\t\u2028"`, "{\n  \"s\": " + `"<a> & \"b\"\\c\t\u2028"` + "\n}\n"},
		{"expands the path variables in strings", "", "${DOTAI_TOOLS}: [\"${DOTAI_TOOLS}/x\", \"${DOTAI_PROJECT_TOOLS}\"]", "{\n  \"${DOTAI_TOOLS}\": [\n    \"C:/home/.dotai/tools/x\",\n    \"p/.dotai/tools\"\n  ]\n}\n"},
		{"appends to the keys rulesync wrote", "// generated\n{\"hooks\": {\"a\": [1]}, \"$schema\": \"s\",}", "model: opus", "{\n  \"hooks\": {\n    \"a\": [\n      1\n    ]\n  },\n  \"$schema\": \"s\",\n  \"model\": \"opus\"\n}\n"},
		{"takes an empty rulesync file as no keys", " \n", "model: opus", "{\n  \"model\": \"opus\"\n}\n"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var existing []byte
			if tt.rulesync != "" {
				existing = []byte(tt.rulesync)
			}
			got, err := app.EncodeSettings(existing, settings(t, tt.settings), tools)
			if err != nil || string(got) != tt.want {
				t.Errorf("EncodeSettings() = %q, %v, want %q", got, err, tt.want)
			}
		})
	}
}

func TestEncodeSettingsFails(t *testing.T) {
	errHome := errors.New("no home")
	for _, tt := range []struct {
		name, rulesync, settings string
		tools                    app.ToolPaths
		want                     error
		wantText                 string
		conflict                 string // the key of a settings conflict
	}{
		{"a key rulesync also wrote", `{"$schema": "s", "hooks": {}}`, "model: m\nhooks: {}", app.ToolPaths{}, nil, `"hooks"`, "hooks"},
		{"an escaped key rulesync also wrote", `{"model": "x"}`, "model: m", app.ToolPaths{}, nil, `"model"`, "model"},
		{"a rulesync file that is not an object", `[]`, "model: m", app.ToolPaths{}, nil, "not a JSON object", ""},
		{"a path variable without its directory", "", "s: ${DOTAI_TOOLS}", app.ToolPaths{UserErr: errHome}, errHome, "${DOTAI_TOOLS}", ""},
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
			var c *app.SettingsConflict
			if errors.As(err, &c) != (tt.conflict != "") || c != nil && (c.Key != tt.conflict || c.Layer != "base") {
				t.Errorf("conflict = %+v, want key %q from base", c, tt.conflict)
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
