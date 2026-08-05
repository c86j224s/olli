package tools

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// ViewFile reads file contents within workspace boundary
func ViewFile(filePath string, startLine int, endLine int, workspace string) (string, error) {
	safePath, err := IsPathSafe(filePath, workspace)
	if err != nil {
		return "", fmt.Errorf("security block: %w", err)
	}

	file, err := os.Open(safePath)
	if err != nil {
		return "", fmt.Errorf("failed to open file '%s': %w", safePath, err)
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	var lines []string
	lineNo := 0

	for scanner.Scan() {
		lineNo++
		if startLine > 0 && lineNo < startLine {
			continue
		}
		if endLine > 0 && lineNo > endLine {
			break
		}
		lines = append(lines, fmt.Sprintf("%4d: %s", lineNo, scanner.Text()))
		if len(lines) >= 800 { // Max 800 lines limit
			lines = append(lines, "... [Truncated at 800 lines limit]")
			break
		}
	}

	if err := scanner.Err(); err != nil {
		return "", fmt.Errorf("error reading file: %w", err)
	}

	return fmt.Sprintf("File: %s (Lines %d-%d):\n%s", safePath, startLine, endLine, strings.Join(lines, "\n")), nil
}

// EditFile replaces content in a file within workspace boundary
func EditFile(filePath string, targetContent string, replacementContent string, workspace string) (string, error) {
	safePath, err := IsPathSafe(filePath, workspace)
	if err != nil {
		return "", fmt.Errorf("security block: %w", err)
	}

	data, err := os.ReadFile(safePath)
	if err != nil {
		if os.IsNotExist(err) && targetContent == "" {
			if err := os.WriteFile(safePath, []byte(replacementContent), 0644); err != nil {
				return "", fmt.Errorf("failed to create file '%s': %w", safePath, err)
			}
			return fmt.Sprintf("File '%s' successfully created.", safePath), nil
		}
		return "", fmt.Errorf("failed to read file '%s': %w", safePath, err)
	}

	content := string(data)
	if targetContent != "" {
		if !strings.Contains(content, targetContent) {
			return "", fmt.Errorf("target content not found in file '%s'", safePath)
		}
		content = strings.Replace(content, targetContent, replacementContent, 1)
	} else {
		content = replacementContent
	}

	if err := os.WriteFile(safePath, []byte(content), 0644); err != nil {
		return "", fmt.Errorf("failed to write edited content to file '%s': %w", safePath, err)
	}

	return fmt.Sprintf("File '%s' successfully updated.", safePath), nil
}

// GrepSearch performs pattern search across files in workspace
func GrepSearch(query string, searchPath string, workspace string) (string, error) {
	if searchPath == "" {
		searchPath = workspace
	}

	safePath, err := IsPathSafe(searchPath, workspace)
	if err != nil {
		return "", fmt.Errorf("security block: %w", err)
	}

	queryLower := strings.ToLower(query)
	var matches []string

	err = filepath.Walk(safePath, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if info.IsDir() {
			name := info.Name()
			if name == ".git" || name == "node_modules" || name == "bin" || name == "sessions" {
				return filepath.SkipDir
			}
			return nil
		}

		file, err := os.Open(path)
		if err != nil {
			return nil
		}
		defer file.Close()

		scanner := bufio.NewScanner(file)
		lineNo := 0
		for scanner.Scan() {
			lineNo++
			text := scanner.Text()
			if strings.Contains(strings.ToLower(text), queryLower) {
				relPath, _ := filepath.Rel(workspace, path)
				matches = append(matches, fmt.Sprintf("%s:%d: %s", relPath, lineNo, strings.TrimSpace(text)))
				if len(matches) >= 50 {
					return fmt.Errorf("limit reached")
				}
			}
		}
		return nil
	})

	if len(matches) == 0 {
		return fmt.Sprintf("No matches found for query '%s' in %s", query, searchPath), nil
	}

	return fmt.Sprintf("Grep Search Results for '%s' (%d matches):\n%s", query, len(matches), strings.Join(matches, "\n")), nil
}

// ListDir lists directory contents safely
func ListDir(dirPath string, workspace string) (string, error) {
	if dirPath == "" {
		dirPath = workspace
	}

	safePath, err := IsPathSafe(dirPath, workspace)
	if err != nil {
		return "", fmt.Errorf("security block: %w", err)
	}

	entries, err := os.ReadDir(safePath)
	if err != nil {
		return "", fmt.Errorf("failed to list directory '%s': %w", safePath, err)
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Contents of '%s':\n", safePath))
	for _, entry := range entries {
		kind := "FILE"
		if entry.IsDir() {
			kind = "DIR "
		}
		sb.WriteString(fmt.Sprintf("  [%s] %s\n", kind, entry.Name()))
	}

	return sb.String(), nil
}

// ParseCommandArgs extracts command string gracefully from various LLM argument schemas
func ParseCommandArgs(args map[string]interface{}) string {
	possibleKeys := []string{"command", "cmd", "command_string", "args", "arguments"}

	for _, k := range possibleKeys {
		val, exists := args[k]
		if !exists || val == nil {
			continue
		}

		switch v := val.(type) {
		case string:
			trimmed := strings.TrimSpace(v)
			if trimmed != "" {
				return cleanCommandString(trimmed)
			}
		case []interface{}:
			var parts []string
			for _, elem := range v {
				parts = append(parts, fmt.Sprintf("%v", elem))
			}
			joined := strings.TrimSpace(strings.Join(parts, " "))
			if joined != "" {
				return cleanCommandString(joined)
			}
		}
	}

	if cmd, ok := args["command"].(string); ok {
		return cleanCommandString(cmd)
	}

	return ""
}

func cleanCommandString(cmdStr string) string {
	cmdStr = strings.TrimSpace(cmdStr)
	if (strings.HasPrefix(cmdStr, `"`) && strings.HasSuffix(cmdStr, `"`)) ||
		(strings.HasPrefix(cmdStr, `'`) && strings.HasSuffix(cmdStr, `'`)) {
		cmdStr = strings.Trim(cmdStr, `"'`)
	}
	return strings.TrimSpace(cmdStr)
}

// ExecuteCommand runs terminal commands safely inside workspace
func ExecuteCommand(cmdStr string, workspace string) (string, error) {
	return ExecuteCommandWithContext(context.Background(), cmdStr, workspace)
}

// ExecuteCommandWithContext runs terminal command with cancellation context support to kill process on interrupt
func ExecuteCommandWithContext(ctx context.Context, cmdStr string, workspace string) (string, error) {
	output, _, err := ExecuteCommandWithWorkspace(ctx, cmdStr, workspace)
	return output, err
}

// ExecuteCommandWithWorkspace executes command and returns updated workspace if 'cd' was invoked
func ExecuteCommandWithWorkspace(ctx context.Context, cmdStr string, workspace string) (output string, newWorkspace string, err error) {
	newWorkspace = workspace
	if cmdStr == "" {
		return "", newWorkspace, fmt.Errorf("empty terminal command received")
	}

	if err := ValidateCommandSafety(cmdStr, workspace); err != nil {
		return "", newWorkspace, fmt.Errorf("security block: %w", err)
	}

	// If command contains 'cd', calculate resulting directory and update active workspace
	trimmed := strings.TrimSpace(cmdStr)
	if strings.HasPrefix(trimmed, "cd ") || strings.Contains(trimmed, " cd ") || strings.Contains(trimmed, ";cd ") || strings.Contains(trimmed, "&&cd ") {
		cdCheckCmd := fmt.Sprintf("%s && pwd", trimmed)
		checkCmd := exec.CommandContext(ctx, "bash", "-c", cdCheckCmd)
		checkCmd.Dir = workspace
		checkOut, checkErr := checkCmd.CombinedOutput()
		if checkErr == nil {
			lines := strings.Split(strings.TrimSpace(string(checkOut)), "\n")
			if len(lines) > 0 {
				lastLine := strings.TrimSpace(lines[len(lines)-1])
				if safeDir, sErr := IsPathSafe(lastLine, workspace); sErr == nil {
					newWorkspace = safeDir
				}
			}
		}
	}

	cmd := exec.CommandContext(ctx, "bash", "-c", cmdStr)
	cmd.Dir = workspace

	out, err := cmd.CombinedOutput()
	outputStr := string(out)
	if err != nil {
		if ctx.Err() == context.Canceled {
			return "", newWorkspace, context.Canceled
		}
		return fmt.Sprintf("Command exited with error: %v\nOutput:\n%s", err, outputStr), newWorkspace, nil
	}

	if len(outputStr) > 4000 {
		outputStr = outputStr[:4000] + "\n... [Output Truncated]"
	}

	if newWorkspace != workspace {
		outputStr = fmt.Sprintf("%s\n\n📌 [Workspace Directory Updated]: Now working in '%s'", strings.TrimSpace(outputStr), newWorkspace)
	}

	return outputStr, newWorkspace, nil
}
