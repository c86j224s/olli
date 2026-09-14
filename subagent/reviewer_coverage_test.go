package subagent

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReviewerTruncatedWholeViewDoesNotCountAsFullCoverage(t *testing.T) {
	root := t.TempDir()
	var source strings.Builder
	for line := 1; line <= 1000; line++ {
		fmt.Fprintf(&source, "// line %d\n", line)
	}
	if err := os.WriteFile(filepath.Join(root, "large.go"), []byte(source.String()), 0600); err != nil {
		t.Fatal(err)
	}
	files := []string{"large.go"}
	evidence := &executionEvidence{SuccessfulCalls: []successfulToolCall{{Name: "view_file", Arguments: map[string]interface{}{"file_path": "large.go"}, Result: "... [Truncated at 800 lines limit]"}}}
	if err := requireReviewerFileEvidence(files, evidence, root); err == nil || !strings.Contains(err.Error(), "line 801") {
		t.Fatalf("truncated view was accepted as full coverage: %v", err)
	}
	evidence.SuccessfulCalls = append(evidence.SuccessfulCalls, successfulToolCall{Name: "view_file", Arguments: map[string]interface{}{"file_path": "large.go", "start_line": 801, "end_line": 1001}, Result: "remaining lines"})
	if err := requireReviewerFileEvidence(files, evidence, root); err != nil {
		t.Fatalf("complete range coverage rejected: %v", err)
	}
}
