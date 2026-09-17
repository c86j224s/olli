package subagent

import "testing"

func TestCodeReportFromEvidenceUsesSuccessfulWrites(t *testing.T) {
	step := PlanStep{ID: "step-1", Acceptance: []string{"feature complete"}}
	evidence := &executionEvidence{SuccessfulCalls: []successfulToolCall{
		{Name: "view_file", Arguments: map[string]interface{}{"file_path": "main.go"}},
		{Name: "replace_file", Arguments: map[string]interface{}{"file_path": "main.go"}, Succeeded: true},
		{Name: "edit_file", Arguments: map[string]interface{}{"file_path": "main.go"}, Succeeded: true},
	}}
	report := codeReportFromEvidence(step, evidence)
	if report.StepID != "step-1" || len(report.ChangedFiles) != 1 || report.ChangedFiles[0] != "main.go" || len(report.Unresolved) == 0 || !report.EvidenceDerived {
		t.Fatalf("unexpected evidence report: %#v", report)
	}
}
