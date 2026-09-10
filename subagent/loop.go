package subagent

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

type LoopTerminationReason string

const (
	LoopTerminationSucceeded      LoopTerminationReason = "succeeded"
	LoopTerminationMaxIterations  LoopTerminationReason = "max_iterations"
	LoopTerminationRepeatedAction LoopTerminationReason = "repeated_action"
	LoopTerminationNoProgress     LoopTerminationReason = "no_progress"
	LoopTerminationBudgetExceeded LoopTerminationReason = "budget_exceeded"
	LoopTerminationDenied         LoopTerminationReason = "denied"
	LoopTerminationCancelled      LoopTerminationReason = "cancelled"
	LoopTerminationTimedOut       LoopTerminationReason = "timed_out"
	LoopTerminationInvalidOutput  LoopTerminationReason = "invalid_output"
	LoopTerminationFailed         LoopTerminationReason = "failed"
)

type LoopPolicy struct {
	MaxIterations      int
	MaxModelCalls      int
	MaxToolCalls       int
	MaxRepeatedActions int
	MaxNoProgressTurns int
	MaxFormatRepairs   int
}

func DefaultLoopPolicy(structured bool) LoopPolicy {
	policy := LoopPolicy{
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

func (p LoopPolicy) validate() error {
	if p.MaxIterations <= 0 || p.MaxModelCalls <= 0 || p.MaxToolCalls <= 0 {
		return fmt.Errorf("loop iteration, model call, and tool call budgets must be positive")
	}
	if p.MaxRepeatedActions <= 0 || p.MaxNoProgressTurns <= 0 || p.MaxFormatRepairs < 0 {
		return fmt.Errorf("loop repetition, progress, and repair budgets are invalid")
	}
	return nil
}

type LoopMetrics struct {
	Iterations    int                   `json:"iterations"`
	ModelCalls    int                   `json:"model_calls"`
	ToolCalls     int                   `json:"tool_calls"`
	FormatRepairs int                   `json:"format_repairs"`
	NoProgress    int                   `json:"no_progress_turns"`
	Termination   LoopTerminationReason `json:"termination"`
}

type loopGuard struct {
	policy           LoopPolicy
	metrics          LoopMetrics
	lastActions      []string
	lastProgress     string
	seenProgress     bool
	lastDuplicate    bool
	repetitionRepair bool
}

func newLoopGuard(policy LoopPolicy) (*loopGuard, error) {
	if err := policy.validate(); err != nil {
		return nil, err
	}
	return &loopGuard{policy: policy}, nil
}

func (g *loopGuard) beginIteration() LoopTerminationReason {
	if g.metrics.Iterations >= g.policy.MaxIterations {
		return LoopTerminationMaxIterations
	}
	g.metrics.Iterations++
	return ""
}

func (g *loopGuard) recordModelCall() LoopTerminationReason {
	if g.metrics.ModelCalls >= g.policy.MaxModelCalls {
		return LoopTerminationBudgetExceeded
	}
	g.metrics.ModelCalls++
	return ""
}

func (g *loopGuard) recordToolCall(name string, arguments map[string]interface{}) LoopTerminationReason {
	if g.metrics.ToolCalls >= g.policy.MaxToolCalls {
		return LoopTerminationBudgetExceeded
	}
	g.metrics.ToolCalls++
	fingerprint := actionFingerprint(name, arguments)
	g.lastActions = append(g.lastActions, fingerprint)
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
				return LoopTerminationRepeatedAction
			}
			g.lastDuplicate = true
			g.repetitionRepair = true
			return ""
		}
	}
	g.lastDuplicate = false
	return ""
}

func (g *loopGuard) consumeRepetitionRepair() bool {
	if !g.repetitionRepair {
		return false
	}
	g.repetitionRepair = false
	return true
}

func (g *loopGuard) observeProgress(marker string) LoopTerminationReason {
	marker = strings.TrimSpace(marker)
	if marker == "" {
		g.metrics.NoProgress++
	} else if !g.seenProgress || marker != g.lastProgress {
		g.lastProgress = marker
		g.seenProgress = true
		g.metrics.NoProgress = 0
	} else {
		g.metrics.NoProgress++
	}
	if g.metrics.NoProgress >= g.policy.MaxNoProgressTurns {
		return LoopTerminationNoProgress
	}
	return ""
}

func (g *loopGuard) recordFormatRepair() LoopTerminationReason {
	if g.metrics.FormatRepairs >= g.policy.MaxFormatRepairs {
		return LoopTerminationInvalidOutput
	}
	g.metrics.FormatRepairs++
	return ""
}

func (g *loopGuard) terminate(reason LoopTerminationReason) LoopMetrics {
	g.metrics.Termination = reason
	return g.metrics
}

func actionFingerprint(name string, arguments map[string]interface{}) string {
	canonical := canonicalJSON(arguments)
	sum := sha256.Sum256([]byte(strings.TrimSpace(name) + "\x00" + canonical))
	return hex.EncodeToString(sum[:])
}

func canonicalJSON(value interface{}) string {
	normalized := normalizeCanonicalValue(value)
	encoded, err := json.Marshal(normalized)
	if err != nil {
		return "null"
	}
	return string(encoded)
}

func normalizeCanonicalValue(value interface{}) interface{} {
	switch typed := value.(type) {
	case map[string]interface{}:
		keys := make([]string, 0, len(typed))
		for key := range typed {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		ordered := make([]interface{}, 0, len(keys)*2)
		for _, key := range keys {
			ordered = append(ordered, key, normalizeCanonicalValue(typed[key]))
		}
		return ordered
	case []interface{}:
		out := make([]interface{}, len(typed))
		for index, child := range typed {
			out[index] = normalizeCanonicalValue(child)
		}
		return out
	default:
		return typed
	}
}
