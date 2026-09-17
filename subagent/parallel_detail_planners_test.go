package subagent

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/c86j224s/olli/config"
	"github.com/c86j224s/olli/ollama"
)

type detailPlanClient struct {
	mu         sync.Mutex
	started    int
	want       int
	allStarted chan struct{}
	release    chan struct{}
}

func (c *detailPlanClient) ListModels() ([]string, error) { return []string{"model"}, nil }
func (c *detailPlanClient) ListModelsWithContext(context.Context) ([]string, error) {
	return c.ListModels()
}
func (c *detailPlanClient) ChatStreamFull(request ollama.ChatRequest, callbacks ollama.StreamCallbacks) (*ollama.Message, error) {
	return c.ChatStreamFullWithContext(context.Background(), request, callbacks)
}
func (c *detailPlanClient) ChatStreamFullWithContext(ctx context.Context, request ollama.ChatRequest, _ ollama.StreamCallbacks) (*ollama.Message, error) {
	c.mu.Lock()
	c.started++
	if c.started == c.want {
		close(c.allStarted)
	}
	c.mu.Unlock()
	select {
	case <-c.release:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	var payload struct {
		Work ArchitectureWork `json:"work_package"`
	}
	for _, message := range request.Messages {
		if json.Unmarshal([]byte(message.Content), &payload) == nil && payload.Work.ID != "" {
			break
		}
	}
	detail := DetailPlan{PackageID: payload.Work.ID, Steps: []PlanStep{{ID: "step-1", Objective: payload.Work.Objective, AllowedFiles: append([]string(nil), payload.Work.Files...), Acceptance: append([]string(nil), payload.Work.Acceptance...), Verification: []string{}}}}
	encoded, _ := json.Marshal(detail)
	return &ollama.Message{Role: "assistant", Content: string(encoded)}, nil
}

func TestDetailArchitectureWorksFansOutAndPreservesPackageOrder(t *testing.T) {
	root := t.TempDir()
	cfg, err := config.LoadConfig(root + "/config.json")
	if err != nil {
		t.Fatal(err)
	}
	client := &detailPlanClient{want: 3, allStarted: make(chan struct{}), release: make(chan struct{})}
	provider := &recordingLeaseProvider{client: client}
	runner := NewRunner(provider, "model", cfg, root, "", SubagentCallbacks{}, root)
	roles, err := NewModelTeamRolesWithModels(runner, TeamModels{DetailPlanner: "model"})
	if err != nil {
		t.Fatal(err)
	}
	architecture := &ArchitecturePlan{Goal: "feature", Packages: []ArchitectureWork{
		{ID: "package-1", Objective: "one", Files: []string{"one.go"}, Acceptance: []string{"one works"}},
		{ID: "package-2", Objective: "two", Files: []string{"two.go"}, Acceptance: []string{"two works"}},
		{ID: "package-3", Objective: "three", Files: []string{"three.go"}, Acceptance: []string{"three works"}},
	}}
	done := make(chan struct{})
	var details []DetailPlan
	var runErr error
	go func() {
		details, runErr = roles.detailArchitectureWorks(context.Background(), architecture)
		close(done)
	}()
	select {
	case <-client.allStarted:
		close(client.release)
	case <-time.After(time.Second):
		t.Fatal("detail planners did not start concurrently")
	}
	<-done
	if runErr != nil {
		t.Fatal(runErr)
	}
	if len(details) != len(architecture.Packages) {
		t.Fatalf("unexpected details: %#v", details)
	}
	for index, work := range architecture.Packages {
		if details[index].PackageID != work.ID {
			t.Fatalf("detail order changed: %#v", details)
		}
	}
}
