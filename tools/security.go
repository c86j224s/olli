package tools

import (
	"fmt"
	"os"
	accountuser "os/user"
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

	homeDir := preferredHomeDir()
	if homeDir == "" {
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
		if _, statErr := os.Stat(targetPath); statErr == nil {
			return targetPath
		}
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
// OS system roots and the real home directory itself are forbidden as workspace roots.
func IsWorkspaceLocationSafe(dir string) error {
	if strings.TrimSpace(dir) == "" {
		return fmt.Errorf("workspace path cannot be empty")
	}

	dir = ExpandTilde(dir)
	abs, err := filepath.Abs(dir)
	if err != nil {
		return fmt.Errorf("invalid path: %w", err)
	}
	if eval, err := filepath.EvalSymlinks(abs); err == nil {
		abs = eval
	}

	for _, sysRoot := range GetForbiddenSystemRootDirectories() {
		if pathSameFileOrString(abs, sysRoot) {
			return fmt.Errorf("SECURITY RISK: Directory '%s' is a protected System Root directory (/ or /System). Access is FORBIDDEN.", abs)
		}
	}
	for _, homeDir := range knownHomeDirs() {
		if pathSameFileOrString(abs, homeDir) {
			return fmt.Errorf("SECURITY RISK: Directory '%s' is a user home directory. Use a project subdirectory instead.", abs)
		}
	}
	return nil
}

func pathSameFileOrString(path string, protected string) bool {
	canonicalPath, pathErr := canonicalExistingPath(path)
	canonicalProtected, protectedErr := canonicalExistingPath(protected)
	if pathErr == nil && protectedErr == nil {
		if pathInfo, err := os.Stat(canonicalPath); err == nil {
			if protectedInfo, err := os.Stat(canonicalProtected); err == nil {
				return os.SameFile(pathInfo, protectedInfo)
			}
		}
	}
	if pathErr == nil {
		path = canonicalPath
	}
	if protectedErr == nil {
		protected = canonicalProtected
	}
	return filepath.Clean(path) == filepath.Clean(protected)
}

func preferredHomeDir() string {
	for _, homeDir := range knownHomeDirs() {
		if homeDir != "" {
			return homeDir
		}
	}
	return ""
}

func knownHomeDirs() []string {
	var homes []string
	if current, err := accountuser.Current(); err == nil && strings.TrimSpace(current.HomeDir) != "" {
		homes = append(homes, current.HomeDir)
	}
	if envHome, err := os.UserHomeDir(); err == nil && strings.TrimSpace(envHome) != "" {
		duplicate := false
		for _, home := range homes {
			if home == envHome {
				duplicate = true
				break
			}
		}
		if !duplicate {
			homes = append(homes, envHome)
		}
	}
	return homes
}

func canonicalExistingPath(path string) (string, error) {
	abs, err := filepath.Abs(ExpandTilde(path))
	if err != nil {
		return "", err
	}
	if eval, err := filepath.EvalSymlinks(abs); err == nil {
		abs = eval
	}
	return filepath.Clean(abs), nil
}

// IsPathSafe verifies that a target path is safe and resolves relative paths against allowedRootDir.
func IsPathSafe(targetPath string, allowedRootDir string) (string, error) {
	return IsPathSafeFrom(targetPath, allowedRootDir, allowedRootDir)
}

// IsPathSafeFrom resolves targetPath against baseDir while enforcing containment in allowedRootDir.
func IsPathSafeFrom(targetPath string, baseDir string, allowedRootDir string) (string, error) {
	targetPath = strings.TrimSpace(targetPath)
	if targetPath == "" {
		return "", fmt.Errorf("path cannot be empty")
	}

	root, err := canonicalWorkspaceRoot(allowedRootDir)
	if err != nil {
		return "", err
	}

	base, err := canonicalBaseWithinRoot(baseDir, root)
	if err != nil {
		return "", err
	}

	targetPath = ExpandTilde(targetPath)
	if !filepath.IsAbs(targetPath) {
		targetPath = filepath.Join(base, targetPath)
	}

	absTarget, err := filepath.Abs(targetPath)
	if err != nil {
		return "", fmt.Errorf("invalid target path: %w", err)
	}

	canonicalTarget, err := canonicalizeNearestExisting(absTarget)
	if err != nil {
		return "", err
	}
	canonicalTarget = filepath.Clean(canonicalTarget)

	if isForbiddenSystemRoot(canonicalTarget) {
		return "", fmt.Errorf("SECURITY BLOCK: Target path '%s' is a protected OS system root directory", canonicalTarget)
	}

	if err := ensureContained(canonicalTarget, root); err != nil {
		return "", err
	}

	return canonicalTarget, nil
}

func canonicalWorkspaceRoot(rootDir string) (string, error) {
	rootDir = strings.TrimSpace(ExpandTilde(rootDir))
	if rootDir == "" {
		return "", fmt.Errorf("allowed root directory cannot be empty")
	}

	absRoot, err := filepath.Abs(rootDir)
	if err != nil {
		return "", fmt.Errorf("invalid allowed root directory: %w", err)
	}

	canonicalRoot, err := filepath.EvalSymlinks(absRoot)
	if err != nil {
		return "", fmt.Errorf("allowed root directory must exist and be resolvable: %w", err)
	}

	info, err := os.Stat(canonicalRoot)
	if err != nil {
		return "", fmt.Errorf("allowed root directory cannot be read: %w", err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("allowed root '%s' is not a directory", canonicalRoot)
	}

	if err := IsWorkspaceLocationSafe(canonicalRoot); err != nil {
		return "", fmt.Errorf("SECURITY BLOCK: allowed root '%s' is not safe: %w", canonicalRoot, err)
	}

	return filepath.Clean(canonicalRoot), nil
}

func canonicalBaseWithinRoot(baseDir string, root string) (string, error) {
	baseDir = strings.TrimSpace(baseDir)
	if baseDir == "" {
		baseDir = root
	}
	baseDir = ExpandTilde(baseDir)
	if !filepath.IsAbs(baseDir) {
		baseDir = filepath.Join(root, baseDir)
	}

	absBase, err := filepath.Abs(baseDir)
	if err != nil {
		return "", fmt.Errorf("invalid base directory: %w", err)
	}

	canonicalBase, err := filepath.EvalSymlinks(absBase)
	if err != nil {
		return "", fmt.Errorf("base directory must exist and be resolvable: %w", err)
	}

	info, err := os.Stat(canonicalBase)
	if err != nil {
		return "", fmt.Errorf("base directory cannot be read: %w", err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("base path '%s' is not a directory", canonicalBase)
	}

	canonicalBase = filepath.Clean(canonicalBase)
	if err := ensureContained(canonicalBase, root); err != nil {
		return "", fmt.Errorf("base directory rejected: %w", err)
	}
	return canonicalBase, nil
}

func canonicalizeNearestExisting(targetPath string) (string, error) {
	targetPath = filepath.Clean(targetPath)
	if evalPath, err := filepath.EvalSymlinks(targetPath); err == nil {
		return evalPath, nil
	} else if !os.IsNotExist(err) {
		return "", fmt.Errorf("target path cannot be resolved safely: %w", err)
	}

	parent := targetPath
	var missingParts []string
	for {
		nextParent := filepath.Dir(parent)
		if nextParent == parent {
			return "", fmt.Errorf("target path has no resolvable parent: %s", targetPath)
		}
		missingParts = append([]string{filepath.Base(parent)}, missingParts...)
		parent = nextParent

		evalParent, err := filepath.EvalSymlinks(parent)
		if err == nil {
			info, statErr := os.Stat(evalParent)
			if statErr != nil {
				return "", fmt.Errorf("target parent cannot be read: %w", statErr)
			}
			if !info.IsDir() {
				return "", fmt.Errorf("target parent '%s' is not a directory", evalParent)
			}
			parts := append([]string{evalParent}, missingParts...)
			return filepath.Clean(filepath.Join(parts...)), nil
		}
		if !os.IsNotExist(err) {
			return "", fmt.Errorf("target parent cannot be resolved safely: %w", err)
		}
	}
}

func ensureContained(target string, root string) error {
	rel, err := filepath.Rel(root, target)
	if err != nil {
		return fmt.Errorf("SECURITY BLOCK: cannot compare path '%s' with allowed root '%s': %w", target, root, err)
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) || filepath.IsAbs(rel) {
		return fmt.Errorf("SECURITY BLOCK: path '%s' escapes allowed root '%s'", target, root)
	}
	return nil
}

func isForbiddenSystemRoot(path string) bool {
	path = filepath.Clean(path)
	for _, sysRoot := range GetForbiddenSystemRootDirectories() {
		if path == filepath.Clean(sysRoot) {
			return true
		}
	}
	return false
}

// ValidateCommandSafety inspects CLI command strings for dangerous deletion or system-wiping patterns
func ValidateCommandSafety(cmdStr string, allowedRootDir string) error {
	trimmed := strings.TrimSpace(cmdStr)
	if trimmed == "" {
		return fmt.Errorf("empty command")
	}

	if containsDestructiveShellCommand(trimmed) {
		return fmt.Errorf("SECURITY BLOCK: destructive shell command is forbidden: '%s'", cmdStr)
	}

	return nil
}

var destructiveCommandNames = map[string]struct{}{
	"dd":     {},
	"mkfs":   {},
	"rm":     {},
	"rmdir":  {},
	"shred":  {},
	"unlink": {},
}

var destructiveShellPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)(^|[\s;&|()])find\b[^;&|()]*\s-delete\b`),
	regexp.MustCompile(`(?i)(^|[\s;&|()])find\b[^;&|()]*\s-exec\s+(?:sudo\s+)?(?:rm|rmdir|shred|unlink|dd|mkfs)\b`),
	regexp.MustCompile(`(?i)(^|[\s;&|()])xargs\b[^;&|()]*\b(?:rm|rmdir|shred|unlink|dd|mkfs)\b`),
	regexp.MustCompile(`(?i)(^|[\s;&|()])(?:bash|sh|zsh)\b[^;&|()]*\s-c\s+['"][^'"]*\b(?:rm|rmdir|shred|unlink|dd|mkfs)\b`),
}

func containsDestructiveShellCommand(cmdStr string) bool {
	for _, pattern := range destructiveShellPatterns {
		if pattern.MatchString(cmdStr) {
			return true
		}
	}

	for _, segment := range strings.FieldsFunc(cmdStr, isShellCommandSeparator) {
		if segmentStartsWithDestructiveCommand(segment) {
			return true
		}
	}

	return false
}

func isShellCommandSeparator(r rune) bool {
	switch r {
	case '\n', ';', '|', '&', '(', ')':
		return true
	default:
		return false
	}
}

func segmentStartsWithDestructiveCommand(segment string) bool {
	fields := strings.Fields(segment)
	for _, field := range fields {
		token := trimShellCommandToken(field)
		if token == "" {
			continue
		}
		if token == "sudo" || token == "command" || token == "builtin" || token == "nohup" {
			continue
		}
		if token == "env" || isEnvAssignment(token) {
			continue
		}
		if strings.HasPrefix(token, "-") {
			continue
		}
		_, destructive := destructiveCommandNames[filepath.Base(token)]
		return destructive
	}

	return false
}

func trimShellCommandToken(token string) string {
	return strings.Trim(token, `"'`)
}

func isEnvAssignment(token string) bool {
	eq := strings.Index(token, "=")
	return eq > 0 && !strings.ContainsAny(token[:eq], `/\`)
}
