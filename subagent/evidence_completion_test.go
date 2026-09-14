package subagent

import (
	"encoding/json"
	"testing"
)

func TestBuildEvidenceCompletionForCoderFix(t *testing.T) {
	task := CodeTask{
		Goal: "fix the defect",
		Step: PlanStep{
			ID:           "step-review-fix-1",
			Objective:    "fix review finding",
			AllowedFiles: []string{"main.go"},
			Acceptance:   []string{"piece rotation remains stable"},
		},
		ReviewFixes: []Finding{{ID: "rotation-1"}},
	}
	payload, err := json.Marshal(task)
	if err != nil {
		t.Fatal(err)
	}
	evidence := &executionEvidence{SuccessfulCalls: []successfulToolCall{{
		Name: "edit_file", Arguments: map[string]interface{}{"file_path": "main.go"}, Succeeded: true,
	}}}
	raw, err := buildEvidenceCompletion(string(TypeCoder), string(payload), evidence)
	if err != nil {
		t.Fatal(err)
	}
	report, err := parseCodeReport(raw)
	if err != nil {
		t.Fatal(err)
	}
	if report.StepID != task.Step.ID || len(report.ChangedFiles) != 1 || report.ChangedFiles[0] != "main.go" {
		t.Fatalf("unexpected evidence completion: %#v", report)
	}
	if len(report.Unresolved) != 0 || len(report.AddressedFindings) != 1 || report.AddressedFindings[0].ID != "rotation-1" {
		t.Fatalf("fix evidence was not carried into coder completion: %#v", report)
	}
	if report.EvidenceDerived {
		t.Fatal("successful deterministic completion was marked unresolved")
	}
}

func TestBuildEvidenceCompletionForCoderMarksUnchangedFindingUnaddressed(t *testing.T) {
	task := CodeTask{
		Step:        PlanStep{ID: "step-review-fix-1", AllowedFiles: []string{"a.go", "b.go"}, Acceptance: []string{"fixed"}},
		ReviewFixes: []Finding{{ID: "finding-b", File: "b.go"}},
	}
	payload, _ := json.Marshal(task)
	evidence := &executionEvidence{SuccessfulCalls: []successfulToolCall{{
		Name: "edit_file", Arguments: map[string]interface{}{"file_path": "a.go"}, Succeeded: true,
	}}}
	raw, err := buildEvidenceCompletion(string(TypeCoder), string(payload), evidence)
	if err != nil {
		t.Fatal(err)
	}
	report, err := parseCodeReport(raw)
	if err != nil {
		t.Fatal(err)
	}
	if len(report.AddressedFindings) != 1 || report.AddressedFindings[0].Status != "not_addressed" {
		t.Fatalf("unchanged finding file was claimed addressed: %#v", report)
	}
}

func TestBuildEvidenceCompletionForTesterUsesActualExitCodes(t *testing.T) {
	task := `{"required_commands":["go_test ./...","go_vet ./..."]}`
	evidence := &executionEvidence{AttemptedCalls: []successfulToolCall{
		{Name: "execute_action", Arguments: map[string]interface{}{"action": "go_test", "target": "./..."}, Result: "ok", ExitCode: 0},
		{Name: "execute_action", Arguments: map[string]interface{}{"action": "go_vet", "target": "./..."}, Result: "failed", ExitCode: 2},
	}}
	raw, err := buildEvidenceCompletion(string(TypeTester), task, evidence)
	if err != nil {
		t.Fatal(err)
	}
	report, err := parseTestReport(raw)
	if err != nil {
		t.Fatal(err)
	}
	if report.Passed || len(report.Commands) != 2 || report.Commands[1].ExitCode != 2 {
		t.Fatalf("actual tester failure was not preserved: %#v", report)
	}
}

func TestBuildEvidenceCompletionLeavesReviewerModelAuthored(t *testing.T) {
	if _, err := buildEvidenceCompletion(string(TypeReviewer), `{}`, &executionEvidence{}); err == nil {
		t.Fatal("reviewer completion must remain model-authored")
	}
}
