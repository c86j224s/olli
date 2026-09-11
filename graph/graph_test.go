package graph

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"
)

type counterState struct {
	Count int
}

func TestRunnerExecutesBoundedCycleThenCompletes(t *testing.T) {
	definition := Definition{
		ID: "bounded-cycle", Start: "work",
		Nodes: map[string]Node{
			"work": NodeFunc(func(_ context.Context, raw State) (NodeResult, error) {
				state := raw.(*counterState)
				state.Count++
				return NodeResult{Route: "check"}, nil
			}),
			"check": NodeFunc(func(_ context.Context, raw State) (NodeResult, error) {
				if raw.(*counterState).Count < 3 {
					return NodeResult{Route: "again"}, nil
				}
				return NodeResult{Route: "done"}, nil
			}),
		},
		Edges: []Edge{{From: "work", Route: "check", To: "check"}, {From: "check", Route: "again", To: "work"}, {From: "check", Route: "done", To: End}},
	}
	runner, err := NewRunner(definition, Policy{MaxTransitions: 8, MaxNodeVisits: 4})
	if err != nil {
		t.Fatal(err)
	}
	state := &counterState{}
	result := runner.Run(context.Background(), state)
	if result.Status != StatusSucceeded || state.Count != 3 || result.Visits["work"] != 3 {
		t.Fatalf("unexpected graph result: %#v state=%#v", result, state)
	}
}

func TestRunnerStopsUnboundedCycleAtVisitLimit(t *testing.T) {
	definition := Definition{ID: "cycle", Start: "a", Nodes: map[string]Node{
		"a": NodeFunc(func(context.Context, State) (NodeResult, error) { return NodeResult{Route: "next"}, nil }),
		"b": NodeFunc(func(context.Context, State) (NodeResult, error) { return NodeResult{Route: "next"}, nil }),
	}, Edges: []Edge{{From: "a", Route: "next", To: "b"}, {From: "b", Route: "next", To: "a"}}}
	runner, err := NewRunner(definition, Policy{MaxTransitions: 20, MaxNodeVisits: 2})
	if err != nil {
		t.Fatal(err)
	}
	result := runner.Run(context.Background(), nil)
	if result.Status != StatusFailed || !strings.Contains(result.Failure, "visit limit") {
		t.Fatalf("unbounded cycle was not stopped: %#v", result)
	}
}

func TestRunnerExecutesNodeReachedByFinalTransition(t *testing.T) {
	definition := Definition{ID: "last-transition", Start: "a", Nodes: map[string]Node{
		"a": NodeFunc(func(context.Context, State) (NodeResult, error) { return NodeResult{Route: "next"}, nil }),
		"b": NodeFunc(func(context.Context, State) (NodeResult, error) { return NodeResult{Completed: true}, nil }),
	}, Edges: []Edge{{From: "a", Route: "next", To: "b"}}}
	runner, _ := NewRunner(definition, Policy{MaxTransitions: 1, MaxNodeVisits: 1})
	result := runner.Run(context.Background(), nil)
	if result.Status != StatusSucceeded || result.Visits["b"] != 1 {
		t.Fatalf("final-transition target was not executed: %#v", result)
	}
}

func TestRunnerStopsAtTransitionLimit(t *testing.T) {
	definition := Definition{ID: "cycle", Start: "a", Nodes: map[string]Node{
		"a": NodeFunc(func(context.Context, State) (NodeResult, error) { return NodeResult{Route: "next"}, nil }),
		"b": NodeFunc(func(context.Context, State) (NodeResult, error) { return NodeResult{Route: "next"}, nil }),
	}, Edges: []Edge{{From: "a", Route: "next", To: "b"}, {From: "b", Route: "next", To: "a"}}}
	runner, _ := NewRunner(definition, Policy{MaxTransitions: 3, MaxNodeVisits: 10})
	result := runner.Run(context.Background(), nil)
	if result.Status != StatusFailed || !strings.Contains(result.Failure, "transition limit") {
		t.Fatalf("transition limit not enforced: %#v", result)
	}
}

func TestRunnerClassifiesNodeLocalContextErrors(t *testing.T) {
	wrappedDeadline := NodeFunc(func(context.Context, State) (NodeResult, error) {
		return NodeResult{}, fmt.Errorf("child deadline: %w", context.DeadlineExceeded)
	})
	runner, _ := NewRunner(Definition{ID: "child-deadline", Start: "work", Nodes: map[string]Node{"work": wrappedDeadline}}, DefaultPolicy())
	if result := runner.Run(context.Background(), nil); result.Status != StatusTimedOut {
		t.Fatalf("wrapped node deadline was misclassified: %#v", result)
	}
	wrappedCancel := NodeFunc(func(context.Context, State) (NodeResult, error) {
		return NodeResult{}, fmt.Errorf("child cancel: %w", context.Canceled)
	})
	runner, _ = NewRunner(Definition{ID: "child-cancel", Start: "work", Nodes: map[string]Node{"work": wrappedCancel}}, DefaultPolicy())
	if result := runner.Run(context.Background(), nil); result.Status != StatusCancelled {
		t.Fatalf("wrapped node cancellation was misclassified: %#v", result)
	}
}

func TestRunnerDoesNotSucceedAfterCancellationDuringNode(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	definition := Definition{ID: "late-cancel", Start: "work", Nodes: map[string]Node{
		"work": NodeFunc(func(context.Context, State) (NodeResult, error) {
			cancel()
			return NodeResult{Completed: true}, nil
		}),
	}}
	runner, _ := NewRunner(definition, DefaultPolicy())
	result := runner.Run(ctx, nil)
	if result.Status != StatusCancelled {
		t.Fatalf("late cancellation was ignored: %#v", result)
	}
}

func TestRunnerReturnsInterruptAsNormalStatus(t *testing.T) {
	definition := Definition{ID: "interrupt", Start: "approval", Nodes: map[string]Node{
		"approval": NodeFunc(func(context.Context, State) (NodeResult, error) {
			return NodeResult{Interrupt: &Interrupt{Reason: "approval required"}}, nil
		}),
	}}
	runner, _ := NewRunner(definition, DefaultPolicy())
	result := runner.Run(context.Background(), nil)
	if result.Status != StatusInterrupted || result.Interrupt == nil || result.Failure != "" {
		t.Fatalf("interrupt treated as failure: %#v", result)
	}
}

func TestRunnerDistinguishesDeadlineAndCancellation(t *testing.T) {
	block := NodeFunc(func(ctx context.Context, _ State) (NodeResult, error) {
		<-ctx.Done()
		return NodeResult{}, ctx.Err()
	})
	definition := Definition{ID: "context", Start: "block", Nodes: map[string]Node{"block": block}}
	runner, _ := NewRunner(definition, Policy{MaxTransitions: 2, MaxNodeVisits: 1, Deadline: time.Nanosecond})
	result := runner.Run(context.Background(), nil)
	if result.Status != StatusTimedOut {
		t.Fatalf("node deadline was misclassified: %#v", result)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	runner, _ = NewRunner(Definition{ID: "cancel", Start: "done", Nodes: map[string]Node{"done": NodeFunc(func(context.Context, State) (NodeResult, error) { return NodeResult{Completed: true}, nil })}}, DefaultPolicy())
	result = runner.Run(ctx, nil)
	if result.Status != StatusCancelled {
		t.Fatalf("cancellation misclassified: %#v", result)
	}
}

func TestNewRunnerRejectsUnknownAndDuplicateEdges(t *testing.T) {
	node := NodeFunc(func(context.Context, State) (NodeResult, error) { return NodeResult{Completed: true}, nil })
	if _, err := NewRunner(Definition{ID: "bad", Start: "a", Nodes: map[string]Node{"a": node}, Edges: []Edge{{From: "a", Route: "x", To: "missing"}}}, DefaultPolicy()); err == nil {
		t.Fatal("unknown edge target accepted")
	}
	if _, err := NewRunner(Definition{ID: "bad", Start: "a", Nodes: map[string]Node{"a": node}, Edges: []Edge{{From: "a", Route: "x", To: End}, {From: "a", Route: "x", To: End}}}, DefaultPolicy()); err == nil {
		t.Fatal("duplicate route accepted")
	}
}
