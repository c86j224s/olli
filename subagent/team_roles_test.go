package subagent

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/c86j224s/olli/tools"
)

func TestTeamCoderRegistryEnforcesAllowedFiles(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "allowed.go"), []byte("package demo\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "outside.go"), []byte("package demo\n"), 0600); err != nil {
		t.Fatal(err)
	}
	reg := tools.NewEmptyRegistry()
	reg.SetWorkspaceRoot(root)
	reg.SetWorkspace(root)
	registerTeamCoderTools(reg, []string{"allowed.go"})

	if _, err := reg.Execute("edit_file", map[string]interface{}{
		"file_path": "outside.go", "target_content": "package demo", "replacement_content": "package changed",
	}); err == nil || !strings.Contains(err.Error(), "outside allowed_files") {
		t.Fatalf("coder edited an unplanned file: %v", err)
	}
	content, err := os.ReadFile(filepath.Join(root, "outside.go"))
	if err != nil || string(content) != "package demo\n" {
		t.Fatalf("unplanned file changed: %q, %v", content, err)
	}
}

func TestTeamTesterRegistryCannotEditFiles(t *testing.T) {
	root := t.TempDir()
	reg := tools.NewEmptyRegistry()
	reg.SetWorkspaceRoot(root)
	reg.SetWorkspace(root)
	registerTeamTesterTools(reg)
	if _, ok := reg.GetDefinition("edit_file"); ok {
		t.Fatal("tester received edit_file")
	}
	definition, ok := reg.GetDefinition("execute_action")
	if !ok {
		t.Fatal("tester is missing execute_action")
	}
	for _, action := range definition.Function.Parameters.Properties["action"].Enum {
		if action == "go_build" {
			t.Fatal("tester received mutating go_build action")
		}
	}
	if _, err := reg.ExecuteContext(context.Background(), "execute_action", map[string]interface{}{"action": "go_build", "target": "."}); err == nil {
		t.Fatal("tester executed mutating go_build action")
	}
}

func TestTeamReviewerRegistryIsReadOnly(t *testing.T) {
	root := t.TempDir()
	reg := tools.NewEmptyRegistry()
	reg.SetWorkspaceRoot(root)
	reg.SetWorkspace(root)
	registerTeamReviewerTools(reg)
	for _, tool := range []string{"edit_file", "append_file", "insert_content", "execute_action"} {
		if _, ok := reg.GetDefinition(tool); ok {
			t.Fatalf("reviewer received mutation tool %s", tool)
		}
	}
}

func TestParseTeamReportsRejectsUnknownFields(t *testing.T) {
	if _, err := parseCodeReport(`{"step_id":"step-1","changed_files":["a.go"],"completed":["done"],"unresolved":[],"extra":true}`); err == nil {
		t.Fatal("unknown coder report field accepted")
	}
	if _, err := parseCodeReport(`{"step_id":"step-1","changed_files":["a.go"],"completed":["done"],"unresolved":[]} trailing`); err == nil {
		t.Fatal("trailing coder output accepted")
	}
	if _, err := parseTestReport(`{"passed":true,"commands":[],"summary":"ok","extra":true}`); err == nil {
		t.Fatal("unknown tester report field accepted")
	}
	if _, err := parseReviewReport(`{"findings":[],"summary":"ok","extra":true}`); err == nil {
		t.Fatal("unknown reviewer report field accepted")
	}
}

func TestRequireCoderEditEvidenceMatchesReportedFiles(t *testing.T) {
	report := &CodeReport{ChangedFiles: []string{"a.go"}}
	evidence := &executionEvidence{SuccessfulCalls: []successfulToolCall{{Name: "edit_file", Arguments: map[string]interface{}{"file_path": "b.go"}}}}
	if err := requireCoderEditEvidence(report, evidence); err == nil {
		t.Fatal("coder report without matching edit evidence accepted")
	}
	evidence.SuccessfulCalls = []successfulToolCall{{Name: "edit_file", Arguments: map[string]interface{}{"file_path": "a.go"}}}
	if err := requireCoderEditEvidence(report, evidence); err != nil {
		t.Fatalf("matching coder edit evidence rejected: %v", err)
	}
	evidence.SuccessfulCalls = append(evidence.SuccessfulCalls, successfulToolCall{Name: "edit_file", Arguments: map[string]interface{}{"file_path": "c.go"}})
	if err := requireCoderEditEvidence(report, evidence); err == nil {
		t.Fatal("unreported successful edit was accepted")
	}
}

func TestRequireReviewerFileEvidenceCoversEveryChangedFile(t *testing.T) {
	reports := []CodeReport{{ChangedFiles: []string{"a.go", "b.go"}}}
	evidence := &executionEvidence{SuccessfulCalls: []successfulToolCall{{Name: "view_file", Arguments: map[string]interface{}{"file_path": "a.go"}}}}
	if err := requireReviewerFileEvidence(reports, evidence); err == nil {
		t.Fatal("reviewer evidence omitted a changed file")
	}
	evidence.SuccessfulCalls = append(evidence.SuccessfulCalls, successfulToolCall{Name: "view_file", Arguments: map[string]interface{}{"file_path": "b.go"}})
	if err := requireReviewerFileEvidence(reports, evidence); err != nil {
		t.Fatalf("complete reviewer evidence rejected: %v", err)
	}
}

func TestRequireExecutedActionEvidenceMatchesActionAndTarget(t *testing.T) {
	evidence := &executionEvidence{SuccessfulCalls: []successfulToolCall{{Name: "execute_action", Arguments: map[string]interface{}{"action": "go_test", "target": "./..."}}}}
	if err := requireExecutedActionEvidence([]string{"go_test ./..."}, evidence); err != nil {
		t.Fatalf("matching execution evidence rejected: %v", err)
	}
	if err := requireExecutedActionEvidence([]string{"go_vet ./..."}, evidence); err == nil {
		t.Fatal("missing execution evidence accepted")
	}
}

func TestTeamModelsUseRoleOverridesAndFallback(t *testing.T) {
	models := (TeamModels{Planner: "planner", Tester: "tester"}).withFallback("fallback")
	if models.Planner != "planner" || models.Tester != "tester" || models.Coder != "fallback" || models.Reviewer != "fallback" {
		t.Fatalf("unexpected model selection: %#v", models)
	}
}

func TestNewModelTeamRolesRequiresRunnerClient(t *testing.T) {
	if _, err := NewModelTeamRoles(nil); err == nil {
		t.Fatal("nil team role runner accepted")
	}
	if _, err := NewModelTeamRoles(&SubagentRunner{}); err == nil {
		t.Fatal("runner without client accepted")
	}
}
