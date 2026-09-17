package subagent

import (
	"context"
	"sync"
	"testing"
	"time"
)

type parallelReviewRoles struct {
	*scriptedTeamRoles
	mu         sync.Mutex
	started    int
	allStarted chan struct{}
	release    chan struct{}
}

func (p *parallelReviewRoles) ParallelReviewsEnabled() bool { return true }

func (p *parallelReviewRoles) Review(ctx context.Context, task ReviewTask) (*ReviewReport, error) {
	p.mu.Lock()
	p.started++
	if p.started == len(defaultReviewDimensions) {
		close(p.allStarted)
	}
	p.mu.Unlock()
	select {
	case <-p.release:
		return &ReviewReport{Summary: string(task.Dimension)}, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func TestRunDimensionReviewsFansOutAndPreservesOrder(t *testing.T) {
	roles := &parallelReviewRoles{scriptedTeamRoles: &scriptedTeamRoles{}, allStarted: make(chan struct{}), release: make(chan struct{})}
	done := make(chan struct{})
	var reviews []DimensionReview
	var runErr error
	go func() {
		reviews, runErr = runDimensionReviews(context.Background(), roles, defaultReviewDimensions, ReviewContext{})
		close(done)
	}()
	select {
	case <-roles.allStarted:
		close(roles.release)
	case <-time.After(time.Second):
		t.Fatal("review dimensions did not start concurrently")
	}
	<-done
	if runErr != nil {
		t.Fatal(runErr)
	}
	if len(reviews) != len(defaultReviewDimensions) {
		t.Fatalf("unexpected reviews: %#v", reviews)
	}
	for index, dimension := range defaultReviewDimensions {
		if reviews[index].Dimension != dimension || reviews[index].Report.Summary != string(dimension) {
			t.Fatalf("review order changed: %#v", reviews)
		}
	}
}
