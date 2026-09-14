package subagent

import (
	"testing"

	agentloop "github.com/c86j224s/olli/loop"
)

func TestRequiredCommandCallsRequireEveryExactCommand(t *testing.T) {
	commands := []string{"go_test ./...", "go_vet ./..."}
	evidence := &executionEvidence{RequiredCalls: requiredCommandCalls(commands)}
	if missing := evidence.missingRequiredTools(); len(missing) != 2 {
		t.Fatalf("unexpected initial command requirements: %v", missing)
	}
	testArgs := map[string]interface{}{"action": "go_test", "target": "./..."}
	testCall := evidence.recordAttempt("execute_action", testArgs, "ok", nil)
	evidence.recordSuccess(testCall)
	if missing := evidence.missingRequiredTools(); len(missing) != 1 || missing[0] != "go_vet ./..." {
		t.Fatalf("missing command was not retained: %v", missing)
	}
	vetArgs := map[string]interface{}{"action": "go_vet", "target": "./..."}
	evidence.recordAttempt("execute_action", vetArgs, "failed", fakeExitError(2))
	if missing := evidence.missingRequiredTools(); len(missing) != 0 {
		t.Fatalf("failed but attempted command still marked missing: %v", missing)
	}
	if evidence.RequiredCalls[0].Fingerprint != agentloop.ActionFingerprint("execute_action", testArgs) {
		t.Fatal("required command fingerprint mismatch")
	}
}

type fakeExitError int

func (e fakeExitError) Error() string { return "exit error" }
func (e fakeExitError) ExitCode() int { return int(e) }
