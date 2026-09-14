package subagent

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/c86j224s/olli/tools"
)

func validDevelopmentPlan() *DevelopmentPlan {
	return &DevelopmentPlan{
		Goal:  "Add a read-only planner role",
		Files: []string{"subagent/planner.go", "subagent/contracts.go", "agent/subagents.go"},
		Steps: []PlanStep{{
			ID:           "step-1",
			Objective:    "Add validated planner output",
			AllowedFiles: []string{"subagent/planner.go", "subagent/contracts.go"},
			Acceptance:   []string{"planner cannot mutate files", "invalid paths are rejected"},
			Verification: []string{"go_test ./subagent"},
		}},
		FinalVerification: []string{"go_test ./...", "go_vet ./..."},
	}
}

func TestValidateDevelopmentPlanAcceptsBoundedRelativePlan(t *testing.T) {
	plan := validDevelopmentPlan()
	if err := validateDevelopmentPlan(plan); err != nil {
		t.Fatalf("valid plan rejected: %v", err)
	}
}

func TestValidateDevelopmentPlanRejectsUnsafeOrAmbiguousPlans(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(*DevelopmentPlan)
		wantErr string
	}{
		{name: "absolute path", mutate: func(plan *DevelopmentPlan) {
			plan.Steps[0].AllowedFiles = []string{filepath.Join(string(filepath.Separator), "absolute", "file.go")}
		}, wantErr: "workspace-relative"},
		{name: "path escape", mutate: func(plan *DevelopmentPlan) { plan.Steps[0].AllowedFiles = []string{"../file.go"} }, wantErr: "escapes"},
		{name: "duplicate step", mutate: func(plan *DevelopmentPlan) { plan.Steps = append(plan.Steps, plan.Steps[0]) }, wantErr: "duplicate"},
		{name: "missing files", mutate: func(plan *DevelopmentPlan) { plan.Files = nil }, wantErr: "requires files"},
		{name: "blank final verification", mutate: func(plan *DevelopmentPlan) { plan.FinalVerification = []string{" "} }, wantErr: "non-empty final_verification"},
		{name: "shell-style verification", mutate: func(plan *DevelopmentPlan) { plan.FinalVerification = []string{"go test ./..."} }, wantErr: "command must be"},
		{name: "mutating verification", mutate: func(plan *DevelopmentPlan) { plan.FinalVerification = []string{"go_build ."} }, wantErr: "not allowed"},
		{name: "blank acceptance", mutate: func(plan *DevelopmentPlan) { plan.Steps[0].Acceptance = []string{" "} }, wantErr: "non-empty acceptance"},
		{name: "step file missing from plan", mutate: func(plan *DevelopmentPlan) { plan.Files = []string{"other.go"} }, wantErr: "missing from plan files"},
		{name: "missing acceptance", mutate: func(plan *DevelopmentPlan) { plan.Steps[0].Acceptance = nil }, wantErr: "acceptance"},
		{name: "too many steps", mutate: func(plan *DevelopmentPlan) {
			step := plan.Steps[0]
			plan.Steps = nil
			for index := 1; index <= maxPlanSteps+1; index++ {
				copy := step
				copy.ID = "step-" + strconv.Itoa(index)
				plan.Steps = append(plan.Steps, copy)
			}
		}, wantErr: "1-12"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			plan := validDevelopmentPlan()
			tt.mutate(plan)
			err := validateDevelopmentPlan(plan)
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("expected error containing %q, got %v", tt.wantErr, err)
			}
		})
	}
}

func TestValidateDevelopmentPlanAllowsBoundedMilestonesForOneFile(t *testing.T) {
	plan := validDevelopmentPlan()
	plan.Files = []string{"subagent/planner.go"}
	plan.Steps[0].AllowedFiles = []string{"subagent/planner.go"}
	plan.Steps[0].Objective = "Add parseable data structures"
	plan.Steps = append(plan.Steps, PlanStep{ID: "step-2", Objective: "Complete behavior", AllowedFiles: []string{"subagent/planner.go"}, Acceptance: []string{"entire objective works"}})
	if err := validateDevelopmentPlan(plan); err != nil {
		t.Fatalf("bounded single-file milestones rejected: %v", err)
	}
}

func TestValidateDevelopmentPlanAddsCoreVerification(t *testing.T) {
	plan := validDevelopmentPlan()
	plan.FinalVerification = []string{"git_status"}
	if err := validateDevelopmentPlan(plan); err != nil {
		t.Fatalf("safe planner verification defaulting failed: %v", err)
	}
	joined := strings.Join(plan.FinalVerification, "|")
	if !strings.Contains(joined, "go_test ./...") || !strings.Contains(joined, "go_vet ./...") {
		t.Fatalf("core final verification was not added: %v", plan.FinalVerification)
	}
}

func TestParseDevelopmentPlanRejectsUnknownFields(t *testing.T) {
	raw := `{"goal":"g","assumptions":[],"files":["a.go"],"steps":[{"id":"step-1","objective":"o","allowed_files":["a.go"],"acceptance":["a"],"verification":[]}],"risks":[],"final_verification":["go_test"],"unexpected":true}`
	if _, err := parseDevelopmentPlan(raw); err == nil {
		t.Fatal("unknown planner output field was accepted")
	}
}

func TestNormalizePlannerToolPathRejectsOutsideAbsoluteRetargeting(t *testing.T) {
	workspace := filepath.Join(string(filepath.Separator), "workspace")
	outside := filepath.Join(string(filepath.Separator), "outside", "file.go")
	if got := normalizePlannerToolPath(outside, workspace); got != outside {
		t.Fatalf("outside absolute path was retargeted: %q", got)
	}
	inside := filepath.Join(workspace, "file.go")
	if got := normalizePlannerToolPath(inside, workspace); got != "file.go" {
		t.Fatalf("inside absolute path was not normalized: %q", got)
	}
}

func TestPlannerRegistryIsReadOnly(t *testing.T) {
	root := t.TempDir()
	workspace := filepath.Join(root, "workspace")
	if err := os.Mkdir(workspace, 0755); err != nil {
		t.Fatal(err)
	}
	reg := tools.NewEmptyRegistry()
	reg.SetWorkspaceRoot(root)
	reg.SetWorkspace(workspace)
	registerPlannerTools(reg)
	for _, forbidden := range []string{"edit_file", "append_file", "insert_content", "execute_action"} {
		if _, ok := reg.GetDefinition(forbidden); ok {
			t.Fatalf("planner registered mutation tool %s", forbidden)
		}
	}
	for _, required := range []string{"list_dir", "grep_search", "view_file"} {
		if _, ok := reg.GetDefinition(required); !ok {
			t.Fatalf("planner is missing read-only tool %s", required)
		}
	}
}
