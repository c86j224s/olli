package subagent

import "testing"

func TestSingleExplicitTaskFile(t *testing.T) {
	if got := singleExplicitTaskFile("Implement the feature in main.go using Go."); got != "main.go" {
		t.Fatalf("explicit source file was not detected: %q", got)
	}
	if got := singleExplicitTaskFile("Update a.go and b.go"); got != "" {
		t.Fatalf("ambiguous files should not select one: %q", got)
	}
	if got := singleExplicitTaskFile("Inspect go.mod, then implement main.go"); got != "main.go" {
		t.Fatalf("module metadata hid the implementation file: %q", got)
	}
}

func TestRequiredPlannerViewCallsTargetsExplicitFile(t *testing.T) {
	calls := requiredPlannerViewCalls("Complete the program in main.go")
	if len(calls) != 1 || calls[0].Description != "view_file main.go" {
		t.Fatalf("unexpected planner requirement: %#v", calls)
	}
}
