package loop

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
)

type TerminationReason string

const (
	TerminationRunning        TerminationReason = "running"
	TerminationSucceeded      TerminationReason = "succeeded"
	TerminationMaxIterations  TerminationReason = "max_iterations"
	TerminationRepeatedAction TerminationReason = "repeated_action"
	TerminationNoProgress     TerminationReason = "no_progress"
	TerminationBudgetExceeded TerminationReason = "budget_exceeded"
	TerminationDenied         TerminationReason = "denied"
	TerminationCancelled      TerminationReason = "cancelled"
	TerminationTimedOut       TerminationReason = "timed_out"
	TerminationInvalidOutput  TerminationReason = "invalid_output"
	TerminationFailed         TerminationReason = "failed"
)

type Policy struct {
	MaxIterations      int
	MaxModelCalls      int
	MaxToolCalls       int
	MaxRepeatedActions int
	MaxNoProgressTurns int
	MaxFormatRepairs   int
}

func DefaultPolicy(structured bool) Policy {
	policy := Policy{
		MaxIterations:      5,
		MaxModelCalls:      6,
		MaxToolCalls:       12,
		MaxRepeatedActions: 2,
		MaxNoProgressTurns: 2,
		MaxFormatRepairs:   0,
	}
	if structured {
		policy.MaxIterations = 8
		policy.MaxModelCalls = 10
		policy.MaxToolCalls = 16
		policy.MaxFormatRepairs = 1
	}
	return policy
}

func MainAgentPolicy() Policy {
	return Policy{
		MaxIterations:      10,
		MaxModelCalls:      10,
		MaxToolCalls:       24,
		MaxRepeatedActions: 2,
		MaxNoProgressTurns: 2,
		MaxFormatRepairs:   0,
	}
}

func (p Policy) validate() error {
	if p.MaxIterations <= 0 || p.MaxModelCalls <= 0 || p.MaxToolCalls <= 0 {
		return fmt.Errorf("loop iteration, model call, and tool call budgets must be positive")
	}
	if p.MaxRepeatedActions <= 0 || p.MaxNoProgressTurns <= 0 || p.MaxFormatRepairs < 0 {
		return fmt.Errorf("loop repetition, progress, and repair budgets are invalid")
	}
	return nil
}

type Metrics struct {
	Iterations    int               `json:"iterations"`
	ModelCalls    int               `json:"model_calls"`
	ToolCalls     int               `json:"tool_calls"`
	FormatRepairs int               `json:"format_repairs"`
	NoProgress    int               `json:"no_progress_turns"`
	Termination   TerminationReason `json:"termination"`
}

type Controller struct {
	policy             Policy
	metrics            Metrics
	lastActions        []string
	lastProgress       string
	seenProgress       bool
	lastDuplicate      bool
	repetitionRepair   bool
	alternatingCycle   []string
	cycleRepairPattern string
}

func NewController(policy Policy) (*Controller, error) {
	if err := policy.validate(); err != nil {
		return nil, err
	}
	return &Controller{policy: policy}, nil
}

func (g *Controller) BeginIteration() TerminationReason {
	if g.metrics.Iterations >= g.policy.MaxIterations {
		return TerminationMaxIterations
	}
	g.metrics.Iterations++
	return ""
}

func (g *Controller) RecordModelCall() TerminationReason {
	if g.metrics.ModelCalls >= g.policy.MaxModelCalls {
		return TerminationBudgetExceeded
	}
	g.metrics.ModelCalls++
	return ""
}

func (g *Controller) RecordToolCall(name string, arguments map[string]interface{}) TerminationReason {
	if g.metrics.ToolCalls >= g.policy.MaxToolCalls {
		return TerminationBudgetExceeded
	}
	g.metrics.ToolCalls++
	fingerprint := ActionFingerprint(name, arguments)
	g.lastActions = append(g.lastActions, fingerprint)
	g.alternatingCycle = append(g.alternatingCycle, fingerprint)
	if len(g.alternatingCycle) > 6 {
		g.alternatingCycle = g.alternatingCycle[len(g.alternatingCycle)-6:]
	}
	if len(g.alternatingCycle) >= 4 {
		n := len(g.alternatingCycle)
		if g.alternatingCycle[n-1] == g.alternatingCycle[n-3] && g.alternatingCycle[n-2] == g.alternatingCycle[n-4] && g.alternatingCycle[n-1] != g.alternatingCycle[n-2] {
			left, right := g.alternatingCycle[n-2], g.alternatingCycle[n-1]
			if left > right {
				left, right = right, left
			}
			pattern := left + ":" + right
			if g.cycleRepairPattern == pattern {
				return TerminationRepeatedAction
			}
			g.cycleRepairPattern = pattern
			g.repetitionRepair = true
		}
	}
	if len(g.lastActions) > g.policy.MaxRepeatedActions {
		g.lastActions = g.lastActions[len(g.lastActions)-g.policy.MaxRepeatedActions:]
	}
	if len(g.lastActions) == g.policy.MaxRepeatedActions {
		allSame := true
		for _, current := range g.lastActions[1:] {
			if current != g.lastActions[0] {
				allSame = false
				break
			}
		}
		if allSame {
			if g.lastDuplicate {
				return TerminationRepeatedAction
			}
			g.lastDuplicate = true
			g.repetitionRepair = true
			return ""
		}
	}
	g.lastDuplicate = false
	return ""
}

func (g *Controller) ConsumeRepetitionRepair() bool {
	if !g.repetitionRepair {
		return false
	}
	g.repetitionRepair = false
	return true
}

func (g *Controller) ObserveProgress(marker string) TerminationReason {
	marker = strings.TrimSpace(marker)
	if marker == "" {
		g.metrics.NoProgress++
	} else if !g.seenProgress || marker != g.lastProgress {
		g.lastProgress = marker
		g.seenProgress = true
		g.metrics.NoProgress = 0
		g.cycleRepairPattern = ""
		g.alternatingCycle = nil
	} else {
		g.metrics.NoProgress++
	}
	if g.metrics.NoProgress >= g.policy.MaxNoProgressTurns {
		return TerminationNoProgress
	}
	return ""
}

func (g *Controller) RecordFormatRepair() TerminationReason {
	if g.metrics.FormatRepairs >= g.policy.MaxFormatRepairs {
		return TerminationInvalidOutput
	}
	g.metrics.FormatRepairs++
	return ""
}

func (g *Controller) Terminate(reason TerminationReason) Metrics {
	g.metrics.Termination = reason
	return g.metrics
}

func (g *Controller) Metrics() Metrics {
	return g.metrics
}

func (g *Controller) Policy() Policy {
	return g.policy
}

func ActionFingerprint(name string, arguments map[string]interface{}) string {
	canonical := CanonicalJSON(arguments)
	sum := sha256.Sum256([]byte(strings.TrimSpace(name) + "\x00" + canonical))
	return hex.EncodeToString(sum[:])
}

func CanonicalJSON(value interface{}) string {
	encoded, err := json.Marshal(value)
	if err != nil {
		return "null"
	}
	return string(encoded)
}
