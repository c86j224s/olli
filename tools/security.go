package tools

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// ExpandTilde resolves paths starting with "~", "~/", or typos like "~llm-pg" to user home directory
func ExpandTilde(path string) string {
	trimmed := strings.TrimSpace(path)
	if trimmed == "" {
		return trimmed
	}

	homeDir, err := os.UserHomeDir()
	if err != nil || homeDir == "" {
		return trimmed
	}

	if trimmed == "~" {
		return homeDir
	}

	if strings.HasPrefix(trimmed, "~/") {
		return filepath.Join(homeDir, trimmed[2:])
	}

	// Handle missing slash typo like "~llm-pg" -> "~/llm-pg"
	if strings.HasPrefix(trimmed, "~") && !strings.HasPrefix(trimmed, "~/") {
		folderName := trimmed[1:]
		targetPath := filepath.Join(homeDir, folderName)
		// Check if folder exists in $HOME
		if _, statErr := os.Stat(targetPath); statErr == nil {
			return targetPath
		}
		// Fallback join
		return targetPath
	}

	return trimmed
}

// ForbiddenSystemRootDirectories defines critical OS system directories
// that must never be set as workspaces or written to.
func GetForbiddenSystemRootDirectories() []string {
	return []string{
		"/",
		"/Users",
		"/home",
		"/System",
		"/Library",
		"/usr",
		"/bin",
		"/sbin",
		"/etc",
		"/var",
		"/dev",
	}
}

// IsWorkspaceLocationSafe checks if a directory is safe to be used as a workspace.
// OS System Roots (/, /System, /usr) are forbidden. User home ($HOME) and subdirectories are ALLOWED.
func IsWorkspaceLocationSafe(dir string) error {
	dir = ExpandTilde(dir)
	abs, err := filepath.Abs(dir)
	if err != nil {
		return fmt.Errorf("invalid path: %w", err)
	}
	if eval, err := filepath.EvalSymlinks(abs); err == nil {
		abs = eval
	}

	for _, sysRoot := range GetForbiddenSystemRootDirectories() {
		if abs == sysRoot {
			return fmt.Errorf("SECURITY RISK: Directory '%s' is a protected System Root directory (/ or /System). Access is FORBIDDEN.", abs)
		}
	}
	return nil
}

// IsPathSafe verifies that a target path is safe (not OS system root) and accessible
func IsPathSafe(targetPath string, allowedRootDir string) (string, error) {
	if strings.TrimSpace(targetPath) == "" {
		return "", fmt.Errorf("path cannot be empty")
	}

	targetPath = ExpandTilde(targetPath)
	allowedRootDir = ExpandTilde(allowedRootDir)

	// 1. Resolve target absolute path
	absTarget, err := filepath.Abs(targetPath)
	if err != nil {
		return "", fmt.Errorf("invalid target path: %w", err)
	}

	if evalPath, err := filepath.EvalSymlinks(absTarget); err == nil {
		absTarget = evalPath
	}

	// 2. Protect OS System Root directories (/, /System, /usr, /etc)
	for _, sysRoot := range GetForbiddenSystemRootDirectories() {
		if absTarget == sysRoot {
			return "", fmt.Errorf("SECURITY BLOCK: Target path '%s' is a protected OS system root directory", absTarget)
		}
	}

	// 3. Verify workspace location safety
	if err := IsWorkspaceLocationSafe(absTarget); err != nil {
		return "", fmt.Errorf("SECURITY BLOCK: Target path '%s' is not a safe directory: %w", absTarget, err)
	}

	return absTarget, nil
}

// ValidateCommandSafety inspects CLI command strings for dangerous deletion or system-wiping patterns
func ValidateCommandSafety(cmdStr string, allowedRootDir string) error {
	trimmed := strings.TrimSpace(cmdStr)
	if trimmed == "" {
		return fmt.Errorf("empty command")
	}

	// Dangerous deletion command patterns
	dangerousRegex := regexp.MustCompile(`(?i)\b(rm|rmdir|shred|unlink|dd|mkfs)\b`)
	if dangerousRegex.MatchString(trimmed) {
		// Check for self-deletion commands like "rm -rf .", "rm -rf ./", "rmdir ."
		if strings.Contains(trimmed, " .") || strings.Contains(trimmed, " ./") || strings.Contains(trimmed, " *") {
			homeDir, _ := os.UserHomeDir()
			if allowedRootDir == homeDir || allowedRootDir == "/" {
				return fmt.Errorf("SECURITY BLOCK: Self-deletion command '%s' targeting home or system root is FORBIDDEN", cmdStr)
			}
		}

		// Check if command targets root, home root, or wildcard
		homeDir, _ := os.UserHomeDir()
		dangerousTargets := []string{"/", "/ *", "~", "$HOME", "/Users", "/Users/*", homeDir}

		for _, dt := range dangerousTargets {
			if dt != "" && strings.Contains(trimmed, dt) {
				return fmt.Errorf("SECURITY BLOCK: Dangerous deletion command targeting home/root detected: '%s'", cmdStr)
			}
		}

		// Verify target paths for deletion
		parts := strings.Fields(trimmed)
		for _, p := range parts {
			if strings.HasPrefix(p, "-") {
				continue
			}
			if p == "rm" || p == "rmdir" || p == "shred" || p == "unlink" {
				continue
			}
			_, err := IsPathSafe(p, allowedRootDir)
			if err != nil {
				return fmt.Errorf("SECURITY BLOCK: Deletion command target error: %w", err)
			}
		}
	}

	return nil
}
