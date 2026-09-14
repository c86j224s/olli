package subagent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/c86j224s/olli/tools"
)

const architectPlannerPrompt = `ROLE: Read-only software architect for a small-model development team.

TASK:
Inspect the existing workspace and design a coarse implementation architecture. Define module boundaries by reason to change, not by line count. Prefer several small cohesive files over one growing implementation file when the objective has separable state, domain rules, rendering, I/O, or tests.

RULES:
- Never modify files and never run commands.
- Inspect every existing file that constrains the architecture.
- Return 1-8 ordered work packages. A work package owns one cohesive responsibility and a small set of files.
- New files are allowed when they reduce coder context and have a clear package-level responsibility.
- Existing public APIs and entry points must remain explicit in acceptance criteria.
- Each package must leave the repository parseable and identify dependencies on earlier packages.
- Cover the entire objective; do not defer requirements.
- Return JSON only, matching ArchitecturePlan.`

const cassandraPrompt = `ROLE: Cassandra, a hostile read-only reviewer of software architecture plans.

TASK:
Try to prove the proposed architecture will fail before implementation starts.

RULES:
- Never modify files and never run commands.
- Inspect the architecture and original objective, not implementation code.
- Report only concrete planning defects: missing requirement coverage, oversized work packages, incoherent file boundaries, dependency cycles, impossible intermediate compile states, conflicting ownership, or unverifiable acceptance criteria.
- Do not rewrite the plan. Return findings with stable CASSANDRA-* ids and required outcomes.
- passed is true only when no finding remains.
- Return JSON only, matching ArchitectureReview.`

const detailPlannerPrompt = `ROLE: Read-only detail planner for one approved architecture work package.

TASK:
Expand exactly one work package into minimal coder milestones.

RULES:
- Never modify files and never run commands.
- Return 1-6 sequential milestones for this work package only.
- One milestone performs one cohesive state change or behavior and touches as few files as possible.
- Every milestone must leave touched source parseable and independently suitable for deterministic static preflight.
- The final milestone must satisfy every acceptance criterion of the work package.
- Do not repeat the entire architecture or expand another work package.
- Verification entries use only canonical approved commands.
- Return JSON only, matching DetailPlan.`

type ArchitecturePlan struct {
	Goal              string             `json:"goal"`
	Packages          []ArchitectureWork `json:"packages"`
	FinalVerification []string           `json:"final_verification"`
}

type ArchitectureWork struct {
	ID         string   `json:"id"`
	Objective  string   `json:"objective"`
	Files      []string `json:"files"`
	DependsOn  []string `json:"depends_on"`
	Acceptance []string `json:"acceptance"`
}

type ArchitectureFinding struct {
	ID              string `json:"id"`
	PackageID       string `json:"package_id"`
	Summary         string `json:"summary"`
	FailureScenario string `json:"failure_scenario"`
	RequiredOutcome string `json:"required_outcome"`
}

type ArchitectureReview struct {
	Passed   bool                  `json:"passed"`
	Findings []ArchitectureFinding `json:"findings"`
	Summary  string                `json:"summary"`
}

type DetailPlan struct {
	PackageID string     `json:"package_id"`
	Steps     []PlanStep `json:"steps"`
}

type PlanningReport struct {
	Architecture ArchitecturePlan     `json:"architecture"`
	Reviews      []ArchitectureReview `json:"reviews"`
}

type architecturePlanningRoles interface {
	PlanArchitecture(context.Context, string) (*DevelopmentPlan, *PlanningReport, error)
}

func architecturePlanSchema() map[string]any {
	stringArray := func(min int) map[string]any {
		return map[string]any{"type": "array", "minItems": min, "items": map[string]any{"type": "string", "minLength": 1}}
	}
	return map[string]any{
		"type": "object", "additionalProperties": false,
		"required": []string{"goal", "packages", "final_verification"},
		"properties": map[string]any{
			"goal":               map[string]any{"type": "string", "minLength": 1},
			"final_verification": stringArray(1),
			"packages": map[string]any{"type": "array", "minItems": 1, "maxItems": 8, "items": map[string]any{
				"type": "object", "additionalProperties": false,
				"required": []string{"id", "objective", "files", "depends_on", "acceptance"},
				"properties": map[string]any{
					"id":        map[string]any{"type": "string", "pattern": `^package-[1-9][0-9]*$`},
					"objective": map[string]any{"type": "string", "minLength": 1},
					"files":     stringArray(1), "depends_on": stringArray(0), "acceptance": stringArray(1),
				},
			}},
		},
	}
}

func architectureReviewSchema() map[string]any {
	return map[string]any{
		"type": "object", "additionalProperties": false,
		"required": []string{"passed", "findings", "summary"},
		"properties": map[string]any{
			"passed": map[string]any{"type": "boolean"}, "summary": map[string]any{"type": "string"},
			"findings": map[string]any{"type": "array", "items": map[string]any{
				"type": "object", "additionalProperties": false,
				"required": []string{"id", "package_id", "summary", "failure_scenario", "required_outcome"},
				"properties": map[string]any{
					"id": map[string]any{"type": "string", "minLength": 1}, "package_id": map[string]any{"type": "string"},
					"summary": map[string]any{"type": "string", "minLength": 1}, "failure_scenario": map[string]any{"type": "string", "minLength": 1},
					"required_outcome": map[string]any{"type": "string", "minLength": 1},
				},
			}},
		},
	}
}

func detailPlanSchema() map[string]any {
	stepSchema := developmentPlanSchema()["properties"].(map[string]any)["steps"].(map[string]any)
	return map[string]any{
		"type": "object", "additionalProperties": false,
		"required": []string{"package_id", "steps"},
		"properties": map[string]any{
			"package_id": map[string]any{"type": "string", "minLength": 1},
			"steps":      stepSchema,
		},
	}
}

func validateArchitecturePlan(plan *ArchitecturePlan) error {
	if plan == nil || strings.TrimSpace(plan.Goal) == "" || len(plan.Packages) == 0 || len(plan.Packages) > 8 {
		return fmt.Errorf("architecture requires a goal and 1-8 work packages")
	}
	plan.FinalVerification = uniqueStrings(plan.FinalVerification)
	for index, command := range plan.FinalVerification {
		canonical, err := normalizeVerificationCommand(command)
		if err != nil {
			return fmt.Errorf("architecture final verification %q: %w", command, err)
		}
		plan.FinalVerification[index] = canonical
	}
	plan.FinalVerification = ensureCoreFinalVerification(plan.FinalVerification)
	ids := make(map[string]int, len(plan.Packages))
	for index := range plan.Packages {
		work := &plan.Packages[index]
		if work.ID != fmt.Sprintf("package-%d", index+1) {
			return fmt.Errorf("architecture package %q must be ordered as package-%d", work.ID, index+1)
		}
		if strings.TrimSpace(work.Objective) == "" || len(work.Files) == 0 || len(uniqueStrings(work.Acceptance)) == 0 {
			return fmt.Errorf("architecture package %s requires objective, files, and acceptance", work.ID)
		}
		for fileIndex, path := range work.Files {
			normalized, err := normalizePlanPath(path)
			if err != nil {
				return fmt.Errorf("architecture package %s file %q: %w", work.ID, path, err)
			}
			work.Files[fileIndex] = normalized
		}
		work.Files = uniqueStrings(work.Files)
		work.Acceptance = uniqueStrings(work.Acceptance)
		ids[work.ID] = index
		for _, dependency := range uniqueStrings(work.DependsOn) {
			dependencyIndex, exists := ids[dependency]
			if !exists || dependencyIndex >= index {
				return fmt.Errorf("architecture package %s has unordered dependency %q", work.ID, dependency)
			}
		}
		work.DependsOn = uniqueStrings(work.DependsOn)
	}
	return nil
}

func validateArchitectureReview(plan *ArchitecturePlan, review *ArchitectureReview) error {
	if review == nil {
		return fmt.Errorf("architecture review is required")
	}
	known := map[string]struct{}{"": struct{}{}}
	for _, work := range plan.Packages {
		known[work.ID] = struct{}{}
	}
	seen := make(map[string]struct{})
	for index := range review.Findings {
		finding := &review.Findings[index]
		finding.ID = strings.TrimSpace(finding.ID)
		if !strings.HasPrefix(strings.ToUpper(finding.ID), "CASSANDRA-") {
			finding.ID = "CASSANDRA-" + finding.ID
		}
		if _, duplicate := seen[finding.ID]; duplicate {
			return fmt.Errorf("architecture finding %q is duplicated", finding.ID)
		}
		seen[finding.ID] = struct{}{}
		if _, exists := known[finding.PackageID]; !exists {
			return fmt.Errorf("architecture finding %q references unknown package %q", finding.ID, finding.PackageID)
		}
		if strings.TrimSpace(finding.Summary) == "" || strings.TrimSpace(finding.FailureScenario) == "" || strings.TrimSpace(finding.RequiredOutcome) == "" {
			return fmt.Errorf("architecture finding %q is incomplete", finding.ID)
		}
	}
	if review.Passed != (len(review.Findings) == 0) {
		return fmt.Errorf("architecture review passed flag disagrees with findings")
	}
	return nil
}

func validateDetailPlan(work ArchitectureWork, detail *DetailPlan) error {
	if detail == nil || detail.PackageID != work.ID || len(detail.Steps) == 0 || len(detail.Steps) > 6 {
		return fmt.Errorf("detail plan for %s requires 1-6 milestones", work.ID)
	}
	allowed := make(map[string]struct{}, len(work.Files))
	for _, path := range work.Files {
		allowed[path] = struct{}{}
	}
	for index := range detail.Steps {
		step := &detail.Steps[index]
		step.ID = fmt.Sprintf("step-%d", index+1)
		step.AllowedFiles = uniqueStrings(step.AllowedFiles)
		if strings.TrimSpace(step.Objective) == "" || len(step.AllowedFiles) == 0 || len(uniqueStrings(step.Acceptance)) == 0 {
			return fmt.Errorf("detail milestone %d is incomplete", index+1)
		}
		for _, path := range step.AllowedFiles {
			if _, exists := allowed[path]; !exists {
				return fmt.Errorf("detail milestone %d file %q is outside package %s", index+1, path, work.ID)
			}
		}
		for verificationIndex, command := range step.Verification {
			canonical, err := normalizeVerificationCommand(command)
			if err != nil {
				return fmt.Errorf("detail milestone %d verification: %w", index+1, err)
			}
			step.Verification[verificationIndex] = canonical
		}
	}
	return nil
}

func flattenArchitecturePlan(architecture ArchitecturePlan, details []DetailPlan) (*DevelopmentPlan, error) {
	if len(details) != len(architecture.Packages) {
		return nil, fmt.Errorf("detail plan count does not match architecture")
	}
	plan := &DevelopmentPlan{Goal: architecture.Goal, FinalVerification: append([]string(nil), architecture.FinalVerification...)}
	stepIndex := 1
	for index, work := range architecture.Packages {
		detail := details[index]
		if err := validateDetailPlan(work, &detail); err != nil {
			return nil, err
		}
		plan.Files = append(plan.Files, work.Files...)
		for _, step := range detail.Steps {
			step.ID = fmt.Sprintf("step-%d", stepIndex)
			plan.Steps = append(plan.Steps, step)
			stepIndex++
		}
	}
	if len(plan.Steps) > maxPlanSteps {
		return nil, fmt.Errorf("expanded architecture has %d milestones; maximum is %d", len(plan.Steps), maxPlanSteps)
	}
	plan.Files = uniqueStrings(plan.Files)
	if err := validateDevelopmentPlan(plan); err != nil {
		return nil, err
	}
	return plan, nil
}

func (m *ModelTeamRoles) createArchitecture(ctx context.Context, objective string, feedback []ArchitectureFinding) (*ArchitecturePlan, error) {
	payload, _ := json.Marshal(map[string]any{"objective": objective, "cassandra_findings": feedback})
	reg := m.runner.newRoleRegistry()
	registerPlannerTools(reg)
	temperature := 0.1
	evidence := &executionEvidence{ProgressMarker: plannerProgressMarker, RequiredCalls: requiredPlannerViewCalls(objective)}
	evidence.ProgressState = func() string { return evidenceProgressSet(evidence) }
	evidence.CompletionReady = func() bool { return len(evidence.missingRequiredTools()) == 0 }
	callCtx, cancel := withRoleTimeout(ctx, m.runner.roleBudget(TypePlanner))
	defer cancel()
	report, err := m.runner.withModel(m.models.Planner).executeSubagentLoopWithFormat(callCtx, newSubagentID("architect"), string(TypePlanner), string(payload), architectPlannerPrompt, reg, architecturePlanSchema(), &temperature, evidence)
	if err != nil {
		return nil, err
	}
	if report.Status != "SUCCESS" {
		return nil, fmt.Errorf("architect loop %s: %s", report.Termination, report.Summary)
	}
	var plan ArchitecturePlan
	if err := decodeStrictJSON(report.Summary, &plan); err != nil {
		return nil, err
	}
	if err := validateArchitecturePlan(&plan); err != nil {
		return nil, err
	}
	return &plan, nil
}

func (m *ModelTeamRoles) reviewArchitecture(ctx context.Context, objective string, plan *ArchitecturePlan) (*ArchitectureReview, error) {
	payload, _ := json.Marshal(map[string]any{"objective": objective, "architecture": plan})
	reg := tools.NewEmptyRegistry()
	temperature := 0.1
	callCtx, cancel := withRoleTimeout(ctx, m.runner.roleBudget(TypeReviewer))
	defer cancel()
	cassandraRunner := m.runner.withModel(m.models.Cassandra)
	if m.models.ReviewerThinking != nil {
		cassandraRunner = cassandraRunner.withThinking(*m.models.ReviewerThinking)
	}
	report, err := cassandraRunner.executeSubagentLoopWithFormat(callCtx, newSubagentID("cassandra"), string(TypeReviewer), string(payload), cassandraPrompt, reg, architectureReviewSchema(), &temperature, nil)
	if err != nil {
		return nil, err
	}
	if report.Status != "SUCCESS" {
		return nil, fmt.Errorf("cassandra loop %s: %s", report.Termination, report.Summary)
	}
	var review ArchitectureReview
	if err := decodeStrictJSON(report.Summary, &review); err != nil {
		return nil, err
	}
	if err := validateArchitectureReview(plan, &review); err != nil {
		return nil, err
	}
	return &review, nil
}

func (m *ModelTeamRoles) detailArchitectureWork(ctx context.Context, architecture *ArchitecturePlan, work ArchitectureWork) (*DetailPlan, error) {
	payload, _ := json.Marshal(map[string]any{"goal": architecture.Goal, "work_package": work})
	reg := tools.NewEmptyRegistry()
	temperature := 0.1
	callCtx, cancel := withRoleTimeout(ctx, m.runner.roleBudget(TypePlanner))
	defer cancel()
	report, err := m.runner.withModel(m.models.DetailPlanner).executeSubagentLoopWithFormat(callCtx, newSubagentID("detail-planner"), string(TypePlanner), string(payload), detailPlannerPrompt, reg, detailPlanSchema(), &temperature, nil)
	if err != nil {
		return nil, err
	}
	if report.Status != "SUCCESS" {
		return nil, fmt.Errorf("detail planner loop %s: %s", report.Termination, report.Summary)
	}
	var detail DetailPlan
	if err := decodeStrictJSON(report.Summary, &detail); err != nil {
		return nil, err
	}
	if err := validateDetailPlan(work, &detail); err != nil {
		return nil, err
	}
	return &detail, nil
}
