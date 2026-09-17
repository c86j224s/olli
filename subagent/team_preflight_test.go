package subagent

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestStaticPreflightTypeChecksChangedGoFiles(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "main.go"), []byte("package main\n\nvar Value int = \"bad\"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	report := runStaticPreflight(context.Background(), root, []string{"main.go"})
	if report.Passed || report.Commands[0].ExitCode == 0 || !strings.Contains(report.Commands[0].Output, "type check") {
		t.Fatalf("type error passed static preflight: %#v", report)
	}
}

func TestStaticPreflightAcceptsValidGoFile(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "main.go"), []byte("package main\n\nvar Value int = 1\n"), 0600); err != nil {
		t.Fatal(err)
	}
	report := runStaticPreflight(context.Background(), root, []string{"main.go"})
	if !report.Passed || report.Commands[0].ExitCode != 0 {
		t.Fatalf("valid source failed static preflight: %#v", report)
	}
}

func TestStaticPreflightIncludesUnchangedPackageFiles(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "changed.go"), []byte("package demo\n\nfunc Value() string { return helper() }\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "helper.go"), []byte("package demo\n\nfunc helper() int { return 1 }\n"), 0600); err != nil {
		t.Fatal(err)
	}
	report := runStaticPreflight(context.Background(), root, []string{"changed.go"})
	if report.Passed || !strings.Contains(report.Commands[0].Output, "cannot use") {
		t.Fatalf("cross-file type error passed static preflight: %#v", report)
	}
}

func TestStaticPreflightRejectsMultipleImplementationPackagesInDirectory(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "one.go"), []byte("package one\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "two.go"), []byte("package two\n"), 0600); err != nil {
		t.Fatal(err)
	}
	report := runStaticPreflight(context.Background(), root, []string{"two.go"})
	if report.Passed || !strings.Contains(report.Commands[0].Output, "multiple non-test Go packages") {
		t.Fatalf("mixed implementation packages passed preflight: %#v", report)
	}
}

func TestStaticPreflightIgnoresTestPackages(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "main.go"), []byte("package demo\n\nvar Value = 1\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "main_test.go"), []byte("package demo_test\n\nvar Broken int = \"bad\"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	report := runStaticPreflight(context.Background(), root, []string{"main.go"})
	if !report.Passed {
		t.Fatalf("test-only package should be left to go_test: %#v", report)
	}
}

func TestDevelopmentTeamRoutesStaticFailureDirectlyToCoder(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "feature.go")
	if err := os.WriteFile(path, []byte("package demo\n\nvar Value int = \"bad\"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	findingID := "TESTS-STATIC-PREFLIGHT"
	roles := &preflightFixRoles{
		scriptedTeamRoles: &scriptedTeamRoles{
			plan: &DevelopmentPlan{
				Goal: "fix feature", Files: []string{"feature.go"},
				Steps:             []PlanStep{{ID: "step-1", Objective: "implement", AllowedFiles: []string{"feature.go"}, Acceptance: []string{"valid source"}}},
				FinalVerification: []string{"go_test ./...", "go_vet ./..."},
			},
			codeReports: []*CodeReport{
				{StepID: "step-1", ChangedFiles: []string{"feature.go"}, Completed: []string{"draft"}},
				{StepID: "step-review-fix-1", ChangedFiles: []string{"feature.go"}, Completed: []string{"type fixed"}, AddressedFindings: []AddressedFinding{{ID: findingID, Status: "addressed", Evidence: "changed declaration"}}},
			},
			reviews:      []*ReviewReport{{Summary: "clean"}},
			verification: &TestReport{Passed: true, Commands: []CommandResult{passingCommand("go_test ./..."), passingCommand("go_vet ./...")}},
		},
		path: path,
	}
	runner, _ := NewDevelopmentTeamRunner(roles, 2)
	runner = runner.WithWorkspace(root)
	report := runner.Run(context.Background(), "fix feature")
	if report.Status != "SUCCESS" || report.FixRounds != 1 || roles.reviewCalls != len(defaultReviewDimensions) {
		t.Fatalf("static failure did not route through one coder fix then assembled semantic review: %#v reviews=%d", report, roles.reviewCalls)
	}
	if len(report.Preflights) != 2 || report.Preflights[0].Passed || !report.Preflights[1].Passed {
		t.Fatalf("static preflight evidence missing: %#v", report.Preflights)
	}
}

type preflightFixRoles struct {
	*scriptedTeamRoles
	path string
}

func (p *preflightFixRoles) Code(ctx context.Context, task CodeTask) (*CodeReport, error) {
	report, err := p.scriptedTeamRoles.Code(ctx, task)
	if err != nil {
		return nil, err
	}
	if task.Step.ID == "step-review-fix-1" {
		if err := os.WriteFile(p.path, []byte("package demo\n\nvar Value int = 1\n"), 0600); err != nil {
			return nil, err
		}
	}
	return report, nil
}
