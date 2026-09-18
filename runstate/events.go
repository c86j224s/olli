package runstate

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
)

type Kind string

const (
	KindRunStarted       Kind = "run_started"
	KindRunCompleted     Kind = "run_completed"
	KindRunFailed        Kind = "run_failed"
	KindRunCancelled     Kind = "run_cancelled"
	KindPhaseChanged     Kind = "phase_changed"
	KindNodeStarted      Kind = "node_started"
	KindNodeHeartbeat    Kind = "node_heartbeat"
	KindNodeCompleted    Kind = "node_completed"
	KindNodeFailed       Kind = "node_failed"
	KindModelRouted      Kind = "model_routed"
	KindToolStarted      Kind = "tool_started"
	KindToolCompleted    Kind = "tool_completed"
	KindFindingCreated   Kind = "finding_created"
	KindArtifactCreated  Kind = "artifact_created"
	KindGatewaySnapshot  Kind = "gateway_snapshot"
	KindAssistantContent Kind = "assistant_content"
)

type Event struct {
	Sequence    uint64         `json:"sequence"`
	Timestamp   time.Time      `json:"timestamp"`
	RunID       string         `json:"run_id"`
	Kind        Kind           `json:"kind"`
	GraphID     string         `json:"graph_id,omitempty"`
	NodeID      string         `json:"node_id,omitempty"`
	ParentID    string         `json:"parent_id,omitempty"`
	Phase       string         `json:"phase,omitempty"`
	Role        string         `json:"role,omitempty"`
	Model       string         `json:"model,omitempty"`
	RouteNodeID string         `json:"route_node_id,omitempty"`
	Status      string         `json:"status,omitempty"`
	Message     string         `json:"message,omitempty"`
	ToolName    string         `json:"tool_name,omitempty"`
	DurationMS  int64          `json:"duration_ms,omitempty"`
	Metadata    map[string]any `json:"metadata,omitempty"`
}

type Filter struct {
	RunID string
	After uint64
	Limit int
}

type Store interface {
	Publish(Event) Event
	Snapshot(Filter) []Event
	Subscribe(context.Context, Filter) (<-chan Event, func())
}

type subscriber struct {
	filter Filter
	ch     chan Event
}

type MemoryStore struct {
	mu              sync.RWMutex
	events          []Event
	nextSequence    uint64
	nextSubscriber  uint64
	subscribers     map[uint64]subscriber
	capacity        int
	subscriberQueue int
	now             func() time.Time
}

func NewMemoryStore(capacity int) *MemoryStore {
	if capacity <= 0 {
		capacity = 4096
	}
	return &MemoryStore{
		nextSequence:    1,
		subscribers:     make(map[uint64]subscriber),
		capacity:        capacity,
		subscriberQueue: 256,
		now:             time.Now,
	}
}

func (s *MemoryStore) Publish(event Event) Event {
	if s == nil {
		return event
	}
	event.RunID = strings.TrimSpace(event.RunID)
	if event.RunID == "" {
		event.RunID = "system"
	}
	if event.Timestamp.IsZero() {
		event.Timestamp = s.now().UTC()
	} else {
		event.Timestamp = event.Timestamp.UTC()
	}
	event.Message = boundedText(event.Message, 4096)
	event.Metadata = sanitizeMetadata(event.Metadata)

	s.mu.Lock()
	event.Sequence = s.nextSequence
	s.nextSequence++
	s.events = append(s.events, event)
	if overflow := len(s.events) - s.capacity; overflow > 0 {
		copy(s.events, s.events[overflow:])
		s.events = s.events[:s.capacity]
	}
	for _, sub := range s.subscribers {
		if !matches(event, sub.filter) {
			continue
		}
		select {
		case sub.ch <- event:
		default:
		}
	}
	s.mu.Unlock()
	return event
}

func (s *MemoryStore) Snapshot(filter Filter) []Event {
	if s == nil {
		return nil
	}
	s.mu.RLock()
	result := make([]Event, 0, len(s.events))
	for _, event := range s.events {
		if matches(event, filter) {
			result = append(result, cloneEvent(event))
		}
	}
	s.mu.RUnlock()
	if filter.Limit > 0 && len(result) > filter.Limit {
		result = result[len(result)-filter.Limit:]
	}
	return result
}

func (s *MemoryStore) Subscribe(ctx context.Context, filter Filter) (<-chan Event, func()) {
	if s == nil {
		closed := make(chan Event)
		close(closed)
		return closed, func() {}
	}
	if ctx == nil {
		ctx = context.Background()
	}
	s.mu.Lock()
	id := s.nextSubscriber
	s.nextSubscriber++
	ch := make(chan Event, s.subscriberQueue)
	s.subscribers[id] = subscriber{filter: filter, ch: ch}
	s.mu.Unlock()
	var once sync.Once
	cancel := func() {
		once.Do(func() {
			s.mu.Lock()
			if _, exists := s.subscribers[id]; exists {
				delete(s.subscribers, id)
				close(ch)
			}
			s.mu.Unlock()
		})
	}
	go func() {
		<-ctx.Done()
		cancel()
	}()
	return ch, cancel
}

func matches(event Event, filter Filter) bool {
	if filter.RunID != "" && event.RunID != filter.RunID {
		return false
	}
	return event.Sequence > filter.After
}

func cloneEvent(event Event) Event {
	cloned := event
	if event.Metadata != nil {
		cloned.Metadata = make(map[string]any, len(event.Metadata))
		for key, value := range event.Metadata {
			cloned.Metadata[key] = value
		}
	}
	return cloned
}

func sanitizeMetadata(metadata map[string]any) map[string]any {
	if len(metadata) == 0 {
		return nil
	}
	keys := make([]string, 0, len(metadata))
	for key := range metadata {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	result := make(map[string]any, len(keys))
	for _, key := range keys {
		lower := strings.ToLower(key)
		if strings.Contains(lower, "token") || strings.Contains(lower, "secret") || strings.Contains(lower, "authorization") || strings.Contains(lower, "header") || strings.Contains(lower, "content") || strings.Contains(lower, "source") {
			continue
		}
		switch value := metadata[key].(type) {
		case string:
			result[key] = boundedText(value, 512)
		case bool, int, int32, int64, uint, uint32, uint64, float32, float64:
			result[key] = value
		case []string:
			if len(value) > 32 {
				value = value[:32]
			}
			copied := make([]string, len(value))
			for index, item := range value {
				copied[index] = boundedText(item, 128)
			}
			result[key] = copied
		default:
			result[key] = boundedText(fmt.Sprint(value), 256)
		}
	}
	return result
}

func boundedText(value string, limit int) string {
	value = strings.TrimSpace(value)
	if limit <= 0 || len(value) <= limit {
		return value
	}
	return value[:limit] + "…"
}

var ErrPermissionPending = errors.New("permission decision is pending")
