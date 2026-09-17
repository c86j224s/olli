package subagent

import (
	"context"
	"fmt"
	"go/ast"
	"go/importer"
	"go/parser"
	"go/token"
	"go/types"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/c86j224s/olli/ollama"
	"github.com/c86j224s/olli/tools"
)

func TestDevelopmentTeamBuildsTextTetrisSmoke(t *testing.T) {
	if os.Getenv("OLLI_TETRIS_SMOKE") != "1" {
		t.Skip("set OLLI_TETRIS_SMOKE=1 in a disposable smoke workspace")
	}
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example/tetris\n\ngo 1.26\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "main.go"), []byte(tetrisStarterSource), 0600); err != nil {
		t.Fatal(err)
	}

	roles := smokeModelRoles(t, root)
	roles.runner.cfg.NumCtx = 16384
	roles.runner.heartbeatInterval = 10 * time.Second
	roles.runner.budgetOverrides = map[SubagentType]roleBudget{
		TypePlanner:  {NumPredict: 2048, Timeout: defaultPlannerTimeout},
		TypeCoder:    {NumPredict: defaultCoderNumPredict, Timeout: defaultCoderTimeout},
		TypeTester:   {NumPredict: defaultTesterNumPredict, Timeout: defaultTesterTimeout},
		TypeReviewer: {NumPredict: defaultReviewerNumPredict, Timeout: defaultReviewerTimeout},
	}
	roles.runner.callbacks.OnModelHeartbeat = func(subType string, elapsed time.Duration) {
		t.Logf("%s model response still generating after %s", subType, elapsed)
	}
	coderModel := os.Getenv("OLLI_TETRIS_CODER_MODEL")
	if coderModel == "" {
		coderModel = "qwen3.8:27b"
	}
	roles.models.Coder = coderModel
	testCoderModel := os.Getenv("OLLI_TETRIS_TEST_CODER_MODEL")
	if testCoderModel == "" {
		testCoderModel = "gemma4:12b"
	}
	roles.models.TestCoder = testCoderModel
	reviewerModel := os.Getenv("OLLI_TETRIS_REVIEWER_MODEL")
	if reviewerModel == "" {
		reviewerModel = "gemma4:12b"
	}
	roles.models.Reviewer = reviewerModel
	roles.models.RequirementReviewer = reviewerModel
	roles.models.LogicReviewer = reviewerModel
	roles.models.SafetyReviewer = reviewerModel
	roles.models.TestReviewer = reviewerModel
	roles.models.Cassandra = os.Getenv("OLLI_CASSANDRA_MODEL")
	if roles.models.Cassandra == "" {
		roles.models.Cassandra = roles.models.Planner
	}
	roles.models.DetailPlanner = roles.models.Planner
	thinking := false
	roles.models.CoderThinking = &thinking
	roles.models.ReviewerThinking = &thinking
	roles.runner.callbacks.OnToolCall = func(subType string, toolName string, _ map[string]interface{}, _ string, execErr error) {
		t.Logf("%s tool=%s err=%v", subType, toolName, execErr)
	}
	roles.testerRegistry = func() *tools.Registry { return newTetrisSmokeTesterRegistry(root) }
	ctx, cancel := context.WithTimeout(context.Background(), 220*time.Minute)
	defer cancel()
	team, err := NewDevelopmentTeamRunner(roles, 2)
	if err != nil {
		t.Fatal(err)
	}
	result := team.Run(ctx, `Build a small playable text CUI Tetris in Go using only the standard library.
Requirements:
- Use exactly five ordered architecture work packages in the same main package, with unique ownership: (1) types.go for shared state; (2) sequence.go for the deterministic seven-piece sequence; (3) game.go for collision, rotation, locking, line clearing, score, and game-over; (4) io.go and main.go for ASCII rendering, blocking input, and the small entry-point game loop; (5) game_test.go for all required tests.
- The approved architecture must assign game_test.go with exactly four or five concise table-driven tests: deterministic sequence, rotation/collision, line clearing with score, and game-over. Keep the test file under 250 lines and do not duplicate equivalent cases.
- Use a 10x20 board and implement collision, locking, full-line clearing, score, game-over, and piece rotation.
- Render with ASCII text and accept commands a/d/s/w/q followed by Enter.
- This is intentionally turn-based: blocking for each Enter-terminated command is correct. Do not add real-time gravity, asynchronous input, concurrency, or raw terminal mode.
- Cohesive files may share plain Go structs in the same package; strict getter/setter encapsulation is not required.
- Use a deterministic seven-piece sequence; no external packages, networking, shell commands, or filesystem access.
- Every architecture work package must leave the package parseable and depend only on earlier packages.
- Detail-plan each approved work package into minimal milestones. Intermediate milestones use deterministic static preflight; semantic reviewers run on the assembled implementation.
- Use final_verification ["go_test ./...", "go_vet ./..."].`)
	if result.Status != "SUCCESS" {
		t.Fatalf("development team failed: %s; planning=%+v; plan=%+v; graph=%+v\n\ngenerated sources:\n%s", result.Failure, result.Planning, result.Plan, result.Graph, readTetrisSources(root))
	}
	if err := inspectTetrisWorkspace(root); err != nil {
		t.Fatalf("generated Tetris failed inspection: %v\n\n%s", err, readTetrisSources(root))
	}
	t.Logf("generated sources:\n%s", readTetrisSources(root))
}

func readTetrisSources(root string) string {
	var output strings.Builder
	_ = filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() || filepath.Ext(path) != ".go" || strings.Contains(path, string(filepath.Separator)+"sessions"+string(filepath.Separator)) {
			return nil
		}
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			return nil
		}
		relative, _ := filepath.Rel(root, path)
		fmt.Fprintf(&output, "--- %s ---\n%s\n", relative, data)
		return nil
	})
	return output.String()
}

func inspectTetrisWorkspace(root string) error {
	sources := readTetrisSources(root)
	if len(sources) < 2500 {
		return fmt.Errorf("implementation is suspiciously small (%d bytes)", len(sources))
	}
	if err := parseAndTypeCheckWorkspace(root); err != nil {
		return err
	}
	lower := strings.ToLower(sources)
	for _, marker := range []string{"func main(", "rotate", "clear", "score", "game", "10", "20", `"a"`, `"d"`, `"s"`, `"w"`, `"q"`} {
		if !strings.Contains(lower, strings.ToLower(marker)) {
			return fmt.Errorf("missing required implementation marker %q", marker)
		}
	}
	if !strings.Contains(lower, "collision") && !strings.Contains(lower, "collides") && !strings.Contains(lower, "valid(") {
		return fmt.Errorf("missing collision-detection implementation marker")
	}
	for _, forbidden := range []string{"os/exec", "net/http", "unsafe"} {
		if strings.Contains(sources, `"`+forbidden+`"`) {
			return fmt.Errorf("forbidden import %q", forbidden)
		}
	}
	return nil
}

func parseAndTypeCheckWorkspace(root string) error {
	packageDirs := make(map[string]struct{})
	err := filepath.Walk(root, func(path string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if info.IsDir() {
			if path != root && filepath.Base(path) == "sessions" {
				return filepath.SkipDir
			}
			return nil
		}
		if filepath.Ext(path) == ".go" {
			packageDirs[filepath.Dir(path)] = struct{}{}
		}
		return nil
	})
	if err != nil {
		return err
	}
	if len(packageDirs) == 0 {
		return fmt.Errorf("no Go implementation files generated")
	}
	orderedDirs := make([]string, 0, len(packageDirs))
	for dir := range packageDirs {
		orderedDirs = append(orderedDirs, dir)
	}
	sort.Strings(orderedDirs)
	for _, dir := range orderedDirs {
		files := token.NewFileSet()
		implementation := make(map[string][]*ast.File)
		tests := make(map[string][]*ast.File)
		entries, err := os.ReadDir(dir)
		if err != nil {
			return err
		}
		for _, entry := range entries {
			if entry.IsDir() || filepath.Ext(entry.Name()) != ".go" {
				continue
			}
			file, err := parser.ParseFile(files, filepath.Join(dir, entry.Name()), nil, parser.AllErrors)
			if err != nil {
				return fmt.Errorf("parse %s: %w", entry.Name(), err)
			}
			if strings.HasSuffix(entry.Name(), "_test.go") {
				tests[file.Name.Name] = append(tests[file.Name.Name], file)
			} else {
				implementation[file.Name.Name] = append(implementation[file.Name.Name], file)
			}
		}
		packagePath, _ := filepath.Rel(root, dir)
		if packagePath == "." {
			packagePath = "example/tetris"
		} else {
			packagePath = "example/tetris/" + filepath.ToSlash(packagePath)
		}
		for name, parsed := range implementation {
			combined := append(append([]*ast.File(nil), parsed...), tests[name]...)
			config := types.Config{Importer: importer.Default()}
			if _, err := config.Check(packagePath, files, combined, nil); err != nil {
				return fmt.Errorf("type check %s (%s): %w", packagePath, name, err)
			}
		}
	}
	return nil
}

func newTetrisSmokeTesterRegistry(root string) *tools.Registry {
	reg := tools.NewEmptyRegistry()
	reg.SetWorkspaceRoot(root)
	reg.SetWorkspace(root)
	reg.RegisterContext(ollama.Tool{Type: "function", Function: ollama.FunctionDef{Name: "execute_action", Description: "Statically parse and type-check the generated Go program", Parameters: ollama.FunctionParamSchema{Type: "object", Properties: map[string]ollama.FunctionParamProperty{
		"action": {Type: "string", Enum: []string{"go_test", "go_vet"}}, "target": {Type: "string"},
	}, Required: []string{"action"}}}}, tools.ToolMetadata{}, func(_ context.Context, args map[string]interface{}) (string, error) {
		action, _ := args["action"].(string)
		if action != "go_test" && action != "go_vet" {
			return "", fmt.Errorf("unsupported static smoke action %q", action)
		}
		if err := parseAndTypeCheckWorkspace(root); err != nil {
			return "", err
		}
		return action + " static parse and type check passed", nil
	})
	return reg
}

func parseAndTypeCheckGo(source string) error {
	files := token.NewFileSet()
	file, err := parser.ParseFile(files, "main.go", source, parser.AllErrors)
	if err != nil {
		return fmt.Errorf("parse failed: %w", err)
	}
	config := types.Config{Importer: importer.Default()}
	if _, err := config.Check("example/tetris", files, []*ast.File{file}, nil); err != nil {
		return fmt.Errorf("type check failed: %w", err)
	}
	return nil
}

func inspectTetrisSource(source string) error {
	if err := parseAndTypeCheckGo(source); err != nil {
		return err
	}
	if len(source) < 1500 {
		return fmt.Errorf("implementation is suspiciously small (%d bytes)", len(source))
	}
	required := []string{"func main(", "rotate", "clear", "score", "game", "10", "20", `"a"`, `"d"`, `"s"`, `"w"`, `"q"`}
	lower := strings.ToLower(source)
	for _, marker := range required {
		if !strings.Contains(lower, strings.ToLower(marker)) {
			return fmt.Errorf("missing required implementation marker %q", marker)
		}
	}
	if !strings.Contains(lower, "collision") && !strings.Contains(lower, "collides") && !strings.Contains(lower, "valid(") {
		return fmt.Errorf("missing collision-detection implementation marker")
	}
	for _, forbidden := range []string{"os/exec", "net/http", "unsafe"} {
		if strings.Contains(source, `"`+forbidden+`"`) {
			return fmt.Errorf("forbidden import %q", forbidden)
		}
	}
	return nil
}

const tetrisStarterSource = `package main

// Implement a compact standard-library-only text CUI Tetris here.
func main() {}
`
