package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
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

type request struct {
	ID      string         `json:"id"`
	Method  string         `json:"method"`
	Prompt  string         `json:"prompt,omitempty"`
	RunID   string         `json:"run_id,omitempty"`
	NodeID  string         `json:"node_id,omitempty"`
	Allowed *bool          `json:"allowed,omitempty"`
	Always  bool           `json:"always,omitempty"`
	Params  map[string]any `json:"params,omitempty"`
}

type response struct {
	Type   string         `json:"type"`
	ID     string         `json:"id,omitempty"`
	OK     bool           `json:"ok,omitempty"`
	Error  string         `json:"error,omitempty"`
	Result any            `json:"result,omitempty"`
	Event  runstate.Event `json:"event,omitempty"`
}

type permissionRequest struct {
	ID       string         `json:"id"`
	RunID    string         `json:"run_id"`
	ToolName string         `json:"tool_name"`
	Args     map[string]any `json:"args,omitempty"`
}

type pendingPermission struct {
	decision chan permissionDecision
}

type permissionDecision struct {
	allowed bool
	always  bool
}

type service struct {
	mu          sync.Mutex
	writeMu     sync.Mutex
	agent       *agent.Agent
	gateway     *gateway.Gateway
	store       *runstate.MemoryStore
	recorder    *runstate.Recorder
	currentRun  string
	cancelRun   context.CancelFunc
	permissions map[string]pendingPermission
	nextID      uint64
	workspace   string
}

func main() {
	workspaceFlag := flag.String("workspace", "", "workspace root")
	configFlag := flag.String("config", "", "config.json path")
	flag.Parse()
	workspace, err := resolveWorkspace(*workspaceFlag)
	if err != nil {
		fatal(err)
	}
	configPath := *configFlag
	if configPath == "" {
		configPath = filepath.Join(workspace, "config.json")
	}
	cfg, err := config.LoadConfig(configPath)
	if err != nil {
		fatal(err)
	}

	var client ollama.ChatClient = ollama.NewClient("http://localhost:11434")
	var aiGateway *gateway.Gateway
	if cfg.AIGateway.Enabled {
		aiGateway, err = gateway.New(cfg.AIGateway)
		if err != nil {
			fatal(err)
		}
		gatewayCtx, cancel := context.WithCancel(context.Background())
		defer cancel()
		aiGateway.Start(gatewayCtx)
		defer aiGateway.Close()
		client = aiGateway
	}
	models, err := client.ListModels()
	if err != nil || len(models) == 0 {
		fatal(fmt.Errorf("load Ollama models: %w", err))
	}
	model := chooseModel(models)
	sessions, err := session.NewManager(filepath.Join(workspace, "sessions"), workspace)
	if err != nil {
		fatal(err)
	}
	ag := agent.New(client, model, "You are an intelligent AI assistant equipped with Goal Steering and Subagent Delegation capabilities. Stay focused on achieving active goals.", sessions, cfg)
	if err := ag.EnableWorkflows(workspace); err != nil {
		fatal(err)
	}
	defer ag.Close()

	recorder, err := runstate.NewRecorder(workspace)
	if err != nil {
		fatal(err)
	}
	svc := &service{agent: ag, gateway: aiGateway, store: runstate.NewMemoryStore(8192), recorder: recorder, permissions: make(map[string]pendingPermission), workspace: workspace}
	if aiGateway != nil {
		aiGateway.SetRouteObserver(func(route gateway.RouteEvent) {
			svc.publish(runstate.Event{RunID: svc.activeRunID(), Kind: runstate.KindModelRouted, NodeID: route.NodeID, Role: route.Role, Model: route.Model, RouteNodeID: route.NodeID, Status: route.Status})
		})
	}
	events, cancelEvents := svc.store.Subscribe(context.Background(), runstate.Filter{})
	defer cancelEvents()
	go func() {
		for event := range events {
			svc.write(response{Type: "event", Event: event})
		}
	}()
	svc.write(response{Type: "ready", OK: true, Result: map[string]any{"workspace": workspace, "model": model, "models": models, "gateway_enabled": aiGateway != nil}})

	scanner := bufio.NewScanner(os.Stdin)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)
	for scanner.Scan() {
		var req request
		if err := json.Unmarshal(scanner.Bytes(), &req); err != nil {
			svc.write(response{Type: "response", ID: req.ID, Error: "invalid request JSON"})
			continue
		}
		go svc.handle(req)
	}
	if err := scanner.Err(); err != nil {
		fatal(err)
	}
}

func (s *service) handle(req request) {
	switch req.Method {
	case "start_run":
		s.startRun(req)
	case "cancel_run":
		s.cancelActive(req.ID)
	case "permission_decision":
		s.resolvePermission(req)
	case "snapshot":
		events := s.store.Snapshot(runstate.Filter{RunID: req.RunID, Limit: 4096})
		if len(events) == 0 && req.RunID != "" {
			events, _ = s.recorder.Load(req.RunID, 4096)
		}
		s.write(response{Type: "response", ID: req.ID, OK: true, Result: events})
	case "list_runs":
		runs, err := s.recorder.List()
		if err != nil {
			s.write(response{Type: "response", ID: req.ID, Error: err.Error()})
			return
		}
		s.write(response{Type: "response", ID: req.ID, OK: true, Result: runs})
	case "gateway_status":
		var result any = []gateway.NodeStatus{}
		if s.gateway != nil {
			result = s.gateway.Status()
		}
		s.write(response{Type: "response", ID: req.ID, OK: true, Result: result})
	case "gateway_drain", "gateway_resume":
		if s.gateway == nil {
			s.write(response{Type: "response", ID: req.ID, Error: "AI gateway is disabled"})
			return
		}
		err := s.gateway.SetDrain(req.NodeID, req.Method == "gateway_drain")
		if err != nil {
			s.write(response{Type: "response", ID: req.ID, Error: err.Error()})
			return
		}
		s.write(response{Type: "response", ID: req.ID, OK: true})
	default:
		s.write(response{Type: "response", ID: req.ID, Error: "unknown method"})
	}
}

func (s *service) startRun(req request) {
	prompt := strings.TrimSpace(req.Prompt)
	if prompt == "" {
		s.write(response{Type: "response", ID: req.ID, Error: "prompt is required"})
		return
	}
	s.mu.Lock()
	if s.cancelRun != nil {
		s.mu.Unlock()
		s.write(response{Type: "response", ID: req.ID, Error: "another run is active"})
		return
	}
	s.nextID++
	runID := req.RunID
	if runID == "" {
		runID = fmt.Sprintf("run-%d-%d", time.Now().UnixMilli(), s.nextID)
	}
	ctx, cancel := context.WithCancel(context.Background())
	s.currentRun = runID
	s.cancelRun = cancel
	s.mu.Unlock()
	s.write(response{Type: "response", ID: req.ID, OK: true, Result: map[string]any{"run_id": runID}})

	callbacks := agent.Callbacks{
		OnRunEvent: func(event agent.AgentRunEvent) {
			s.publish(runstate.Event{RunID: runID, Kind: runstate.Kind(event.Kind), GraphID: event.GraphID, NodeID: event.NodeID, ParentID: event.ParentID, Phase: event.Phase, Role: event.Role, Model: event.Model, RouteNodeID: event.RouteNodeID, Status: event.Status, Message: event.Message, ToolName: event.ToolName, DurationMS: event.DurationMS, Metadata: event.Metadata})
		},
		OnContentToken: func(token string) {
			s.publish(runstate.Event{RunID: runID, Kind: runstate.KindAssistantContent, GraphID: "main-agent", NodeID: "main", Role: "main", Model: s.agent.GetModel(), Status: "streaming", Message: token})
		},
		ConfirmToolCallWithActionContext: func(ctx context.Context, toolName string, args map[string]interface{}) (bool, bool) {
			return s.requestPermission(ctx, runID, toolName, args)
		},
	}
	answer, err := s.agent.AskWithContext(ctx, prompt, callbacks)
	if err == nil && strings.TrimSpace(answer) != "" {
		s.publish(runstate.Event{RunID: runID, Kind: runstate.KindAssistantContent, GraphID: "main-agent", NodeID: "main", Role: "main", Model: s.agent.GetModel(), Status: "completed", Message: answer})
	}
	if errors.Is(err, context.Canceled) {
		s.publish(runstate.Event{RunID: runID, Kind: runstate.KindRunCancelled, GraphID: "main-agent", NodeID: "main", Status: "cancelled", Message: "Run cancelled"})
	}
	s.mu.Lock()
	if s.currentRun == runID {
		s.currentRun = ""
		s.cancelRun = nil
	}
	s.mu.Unlock()
}

func (s *service) requestPermission(ctx context.Context, runID, toolName string, args map[string]interface{}) (bool, bool) {
	s.mu.Lock()
	s.nextID++
	id := fmt.Sprintf("permission-%d", s.nextID)
	pending := pendingPermission{decision: make(chan permissionDecision, 1)}
	s.permissions[id] = pending
	s.mu.Unlock()
	s.write(response{Type: "permission", Result: permissionRequest{ID: id, RunID: runID, ToolName: toolName, Args: redactArgs(args)}})
	select {
	case decision := <-pending.decision:
		return decision.allowed, decision.always
	case <-ctx.Done():
		s.mu.Lock()
		delete(s.permissions, id)
		s.mu.Unlock()
		return false, false
	}
}

func (s *service) resolvePermission(req request) {
	s.mu.Lock()
	pending, exists := s.permissions[req.ID]
	if exists {
		delete(s.permissions, req.ID)
	}
	s.mu.Unlock()
	if !exists || req.Allowed == nil {
		s.write(response{Type: "response", ID: req.ID, Error: "unknown permission request"})
		return
	}
	pending.decision <- permissionDecision{allowed: *req.Allowed, always: req.Always}
	s.write(response{Type: "response", ID: req.ID, OK: true})
}

func (s *service) cancelActive(id string) {
	s.mu.Lock()
	cancel := s.cancelRun
	s.mu.Unlock()
	if cancel == nil {
		s.write(response{Type: "response", ID: id, Error: "no active run"})
		return
	}
	cancel()
	s.write(response{Type: "response", ID: id, OK: true})
}

func (s *service) publish(event runstate.Event) {
	event = s.store.Publish(event)
	if event.RunID != "system" {
		if err := s.recorder.Append(event); err != nil {
			s.write(response{Type: "diagnostic", Error: "run event persistence failed: " + err.Error()})
		}
	}
}
func (s *service) activeRunID() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.currentRun == "" {
		return "system"
	}
	return s.currentRun
}
func (s *service) write(value response) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	_ = json.NewEncoder(os.Stdout).Encode(value)
}

func redactArgs(args map[string]interface{}) map[string]any {
	result := make(map[string]any)
	for key, value := range args {
		lower := strings.ToLower(key)
		if strings.Contains(lower, "token") || strings.Contains(lower, "secret") || strings.Contains(lower, "password") || strings.Contains(lower, "content") {
			continue
		}
		result[key] = value
	}
	return result
}

func resolveWorkspace(value string) (string, error) {
	if strings.TrimSpace(value) == "" {
		return os.Getwd()
	}
	return filepath.Abs(value)
}

func chooseModel(models []string) string {
	for _, preferred := range []string{"gemma4:12b", "qwen3.5:0.8b"} {
		for _, model := range models {
			if strings.HasPrefix(model, preferred) {
				return model
			}
		}
	}
	return models[0]
}

func fatal(err error) {
	_, _ = fmt.Fprintln(os.Stderr, err)
	os.Exit(1)
}
