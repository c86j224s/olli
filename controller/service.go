package controller

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/c86j224s/olli/agent"
	"github.com/c86j224s/olli/config"
	"github.com/c86j224s/olli/gateway"
	"github.com/c86j224s/olli/ollama"
	"github.com/c86j224s/olli/runstate"
	"github.com/c86j224s/olli/session"
)

type StartResult struct {
	RunID string `json:"run_id"`
}

type PermissionRequest struct {
	ID       string         `json:"id"`
	RunID    string         `json:"run_id"`
	ToolName string         `json:"tool_name"`
	Args     map[string]any `json:"args,omitempty"`
}

type ReadyInfo struct {
	Workspace      string   `json:"workspace"`
	Model          string   `json:"model"`
	Models         []string `json:"models"`
	GatewayEnabled bool     `json:"gateway_enabled"`
}

type Hooks struct {
	OnPermission func(PermissionRequest)
	OnDiagnostic func(string)
}

type permissionDecision struct {
	allowed bool
	always  bool
}

type pendingPermission struct {
	decision chan permissionDecision
}

type Service struct {
	mu             sync.Mutex
	publishMu      sync.Mutex
	agent          *agent.Agent
	gateway        *gateway.Gateway
	store          *runstate.MemoryStore
	recorder       *runstate.Recorder
	currentRun     string
	cancelRun      context.CancelFunc
	permissions    map[string]pendingPermission
	permissionInfo map[string]PermissionRequest
	nextID         uint64
	workspace      string
	model          string
	models         []string
	hooks          Hooks
	close          func()
}

func New(workspace, configPath string, hooks Hooks) (*Service, error) {
	workspace = strings.TrimSpace(workspace)
	if workspace == "" {
		return nil, fmt.Errorf("workspace is required")
	}
	root, err := filepath.Abs(workspace)
	if err != nil {
		return nil, err
	}
	if configPath == "" {
		configPath = filepath.Join(root, "config.json")
	}
	cfg, err := config.LoadConfig(configPath)
	if err != nil {
		return nil, err
	}
	var client ollama.ChatClient = ollama.NewClient("http://localhost:11434")
	var aiGateway *gateway.Gateway
	closeGateway := func() {}
	if cfg.AIGateway.Enabled {
		aiGateway, err = gateway.New(cfg.AIGateway)
		if err != nil {
			return nil, err
		}
		gatewayCtx, cancel := context.WithCancel(context.Background())
		aiGateway.Start(gatewayCtx)
		closeGateway = func() { cancel(); aiGateway.Close() }
		client = aiGateway
	}
	models, err := client.ListModels()
	if err != nil || len(models) == 0 {
		closeGateway()
		return nil, fmt.Errorf("load Ollama models: %w", err)
	}
	model := ChooseModel(models)
	sessions, err := session.NewManager(filepath.Join(root, "sessions"), root)
	if err != nil {
		closeGateway()
		return nil, err
	}
	ag := agent.New(client, model, "You are an intelligent AI assistant equipped with Goal Steering and Subagent Delegation capabilities. Stay focused on achieving active goals.", sessions, cfg)
	if err := ag.EnableWorkflows(root); err != nil {
		closeGateway()
		return nil, err
	}
	recorder, err := runstate.NewRecorder(root)
	if err != nil {
		_ = ag.Close()
		closeGateway()
		return nil, err
	}
	service := &Service{agent: ag, gateway: aiGateway, store: runstate.NewMemoryStore(8192), recorder: recorder, permissions: make(map[string]pendingPermission), permissionInfo: make(map[string]PermissionRequest), workspace: root, model: model, models: models, hooks: hooks}
	service.close = func() { _ = ag.Close(); closeGateway() }
	if aiGateway != nil {
		aiGateway.SetRouteObserver(func(route gateway.RouteEvent) {
			service.Publish(runstate.Event{RunID: service.ActiveRunID(), Kind: runstate.KindModelRouted, NodeID: route.NodeID, Role: route.Role, Model: route.Model, RouteNodeID: route.NodeID, Status: route.Status})
		})
	}
	return service, nil
}

func (s *Service) Close() {
	if s == nil {
		return
	}
	s.mu.Lock()
	cancel := s.cancelRun
	s.cancelRun = nil
	s.currentRun = ""
	s.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	if s.close != nil {
		s.close()
	}
}

func (s *Service) ReadyInfo() ReadyInfo {
	if s == nil {
		return ReadyInfo{}
	}
	return ReadyInfo{Workspace: s.workspace, Model: s.model, Models: append([]string(nil), s.models...), GatewayEnabled: s.gateway != nil}
}

func (s *Service) Events() *runstate.MemoryStore { return s.store }

func (s *Service) StartRun(prompt, requestedID string) (StartResult, error) {
	prompt = strings.TrimSpace(prompt)
	if prompt == "" {
		return StartResult{}, fmt.Errorf("prompt is required")
	}
	s.mu.Lock()
	if s.cancelRun != nil {
		s.mu.Unlock()
		return StartResult{}, fmt.Errorf("another run is active")
	}
	s.nextID++
	runID := strings.TrimSpace(requestedID)
	if runID == "" {
		runID = fmt.Sprintf("run-%d-%d", time.Now().UnixMilli(), s.nextID)
	}
	if !runstate.ValidRunID(runID) {
		s.mu.Unlock()
		return StartResult{}, fmt.Errorf("invalid run id")
	}
	ctx, cancel := context.WithCancel(context.Background())
	s.currentRun = runID
	s.cancelRun = cancel
	s.mu.Unlock()
	go s.executeRun(ctx, runID, prompt)
	return StartResult{RunID: runID}, nil
}

func (s *Service) executeRun(ctx context.Context, runID, prompt string) {
	callbacks := agent.Callbacks{
		OnRunEvent: func(event agent.AgentRunEvent) {
			s.Publish(runstate.Event{RunID: runID, Kind: runstate.Kind(event.Kind), GraphID: event.GraphID, NodeID: event.NodeID, ParentID: event.ParentID, Phase: event.Phase, Role: event.Role, Model: event.Model, RouteNodeID: event.RouteNodeID, Status: event.Status, Message: event.Message, ToolName: event.ToolName, DurationMS: event.DurationMS, Metadata: event.Metadata})
		},
		OnContentToken: func(token string) {
			s.Publish(runstate.Event{RunID: runID, Kind: runstate.KindAssistantContent, GraphID: "main-agent", NodeID: "main", Role: "main", Model: s.model, Status: "streaming", Message: token})
		},
		ConfirmToolCallWithActionContext: func(ctx context.Context, toolName string, args map[string]interface{}) (bool, bool) {
			return s.requestPermission(ctx, runID, toolName, args)
		},
	}
	answer, err := s.agent.AskWithContext(ctx, prompt, callbacks)
	if err == nil && strings.TrimSpace(answer) != "" {
		s.Publish(runstate.Event{RunID: runID, Kind: runstate.KindAssistantContent, GraphID: "main-agent", NodeID: "main", Role: "main", Model: s.model, Status: "completed", Message: answer})
	}
	if errors.Is(err, context.Canceled) {
		s.Publish(runstate.Event{RunID: runID, Kind: runstate.KindRunCancelled, GraphID: "main-agent", NodeID: "main", Status: "cancelled", Message: "Run cancelled"})
	}
	s.mu.Lock()
	if s.currentRun == runID {
		s.currentRun = ""
		s.cancelRun = nil
	}
	s.mu.Unlock()
}

func (s *Service) CancelRun() error {
	s.mu.Lock()
	cancel := s.cancelRun
	s.mu.Unlock()
	if cancel == nil {
		return fmt.Errorf("no active run")
	}
	cancel()
	return nil
}

func (s *Service) requestPermission(ctx context.Context, runID, toolName string, args map[string]interface{}) (bool, bool) {
	s.mu.Lock()
	s.nextID++
	id := fmt.Sprintf("permission-%d", s.nextID)
	pending := pendingPermission{decision: make(chan permissionDecision, 1)}
	permission := PermissionRequest{ID: id, RunID: runID, ToolName: toolName, Args: RedactArgs(args)}
	s.permissions[id] = pending
	s.permissionInfo[id] = permission
	s.mu.Unlock()
	if s.hooks.OnPermission != nil {
		s.hooks.OnPermission(permission)
	}
	select {
	case decision := <-pending.decision:
		return decision.allowed, decision.always
	case <-ctx.Done():
		s.mu.Lock()
		delete(s.permissions, id)
		delete(s.permissionInfo, id)
		s.mu.Unlock()
		return false, false
	}
}

func (s *Service) ResolvePermission(id string, allowed, always bool) error {
	s.mu.Lock()
	pending, exists := s.permissions[id]
	if exists {
		delete(s.permissions, id)
		delete(s.permissionInfo, id)
	}
	s.mu.Unlock()
	if !exists {
		return fmt.Errorf("unknown permission request")
	}
	pending.decision <- permissionDecision{allowed: allowed, always: always}
	return nil
}

func (s *Service) PendingPermissions() []PermissionRequest {
	s.mu.Lock()
	defer s.mu.Unlock()
	result := make([]PermissionRequest, 0, len(s.permissionInfo))
	for _, permission := range s.permissionInfo {
		result = append(result, permission)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].ID < result[j].ID })
	return result
}

func (s *Service) Snapshot(runID string, after uint64, limit int) ([]runstate.Event, error) {
	events := s.store.Snapshot(runstate.Filter{RunID: runID, After: after, Limit: limit})
	if len(events) == 0 && runID != "" && after == 0 {
		return s.recorder.Load(runID, limit)
	}
	return events, nil
}
func (s *Service) ListRuns() ([]string, error) { return s.recorder.List() }
func (s *Service) GatewayStatus() []gateway.NodeStatus {
	if s.gateway == nil {
		return []gateway.NodeStatus{}
	}
	return s.gateway.Status()
}
func (s *Service) SetDrain(nodeID string, draining bool) error {
	if s.gateway == nil {
		return fmt.Errorf("AI gateway is disabled")
	}
	return s.gateway.SetDrain(nodeID, draining)
}
func (s *Service) ActiveRunID() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.currentRun == "" {
		return "system"
	}
	return s.currentRun
}
func (s *Service) Publish(event runstate.Event) {
	s.publishMu.Lock()
	defer s.publishMu.Unlock()
	event = s.store.Publish(event)
	if event.RunID != "system" {
		if err := s.recorder.Append(event); err != nil && s.hooks.OnDiagnostic != nil {
			s.hooks.OnDiagnostic("run event persistence failed: " + err.Error())
		}
	}
}

func RedactArgs(args map[string]interface{}) map[string]any {
	result := make(map[string]any)
	for key, value := range args {
		lower := strings.ToLower(key)
		if strings.Contains(lower, "token") || strings.Contains(lower, "secret") || strings.Contains(lower, "password") || strings.Contains(lower, "content") || strings.Contains(lower, "source") || strings.Contains(lower, "header") {
			continue
		}
		result[key] = value
	}
	return result
}

func ChooseModel(models []string) string {
	for _, preferred := range []string{"gemma4:12b", "qwen3.5:0.8b"} {
		for _, model := range models {
			if strings.HasPrefix(model, preferred) {
				return model
			}
		}
	}
	if len(models) == 0 {
		return ""
	}
	return models[0]
}
