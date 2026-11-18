package cli_test

import (
	"bytes"
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/lartweib/dotai/internal/app"
	"github.com/lartweib/dotai/internal/cli"
	"github.com/lartweib/dotai/internal/plan"
)

// templateWarning is the warning of a plan that installs a file of the
// pack template unchanged (decision 0046).
var templateWarning = plan.Warning{Code: app.CodeTemplateUnchanged, Item: "base/.rulesync/rules/example-rule.md",
	Message: "base/.rulesync/rules/example-rule.md is an example file of dotai's pack template, unchanged"}

// TestRunInstallAsksOnceForTemplateFiles: an isolated install whose plan
// installs a template file unchanged asks once on a terminal, naming
// only that, and is refused with --json unless --yes.
func TestRunInstallAsksOnceForTemplateFiles(t *testing.T) {
	p := app.InstallPlan{Mode: app.ModeIsolated, Consent: true, Warnings: []plan.Warning{templateWarning},
		Harnesses: []app.HarnessPlan{}, Ops: []app.InstallOp{}, Notices: []string{}, Diagnostics: []app.Diagnostic{}}
	for _, tt := range []struct {
		args      []string
		prompt    bool
		want      bool
		questions int
	}{
		{args: []string{"install"}, prompt: true, want: true, questions: 1},
		{args: []string{"install", "--json"}, prompt: true, want: false},
		{args: []string{"install", "--json", "--yes"}, prompt: true, want: true},
		{args: []string{"install"}, want: false},
	} {
		var stdout bytes.Buffer
		env := testEnv(&stdout, &bytes.Buffer{})
		var questions []string
		if tt.prompt {
			env.Prompt = func(_ context.Context, q string) (bool, error) { questions = append(questions, q); return true, nil }
		}
		var got bool
		env.Install = func(_ context.Context, _, _, _ string, _ app.InstallOptions, _ app.Progress, _ app.Warn, consent app.Consent) (app.InstallResult, error) {
			got, _ = consent(p)
			return app.InstallResult{InstallPlan: p, Warnings: []string{}}, nil
		}
		cli.Run(tt.args, env)
		if got != tt.want || len(questions) != tt.questions {
			t.Errorf("%v: consent %v after %d questions, want %v after %d", tt.args, got, len(questions), tt.want, tt.questions)
		}
		for _, q := range questions {
			if !strings.Contains(q, "installs 1 example file of dotai's pack template, unchanged") || strings.Contains(q, "what dotai does not own") {
				t.Errorf("question = %q, want it to name the template file only", q)
			}
		}
	}
}

func TestRunApplyAsksForTemplateFilesAndConflicts(t *testing.T) {
	p := projectPlan
	p.Warnings = append([]plan.Warning{}, templateWarning)
	p.Ops = append([]plan.ProjectOp{}, p.Ops...)
	var questions []string
	var stdout bytes.Buffer
	env := testEnv(&stdout, &bytes.Buffer{})
	env.Prompt = func(_ context.Context, q string) (bool, error) { questions = append(questions, q); return false, nil }
	env.Apply = func(_ context.Context, _, _, _, _ string, _ app.ApplyOptions, _ app.Progress, consent app.ApplyConsent) (app.ApplyResult, error) {
		if ok, _ := consent(p); !ok {
			return app.ApplyResult{}, &app.ApplyConsentError{Plan: p, Templates: p.TemplateFiles()}
		}
		return app.ApplyResult{ProjectPlan: p}, nil
	}
	if code := cli.Run([]string{"apply", "--profile", "web"}, env); code != cli.ExitInternal || len(questions) != 1 {
		t.Fatalf("exit code %d after %d questions, want a refusal after one", code, len(questions))
	}
	if q := questions[0]; !strings.Contains(q, "installs 1 example file of dotai's pack template, unchanged") {
		t.Errorf("question = %q, want the template file", q)
	}
}

// TestRunApplyConsentForTemplateFiles mirrors the install's matrix for an
// apply whose only consent is a template file: asked once on a terminal,
// refused with --json or without a terminal unless --yes.
func TestRunApplyConsentForTemplateFiles(t *testing.T) {
	p := projectPlan
	p.Consent = true
	p.Warnings = append([]plan.Warning{}, templateWarning)
	p.Ops = nil
	for _, op := range projectPlan.Ops { // the plan's ops without its conflicts
		if op.Action == plan.Conflict || slices.ContainsFunc(op.Keys, func(k plan.ProjectKey) bool { return k.Action == plan.Conflict }) {
			continue
		}
		p.Ops = append(p.Ops, op)
	}
	if p.ConflictConsent() {
		t.Fatal("the plan still has conflicts")
	}
	for _, tt := range []struct {
		args      []string
		prompt    bool
		want      bool
		questions int
	}{
		{args: []string{"apply", "--profile", "web"}, prompt: true, want: true, questions: 1},
		{args: []string{"apply", "--profile", "web", "--json"}, prompt: true, want: false},
		{args: []string{"apply", "--profile", "web", "--json", "--yes"}, prompt: true, want: true},
		{args: []string{"apply", "--profile", "web"}, want: false},
	} {
		var stdout bytes.Buffer
		env := testEnv(&stdout, &bytes.Buffer{})
		var questions []string
		if tt.prompt {
			env.Prompt = func(_ context.Context, q string) (bool, error) { questions = append(questions, q); return true, nil }
		}
		got, called := false, false
		env.Apply = func(_ context.Context, _, _, _, _ string, _ app.ApplyOptions, _ app.Progress, consent app.ApplyConsent) (app.ApplyResult, error) {
			called = true
			got, _ = consent(p)
			return app.ApplyResult{ProjectPlan: p}, nil
		}
		cli.Run(tt.args, env)
		if !called || got != tt.want || len(questions) != tt.questions {
			t.Errorf("%v: consent %v after %d questions (apply called %v), want %v after %d", tt.args, got, len(questions), called, tt.want, tt.questions)
		}
		for _, q := range questions {
			if !strings.Contains(q, "installs 1 example file of dotai's pack template, unchanged") || strings.Contains(q, "conflicts") {
				t.Errorf("question = %q, want it to name the template file only", q)
			}
		}
	}
}
