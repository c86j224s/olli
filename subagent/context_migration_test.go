package subagent

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func TestNewSubagentIDConcurrentUnique(t *testing.T) {
	const count = 256
	ids := make(chan string, count)
	var wg sync.WaitGroup
	for i := 0; i < count; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ids <- newSubagentID("tester")
		}()
	}
	wg.Wait()
	close(ids)
	seen := make(map[string]struct{}, count)
	for id := range ids {
		if _, exists := seen[id]; exists {
			t.Fatalf("duplicate subagent ID: %s", id)
		}
		seen[id] = struct{}{}
	}
}

func TestOpenSubagentLogFileNoFollowDoesNotTruncateExistingFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "existing.jsonl")
	if err := os.WriteFile(path, []byte("existing\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := openSubagentLogFileNoFollow(path); err == nil {
		t.Fatal("expected existing log creation to fail closed")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "existing\n" {
		t.Fatalf("existing log was changed: %q", data)
	}
}
