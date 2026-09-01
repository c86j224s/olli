package subagent

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
)

const maxPlanSteps = 6

var planStepIDPattern = regexp.MustCompile(`^step-[1-9][0-9]*$`)

// DevelopmentPlan is the validated handoff from a read-only planner to the
// deterministic development-team orchestrator.
type DevelopmentPlan struct {
	Goal              string     `json:"goal"`
	Assumptions       []string   `json:"assumptions"`
	Files             []string   `json:"files"`
	Steps             []PlanStep `json:"steps"`
	Risks             []string   `json:"risks"`
	FinalVerification []string   `json:"final_verification"`
}

type PlanStep struct {
	ID           string   `json:"id"`
	Objective    string   `json:"objective"`
	AllowedFiles []string `json:"allowed_files"`
	Acceptance   []string `json:"acceptance"`
	Verification []string `json:"verification"`
}

func parseDevelopmentPlan(raw string) (*DevelopmentPlan, error) {
	var plan DevelopmentPlan
	decoder := json.NewDecoder(strings.NewReader(strings.TrimSpace(raw)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&plan); err != nil {
		return nil, fmt.Errorf("planner output is not valid DevelopmentPlan JSON: %w", err)
	}
	if decoder.More() {
		return nil, fmt.Errorf("planner output contains trailing JSON")
	}
	var trailing any
	if err := decoder.Decode(&trailing); err == nil {
		return nil, fmt.Errorf("planner output contains trailing JSON")
	}
	if err := validateDevelopmentPlan(&plan); err != nil {
		return nil, err
	}
	return &plan, nil
}

func validateDevelopmentPlan(plan *DevelopmentPlan) error {
	if plan == nil {
		return fmt.Errorf("development plan is required")
	}
	if strings.TrimSpace(plan.Goal) == "" {
		return fmt.Errorf("development plan goal is required")
	}
	if len(plan.Steps) == 0 || len(plan.Steps) > maxPlanSteps {
		return fmt.Errorf("development plan must contain 1-%d steps", maxPlanSteps)
	}
	plan.FinalVerification = uniqueStrings(plan.FinalVerification)
	if len(plan.FinalVerification) == 0 {
		return fmt.Errorf("development plan requires non-empty final_verification")
	}
	plan.Assumptions = uniqueStrings(plan.Assumptions)
	plan.Risks = uniqueStrings(plan.Risks)
	if len(plan.Files) == 0 {
		return fmt.Errorf("development plan requires files")
	}

	seenIDs := make(map[string]struct{}, len(plan.Steps))
	for index := range plan.Files {
		path, err := normalizePlanPath(plan.Files[index])
		if err != nil {
			return fmt.Errorf("development plan file %q: %w", plan.Files[index], err)
		}
		plan.Files[index] = path
	}
	plan.Files = uniqueStrings(plan.Files)

	for index := range plan.Steps {
		step := &plan.Steps[index]
		if !planStepIDPattern.MatchString(step.ID) {
			return fmt.Errorf("development plan step id %q must match step-N", step.ID)
		}
		if _, exists := seenIDs[step.ID]; exists {
			return fmt.Errorf("development plan has duplicate step id %q", step.ID)
		}
		seenIDs[step.ID] = struct{}{}
		if strings.TrimSpace(step.Objective) == "" {
			return fmt.Errorf("development plan step %s requires an objective", step.ID)
		}
		if len(step.AllowedFiles) == 0 {
			return fmt.Errorf("development plan step %s requires allowed_files", step.ID)
		}
		step.Acceptance = uniqueStrings(step.Acceptance)
		if len(step.Acceptance) == 0 {
			return fmt.Errorf("development plan step %s requires non-empty acceptance criteria", step.ID)
		}
		step.Verification = uniqueStrings(step.Verification)
		for fileIndex := range step.AllowedFiles {
			path, err := normalizePlanPath(step.AllowedFiles[fileIndex])
			if err != nil {
				return fmt.Errorf("development plan step %s file %q: %w", step.ID, step.AllowedFiles[fileIndex], err)
			}
			step.AllowedFiles[fileIndex] = path
		}
		step.AllowedFiles = uniqueStrings(step.AllowedFiles)
	}
	return nil
}

func normalizePlanPath(path string) (string, error) {
	path = strings.TrimSpace(path)
	if path == "" || filepath.IsAbs(path) {
		return "", fmt.Errorf("path must be workspace-relative")
	}
	clean := filepath.Clean(path)
	if clean == "." || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("path escapes the workspace")
	}
	return clean, nil
}

func uniqueStrings(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}

func developmentPlanSchema() map[string]any {
	stringArray := func(minItems int) map[string]any {
		return map[string]any{"type": "array", "minItems": minItems, "items": map[string]any{"type": "string", "minLength": 1}}
	}
	return map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"required":             []string{"goal", "assumptions", "files", "steps", "risks", "final_verification"},
		"properties": map[string]any{
			"goal":               map[string]any{"type": "string", "minLength": 1},
			"assumptions":        stringArray(0),
			"files":              stringArray(1),
			"risks":              stringArray(0),
			"final_verification": stringArray(1),
			"steps": map[string]any{
				"type": "array", "minItems": 1, "maxItems": maxPlanSteps,
				"items": map[string]any{
					"type":                 "object",
					"additionalProperties": false,
					"required":             []string{"id", "objective", "allowed_files", "acceptance", "verification"},
					"properties": map[string]any{
						"id":            map[string]any{"type": "string", "pattern": `^step-[1-9][0-9]*$`},
						"objective":     map[string]any{"type": "string", "minLength": 1},
						"allowed_files": stringArray(1),
						"acceptance":    stringArray(1),
						"verification":  stringArray(0),
					},
				},
			},
		},
	}
}
