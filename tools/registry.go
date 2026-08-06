package tools

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/c86j224s/olli/ollama"
)

type ToolHandler func(args map[string]interface{}) (string, error)

type Registry struct {
	definitions   []ollama.Tool
	handlers      map[string]ToolHandler
	workspace     string
	workspaceRoot string
	sessionFile   string
}

func NewRegistry() *Registry {
	r := NewEmptyRegistry()
	r.registerDefaultTools()
	return r
}

func NewEmptyRegistry() *Registry {
	wd, err := os.Getwd()
	if err != nil {
		wd = "."
	}
	return &Registry{
		definitions:   make([]ollama.Tool, 0),
		handlers:      make(map[string]ToolHandler),
		workspace:     wd,
		workspaceRoot: wd,
	}
}

func (r *Registry) SetWorkspace(ws string) {
	if ws != "" {
		r.workspace = ws
	}
}

func (r *Registry) GetWorkspace() string {
	return r.workspace
}

func (r *Registry) SetWorkspaceRoot(root string) {
	if root != "" {
		r.workspaceRoot = root
	}
}

func (r *Registry) GetWorkspaceRoot() string {
	if r.workspaceRoot == "" {
		return r.workspace
	}
	return r.workspaceRoot
}

func (r *Registry) SetSessionFile(sf string) {
	r.sessionFile = sf
}

func (r *Registry) GetSessionFile() string {
	return r.sessionFile
}

// ResolvePath resolves relative paths, tildes, or absolute paths against the active workspace
func (r *Registry) ResolvePath(targetPath string) string {
	trimmed := strings.TrimSpace(targetPath)
	if trimmed == "" {
		return r.workspace
	}
	trimmed = ExpandTilde(trimmed)
	if filepath.IsAbs(trimmed) {
		return trimmed
	}
	return filepath.Join(r.workspace, trimmed)
}

func (r *Registry) ResolvePathSafe(targetPath string) (string, error) {
	return IsPathSafeFrom(targetPath, r.workspace, r.GetWorkspaceRoot())
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
			"workspace":  r.workspace,
		}
		b, _ := json.Marshal(info)
		return string(b), nil
	})

	// Tool 4: run_terminal_command (using ExecuteCommandWithWorkspace)
	r.Register(ollama.Tool{
		Type: "function",
		Function: ollama.FunctionDef{
			Name:        "run_terminal_command",
			Description: "Run safe CLI terminal commands like 'ls', 'pwd', 'go test', 'cd <dir>'",
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
		cmdStr := ParseCommandArgs(args)
		if cmdStr == "" {
			return "", fmt.Errorf("invalid command argument")
		}

		output, newWs, err := ExecuteCommandWithWorkspace(context.Background(), cmdStr, r.workspace, r.GetWorkspaceRoot())
		if newWs != r.workspace {
			r.workspace = newWs
		}
		return output, err
	})

	// Tool 5: search_session_history (Scoped to active session file or sessions dir)
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

		targetFile := r.sessionFile
		if targetFile == "" {
			targetFile = filepath.Join(r.GetWorkspaceRoot(), "sessions")
		}

		matches, err := SearchSessionLogs(targetFile, query, r.GetWorkspaceRoot())
		if err != nil {
			return "", err
		}

		if len(matches) == 0 {
			return fmt.Sprintf("No past session logs found matching query '%s'", query), nil
		}

		return fmt.Sprintf("Found %d session log matches for '%s':\n%s", len(matches), query, strings.Join(matches, "\n")), nil
	})
}

func evalMathExpr(expr string) (string, error) {
	expr = strings.ReplaceAll(expr, " ", "")
	var op rune
	var opIdx = -1

	for i, r := range expr {
		if r == '+' || r == '-' || r == '*' || r == '/' {
			if i > 0 {
				op = r
				opIdx = i
				break
			}
		}
	}

	if opIdx == -1 {
		val, err := strconv.ParseFloat(expr, 64)
		if err != nil {
			return "", fmt.Errorf("invalid number")
		}
		return fmt.Sprintf("%.2f", val), nil
	}

	leftStr := expr[:opIdx]
	rightStr := expr[opIdx+1:]

	left, err1 := strconv.ParseFloat(leftStr, 64)
	right, err2 := strconv.ParseFloat(rightStr, 64)

	if err1 != nil || err2 != nil {
		return "", fmt.Errorf("invalid operand numbers")
	}

	var res float64
	switch op {
	case '+':
		res = left + right
	case '-':
		res = left - right
	case '*':
		res = left * right
	case '/':
		if right == 0 {
			return "", fmt.Errorf("division by zero")
		}
		res = left / right
	}

	return fmt.Sprintf("%.2f", res), nil
}

// SearchSessionLogs searches either a single .jsonl session file or entire sessions directory
func SearchSessionLogs(targetPath string, query string, allowedRootDir ...string) ([]string, error) {
	if targetPath == "" {
		return nil, fmt.Errorf("session log target path required")
	}

	root := workspaceRootFor(filepath.Dir(targetPath), allowedRootDir...)
	safeTarget, err := IsPathSafeFrom(targetPath, root, root)
	if err != nil {
		return nil, fmt.Errorf("security block: session log path rejected: %w", err)
	}

	lstat, err := os.Lstat(safeTarget)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to inspect session log target: %w", err)
	}
	if lstat.Mode()&os.ModeSymlink != 0 {
		return nil, fmt.Errorf("security block: session log target symlink is not allowed")
	}

	info, err := os.Stat(safeTarget)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to stat session log target: %w", err)
	}

	queryLower := strings.ToLower(query)
	var results []string

	searchFile := func(filePath string) {
		if _, err := IsPathSafeFrom(filePath, root, root); err != nil {
			return
		}
		lstat, err := os.Lstat(filePath)
		if err != nil || lstat.Mode()&os.ModeSymlink != 0 {
			return
		}
		file, err := os.Open(filePath)
		if err != nil {
			return
		}
		defer file.Close()

		scanner := bufio.NewScanner(file)
		lineNo := 0
		for scanner.Scan() {
			lineNo++
			text := scanner.Text()
			if strings.Contains(strings.ToLower(text), queryLower) {
				relPath, _ := filepath.Rel(".", filePath)
				truncText := text
				if len(truncText) > 200 {
					truncText = truncText[:200] + "... [truncated]"
				}
				results = append(results, fmt.Sprintf("[%s L%d] %s", relPath, lineNo, truncText))
				if len(results) >= 20 {
					return
				}
			}
		}
	}

	if !info.IsDir() {
		searchFile(safeTarget)
		return results, nil
	}

	_ = filepath.Walk(safeTarget, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if info.IsDir() || info.Mode()&os.ModeSymlink != 0 || !strings.HasSuffix(path, ".jsonl") {
			return nil
		}
		searchFile(path)
		if len(results) >= 20 {
			return fmt.Errorf("match limit reached")
		}
		return nil
	})

	return results, nil
}

// ListSubagentReports lists all past subagent research, code analysis, testing, or review logs saved in workspace
func ListSubagentReports(workspace string, allowedRootDir ...string) (string, error) {
	root := workspaceRootFor(workspace, allowedRootDir...)
	subDir, err := IsPathSafeFrom(filepath.Join("sessions", "subagents"), workspace, root)
	if err != nil {
		return "", fmt.Errorf("security block: subagent report directory rejected: %w", err)
	}
	if lstat, err := os.Lstat(subDir); os.IsNotExist(err) {
		return "No subagent investigation reports found in sessions/subagents.", nil
	} else if err != nil {
		return "", fmt.Errorf("failed to inspect subagent logs directory: %w", err)
	} else if lstat.Mode()&os.ModeSymlink != 0 {
		return "", fmt.Errorf("security block: subagent logs directory symlink is not allowed")
	}

	entries, err := os.ReadDir(subDir)
	if err != nil {
		return "", fmt.Errorf("failed to read subagent logs directory: %w", err)
	}

	var reports []string
	for _, entry := range entries {
		if entry.Type()&os.ModeSymlink != 0 {
			continue
		}
		if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".jsonl") {
			reports = append(reports, fmt.Sprintf("  • %s", entry.Name()))
		}
	}

	if len(reports) == 0 {
		return "No subagent investigation reports found.", nil
	}

	return fmt.Sprintf("Past Subagent Investigation Reports (%d files in sessions/subagents):\n%s", len(reports), strings.Join(reports, "\n")), nil
}

// ViewSubagentReport reads and summarizes a specific subagent report JSONL log
func ViewSubagentReport(workspace string, reportFilename string, allowedRootDir ...string) (string, error) {
	reportFilename = strings.TrimSpace(reportFilename)
	if reportFilename == "" {
		return "", fmt.Errorf("report filename required")
	}

	if !strings.HasSuffix(reportFilename, ".jsonl") {
		reportFilename += ".jsonl"
	}

	root := workspaceRootFor(workspace, allowedRootDir...)
	subDir, err := IsPathSafeFrom(filepath.Join("sessions", "subagents"), workspace, root)
	if err != nil {
		return "", fmt.Errorf("security block: subagent report directory rejected: %w", err)
	}
	targetPath, err := IsPathSafeFrom(filepath.Base(reportFilename), subDir, root)
	if err != nil {
		return "", fmt.Errorf("security block: subagent report path rejected: %w", err)
	}
	if lstat, err := os.Lstat(targetPath); err != nil {
		return "", fmt.Errorf("failed to inspect subagent report '%s': %w", targetPath, err)
	} else if lstat.Mode()&os.ModeSymlink != 0 {
		return "", fmt.Errorf("security block: subagent report symlink is not allowed")
	}

	data, err := os.ReadFile(targetPath)
	if err != nil {
		return "", fmt.Errorf("failed to read subagent report '%s': %w", targetPath, err)
	}

	lines := strings.Split(string(data), "\n")
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Subagent Report Contents (%s):\n", filepath.Base(targetPath)))

	count := 0
	for _, l := range lines {
		l = strings.TrimSpace(l)
		if l == "" {
			continue
		}
		count++
		if len(l) > 300 {
			l = l[:300] + "... [truncated]"
		}
		sb.WriteString(fmt.Sprintf("  [%d] %s\n", count, l))
		if count >= 30 {
			sb.WriteString("  ... [Truncated at 30 events limit]\n")
			break
		}
	}

	return sb.String(), nil
}
