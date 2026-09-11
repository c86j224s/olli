package graph

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

type Status string

const (
	StatusSucceeded   Status = "succeeded"
	StatusFailed      Status = "failed"
	StatusCancelled   Status = "cancelled"
	StatusTimedOut    Status = "timed_out"
	StatusInterrupted Status = "interrupted"
)

const End = "__end__"

type State interface{}

type NodeResult struct {
	Route     string
	Completed bool
	Interrupt *Interrupt
}

type Node interface {
	Run(context.Context, State) (NodeResult, error)
}

type NodeFunc func(context.Context, State) (NodeResult, error)

func (f NodeFunc) Run(ctx context.Context, state State) (NodeResult, error) {
	return f(ctx, state)
}

type Interrupt struct {
	Reason  string         `json:"reason"`
	Payload map[string]any `json:"payload,omitempty"`
}

type Edge struct {
	From  string
	Route string
	To    string
}

type Definition struct {
	ID    string
	Start string
	Nodes map[string]Node
	Edges []Edge
}

type Policy struct {
	MaxTransitions int
	MaxNodeVisits  int
	NodeVisitLimit map[string]int
	Deadline       time.Duration
}

func DefaultPolicy() Policy {
	return Policy{MaxTransitions: 32, MaxNodeVisits: 8}
}

type Event struct {
	Sequence   int    `json:"sequence"`
	Node       string `json:"node"`
	Route      string `json:"route,omitempty"`
	Visit      int    `json:"visit"`
	Transition int    `json:"transition"`
}

type Result struct {
	Status      Status         `json:"status"`
	GraphID     string         `json:"graph_id"`
	CurrentNode string         `json:"current_node,omitempty"`
	Visits      map[string]int `json:"visits"`
	Transitions int            `json:"transitions"`
	Events      []Event        `json:"events"`
	Interrupt   *Interrupt     `json:"interrupt,omitempty"`
	Failure     string         `json:"failure,omitempty"`
}

type Runner struct {
	definition Definition
	policy     Policy
	edges      map[string]map[string]string
}

func NewRunner(definition Definition, policy Policy) (*Runner, error) {
	if err := validateDefinition(definition, policy); err != nil {
		return nil, err
	}
	edges := make(map[string]map[string]string)
	for _, edge := range definition.Edges {
		if edges[edge.From] == nil {
			edges[edge.From] = make(map[string]string)
		}
		edges[edge.From][edge.Route] = edge.To
	}
	return &Runner{definition: definition, policy: policy, edges: edges}, nil
}

func validateDefinition(definition Definition, policy Policy) error {
	if strings.TrimSpace(definition.ID) == "" {
		return fmt.Errorf("graph id is required")
	}
	if strings.TrimSpace(definition.Start) == "" {
		return fmt.Errorf("graph start node is required")
	}
	if len(definition.Nodes) == 0 || definition.Nodes[definition.Start] == nil {
		return fmt.Errorf("graph start node %q is not registered", definition.Start)
	}
	if policy.MaxTransitions <= 0 || policy.MaxNodeVisits <= 0 {
		return fmt.Errorf("graph transition and node visit limits must be positive")
	}
	for node, limit := range policy.NodeVisitLimit {
		if definition.Nodes[node] == nil || limit <= 0 || limit > policy.MaxNodeVisits {
			return fmt.Errorf("graph node visit limit for %q is invalid", node)
		}
	}
	seen := make(map[string]struct{}, len(definition.Edges))
	for _, edge := range definition.Edges {
		if definition.Nodes[edge.From] == nil {
			return fmt.Errorf("graph edge source %q is not registered", edge.From)
		}
		if edge.To != End && definition.Nodes[edge.To] == nil {
			return fmt.Errorf("graph edge target %q is not registered", edge.To)
		}
		if strings.TrimSpace(edge.Route) == "" {
			return fmt.Errorf("graph edge from %q requires a route", edge.From)
		}
		key := edge.From + "\x00" + edge.Route
		if _, exists := seen[key]; exists {
			return fmt.Errorf("graph has duplicate route %q from %q", edge.Route, edge.From)
		}
		seen[key] = struct{}{}
	}
	return nil
}

func (r *Runner) Run(ctx context.Context, state State) Result {
	result := Result{Status: StatusFailed, GraphID: r.definition.ID, CurrentNode: r.definition.Start, Visits: make(map[string]int)}
	if ctx == nil {
		result.Failure = "graph context is required"
		return result
	}
	if r.policy.Deadline > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, r.policy.Deadline)
		defer cancel()
	}

	current := r.definition.Start
	for {
		if err := ctx.Err(); err != nil {
			result.CurrentNode = current
			result.Failure = err.Error()
			if err == context.DeadlineExceeded {
				result.Status = StatusTimedOut
			} else {
				result.Status = StatusCancelled
			}
			return result
		}
		limit := r.policy.MaxNodeVisits
		if configured := r.policy.NodeVisitLimit[current]; configured > 0 {
			limit = configured
		}
		if result.Visits[current] >= limit {
			result.CurrentNode = current
			result.Failure = fmt.Sprintf("graph node %q visit limit exceeded", current)
			return result
		}
		result.Visits[current]++

		nodeResult, err := r.definition.Nodes[current].Run(ctx, state)
		if err != nil {
			result.CurrentNode = current
			result.Failure = err.Error()
			if ctx.Err() == context.DeadlineExceeded || errors.Is(err, context.DeadlineExceeded) {
				result.Status = StatusTimedOut
			} else if ctx.Err() == context.Canceled || errors.Is(err, context.Canceled) {
				result.Status = StatusCancelled
			}
			return result
		}
		if err := ctx.Err(); err != nil {
			result.CurrentNode = current
			result.Failure = err.Error()
			if err == context.DeadlineExceeded {
				result.Status = StatusTimedOut
			} else {
				result.Status = StatusCancelled
			}
			return result
		}
		if nodeResult.Interrupt != nil {
			result.Status = StatusInterrupted
			result.CurrentNode = current
			result.Interrupt = nodeResult.Interrupt
			return result
		}
		if nodeResult.Completed {
			result.Status = StatusSucceeded
			result.CurrentNode = current
			return result
		}
		next, exists := r.edges[current][nodeResult.Route]
		if !exists {
			result.CurrentNode = current
			result.Failure = fmt.Sprintf("graph node %q returned unknown route %q", current, nodeResult.Route)
			return result
		}
		if result.Transitions >= r.policy.MaxTransitions {
			result.CurrentNode = current
			result.Failure = "graph transition limit exceeded"
			return result
		}
		result.Transitions++
		result.Events = append(result.Events, Event{Sequence: len(result.Events), Node: current, Route: nodeResult.Route, Visit: result.Visits[current], Transition: result.Transitions})
		if next == End {
			result.Status = StatusSucceeded
			result.CurrentNode = current
			return result
		}
		current = next
		result.CurrentNode = current
	}
}
