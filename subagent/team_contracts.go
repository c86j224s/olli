package subagent

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

func parseCodeReport(raw string) (*CodeReport, error) {
	var report CodeReport
	if err := decodeStrictJSON(raw, &report); err != nil {
		return nil, fmt.Errorf("coder output is not valid CodeReport JSON: %w", err)
	}
	return &report, nil
}

func parseTestReport(raw string) (*TestReport, error) {
	var report TestReport
	if err := decodeStrictJSON(raw, &report); err != nil {
		return nil, fmt.Errorf("tester output is not valid TestReport JSON: %w", err)
	}
	return &report, nil
}

func parseReviewReport(raw string) (*ReviewReport, error) {
	var report ReviewReport
	if err := decodeStrictJSON(raw, &report); err != nil {
		return nil, fmt.Errorf("reviewer output is not valid ReviewReport JSON: %w", err)
	}
	return &report, nil
}

func decodeStrictJSON(raw string, target any) error {
	decoder := json.NewDecoder(strings.NewReader(strings.TrimSpace(raw)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return fmt.Errorf("trailing JSON")
		}
		return fmt.Errorf("invalid trailing data: %w", err)
	}
	return nil
}

func codeReportSchema() map[string]any {
	return map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"required":             []string{"step_id", "changed_files", "completed", "unresolved"},
		"properties": map[string]any{
			"step_id":       map[string]any{"type": "string", "minLength": 1},
			"changed_files": stringArraySchema(1),
			"completed":     stringArraySchema(1),
			"unresolved":    stringArraySchema(0),
			"addressed_findings": map[string]any{
				"type": "array",
				"items": map[string]any{
					"type":                 "object",
					"additionalProperties": false,
					"required":             []string{"id", "status", "evidence"},
					"properties": map[string]any{
						"id":       map[string]any{"type": "string", "minLength": 1},
						"status":   map[string]any{"type": "string", "enum": []string{"addressed", "not_addressed"}},
						"evidence": map[string]any{"type": "string", "minLength": 1},
					},
				},
			},
		},
	}
}

func testReportSchema() map[string]any {
	return map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"required":             []string{"passed", "commands", "summary"},
		"properties": map[string]any{
			"passed":  map[string]any{"type": "boolean"},
			"summary": map[string]any{"type": "string"},
			"commands": map[string]any{
				"type": "array", "minItems": 1,
				"items": map[string]any{
					"type":                 "object",
					"additionalProperties": false,
					"required":             []string{"command", "exit_code", "output"},
					"properties": map[string]any{
						"command":   map[string]any{"type": "string", "minLength": 1},
						"exit_code": map[string]any{"type": "integer"},
						"output":    map[string]any{"type": "string"},
					},
				},
			},
		},
	}
}

func reviewReportSchema() map[string]any {
	return map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"required":             []string{"findings", "finding_resolutions", "summary"},
		"properties": map[string]any{
			"summary": map[string]any{"type": "string"},
			"findings": map[string]any{
				"type": "array",
				"items": map[string]any{
					"type":                 "object",
					"additionalProperties": false,
					"required":             []string{"id", "severity", "file", "line", "summary", "failure_scenario", "required_outcome", "verification"},
					"properties": map[string]any{
						"id":               map[string]any{"type": "string", "minLength": 1},
						"severity":         map[string]any{"type": "string", "enum": []string{"low", "medium", "high", "critical"}},
						"file":             map[string]any{"type": "string", "minLength": 1},
						"line":             map[string]any{"type": "integer", "minimum": 1},
						"summary":          map[string]any{"type": "string", "minLength": 1},
						"failure_scenario": map[string]any{"type": "string", "minLength": 1},
						"required_outcome": map[string]any{"type": "string", "minLength": 1},
						"verification":     stringArraySchema(1),
					},
				},
			},
			"finding_resolutions": map[string]any{
				"type": "array",
				"items": map[string]any{
					"type":                 "object",
					"additionalProperties": false,
					"required":             []string{"id", "status", "evidence"},
					"properties": map[string]any{
						"id":       map[string]any{"type": "string", "minLength": 1},
						"status":   map[string]any{"type": "string", "enum": []string{"resolved", "unresolved"}},
						"evidence": map[string]any{"type": "string", "minLength": 1},
					},
				},
			},
		},
	}
}

func stringArraySchema(minItems int) map[string]any {
	return map[string]any{
		"type": "array", "minItems": minItems,
		"items": map[string]any{"type": "string", "minLength": 1},
	}
}
