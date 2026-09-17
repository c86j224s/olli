package subagent

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestArchitectCassandraTetrisSmoke(t *testing.T) {
	if os.Getenv("OLLI_ARCHITECTURE_SMOKE") != "1" {
		t.Skip("set OLLI_ARCHITECTURE_SMOKE=1 in a disposable smoke workspace")
	}
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example/tetris\n\ngo 1.26\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "main.go"), []byte("package main\n\nfunc main() {}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	roles := smokeModelRoles(t, root)
	roles.runner.cfg.NumCtx = 16384
	roles.runner.heartbeatInterval = 10 * time.Second
	roles.runner.budgetOverrides = map[SubagentType]roleBudget{
		TypePlanner:  {NumPredict: 2048, Timeout: defaultPlannerTimeout},
		TypeReviewer: {NumPredict: 768, Timeout: defaultReviewerTimeout},
	}
	roles.runner.callbacks.OnModelHeartbeat = func(role string, elapsed time.Duration) {
		t.Logf("%s still generating after %s", role, elapsed)
	}
	model := os.Getenv("OLLI_ARCHITECT_MODEL")
	if model == "" {
		model = os.Getenv("OLLI_SMOKE_MODEL")
	}
	if model != "" {
		roles.models.Planner = model
		roles.models.DetailPlanner = model
	}
	cassandra := os.Getenv("OLLI_CASSANDRA_MODEL")
	if cassandra != "" {
		roles.models.Cassandra = cassandra
	}
	thinking := false
	roles.models.ReviewerThinking = &thinking
	ctx, cancel := context.WithTimeout(context.Background(), 80*time.Minute)
	defer cancel()
	planning, err := roles.ReviewArchitecture(ctx, architectureTetrisObjective)
	if err != nil {
		t.Fatalf("architecture smoke failed: %v", err)
	}
	encoded, _ := json.MarshalIndent(planning, "", "  ")
	t.Logf("architecture planning:\n%s", encoded)
	if len(planning.Architecture.Packages) < 3 {
		t.Fatalf("architect did not split Tetris into cohesive file packages: %s", encoded)
	}
	files := make(map[string]struct{})
	for _, work := range planning.Architecture.Packages {
		for _, path := range work.Files {
			files[path] = struct{}{}
		}
	}
	if len(files) < 4 {
		t.Fatalf("architect kept implementation too concentrated: %s", encoded)
	}
	hasTests := false
	for path := range files {
		if strings.HasSuffix(path, "_test.go") {
			hasTests = true
			break
		}
	}
	if !hasTests {
		t.Fatalf("architect omitted concrete test-file ownership: %s", encoded)
	}
	if len(planning.Reviews) == 0 {
		t.Fatalf("Cassandra did not review architecture: %s", encoded)
	}
	latest := planning.Reviews[len(planning.Reviews)-1]
	if len(planning.Reviews) > maxArchitectRepairs+1 {
		t.Fatalf("planning repair exceeded bounded retry limit: %s", encoded)
	}
	if !latest.Passed {
		t.Fatalf("Cassandra did not approve the bounded repaired architecture: %s", encoded)
	}
}

const architectureTetrisObjective = `Build a small playable text CUI Tetris in Go using only the standard library.
Requirements:
- Keep main.go as a small entry point, but split state, seven-piece deterministic sequence, game rules, ASCII rendering, input handling, and tests into cohesive files in the same main package.
- Assign at least one concrete _test.go file with tests for deterministic sequence, rotation/collision, line clearing, and game-over behavior.
- Use a 10x20 board and implement collision, rotation, locking, full-line clearing, score, game-over, and commands a/d/s/w/q followed by Enter.
- This is intentionally a turn-based command game: blocking for each Enter-terminated command is correct. Do not introduce real-time gravity, asynchronous input, concurrency, or raw terminal mode.
- Cohesive files may share plain Go structs in the same package; strict getter/setter encapsulation is not required.
- No external packages, networking, shell commands, or filesystem access.
- Every work package must leave the package parseable and dependencies must point only to earlier packages.
- Final verification must include go_test ./... and go_vet ./....
Architecture and review only; do not implement files.`
