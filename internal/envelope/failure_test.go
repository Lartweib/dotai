package envelope_test

import (
	"errors"
	"fmt"
	"reflect"
	"testing"

	"github.com/lartweib/dotai/internal/app"
	"github.com/lartweib/dotai/internal/envelope"
)

func TestClassify(t *testing.T) {
	installPlan := app.InstallPlan{Mode: "replace"}
	projectPlan := app.ProjectPlan{Project: "/work/repo"}
	conflicts := []app.RestoreConflict{{Target: "/home/u/.claude/CLAUDE.md", Backup: "20261029T101500.000Z_install"}}
	tests := []struct {
		name     string
		err      error
		wantCode string
		wantData any
	}{
		{name: "unknown error is internal", err: errors.New("boom"), wantCode: envelope.CodeInternal},
		{name: "usage", err: &app.UsageError{}, wantCode: envelope.CodeUsage},
		{name: "consent carries the install plan", err: &app.ConsentError{Plan: installPlan}, wantCode: envelope.CodeConsent, wantData: installPlan},
		{name: "refused carries the install plan", err: &app.RefusedError{Plan: installPlan}, wantCode: envelope.CodeRefused, wantData: installPlan},
		{
			name:     "running carries the harnesses and an empty process list",
			err:      &app.RunningError{Harnesses: []string{"claudecode"}},
			wantCode: envelope.CodeRunning,
			wantData: map[string]any{"harnesses": []string{"claudecode"}, "running": []app.RunningHarness{}},
		},
		{name: "project refused", err: &app.ProjectRefusedError{}, wantCode: envelope.CodeProjectRefused},
		{name: "apply consent carries the project plan", err: &app.ApplyConsentError{Plan: projectPlan}, wantCode: envelope.CodeConsent, wantData: projectPlan},
		{name: "apply refused carries the project plan", err: &app.ApplyRefusedError{Plan: projectPlan}, wantCode: envelope.CodeApplyRefused, wantData: projectPlan},
		{name: "record invalid", err: &app.RecordInvalidError{Path: "/home/u/.dotai/projects/x/record.json"}, wantCode: envelope.CodeRecordInvalid},
		{
			name:     "restore conflicts are internal and carry the conflicts",
			err:      &app.ConflictError{Conflicts: conflicts},
			wantCode: envelope.CodeInternal,
			wantData: map[string]any{"conflicts": conflicts},
		},
		{name: "pack name taken", err: &app.PackNameTakenError{ID: "local/x", Path: "/p"}, wantCode: envelope.CodePackNameTaken},
		{name: "host unsupported", err: &app.HostUnsupportedError{URL: "https://gitlab.com/a/b", Host: "gitlab.com"}, wantCode: envelope.CodeHostUnsupported},
		{name: "git missing", err: &app.GitUnavailableError{}, wantCode: envelope.CodeGitMissing},
		{name: "git too old", err: &app.GitUnavailableError{Version: "2.40.0", TooOld: true}, wantCode: envelope.CodeGitTooOld},
		{name: "fetch failed", err: &app.FetchFailedError{URL: "https://github.com/a/b", Err: errors.New("exit 128")}, wantCode: envelope.CodeFetchFailed},
		{name: "fetch by commit refused", err: &app.FetchByCommitRefusedError{URL: "https://github.com/a/b"}, wantCode: envelope.CodeFetchByCommit},
		{
			name:     "a refused tree carries its problems",
			err:      &app.PackTreeRefusedError{Problems: []app.TreeProblem{{Path: "l", Problem: "is a link"}}},
			wantCode: envelope.CodeTreeRefused,
			wantData: map[string]any{"problems": []app.TreeProblem{{Path: "l", Problem: "is a link"}}},
		},
		{name: "an update without consent carries its plan", err: &app.PackConsentError{Plan: app.PackUpdatePlan{ID: "github.com/a/b"}}, wantCode: envelope.CodeConsent, wantData: app.PackUpdatePlan{ID: "github.com/a/b"}},
		{name: "wrapped errors are found", err: fmt.Errorf("wrapped: %w", &app.ConsentError{Plan: installPlan}), wantCode: envelope.CodeConsent, wantData: installPlan},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			code, data := envelope.Classify(tt.err)
			if code != tt.wantCode {
				t.Errorf("Classify() code = %q, want %q", code, tt.wantCode)
			}
			if !reflect.DeepEqual(data, tt.wantData) {
				t.Errorf("Classify() data = %#v, want %#v", data, tt.wantData)
			}
		})
	}
}

func TestFromError(t *testing.T) {
	plan := app.InstallPlan{Mode: "replace"}
	err := &app.ConsentError{Plan: plan}
	e := envelope.FromError("install", err)
	if e.OK || e.Schema != envelope.Schema || e.Command != "install" {
		t.Fatalf("FromError() = %+v, want a failure of install", e)
	}
	if e.Error == nil || e.Error.Code != envelope.CodeConsent || e.Error.Message != err.Error() {
		t.Errorf("FromError() error = %+v, want code %q and the error's message", e.Error, envelope.CodeConsent)
	}
	if !reflect.DeepEqual(e.Data, plan) {
		t.Errorf("FromError() data = %#v, want the plan", e.Data)
	}
}
