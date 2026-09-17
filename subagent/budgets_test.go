package subagent

import (
	"context"
	"testing"
	"time"
)

func TestDefaultRoleBudgetsAreBoundedAndOrdered(t *testing.T) {
	planner := defaultRoleBudget(TypePlanner)
	coder := defaultRoleBudget(TypeCoder)
	tester := defaultRoleBudget(TypeTester)
	reviewer := defaultRoleBudget(TypeReviewer)
	if planner.Timeout != 10*time.Minute || coder.Timeout != 40*time.Minute || tester.Timeout != 6*time.Minute || reviewer.Timeout != 20*time.Minute {
		t.Fatalf("unexpected role timeouts: planner=%v coder=%v tester=%v reviewer=%v", planner.Timeout, coder.Timeout, tester.Timeout, reviewer.Timeout)
	}
	for role, budget := range map[SubagentType]roleBudget{
		TypePlanner: planner, TypeCoder: coder, TypeTester: tester, TypeReviewer: reviewer,
	} {
		if budget.NumPredict <= 0 || budget.Timeout <= 0 {
			t.Fatalf("%s has no bounded budget: %#v", role, budget)
		}
	}
	if coder.NumPredict <= reviewer.NumPredict || coder.Timeout <= reviewer.Timeout {
		t.Fatalf("coder budget should allow longer source generation: coder=%#v reviewer=%#v", coder, reviewer)
	}
	if tester.NumPredict >= planner.NumPredict {
		t.Fatalf("tester output budget should be smaller than planner: tester=%#v planner=%#v", tester, planner)
	}
}

func TestArchitectureReviewBudgetFitsBoundedRepairCalls(t *testing.T) {
	minimum := time.Duration(maxArchitectRepairs+1) * defaultPlannerTimeout
	if architectureReviewTimeout != 70*time.Minute || architectureReviewTimeout <= minimum {
		t.Fatalf("architecture review budget is invalid: %v, minimum %v", architectureReviewTimeout, minimum)
	}
}

func TestWithRoleTimeoutPreservesEarlierParentDeadline(t *testing.T) {
	parent, cancelParent := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancelParent()
	ctx, cancel := withRoleTimeout(parent, defaultRoleBudget(TypeCoder))
	defer cancel()
	parentDeadline, _ := parent.Deadline()
	deadline, _ := ctx.Deadline()
	if !deadline.Equal(parentDeadline) {
		t.Fatalf("role timeout extended parent deadline: parent=%v child=%v", parentDeadline, deadline)
	}
}
