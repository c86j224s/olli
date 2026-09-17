package subagent

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestTestCoderSmallModelSmoke(t *testing.T) {
	if os.Getenv("OLLI_TEST_CODER_SMOKE") != "1" {
		t.Skip("set OLLI_TEST_CODER_SMOKE=1 in a disposable smoke workspace")
	}
	root := t.TempDir()
	files := map[string]string{
		"types.go": `package main

const BoardWidth, BoardHeight = 10, 20
type Point struct{ X, Y int }
type Piece struct{ Cells [4]Point; Pos Point }
type Board [BoardHeight][BoardWidth]bool
type Game struct{ Board Board; Piece Piece; Score int; Over bool }
`,
		"sequence.go": `package main

var sequence = [7]int{0,1,2,3,4,5,6}
func nextPiece(i int) int { return sequence[i%len(sequence)] }
`,
		"game.go": `package main

func collides(b Board, p Piece) bool { for _, c := range p.Cells { x,y := p.Pos.X+c.X,p.Pos.Y+c.Y; if x<0 || x>=BoardWidth || y<0 || y>=BoardHeight || b[y][x] { return true } }; return false }
func rotate(p Piece) Piece { for i,c := range p.Cells { p.Cells[i]=Point{X:c.Y,Y:-c.X} }; return p }
func clearLines(b *Board) int { cleared:=0; for y:=BoardHeight-1;y>=0;y-- { full:=true; for x:=0;x<BoardWidth;x++ { if !b[y][x] { full=false; break } }; if full { cleared++; for yy:=y;yy>0;yy-- { b[yy]=b[yy-1] }; b[0]=[BoardWidth]bool{}; y++ } }; return cleared }
func score(lines int) int { return lines*100 }
func gameOver(b Board,p Piece) bool { return collides(b,p) }
`,
		"main.go": "package main\n\nfunc main() {}\n",
	}
	for name, source := range files {
		if err := os.WriteFile(filepath.Join(root, name), []byte(source), 0600); err != nil {
			t.Fatal(err)
		}
	}
	roles := smokeModelRoles(t, root)
	model := os.Getenv("OLLI_TEST_CODER_MODEL")
	if model == "" {
		model = roles.models.Coder
	}
	roles.models.TestCoder = model
	roles.runner.budgetOverrides = map[SubagentType]roleBudget{TypeCoder: {NumPredict: defaultCoderNumPredict, Timeout: defaultCoderTimeout}}
	step := PlanStep{
		ID: "step-1", Objective: "Create concise table-driven tests for the complete game behavior", AllowedFiles: []string{"game_test.go"},
		Acceptance: []string{"4-5 tests cover deterministic sequence, rotation/collision, line clearing with score, and game over", "game_test.go stays under 250 lines"},
	}
	readOnly := []string{"types.go", "sequence.go", "game.go", "main.go"}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Minute)
	defer cancel()
	report, err := roles.Code(ctx, CodeTask{Goal: "test text Tetris", Step: step, ReadOnlyFiles: readOnly, SourceSnapshots: reviewSourceSnapshots(readOnly, root), Attempt: 1})
	if err != nil {
		t.Fatalf("test Coder smoke failed: %v", err)
	}
	if len(report.ChangedFiles) != 1 || report.ChangedFiles[0] != "game_test.go" {
		t.Fatalf("unexpected test Coder report: %#v", report)
	}
	if err := parseAndTypeCheckWorkspace(root); err != nil {
		t.Fatalf("test Coder output failed type check: %v", err)
	}
}
