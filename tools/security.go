package tools

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// ForbiddenBaseDirectories defines critical system & user directories
// that must never be set as writable target workspaces or wiped out.
func GetForbiddenBaseDirectories() []string {
	forbidden := []string{
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

	homeDir, err := os.UserHomeDir()
	if err == nil && homeDir != "" {
		homeAbs, err := filepath.Abs(homeDir)
		if err == nil {
			if evalHome, err := filepath.EvalSymlinks(homeAbs); err == nil {
				homeAbs = evalHome
			}
			forbidden = append(forbidden, homeAbs)
		}
	}
	return forbidden
}

// IsWorkspaceLocationSafe checks if a directory is safe to be used as a writable workspace.
// If the user launches the agent directly in $HOME or System Root /, write/delete operations are blocked.
func IsWorkspaceLocationSafe(dir string) error {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return fmt.Errorf("invalid path: %w", err)
	}
	if eval, err := filepath.EvalSymlinks(abs); err == nil {
		abs = eval
	}

	for _, fb := range GetForbiddenBaseDirectories() {
		if abs == fb {
			return fmt.Errorf("SECURITY RISK: Directory '%s' is a Protected System/Home Base Directory ($HOME or /). Destructive/Write operations directly targeting this location are FORBIDDEN.", abs)
		}
	}
	return nil
}

// IsPathSafe verifies that a target path is safe (not home/root/system) and accessible
func IsPathSafe(targetPath string, allowedRootDir string) (string, error) {
	if strings.TrimSpace(targetPath) == "" {
		return "", fmt.Errorf("path cannot be empty")
	}

	// 1. Resolve target absolute path
	absTarget, err := filepath.Abs(targetPath)
	if err != nil {
		return "", fmt.Errorf("invalid target path: %w", err)
	}

	if evalPath, err := filepath.EvalSymlinks(absTarget); err == nil {
		absTarget = evalPath
	}

	// 2. Protect Root (/) and System Root directories ($HOME, /System, etc.)
	for _, fb := range GetForbiddenBaseDirectories() {
		if absTarget == fb {
			return "", fmt.Errorf("SECURITY BLOCK: Target path '%s' is a protected system/home root directory", absTarget)
		}
	}

	// 3. Check if target is inside current allowedRootDir
	absAllowed, err := filepath.Abs(allowedRootDir)
	if err == nil {
		if evalAllowed, err := filepath.EvalSymlinks(absAllowed); err == nil {
			absAllowed = evalAllowed
		}

		rel, relErr := filepath.Rel(absAllowed, absTarget)
		if relErr == nil && !strings.HasPrefix(rel, "..") {
			return absTarget, nil
		}
	}

	// 4. If target is outside allowedRootDir, verify it is a safe project directory (not / or $HOME)
	if err := IsWorkspaceLocationSafe(absTarget); err != nil {
		return "", fmt.Errorf("SECURITY BLOCK: Target path '%s' is outside current workspace and is not a safe project directory: %w", absTarget, err)
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
			if err := IsWorkspaceLocationSafe(allowedRootDir); err != nil {
				return fmt.Errorf("SECURITY BLOCK: Self-deletion command '%s' in protected workspace is FORBIDDEN: %w", cmdStr, err)
			}
		}

		// Check if command targets root, home, or wildcard
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
