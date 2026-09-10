package loop

import "testing"

func TestLoopGuardStopsRepeatedActionsAfterRepairChance(t *testing.T) {
	guard, err := NewController(DefaultPolicy(false))
	if err != nil {
		t.Fatal(err)
	}
	action := map[string]interface{}{"file_path": "missing.go"}
	if reason := guard.RecordToolCall("view_file", action); reason != "" {
		t.Fatalf("first action stopped loop: %s", reason)
	}
	if reason := guard.RecordToolCall("view_file", action); reason != "" {
		t.Fatalf("first duplicate must allow one repair turn: %s", reason)
	}
	if reason := guard.RecordToolCall("view_file", action); reason != TerminationRepeatedAction {
		t.Fatalf("third identical action did not stop loop: %s", reason)
	}
}

func TestLoopGuardResetsRepeatedActionSequence(t *testing.T) {
	guard, _ := NewController(DefaultPolicy(false))
	a := map[string]interface{}{"path": "a.go"}
	b := map[string]interface{}{"path": "b.go"}
	guard.RecordToolCall("view_file", a)
	guard.RecordToolCall("view_file", a)
	if reason := guard.RecordToolCall("view_file", b); reason != "" {
		t.Fatalf("changed action stopped loop: %s", reason)
	}
	if reason := guard.RecordToolCall("view_file", a); reason != "" {
		t.Fatalf("non-consecutive action was treated as repeated: %s", reason)
	}
}

func TestLoopGuardStopsAlternatingActionCycle(t *testing.T) {
	guard, _ := NewController(DefaultPolicy(false))
	a := map[string]interface{}{"path": "a.go"}
	b := map[string]interface{}{"path": "b.go"}
	for _, action := range []map[string]interface{}{a, b, a, b} {
		if reason := guard.RecordToolCall("view_file", action); reason != "" {
			t.Fatalf("repairable alternating cycle stopped too early: %s", reason)
		}
	}
	if reason := guard.RecordToolCall("view_file", a); reason != TerminationRepeatedAction {
		t.Fatalf("alternating cycle did not terminate: %s", reason)
	}
}

func TestLoopGuardGivesIndependentCyclesSeparateRepair(t *testing.T) {
	guard, _ := NewController(DefaultPolicy(false))
	call := func(path string) TerminationReason {
		return guard.RecordToolCall("view_file", map[string]interface{}{"path": path})
	}
	for _, path := range []string{"a", "b", "a", "b", "c", "d", "c", "d"} {
		if reason := call(path); reason != "" {
			t.Fatalf("independent cycle stopped without repair: %s", reason)
		}
	}
	if !guard.ConsumeRepetitionRepair() {
		t.Fatal("second independent cycle did not request repair")
	}
}

func TestLoopGuardDetectsNoProgress(t *testing.T) {
	guard, _ := NewController(DefaultPolicy(false))
	if reason := guard.ObserveProgress("state-a"); reason != "" {
		t.Fatalf("initial progress stopped loop: %s", reason)
	}
	if reason := guard.ObserveProgress("state-a"); reason != "" {
		t.Fatalf("first no-progress turn stopped loop: %s", reason)
	}
	if reason := guard.ObserveProgress("state-a"); reason != TerminationNoProgress {
		t.Fatalf("second no-progress turn did not stop loop: %s", reason)
	}
	if reason := guard.ObserveProgress("state-b"); reason != "" {
		t.Fatalf("new progress did not reset guard: %s", reason)
	}
}

func TestLoopGuardEnforcesBudgets(t *testing.T) {
	policy := DefaultPolicy(false)
	policy.MaxIterations = 1
	policy.MaxModelCalls = 1
	policy.MaxToolCalls = 1
	guard, _ := NewController(policy)
	if reason := guard.BeginIteration(); reason != "" {
		t.Fatal(reason)
	}
	if reason := guard.BeginIteration(); reason != TerminationMaxIterations {
		t.Fatalf("iteration budget not enforced: %s", reason)
	}
	if reason := guard.RecordModelCall(); reason != "" {
		t.Fatal(reason)
	}
	if reason := guard.RecordModelCall(); reason != TerminationBudgetExceeded {
		t.Fatalf("model budget not enforced: %s", reason)
	}
	if reason := guard.RecordToolCall("view_file", nil); reason != "" {
		t.Fatal(reason)
	}
	if reason := guard.RecordToolCall("view_file", nil); reason != TerminationBudgetExceeded {
		t.Fatalf("tool budget not enforced: %s", reason)
	}
}

func TestActionFingerprintIgnoresMapIterationOrderWithoutStructuralCollisions(t *testing.T) {
	left := map[string]interface{}{"b": 2, "a": map[string]interface{}{"y": 2, "x": 1}}
	right := map[string]interface{}{"a": map[string]interface{}{"x": 1, "y": 2}, "b": 2}
	if ActionFingerprint("tool", left) != ActionFingerprint("tool", right) {
		t.Fatal("equivalent arguments produced different fingerprints")
	}
	if ActionFingerprint("other", left) == ActionFingerprint("tool", right) {
		t.Fatal("different tools produced the same fingerprint")
	}
	object := map[string]interface{}{"x": map[string]interface{}{"a": 1}}
	array := map[string]interface{}{"x": []interface{}{"a", 1}}
	if ActionFingerprint("tool", object) == ActionFingerprint("tool", array) {
		t.Fatal("object and array arguments produced the same fingerprint")
	}
}

func TestStructuredPolicyAllowsOneFormatRepair(t *testing.T) {
	guard, _ := NewController(DefaultPolicy(true))
	if reason := guard.RecordFormatRepair(); reason != "" {
		t.Fatalf("first repair rejected: %s", reason)
	}
	if reason := guard.RecordFormatRepair(); reason != TerminationInvalidOutput {
		t.Fatalf("second repair was not rejected: %s", reason)
	}
}
