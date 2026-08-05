package tools

import (
	"bufio"
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
		// If file doesn't exist, create it if replacement content is provided
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

// ExecuteCommand runs terminal commands safely inside workspace
func ExecuteCommand(cmdStr string, workspace string) (string, error) {
	if err := ValidateCommandSafety(cmdStr, workspace); err != nil {
		return "", fmt.Errorf("security block: %w", err)
	}

	cmd := exec.Command("bash", "-c", cmdStr)
	cmd.Dir = workspace

	out, err := cmd.CombinedOutput()
	outputStr := string(out)
	if err != nil {
		return fmt.Sprintf("Command exited with error: %v\nOutput:\n%s", err, outputStr), nil
	}

	if len(outputStr) > 4000 {
		outputStr = outputStr[:4000] + "\n... [Output Truncated]"
	}

	return outputStr, nil
}
