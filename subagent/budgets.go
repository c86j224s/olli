package subagent

import (
	"context"
	"time"
)

const (
	defaultPlannerNumPredict  = 1536
	defaultCoderNumPredict    = 4096
	defaultTesterNumPredict   = 512
	defaultReviewerNumPredict = 1536

	defaultPlannerTimeout  = 2 * time.Minute
	defaultCoderTimeout    = 8 * time.Minute
	defaultTesterTimeout   = 90 * time.Second
	defaultReviewerTimeout = 2 * time.Minute
)

type roleBudget struct {
	NumPredict int
	Timeout    time.Duration
}

const planningPipelineTimeout = 20 * time.Minute

func defaultRoleBudget(subType SubagentType) roleBudget {
	switch subType {
	case TypePlanner:
		return roleBudget{NumPredict: defaultPlannerNumPredict, Timeout: defaultPlannerTimeout}
	case TypeCoder:
		return roleBudget{NumPredict: defaultCoderNumPredict, Timeout: defaultCoderTimeout}
	case TypeTester:
		return roleBudget{NumPredict: defaultTesterNumPredict, Timeout: defaultTesterTimeout}
	case TypeReviewer:
		return roleBudget{NumPredict: defaultReviewerNumPredict, Timeout: defaultReviewerTimeout}
	default:
		return roleBudget{}
	}
}

func withRoleTimeout(parent context.Context, budget roleBudget) (context.Context, context.CancelFunc) {
	if budget.Timeout <= 0 {
		return context.WithCancel(parent)
	}
	return context.WithTimeout(parent, budget.Timeout)
}
