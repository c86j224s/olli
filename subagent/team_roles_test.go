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
	if _, err := reg.Execute("replace_file", map[string]interface{}{"file_path": "outside.go", "content": "package changed\n"}); err == nil || !strings.Contains(err.Error(), "outside allowed_files") {
		t.Fatalf("coder replaced an unplanned file: %v", err)
	}
	if _, err := reg.Execute("view_file", map[string]interface{}{"file_path": "allowed.go"}); err != nil {
		t.Fatalf("coder could not inspect allowed file: %v", err)
	}
	if _, err := reg.Execute("edit_file", map[string]interface{}{"file_path": "allowed.go", "target_content": "", "replacement_content": "package changed\n"}); err == nil || !strings.Contains(err.Error(), "use replace_file") {
		t.Fatalf("coder used edit_file for whole-file replacement: %v", err)
	}
	if _, err := reg.Execute("replace_file", map[string]interface{}{"file_path": "allowed.go", "content": "package demo\n\nvar Changed = true\n"}); err != nil {
		t.Fatalf("coder could not replace an allowed file: %v", err)
	}
	content, err := os.ReadFile(filepath.Join(root, "outside.go"))
	if err != nil || string(content) != "package demo\n" {
		t.Fatalf("unplanned file changed: %q, %v", content, err)
	}
}

func TestTeamCoderRegistryGuidesCreationOfMissingAllowedFile(t *testing.T) {
	root := t.TempDir()
	reg := tools.NewEmptyRegistry()
	reg.SetWorkspaceRoot(root)
	reg.SetWorkspace(root)
	registerTeamCoderTools(reg, []string{"new.go"})
	result, err := reg.Execute("view_file", map[string]interface{}{"file_path": "new.go"})
	if err != nil || !strings.Contains(result, "does not exist yet") || !strings.Contains(result, "replace_file") {
		t.Fatalf("missing allowed file did not return creation guidance: result=%q err=%v", result, err)
	}
	if _, err := reg.Execute("replace_file", map[string]interface{}{"file_path": "new.go", "content": "package demo\n"}); err != nil {
		t.Fatalf("coder could not create missing allowed file after guidance: %v", err)
	}
}

func TestTeamCoderRegistryReadsPriorPlannedFilesButCannotEditThem(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "prior.go"), []byte("package demo\n\ntype Shared struct{}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	reg := tools.NewEmptyRegistry()
	reg.SetWorkspaceRoot(root)
	reg.SetWorkspace(root)
	registerTeamCoderTools(reg, []string{"current.go"}, []string{"prior.go"})
	if _, err := reg.Execute("view_file", map[string]interface{}{"file_path": "prior.go"}); err != nil {
		t.Fatalf("coder could not inspect prior planned file: %v", err)
	}
	if _, err := reg.Execute("replace_file", map[string]interface{}{"file_path": "prior.go", "content": "package demo\n"}); err == nil || !strings.Contains(err.Error(), "outside allowed_files") {
		t.Fatalf("coder modified read-only prior file: %v", err)
	}
}

func TestTeamCoderRegistryNormalizesEquivalentAllowedPaths(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "allowed.go"), []byte("package demo\n"), 0600); err != nil {
		t.Fatal(err)
	}
	reg := tools.NewEmptyRegistry()
	reg.SetWorkspaceRoot(root)
	reg.SetWorkspace(root)
	registerTeamCoderTools(reg, []string{"allowed.go"})
	if _, err := reg.Execute("view_file", map[string]interface{}{"file_path": "./allowed.go"}); err != nil {
		t.Fatal(err)
	}
	if _, err := reg.Execute("replace_file", map[string]interface{}{"file_path": "allowed.go", "content": "package changed\n"}); err != nil {
		t.Fatalf("equivalent inspected path was not recognized: %v", err)
	}
}

func TestTeamCoderReplacementRejectsPackageTypeErrorBeforeWrite(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "allowed.go")
	original := "package demo\n\nvar Value = 1\n"
	if err := os.WriteFile(path, []byte(original), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "existing.go"), []byte("package demo\n\ntype Shared struct{}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	reg := tools.NewEmptyRegistry()
	reg.SetWorkspaceRoot(root)
	reg.SetWorkspace(root)
	registerTeamCoderTools(reg, []string{"allowed.go"})
	if _, err := reg.Execute("view_file", map[string]interface{}{"file_path": "allowed.go"}); err != nil {
		t.Fatal(err)
	}
	_, err := reg.Execute("replace_file", map[string]interface{}{"file_path": "allowed.go", "content": "package demo\n\ntype Shared struct{}\n"})
	if err == nil || !strings.Contains(err.Error(), "package type check") {
		t.Fatalf("package type error was accepted: %v", err)
	}
	data, readErr := os.ReadFile(path)
	if readErr != nil || string(data) != original {
		t.Fatalf("package type error changed original: %q, %v", data, readErr)
	}
}

func TestTeamCoderGoEditPreservesOriginalOnSyntaxError(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "allowed.go")
	original := "package demo\n\nfunc Value() int { return 1 }\n"
	if err := os.WriteFile(path, []byte(original), 0600); err != nil {
		t.Fatal(err)
	}
	reg := tools.NewEmptyRegistry()
	reg.SetWorkspaceRoot(root)
	reg.SetWorkspace(root)
	registerTeamCoderTools(reg, []string{"allowed.go"})
	if _, err := reg.Execute("view_file", map[string]interface{}{"file_path": "allowed.go"}); err != nil {
		t.Fatal(err)
	}
	_, err := reg.Execute("edit_file", map[string]interface{}{
		"file_path": "allowed.go", "target_content": "return 1", "replacement_content": "return (",
	})
	if err == nil || !strings.Contains(err.Error(), "original preserved") {
		t.Fatalf("syntax-breaking edit was accepted: %v", err)
	}
	data, readErr := os.ReadFile(path)
	if readErr != nil || string(data) != original {
		t.Fatalf("syntax-breaking edit changed original: %q, %v", data, readErr)
	}
}

func TestReviewSourceSnapshotsCaptureCompleteSafeFiles(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "one.go"), []byte("package demo\n\nvar One = 1\n"), 0600); err != nil {
		t.Fatal(err)
	}
	snapshots := reviewSourceSnapshots([]string{"one.go", "missing.go"}, root)
	if snapshots["one.go"] != "package demo\n\nvar One = 1\n" {
		t.Fatalf("review snapshot was incomplete: %#v", snapshots)
	}
	missing := filesMissingSnapshots([]string{"one.go", "missing.go"}, snapshots)
	if len(missing) != 1 || missing[0] != "missing.go" {
		t.Fatalf("missing review snapshots were not tracked: %v", missing)
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
	for _, tool := range []string{"edit_file", "append_file", "insert_content", "execute_action", "list_dir", "grep_search"} {
		if _, ok := reg.GetDefinition(tool); ok {
			t.Fatalf("reviewer received unnecessary or mutating tool %s", tool)
		}
	}
	if _, ok := reg.GetDefinition("view_file"); !ok {
		t.Fatal("reviewer is missing view_file")
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
	root := t.TempDir()
	for _, name := range []string{"a.go", "b.go"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte("package demo\n"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	files := []string{"a.go", "b.go"}
	evidence := &executionEvidence{SuccessfulCalls: []successfulToolCall{{Name: "view_file", Arguments: map[string]interface{}{"file_path": "a.go"}}}}
	if err := requireReviewerFileEvidence(files, evidence, root); err == nil {
		t.Fatal("reviewer evidence omitted a changed file")
	}
	evidence.SuccessfulCalls = append(evidence.SuccessfulCalls, successfulToolCall{Name: "view_file", Arguments: map[string]interface{}{"file_path": "b.go"}})
	if err := requireReviewerFileEvidence(files, evidence, root); err != nil {
		t.Fatalf("complete reviewer evidence rejected: %v", err)
	}
}

func TestRequireExecutedActionEvidenceMatchesActionAndTarget(t *testing.T) {
	evidence := &executionEvidence{AttemptedCalls: []successfulToolCall{{Name: "execute_action", Arguments: map[string]interface{}{"action": "go_test", "target": "./..."}, Succeeded: true, ExitCode: 0}}}
	report := &TestReport{Passed: true, Commands: []CommandResult{{Command: "go_test ./...", ExitCode: 0}}}
	if err := requireExecutedActionEvidence([]string{"go_test ./..."}, report, evidence); err != nil {
		t.Fatalf("matching execution evidence rejected: %v", err)
	}
	if err := requireExecutedActionEvidence([]string{"go_vet ./..."}, report, evidence); err == nil {
		t.Fatal("missing execution evidence accepted")
	}
}

func TestRequireExecutedActionEvidenceRejectsFabricatedSuccess(t *testing.T) {
	evidence := &executionEvidence{AttemptedCalls: []successfulToolCall{{Name: "execute_action", Arguments: map[string]interface{}{"action": "go_vet", "target": "./..."}, Succeeded: false, ExitCode: 2}}}
	report := &TestReport{Passed: true, Commands: []CommandResult{{Command: "go_vet ./...", ExitCode: 0}}}
	if err := requireExecutedActionEvidence([]string{"go_vet ./..."}, report, evidence); err == nil {
		t.Fatal("fabricated passing exit code was accepted for failed execution")
	}
	report.Passed = false
	report.Commands[0].ExitCode = 1
	if err := requireExecutedActionEvidence([]string{"go_vet ./..."}, report, evidence); err == nil {
		t.Fatal("fabricated nonzero exit code was accepted")
	}
	report.Commands[0].ExitCode = 2
	if err := requireExecutedActionEvidence([]string{"go_vet ./..."}, report, evidence); err != nil {
		t.Fatalf("matching failed exit code evidence rejected: %v", err)
	}
}

func TestTeamModelsUseRoleOverridesAndFallback(t *testing.T) {
	models := (TeamModels{Planner: "planner", Tester: "tester", LogicReviewer: "logic"}).withFallback("fallback")
	if models.Planner != "planner" || models.Tester != "tester" || models.Coder != "fallback" || models.TestCoder != "fallback" || models.Reviewer != "fallback" {
		t.Fatalf("unexpected model selection: %#v", models)
	}
	if models.Cassandra != "fallback" || models.DetailPlanner != "planner" {
		t.Fatalf("unexpected planning model selection: %#v", models)
	}
	if models.reviewerModel(ReviewDimensionLogic) != "logic" || models.reviewerModel(ReviewDimensionSafety) != "fallback" {
		t.Fatalf("unexpected specialist model selection: %#v", models)
	}
}

func TestTestOnlyPlanStep(t *testing.T) {
	if !testOnlyPlanStep(PlanStep{AllowedFiles: []string{"one_test.go", "two_test.go"}}) {
		t.Fatal("test-only milestone was not detected")
	}
	if testOnlyPlanStep(PlanStep{AllowedFiles: []string{"one_test.go", "logic.go"}}) {
		t.Fatal("mixed milestone was classified as test-only")
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
