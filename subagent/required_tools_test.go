package subagent

import "testing"

func TestExecutionEvidenceAcceptsAnyRequiredWriterTool(t *testing.T) {
	evidence := &executionEvidence{RequiredAnyTools: []string{"edit_file", "replace_file"}}
	if missing := evidence.missingRequiredTools(); len(missing) != 1 || missing[0] != "one of edit_file/replace_file" {
		t.Fatalf("unexpected writer requirement: %v", missing)
	}
	evidence.recordSuccess(successfulToolCall{Name: "replace_file", Succeeded: true})
	if missing := evidence.missingRequiredTools(); len(missing) != 0 {
		t.Fatalf("replace_file did not satisfy writer requirement: %v", missing)
	}
}

func TestExecutionEvidenceTracksMissingRequiredTools(t *testing.T) {
	evidence := &executionEvidence{RequiredTools: map[string]int{"view_file": 1, "edit_file": 1}}
	missing := evidence.missingRequiredTools()
	if len(missing) != 2 || missing[0] != "edit_file" || missing[1] != "view_file" {
		t.Fatalf("unexpected missing tools: %v", missing)
	}
	evidence.recordSuccess(successfulToolCall{Name: "view_file", Succeeded: true})
	missing = evidence.missingRequiredTools()
	if len(missing) != 1 || missing[0] != "edit_file" {
		t.Fatalf("successful required tool was not counted: %v", missing)
	}
	evidence.recordSuccess(successfulToolCall{Name: "edit_file", Succeeded: true})
	if missing = evidence.missingRequiredTools(); len(missing) != 0 {
		t.Fatalf("completed required tools still missing: %v", missing)
	}
}
