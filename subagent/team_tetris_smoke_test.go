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
	coderModel := os.Getenv("OLLI_TETRIS_CODER_MODEL")
	if coderModel == "" {
		coderModel = "qwen3.8:27b"
	}
	roles.models.Coder = coderModel
	roles.models.Reviewer = coderModel
	thinking := false
	roles.models.CoderThinking = &thinking
	roles.runner.callbacks.OnToolCall = func(subType string, toolName string, _ map[string]interface{}, _ string, execErr error) {
		t.Logf("%s tool=%s err=%v", subType, toolName, execErr)
	}
	roles.testerRegistry = func() *tools.Registry { return newTetrisSmokeTesterRegistry(root) }
	ctx, cancel := context.WithTimeout(context.Background(), 14*time.Minute)
	defer cancel()
	team, err := NewDevelopmentTeamRunner(roles, 2)
	if err != nil {
		t.Fatal(err)
	}
	result := team.Run(ctx, `Complete a small playable text CUI Tetris in main.go using only the Go standard library.
Requirements:
- Keep all implementation in main.go; do not create other files.
- Use a 10x20 board and implement collision, locking, full-line clearing, score, game-over, and piece rotation.
- Render with ASCII text and accept commands a/d/s/w/q followed by Enter.
- Use a deterministic seven-piece sequence; no external packages, networking, shell commands, or filesystem access.
- Keep the implementation compact and readable.
- Plan exactly one implementation step with allowed_files ["main.go"]. Its acceptance criteria must cover every requirement; do not stop at scaffolding or defer features.
- Use final_verification ["go_test ./...", "go_vet ./..."]. The implementation step verification may be empty.`)
	data, readErr := os.ReadFile(filepath.Join(root, "main.go"))
	if result.Status != "SUCCESS" {
		if readErr == nil {
			t.Fatalf("development team failed: %s; plan=%+v; graph=%+v\n\ngenerated main.go:\n%s", result.Failure, result.Plan, result.Graph, data)
		}
		t.Fatalf("development team failed: %s; plan=%+v; graph=%+v; source read error=%v", result.Failure, result.Plan, result.Graph, readErr)
	}
	if readErr != nil {
		t.Fatal(readErr)
	}
	if err := inspectTetrisSource(string(data)); err != nil {
		t.Fatalf("generated Tetris failed inspection: %v\n\n%s", err, data)
	}
	t.Logf("generated main.go:\n%s", data)
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
		data, err := os.ReadFile(filepath.Join(root, "main.go"))
		if err != nil {
			return "", err
		}
		if err := parseAndTypeCheckGo(string(data)); err != nil {
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
