package agent

import (
	"context"
	"sync"
	"testing"
	"time"
)

func TestSubagentHeartbeatRemainsRequestScoped(t *testing.T) {
	var elapsed time.Duration
	ctx := context.WithValue(context.Background(), callbackContextKey{}, Callbacks{OnSubagentHeartbeat: func(role string, value time.Duration) {
		if role == "Coder" {
			elapsed = value
		}
	}})
	callbacks := (&Agent{}).buildSubagentCallbacks(ctx)
	callbacks.OnModelHeartbeat("Coder", 30*time.Second)
	if elapsed != 30*time.Second {
		t.Fatalf("heartbeat callback was not forwarded: %v", elapsed)
	}
}

func TestSubagentCallbacksRemainRequestScopedConcurrently(t *testing.T) {
	var mu sync.Mutex
	var events []string
	makeCallbacks := func(label string) Callbacks {
		return Callbacks{OnSubagentToolCall: func(string, string, map[string]interface{}, string, error) {
			mu.Lock()
			events = append(events, label)
			mu.Unlock()
		}}
	}
	ctxA := context.WithValue(context.Background(), callbackContextKey{}, makeCallbacks("A"))
	ctxB := context.WithValue(context.Background(), callbackContextKey{}, makeCallbacks("B"))
	callbacksA := (&Agent{}).buildSubagentCallbacks(ctxA)
	callbacksB := (&Agent{}).buildSubagentCallbacks(ctxB)

	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			callbacksA.OnToolCall("x", "y", nil, "", nil)
		}()
		go func() {
			defer wg.Done()
			callbacksB.OnToolCall("x", "y", nil, "", nil)
		}()
	}
	wg.Wait()
	mu.Lock()
	defer mu.Unlock()
	if len(events) != 200 {
		t.Fatalf("expected 200 callback events, got %d", len(events))
	}
	for _, event := range events {
		if event != "A" && event != "B" {
			t.Fatalf("unexpected callback event %q", event)
		}
	}
}
