package tools

import (
	"testing"
)

func TestSecurityChecks(t *testing.T) {
	workspace := "/Users/allthatcode/llm-pg4"

	testCmds := []string{
		"cd ~/llm-pg",
		"cd /Users/allthatcode/llm-pg",
		"cd ../llm-pg",
	}

	for _, cmd := range testCmds {
		err := ValidateCommandSafety(cmd, workspace)
		if err != nil {
			t.Errorf("ValidateCommandSafety failed for '%s': %v", cmd, err)
		} else {
			t.Logf("ValidateCommandSafety PASSED for '%s'", cmd)
		}

		out, newWs, err := ExecuteCommandWithWorkspace(nil, cmd, workspace)
		t.Logf("ExecuteCommandWithWorkspace for '%s' -> err: %v, newWs: %s, out: %s", cmd, err, newWs, out)
	}

	testPaths := []string{
		"~/llm-pg",
		"/Users/allthatcode/llm-pg",
		"../llm-pg",
	}
	for _, p := range testPaths {
		safeP, err := IsPathSafe(p, workspace)
		if err != nil {
			t.Errorf("IsPathSafe failed for '%s': %v", p, err)
		} else {
			t.Logf("IsPathSafe PASSED for '%s' -> %s", p, safeP)
		}
	}
}
