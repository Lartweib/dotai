package backup_test

import (
	"strings"
	"testing"
	"time"

	"github.com/lartweib/dotai/internal/backup"
)

func TestDecodeProjectChecksTheOperationOfTheID(t *testing.T) {
	at := time.Date(2026, 11, 3, 10, 15, 0, 0, time.UTC)
	project := "/work/site"
	for name, tc := range map[string]struct {
		idOp, op string
		want     string // "" for valid
	}{
		"apply":                {backup.OperationApply, "apply", ""},
		"fix":                  {backup.OperationFix, "fix", ""},
		"restore":              {backup.OperationRestore, "restore", ""},
		"another operation":    {backup.OperationFix, "apply", "operation"},
		"not a project backup": {backup.OperationInstall, "install", "operation"},
		"an unknown operation": {"clean", "clean", "operation"},
	} {
		t.Run(name, func(t *testing.T) {
			id := backup.ID(at, tc.idOp)
			data, err := backup.EncodeProject(backup.ProjectManifest{Schema: backup.ProjectSchema, ID: id, Operation: tc.op, CreatedAt: at, Project: project, Copies: []backup.Copy{}})
			if err != nil {
				t.Fatal(err)
			}
			m, err := backup.DecodeProject(data, id, project)
			switch {
			case tc.want == "" && (err != nil || m.Operation != tc.op):
				t.Errorf("DecodeProject() = %q, %v, want the operation %q", m.Operation, err, tc.op)
			case tc.want != "" && (err == nil || !strings.Contains(err.Error(), tc.want)):
				t.Errorf("DecodeProject() error = %v, want one about the %s", err, tc.want)
			}
		})
	}
}
