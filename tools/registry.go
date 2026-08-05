package tools

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/c86j224s/olli/ollama"
)

type ToolHandler func(args map[string]interface{}) (string, error)

type Registry struct {
	definitions []ollama.Tool
	handlers    map[string]ToolHandler
	workspace   string
}

func NewRegistry() *Registry {
	wd, err := os.Getwd()
	if err != nil {
		wd = "."
	}
	r := &Registry{
		definitions: make([]ollama.Tool, 0),
		handlers:    make(map[string]ToolHandler),
		workspace:   wd,
	}
	r.registerDefaultTools()
	return r
}

func (r *Registry) Register(tool ollama.Tool, handler ToolHandler) {
	r.definitions = append(r.definitions, tool)
	r.handlers[tool.Function.Name] = handler
}

func (r *Registry) GetDefinitions() []ollama.Tool {
	return r.definitions
}

func (r *Registry) Execute(name string, args map[string]interface{}) (string, error) {
	handler, ok := r.handlers[name]
	if !ok {
		return "", fmt.Errorf("tool '%s' not registered", name)
	}
	return handler(args)
}

func (r *Registry) registerDefaultTools() {
	// Tool 1: get_current_time
	r.Register(ollama.Tool{
		Type: "function",
		Function: ollama.FunctionDef{
			Name:        "get_current_time",
			Description: "Get the current system date and time",
			Parameters: ollama.FunctionParamSchema{
				Type:       "object",
				Properties: map[string]ollama.FunctionParamProperty{},
			},
		},
	}, func(args map[string]interface{}) (string, error) {
		now := time.Now().Format("2006-01-02 15:04:05 MST (Monday)")
		return fmt.Sprintf("Current time: %s", now), nil
	})

	// Tool 2: calculator
	r.Register(ollama.Tool{
		Type: "function",
		Function: ollama.FunctionDef{
			Name:        "calculator",
			Description: "Perform simple mathematical calculations",
			Parameters: ollama.FunctionParamSchema{
				Type: "object",
				Properties: map[string]ollama.FunctionParamProperty{
					"expression": {
						Type:        "string",
						Description: "Math expression to evaluate, e.g. '25 * 4', '100 / 5', '12 + 34'",
					},
				},
				Required: []string{"expression"},
			},
		},
	}, func(args map[string]interface{}) (string, error) {
		expr, ok := args["expression"].(string)
		if !ok || expr == "" {
			return "", fmt.Errorf("invalid expression argument")
		}
		result, err := evalMathExpr(expr)
		if err != nil {
			return "", fmt.Errorf("failed to evaluate '%s': %w", expr, err)
		}
		return fmt.Sprintf("Result of '%s' = %s", expr, result), nil
	})

	// Tool 3: get_system_info
	r.Register(ollama.Tool{
		Type: "function",
		Function: ollama.FunctionDef{
			Name:        "get_system_info",
			Description: "Get system OS, architecture, Go runtime, and hardware information",
			Parameters: ollama.FunctionParamSchema{
				Type:       "object",
				Properties: map[string]ollama.FunctionParamProperty{},
			},
		},
	}, func(args map[string]interface{}) (string, error) {
		info := map[string]string{
			"os":         runtime.GOOS,
			"arch":       runtime.GOARCH,
			"num_cpu":    fmt.Sprintf("%d", runtime.NumCPU()),
			"go_version": runtime.Version(),
		}
		b, _ := json.Marshal(info)
		return string(b), nil
	})

	// Tool 4: run_terminal_command (with Sandbox Boundary Guard)
	r.Register(ollama.Tool{
		Type: "function",
		Function: ollama.FunctionDef{
			Name:        "run_terminal_command",
			Description: "Run safe CLI terminal commands like 'ls', 'pwd', 'whoami', 'date', 'go version'",
			Parameters: ollama.FunctionParamSchema{
				Type: "object",
				Properties: map[string]ollama.FunctionParamProperty{
					"command": {
						Type:        "string",
						Description: "Terminal command string to run",
					},
				},
				Required: []string{"command"},
			},
		},
	}, func(args map[string]interface{}) (string, error) {
		cmdStr, ok := args["command"].(string)
		if !ok || cmdStr == "" {
			return "", fmt.Errorf("invalid command argument")
		}

		// Security Check 1: Inspect for dangerous home/root deletion patterns
		if err := ValidateCommandSafety(cmdStr, r.workspace); err != nil {
			return "", fmt.Errorf("command execution blocked: %w", err)
		}

		parts := strings.Fields(cmdStr)
		if len(parts) == 0 {
			return "", fmt.Errorf("empty command")
		}

		allowedCmds := map[string]bool{
			"ls": true, "pwd": true, "whoami": true, "date": true,
			"go": true, "echo": true, "uptime": true, "uname": true,
		}

		if !allowedCmds[parts[0]] {
			return "", fmt.Errorf("command '%s' is not in the whitelist for safety reasons", parts[0])
		}

		cmd := exec.Command(parts[0], parts[1:]...)
		output, err := cmd.CombinedOutput()
		if err != nil {
			return fmt.Sprintf("Command error: %v, output: %s", err, string(output)), nil
		}
		return strings.TrimSpace(string(output)), nil
	})

	// Tool 5: search_session_history
	r.Register(ollama.Tool{
		Type: "function",
		Function: ollama.FunctionDef{
			Name:        "search_session_history",
			Description: "Search past conversation session logs by keyword to retrieve specific past messages, user preferences, or prior details",
			Parameters: ollama.FunctionParamSchema{
				Type: "object",
				Properties: map[string]ollama.FunctionParamProperty{
					"query": {
						Type:        "string",
						Description: "Keyword or topic to search in session logs",
					},
				},
				Required: []string{"query"},
			},
		},
	}, func(args map[string]interface{}) (string, error) {
		query, ok := args["query"].(string)
		if !ok || query == "" {
			return "", fmt.Errorf("query argument required")
		}

		matches, err := searchSessionLogs("./sessions", query)
		if err != nil {
			return "", err
		}
		if len(matches) == 0 {
			return fmt.Sprintf("No past conversation logs found matching '%s'", query), nil
		}

		return fmt.Sprintf("Found %d matching past messages:\n%s", len(matches), strings.Join(matches, "\n---\n")), nil
	})
}

func searchSessionLogs(sessionsDir string, query string) ([]string, error) {
	files, err := os.ReadDir(sessionsDir)
	if err != nil {
		return nil, fmt.Errorf("failed to read sessions dir: %w", err)
	}

	queryLower := strings.ToLower(query)
	var matches []string

	for _, f := range files {
		if f.IsDir() || !strings.HasSuffix(f.Name(), ".jsonl") {
			continue
		}

		filePath := filepath.Join(sessionsDir, f.Name())
		file, err := os.Open(filePath)
		if err != nil {
			continue
		}

		scanner := bufio.NewScanner(file)
		lineNo := 0
		for scanner.Scan() {
			lineNo++
			text := scanner.Text()
			if strings.Contains(strings.ToLower(text), queryLower) {
				var raw map[string]interface{}
				if err := json.Unmarshal([]byte(text), &raw); err == nil {
					role, _ := raw["role"].(string)
					content, _ := raw["content"].(string)
					if content != "" {
						matches = append(matches, fmt.Sprintf("[%s L%d - %s]: %s", f.Name(), lineNo, role, content))
					}
				} else {
					matches = append(matches, fmt.Sprintf("[%s L%d]: %s", f.Name(), lineNo, text))
				}
				if len(matches) >= 10 {
					break
				}
			}
		}
		file.Close()
	}

	return matches, nil
}

func evalMathExpr(expr string) (string, error) {
	cmd := exec.Command("python3", "-c", fmt.Sprintf("print(%s)", expr))
	out, err := cmd.Output()
	if err == nil {
		return strings.TrimSpace(string(out)), nil
	}
	return "", fmt.Errorf("calculation failed: %v", err)
}
