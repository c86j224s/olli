package tools

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// ViewFile reads file contents within workspace boundary with line-range slicing
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

// EditFile replaces a specific target section or creates/overwrites file content within workspace boundary
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
			return "", fmt.Errorf("target content chunk not found in file '%s'", safePath)
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

// AppendFile appends new content to the end of a file without overwriting existing content
func AppendFile(filePath string, appendContent string, workspace string) (string, error) {
	safePath, err := IsPathSafe(filePath, workspace)
	if err != nil {
		return "", fmt.Errorf("security block: %w", err)
	}

	file, err := os.OpenFile(safePath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return "", fmt.Errorf("failed to open file '%s' for append: %w", safePath, err)
	}
	defer file.Close()

	if _, err := file.WriteString(appendContent); err != nil {
		return "", fmt.Errorf("failed to append content to file '%s': %w", safePath, err)
	}

	return fmt.Sprintf("Successfully appended %d bytes to file '%s'.", len(appendContent), safePath), nil
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

	_ = filepath.Walk(safePath, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
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
				matches = append(matches, fmt.Sprintf("%s:%d: %s", relPath, lineNo, text))
				if len(matches) >= 100 {
					return fmt.Errorf("max matches reached")
				}
			}
		}
		return nil
	})

	if len(matches) == 0 {
		return fmt.Sprintf("No matches found for query '%s' in path '%s'", query, searchPath), nil
	}

	return fmt.Sprintf("Grep Search Results for '%s' (%d matches):\n%s", query, len(matches), strings.Join(matches, "\n")), nil
}

// ListDir lists directory contents within workspace boundary
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
		return "", fmt.Errorf("failed to read directory '%s': %w", safePath, err)
	}

	var results []string
	for _, entry := range entries {
		info, err := entry.Info()
		typeStr := "[FILE]"
		if entry.IsDir() {
			typeStr = "[DIR ]"
		}
		sizeStr := ""
		if err == nil && !entry.IsDir() {
			sizeStr = fmt.Sprintf(" (%d bytes)", info.Size())
		}
		results = append(results, fmt.Sprintf("  • %s %s%s", typeStr, entry.Name(), sizeStr))
	}

	return fmt.Sprintf("Directory Contents of '%s' (%d items):\n%s", safePath, len(results), strings.Join(results, "\n")), nil
}

// ExecuteCommandWithWorkspace executes terminal commands safely within the target workspace directory
func ExecuteCommandWithWorkspace(ctx context.Context, cmdStr string, workspace string) (string, string, error) {
	cmdStr = strings.TrimSpace(cmdStr)
	if cmdStr == "" {
		return "", workspace, fmt.Errorf("command string cannot be empty")
	}

	// Handle 'cd <dir>' command to mutate current working directory
	if strings.HasPrefix(cmdStr, "cd ") || cmdStr == "cd" {
		targetDir := strings.TrimPrefix(cmdStr, "cd")
		targetDir = strings.TrimSpace(targetDir)
		if targetDir == "" {
			home, _ := os.UserHomeDir()
			targetDir = home
		}

		targetDir = ExpandTilde(targetDir)
		if !filepath.IsAbs(targetDir) {
			targetDir = filepath.Join(workspace, targetDir)
		}

		if err := IsWorkspaceLocationSafe(targetDir); err != nil {
			return "", workspace, fmt.Errorf("security block: directory '%s' is forbidden: %w", targetDir, err)
		}

		info, err := os.Stat(targetDir)
		if err != nil || !info.IsDir() {
			return "", workspace, fmt.Errorf("directory '%s' does not exist or is not a directory", targetDir)
		}

		return fmt.Sprintf("Working directory successfully changed to '%s'", targetDir), targetDir, nil
	}

	// Security check on workspace
	if err := IsWorkspaceLocationSafe(workspace); err != nil {
		return "", workspace, fmt.Errorf("security block: workspace directory '%s' is forbidden: %w", workspace, err)
	}

	// 35-second execution timeout guard to prevent terminal hangs
	execCtx, cancel := context.WithTimeout(ctx, 35*time.Second)
	defer cancel()

	cmd := exec.CommandContext(execCtx, "sh", "-c", cmdStr)
	cmd.Dir = workspace

	outBytes, err := cmd.CombinedOutput()
	output := string(outBytes)

	if execCtx.Err() == context.DeadlineExceeded {
		return output + "\n⚠️ [Timeout Error] Terminal command timed out after 35 seconds.", workspace, fmt.Errorf("command execution timed out after 35s")
	}

	if err != nil {
		return output, workspace, fmt.Errorf("command exited with error: %w", err)
	}

	if output == "" {
		output = "[Command executed successfully with no output]"
	}

	return output, workspace, nil
}

func ParseCommandArgs(args map[string]interface{}) string {
	if cmd, ok := args["command"].(string); ok {
		return cmd
	}
	if cmd, ok := args["cmd"].(string); ok {
		return cmd
	}
	return ""
}
