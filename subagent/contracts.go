package subagent

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
)

const maxPlanSteps = 24

var planStepIDPattern = regexp.MustCompile(`^step-[1-9][0-9]*$`)

var allowedVerificationActions = map[string]struct{}{
	"go_test":    {},
	"go_vet":     {},
	"git_diff":   {},
	"git_status": {},
}

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
	return parseDevelopmentPlanForWorkspace(raw, "")
}

func parseDevelopmentPlanForWorkspace(raw string, workspace string) (*DevelopmentPlan, error) {
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
	if strings.TrimSpace(workspace) != "" {
		if err := relativizeDevelopmentPlanPaths(&plan, workspace); err != nil {
			return nil, err
		}
	}
	if err := validateDevelopmentPlan(&plan); err != nil {
		return nil, err
	}
	return &plan, nil
}

func relativizeDevelopmentPlanPaths(plan *DevelopmentPlan, workspace string) error {
	workspace, err := filepath.Abs(workspace)
	if err != nil {
		return fmt.Errorf("planner workspace is invalid: %w", err)
	}
	convert := func(path string) (string, error) {
		path = strings.TrimSpace(path)
		if !filepath.IsAbs(path) {
			return path, nil
		}
		relative, err := filepath.Rel(workspace, filepath.Clean(path))
		if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) || filepath.IsAbs(relative) {
			return "", fmt.Errorf("planner path %q is outside workspace", path)
		}
		return filepath.Clean(relative), nil
	}
	for index, path := range plan.Files {
		converted, err := convert(path)
		if err != nil {
			return err
		}
		plan.Files[index] = converted
	}
	for stepIndex := range plan.Steps {
		for fileIndex, path := range plan.Steps[stepIndex].AllowedFiles {
			converted, err := convert(path)
			if err != nil {
				return err
			}
			plan.Steps[stepIndex].AllowedFiles[fileIndex] = converted
		}
	}
	return nil
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
	for index, command := range plan.FinalVerification {
		canonical, err := normalizeVerificationCommand(command)
		if err != nil {
			return fmt.Errorf("final verification %q: %w", command, err)
		}
		plan.FinalVerification[index] = canonical
	}
	plan.FinalVerification = ensureCoreFinalVerification(plan.FinalVerification)
	plan.Assumptions = uniqueStrings(plan.Assumptions)
	plan.Risks = uniqueStrings(plan.Risks)
	if len(plan.Files) == 0 {
		return fmt.Errorf("development plan requires files")
	}

	seenIDs := make(map[string]struct{}, len(plan.Steps))
	seenObjectives := make(map[string]struct{}, len(plan.Steps))
	for index := range plan.Files {
		path, err := normalizePlanPath(plan.Files[index])
		if err != nil {
			return fmt.Errorf("development plan file %q: %w", plan.Files[index], err)
		}
		plan.Files[index] = path
	}
	plan.Files = uniqueStrings(plan.Files)
	plannedFiles := make(map[string]struct{}, len(plan.Files))
	for _, path := range plan.Files {
		plannedFiles[path] = struct{}{}
	}

	for index := range plan.Steps {
		step := &plan.Steps[index]
		if !planStepIDPattern.MatchString(step.ID) {
			return fmt.Errorf("development plan step id %q must match step-N", step.ID)
		}
		if _, exists := seenIDs[step.ID]; exists {
			return fmt.Errorf("development plan has duplicate step id %q", step.ID)
		}
		seenIDs[step.ID] = struct{}{}
		step.Objective = strings.TrimSpace(step.Objective)
		if step.Objective == "" {
			return fmt.Errorf("development plan step %s requires an objective", step.ID)
		}
		objectiveKey := strings.ToLower(step.Objective)
		if _, exists := seenObjectives[objectiveKey]; exists {
			return fmt.Errorf("development plan has duplicate objective %q", step.Objective)
		}
		seenObjectives[objectiveKey] = struct{}{}
		if len(step.AllowedFiles) == 0 {
			return fmt.Errorf("development plan step %s requires allowed_files", step.ID)
		}
		step.Acceptance = uniqueStrings(step.Acceptance)
		if len(step.Acceptance) == 0 {
			return fmt.Errorf("development plan step %s requires non-empty acceptance criteria", step.ID)
		}
		step.Verification = uniqueStrings(step.Verification)
		for verificationIndex, command := range step.Verification {
			canonical, err := normalizeVerificationCommand(command)
			if err != nil {
				return fmt.Errorf("development plan step %s verification %q: %w", step.ID, command, err)
			}
			step.Verification[verificationIndex] = canonical
		}
		for fileIndex := range step.AllowedFiles {
			path, err := normalizePlanPath(step.AllowedFiles[fileIndex])
			if err != nil {
				return fmt.Errorf("development plan step %s file %q: %w", step.ID, step.AllowedFiles[fileIndex], err)
			}
			if _, exists := plannedFiles[path]; !exists {
				return fmt.Errorf("development plan step %s file %q is missing from plan files", step.ID, path)
			}
			step.AllowedFiles[fileIndex] = path
		}
		step.AllowedFiles = uniqueStrings(step.AllowedFiles)
	}
	return nil
}

func ensureCoreFinalVerification(commands []string) []string {
	hasTest, hasVet := false, false
	for _, command := range commands {
		if command == "go_test ./..." {
			hasTest = true
		}
		if command == "go_vet ./..." {
			hasVet = true
		}
	}
	if !hasTest {
		commands = append(commands, "go_test ./...")
	}
	if !hasVet {
		commands = append(commands, "go_vet ./...")
	}
	return uniqueStrings(commands)
}

func normalizeVerificationCommand(command string) (string, error) {
	fields := strings.Fields(strings.TrimSpace(command))
	if len(fields) == 0 || len(fields) > 2 {
		return "", fmt.Errorf("command must be action or action target")
	}
	action := fields[0]
	if _, allowed := allowedVerificationActions[action]; !allowed {
		return "", fmt.Errorf("action %q is not allowed for team verification", action)
	}
	if len(fields) == 1 {
		return action, nil
	}
	target := fields[1]
	if strings.ContainsAny(target, ";&|><$`\r\n\t") || strings.HasPrefix(target, "-") {
		return "", fmt.Errorf("target contains unsafe syntax")
	}
	return action + " " + target, nil
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
