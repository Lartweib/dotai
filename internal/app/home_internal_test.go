package app

import (
	"encoding/json"
	"path/filepath"
	"testing"
)

func TestRequireAbs(t *testing.T) {
	abs := t.TempDir()
	if got, err := requireAbs(abs + string(filepath.Separator) + "."); err != nil || got != abs {
		t.Errorf("requireAbs(%q) = %q, %v, want it cleaned", abs, got, err)
	}
	if _, err := requireAbs("relative"); err == nil {
		t.Error("requireAbs(relative) succeeded")
	}
}

func TestProjectNamedFor(t *testing.T) {
	project := filepath.Join(t.TempDir(), "repo")
	id := projectID(project)
	data, err := json.Marshal(map[string]string{"project": project})
	if err != nil {
		t.Fatal(err)
	}
	if got, err := projectNamedFor(data, id); err != nil || got != project {
		t.Errorf("projectNamedFor() = %q, %v, want %q", got, err, project)
	}
	for name, tt := range map[string]struct {
		data []byte
		id   string
	}{
		"another directory":  {data, "0123456789abcdef"},
		"a relative project": {[]byte(`{"project":"repo"}`), projectID("repo")},
		"not JSON":           {[]byte(`{`), id},
	} {
		if _, err := projectNamedFor(tt.data, tt.id); err == nil {
			t.Errorf("projectNamedFor() of %s succeeded", name)
		}
	}
}
