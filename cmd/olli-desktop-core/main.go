package main

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/c86j224s/olli/controller"
	"github.com/c86j224s/olli/runstate"
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

type stdioServer struct {
	service *controller.Service
	writeMu sync.Mutex
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
	server := &stdioServer{}
	service, err := controller.New(workspace, configPath, controller.Hooks{
		OnPermission: func(permission controller.PermissionRequest) {
			server.write(response{Type: "permission", Result: permission})
		},
		OnDiagnostic: func(message string) { server.write(response{Type: "diagnostic", Error: message}) },
	})
	if err != nil {
		fatal(err)
	}
	defer service.Close()
	server.service = service
	events, cancelEvents := service.Events().Subscribe(nil, runstate.Filter{})
	defer cancelEvents()
	go func() {
		for event := range events {
			server.write(response{Type: "event", Event: event})
		}
	}()
	server.write(response{Type: "ready", OK: true, Result: service.ReadyInfo()})

	scanner := bufio.NewScanner(os.Stdin)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)
	for scanner.Scan() {
		var req request
		if err := json.Unmarshal(scanner.Bytes(), &req); err != nil {
			server.write(response{Type: "response", ID: req.ID, Error: "invalid request JSON"})
			continue
		}
		go server.handle(req)
	}
	if err := scanner.Err(); err != nil {
		fatal(err)
	}
}

func (s *stdioServer) handle(req request) {
	result := response{Type: "response", ID: req.ID, OK: true}
	var err error
	switch req.Method {
	case "start_run":
		result.Result, err = s.service.StartRun(req.Prompt, req.RunID)
	case "cancel_run":
		err = s.service.CancelRun()
	case "permission_decision":
		if req.Allowed == nil {
			err = fmt.Errorf("allowed is required")
		} else {
			err = s.service.ResolvePermission(req.ID, *req.Allowed, req.Always)
		}
	case "snapshot":
		result.Result, err = s.service.Snapshot(req.RunID, 0, 4096)
	case "list_runs":
		result.Result, err = s.service.ListRuns()
	case "gateway_status":
		result.Result = s.service.GatewayStatus()
	case "gateway_drain", "gateway_resume":
		err = s.service.SetDrain(req.NodeID, req.Method == "gateway_drain")
	default:
		err = fmt.Errorf("unknown method")
	}
	if err != nil {
		result.OK = false
		result.Error = err.Error()
	}
	s.write(result)
}

func (s *stdioServer) write(value response) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	_ = json.NewEncoder(os.Stdout).Encode(value)
}
func resolveWorkspace(value string) (string, error) {
	if strings.TrimSpace(value) == "" {
		return os.Getwd()
	}
	return filepath.Abs(value)
}
func fatal(err error) { _, _ = fmt.Fprintln(os.Stderr, err); os.Exit(1) }
