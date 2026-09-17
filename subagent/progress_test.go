package subagent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRoleProgressMarkersTrackDomainProgress(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "feature.go")
	if err := os.WriteFile(path, []byte("package demo\n"), 0600); err != nil {
		t.Fatal(err)
	}
	fileMarker := fileProgressMarker(root)
	before := fileMarker("edit_file", map[string]interface{}{"file_path": "feature.go"}, "")
	if err := os.WriteFile(path, []byte("package changed\n"), 0600); err != nil {
		t.Fatal(err)
	}
	after := fileMarker("edit_file", map[string]interface{}{"file_path": "feature.go"}, "")
	if before == "" || before == after {
		t.Fatalf("file mutation did not change progress marker: %q %q", before, after)
	}

	if got := inspectedFileProgressMarker("view_file", map[string]interface{}{"file_path": "feature.go"}, ""); got != "viewed:feature.go:all:all" {
		t.Fatalf("unexpected reviewer marker: %q", got)
	}
	firstRange := inspectedFileProgressMarker("view_file", map[string]interface{}{"file_path": "feature.go", "start_line": 1, "end_line": 50}, "")
	secondRange := inspectedFileProgressMarker("view_file", map[string]interface{}{"file_path": "feature.go", "start_line": 51, "end_line": 100}, "")
	if firstRange == secondRange {
		t.Fatal("different line ranges produced the same reviewer progress marker")
	}
	if got := commandProgressMarker("execute_action", map[string]interface{}{"action": "go_test", "target": "./..."}, ""); got != "command:go_test ./..." {
		t.Fatalf("unexpected tester marker: %q", got)
	}
}

func TestFileProgressMarkerRejectsSymlinkTarget(t *testing.T) {
	root := t.TempDir()
	outside := filepath.Join(t.TempDir(), "outside.go")
	if err := os.WriteFile(outside, []byte("secret\n"), 0600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "link.go")
	if err := os.Symlink(outside, link); err != nil {
		t.Skipf("symlink creation unavailable: %v", err)
	}
	if marker := fileProgressMarker(root)("edit_file", map[string]interface{}{"file_path": "link.go"}, ""); marker != "" {
		t.Fatalf("symlink target produced a progress hash: %q", marker)
	}
}

func TestEvidenceProgressSetIsCumulativeAndOrderIndependent(t *testing.T) {
	evidence := &executionEvidence{SuccessfulCalls: []successfulToolCall{
		{ProgressMarker: "viewed:b.go"},
		{ProgressMarker: "viewed:a.go"},
		{ProgressMarker: "viewed:b.go"},
	}}
	if got := evidenceProgressSet(evidence); got != "viewed:a.go|viewed:b.go" {
		t.Fatalf("unexpected cumulative progress: %q", got)
	}
	if strings.Contains(evidenceProgressSet(nil), "viewed") {
		t.Fatal("nil evidence produced progress")
	}
}
