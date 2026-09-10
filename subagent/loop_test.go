package subagent

import "testing"

func TestLoopGuardStopsRepeatedActionsAfterRepairChance(t *testing.T) {
	guard, err := newLoopGuard(DefaultLoopPolicy(false))
	if err != nil {
		t.Fatal(err)
	}
	action := map[string]interface{}{"file_path": "missing.go"}
	if reason := guard.recordToolCall("view_file", action); reason != "" {
		t.Fatalf("first action stopped loop: %s", reason)
	}
	if reason := guard.recordToolCall("view_file", action); reason != "" {
		t.Fatalf("first duplicate must allow one repair turn: %s", reason)
	}
	if reason := guard.recordToolCall("view_file", action); reason != LoopTerminationRepeatedAction {
		t.Fatalf("third identical action did not stop loop: %s", reason)
	}
}

func TestLoopGuardResetsRepeatedActionSequence(t *testing.T) {
	guard, _ := newLoopGuard(DefaultLoopPolicy(false))
	a := map[string]interface{}{"path": "a.go"}
	b := map[string]interface{}{"path": "b.go"}
	guard.recordToolCall("view_file", a)
	guard.recordToolCall("view_file", a)
	if reason := guard.recordToolCall("view_file", b); reason != "" {
		t.Fatalf("changed action stopped loop: %s", reason)
	}
	if reason := guard.recordToolCall("view_file", a); reason != "" {
		t.Fatalf("non-consecutive action was treated as repeated: %s", reason)
	}
}

func TestLoopGuardDetectsNoProgress(t *testing.T) {
	guard, _ := newLoopGuard(DefaultLoopPolicy(false))
	if reason := guard.observeProgress("state-a"); reason != "" {
		t.Fatalf("initial progress stopped loop: %s", reason)
	}
	if reason := guard.observeProgress("state-a"); reason != "" {
		t.Fatalf("first no-progress turn stopped loop: %s", reason)
	}
	if reason := guard.observeProgress("state-a"); reason != LoopTerminationNoProgress {
		t.Fatalf("second no-progress turn did not stop loop: %s", reason)
	}
	if reason := guard.observeProgress("state-b"); reason != "" {
		t.Fatalf("new progress did not reset guard: %s", reason)
	}
}

func TestLoopGuardEnforcesBudgets(t *testing.T) {
	policy := DefaultLoopPolicy(false)
	policy.MaxIterations = 1
	policy.MaxModelCalls = 1
	policy.MaxToolCalls = 1
	guard, _ := newLoopGuard(policy)
	if reason := guard.beginIteration(); reason != "" {
		t.Fatal(reason)
	}
	if reason := guard.beginIteration(); reason != LoopTerminationMaxIterations {
		t.Fatalf("iteration budget not enforced: %s", reason)
	}
	if reason := guard.recordModelCall(); reason != "" {
		t.Fatal(reason)
	}
	if reason := guard.recordModelCall(); reason != LoopTerminationBudgetExceeded {
		t.Fatalf("model budget not enforced: %s", reason)
	}
	if reason := guard.recordToolCall("view_file", nil); reason != "" {
		t.Fatal(reason)
	}
	if reason := guard.recordToolCall("view_file", nil); reason != LoopTerminationBudgetExceeded {
		t.Fatalf("tool budget not enforced: %s", reason)
	}
}

func TestActionFingerprintIgnoresMapIterationOrder(t *testing.T) {
	left := map[string]interface{}{"b": 2, "a": map[string]interface{}{"y": 2, "x": 1}}
	right := map[string]interface{}{"a": map[string]interface{}{"x": 1, "y": 2}, "b": 2}
	if actionFingerprint("tool", left) != actionFingerprint("tool", right) {
		t.Fatal("equivalent arguments produced different fingerprints")
	}
	if actionFingerprint("other", left) == actionFingerprint("tool", right) {
		t.Fatal("different tools produced the same fingerprint")
	}
}

func TestStructuredLoopPolicyAllowsOneFormatRepair(t *testing.T) {
	guard, _ := newLoopGuard(DefaultLoopPolicy(true))
	if reason := guard.recordFormatRepair(); reason != "" {
		t.Fatalf("first repair rejected: %s", reason)
	}
	if reason := guard.recordFormatRepair(); reason != LoopTerminationInvalidOutput {
		t.Fatalf("second repair was not rejected: %s", reason)
	}
}
