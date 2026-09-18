package runstate

import (
	"context"
	"sync"
	"testing"
	"time"
)

func TestMemoryStoreSequencesConcurrentPublishers(t *testing.T) {
	store := NewMemoryStore(1024)
	var wg sync.WaitGroup
	for worker := 0; worker < 8; worker++ {
		wg.Add(1)
		go func(worker int) {
			defer wg.Done()
			for index := 0; index < 50; index++ {
				store.Publish(Event{RunID: "run", Kind: KindNodeHeartbeat, Message: "alive", Metadata: map[string]any{"worker": worker}})
			}
		}(worker)
	}
	wg.Wait()
	events := store.Snapshot(Filter{RunID: "run"})
	if len(events) != 400 {
		t.Fatalf("got %d events, want 400", len(events))
	}
	for index, event := range events {
		if event.Sequence != uint64(index+1) {
			t.Fatalf("event %d sequence = %d", index, event.Sequence)
		}
	}
}

func TestMemoryStoreFiltersAndBoundsHistory(t *testing.T) {
	store := NewMemoryStore(3)
	for index := 0; index < 5; index++ {
		store.Publish(Event{RunID: "run", Kind: KindPhaseChanged, Message: "phase"})
	}
	events := store.Snapshot(Filter{RunID: "run", After: 2})
	if len(events) != 3 || events[0].Sequence != 3 || events[2].Sequence != 5 {
		t.Fatalf("unexpected bounded history: %#v", events)
	}
	limited := store.Snapshot(Filter{RunID: "run", Limit: 2})
	if len(limited) != 2 || limited[0].Sequence != 4 {
		t.Fatalf("unexpected limited history: %#v", limited)
	}
}

func TestMemoryStoreSubscriptionAndCancellation(t *testing.T) {
	store := NewMemoryStore(10)
	ctx, cancelCtx := context.WithCancel(context.Background())
	stream, cancel := store.Subscribe(ctx, Filter{RunID: "wanted"})
	store.Publish(Event{RunID: "other", Kind: KindNodeStarted})
	want := store.Publish(Event{RunID: "wanted", Kind: KindNodeStarted})
	select {
	case got := <-stream:
		if got.Sequence != want.Sequence {
			t.Fatalf("got sequence %d, want %d", got.Sequence, want.Sequence)
		}
	case <-time.After(time.Second):
		t.Fatal("subscription did not receive event")
	}
	cancelCtx()
	cancel()
	select {
	case _, ok := <-stream:
		if ok {
			t.Fatal("subscription remained open")
		}
	case <-time.After(time.Second):
		t.Fatal("subscription did not close")
	}
}

func TestMemoryStoreRedactsSensitiveMetadata(t *testing.T) {
	store := NewMemoryStore(10)
	event := store.Publish(Event{RunID: "run", Kind: KindModelRouted, Message: " routed ", Metadata: map[string]any{
		"route_node_id": "gpu-a",
		"auth_token":    "secret",
		"headers":       "private",
		"source_text":   "code",
	}})
	if event.Message != "routed" || event.Metadata["route_node_id"] != "gpu-a" {
		t.Fatalf("safe metadata was changed: %#v", event)
	}
	for _, key := range []string{"auth_token", "headers", "source_text"} {
		if _, exists := event.Metadata[key]; exists {
			t.Fatalf("sensitive key %q remained: %#v", key, event.Metadata)
		}
	}
}
