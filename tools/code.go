package tools

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// ViewFile reads file contents within workspace boundary with line-range slicing
func ViewFile(filePath string, startLine int, endLine int, workspace string, allowedRootDir ...string) (string, error) {
	root := workspaceRootFor(workspace, allowedRootDir...)
	safePath, err := IsPathSafeFrom(filePath, workspace, root)
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
func EditFile(filePath string, targetContent string, replacementContent string, workspace string, allowedRootDir ...string) (string, error) {
	root := workspaceRootFor(workspace, allowedRootDir...)
	safePath, err := IsPathSafeFrom(filePath, workspace, root)
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

// InsertContent inserts new content right before or right after an anchor text chunk in the middle of a file
func InsertContent(filePath string, anchorContent string, position string, newContent string, workspace string, allowedRootDir ...string) (string, error) {
	root := workspaceRootFor(workspace, allowedRootDir...)
	safePath, err := IsPathSafeFrom(filePath, workspace, root)
	if err != nil {
		return "", fmt.Errorf("security block: %w", err)
	}

	data, err := os.ReadFile(safePath)
	if err != nil {
		return "", fmt.Errorf("failed to read file '%s': %w", safePath, err)
	}

	content := string(data)
	if !strings.Contains(content, anchorContent) {
		return "", fmt.Errorf("anchor content '%s' not found in file '%s'", anchorContent, safePath)
	}

	var replacement string
	position = strings.ToLower(strings.TrimSpace(position))
	if position == "before" {
		replacement = newContent + anchorContent
	} else { // default to "after"
		replacement = anchorContent + newContent
	}

	updated := strings.Replace(content, anchorContent, replacement, 1)

	if err := os.WriteFile(safePath, []byte(updated), 0644); err != nil {
		return "", fmt.Errorf("failed to write inserted content to file '%s': %w", safePath, err)
	}

	return fmt.Sprintf("Successfully inserted content %s anchor in file '%s'.", position, safePath), nil
}

// AppendFile appends new content to the end of a file without overwriting existing content
func AppendFile(filePath string, appendContent string, workspace string, allowedRootDir ...string) (string, error) {
	root := workspaceRootFor(workspace, allowedRootDir...)
	safePath, err := IsPathSafeFrom(filePath, workspace, root)
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
func GrepSearch(query string, searchPath string, workspace string, allowedRootDir ...string) (string, error) {
	root := workspaceRootFor(workspace, allowedRootDir...)
	if searchPath == "" {
		searchPath = workspace
	}

	safePath, err := IsPathSafeFrom(searchPath, workspace, root)
	if err != nil {
		return "", fmt.Errorf("security block: %w", err)
	}
	safeWorkspace, err := IsPathSafeFrom(".", workspace, root)
	if err != nil {
		return "", fmt.Errorf("security block: %w", err)
	}

	queryLower := strings.ToLower(query)
	var matches []string

	_ = filepath.Walk(safePath, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return nil
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return nil
		}
		if _, err := IsPathSafeFrom(path, safeWorkspace, root); err != nil {
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
				relPath, _ := filepath.Rel(safeWorkspace, path)
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
func ListDir(dirPath string, workspace string, allowedRootDir ...string) (string, error) {
	root := workspaceRootFor(workspace, allowedRootDir...)
	if dirPath == "" {
		dirPath = workspace
	}

	safePath, err := IsPathSafeFrom(dirPath, workspace, root)
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
func ExecuteCommandWithWorkspace(ctx context.Context, cmdStr string, workspace string, allowedRootDir ...string) (string, string, error) {
	if ctx == nil {
		ctx = context.Background()
	}

	root := workspaceRootFor(workspace, allowedRootDir...)
	cmdStr = strings.TrimSpace(cmdStr)
	if cmdStr == "" {
		return "", workspace, fmt.Errorf("command string cannot be empty")
	}

	safeRoot, err := IsPathSafeFrom(".", root, root)
	if err != nil {
		return "", workspace, fmt.Errorf("security block: workspace root '%s' is not safe: %w", root, err)
	}

	safeWorkspace, err := IsPathSafeFrom(".", workspace, safeRoot)
	if err != nil {
		return "", workspace, fmt.Errorf("security block: workspace directory '%s' is outside allowed root: %w", workspace, err)
	}

	if err := ValidateCommandSafety(cmdStr, safeRoot); err != nil {
		return "", workspace, err
	}
	if err := rejectShellMetacharacters(cmdStr); err != nil {
		return "", workspace, err
	}

	fields, err := splitCommandFields(cmdStr)
	if err != nil {
		return "", workspace, err
	}
	if len(fields) == 0 {
		return "", workspace, fmt.Errorf("command string cannot be empty")
	}

	if fields[0] == "cd" {
		return executeSafeCD(fields, safeWorkspace, root, workspace)
	}

	if err := validateAllowedCommand(fields, safeWorkspace, safeRoot); err != nil {
		return "", workspace, err
	}

	env, err := sandboxEnv(safeRoot)
	if err != nil {
		return "", workspace, fmt.Errorf("failed to prepare sandbox environment: %w", err)
	}

	// 35-second execution timeout guard to prevent terminal hangs
	execCtx, cancel := context.WithTimeout(ctx, 35*time.Second)
	defer cancel()

	cmd, err := buildSandboxedCommand(execCtx, fields, safeWorkspace, safeRoot)
	if err != nil {
		return "", workspace, err
	}
	cmd.Env = env

	outBytes, err := cmd.CombinedOutput()
	output := string(outBytes)

	if execCtx.Err() == context.DeadlineExceeded {
		return output + "\n[Timeout Error] Terminal command timed out after 35 seconds.", workspace, fmt.Errorf("command execution timed out after 35s")
	}

	if err != nil {
		return output, workspace, fmt.Errorf("command exited with error: %w", err)
	}

	if output == "" {
		output = "[Command executed successfully with no output]"
	}

	return output, workspace, nil
}

func buildSandboxedCommand(ctx context.Context, fields []string, workspace string, root string) (*exec.Cmd, error) {
	execPath, err := resolveTrustedExecutable(fields[0], root)
	if err != nil {
		return nil, err
	}
	execFields := append([]string{execPath}, fields[1:]...)

	if runtime.GOOS == "darwin" {
		const sandboxExec = "/usr/bin/sandbox-exec"
		if info, err := os.Stat(sandboxExec); err != nil || info.IsDir() {
			return nil, fmt.Errorf("SECURITY BLOCK: macOS sandbox backend is unavailable")
		}
		execFields = hardenedCommandFields(execFields)
		profile := darwinSandboxProfile(root, execFields)
		args := append([]string{"-p", profile}, execFields...)
		cmd := exec.CommandContext(ctx, sandboxExec, args...)
		cmd.Dir = workspace
		return cmd, nil
	}

	return nil, fmt.Errorf("SECURITY BLOCK: no OS sandbox backend is implemented for %s", runtime.GOOS)
}

func resolveTrustedExecutable(name string, root string) (string, error) {
	if strings.ContainsAny(name, `/\`) {
		return "", fmt.Errorf("SECURITY BLOCK: executable paths and scripts are forbidden: %s", name)
	}
	path, err := exec.LookPath(name)
	if err != nil {
		return "", fmt.Errorf("SECURITY BLOCK: command '%s' was not found in PATH", name)
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("SECURITY BLOCK: command '%s' path is invalid: %w", name, err)
	}
	if eval, err := filepath.EvalSymlinks(abs); err == nil {
		abs = eval
	}
	abs = filepath.Clean(abs)
	if err := ensureNotContained(abs, root); err != nil {
		return "", fmt.Errorf("SECURITY BLOCK: refusing to execute workspace-local binary '%s': %w", abs, err)
	}
	for _, homeDir := range knownHomeDirs() {
		if pathIsWithin(abs, homeDir) {
			return "", fmt.Errorf("SECURITY BLOCK: refusing to execute user-home binary '%s'", abs)
		}
	}
	return abs, nil
}

func darwinSandboxProfile(root string, fields []string) string {
	escapedRoot := escapeSandboxProfileString(filepath.Clean(root))
	return fmt.Sprintf(`(version 1)
(deny default)
(allow process*)
(allow mach-lookup)
(allow sysctl-read)
(allow file-read*)
(allow file-write* (literal "/dev/null"))
(allow file-write* (subpath "%s"))`, escapedRoot)
}

func hardenedCommandFields(fields []string) []string {
	if len(fields) == 0 {
		return fields
	}

	switch filepath.Base(fields[0]) {
	case "git":
		if len(fields) < 2 {
			return fields
		}
		hardened := make([]string, 0, len(fields)+10)
		hardened = append(hardened, fields[0],
			"-c", "core.fsmonitor=false",
			"-c", "core.hooksPath=/dev/null",
			"-c", "diff.external=",
			"-c", "pager.branch=false",
		)
		switch fields[1] {
		case "diff", "show", "log":
			hardened = append(hardened, fields[1], "--no-ext-diff", "--no-textconv")
			hardened = append(hardened, fields[2:]...)
		default:
			hardened = append(hardened, fields[1:]...)
		}
		return hardened
	case "rg":
		hardened := make([]string, 0, len(fields)+1)
		hardened = append(hardened, fields[0], "--no-config")
		hardened = append(hardened, fields[1:]...)
		return hardened
	default:
		return fields
	}
}

func ensureNotContained(target string, root string) error {
	rel, err := filepath.Rel(filepath.Clean(root), filepath.Clean(target))
	if err != nil {
		return err
	}
	if rel == "." || (!strings.HasPrefix(rel, ".."+string(os.PathSeparator)) && rel != ".." && !filepath.IsAbs(rel)) {
		return fmt.Errorf("path is inside workspace root")
	}
	return nil
}

func pathIsWithin(target string, root string) bool {
	cleanTarget, targetErr := canonicalExistingPath(target)
	cleanRoot, rootErr := canonicalExistingPath(root)
	if targetErr != nil || rootErr != nil {
		cleanTarget = filepath.Clean(target)
		cleanRoot = filepath.Clean(root)
	}
	rel, err := filepath.Rel(cleanRoot, cleanTarget)
	if err != nil {
		return false
	}
	return rel == "." || (!strings.HasPrefix(rel, ".."+string(os.PathSeparator)) && rel != ".." && !filepath.IsAbs(rel))
}

func escapeSandboxProfileString(path string) string {
	path = strings.ReplaceAll(path, `\`, `\\`)
	path = strings.ReplaceAll(path, `"`, `\"`)
	return path
}

func workspaceRootFor(workspace string, allowedRootDir ...string) string {
	if len(allowedRootDir) > 0 && strings.TrimSpace(allowedRootDir[0]) != "" {
		return allowedRootDir[0]
	}
	return workspace
}

func executeSafeCD(fields []string, workspace string, root string, originalWorkspace string) (string, string, error) {
	if len(fields) != 2 || strings.TrimSpace(fields[1]) == "" {
		return "", originalWorkspace, fmt.Errorf("security block: cd requires an explicit directory path inside the workspace root")
	}

	targetDir, err := IsPathSafeFrom(fields[1], workspace, root)
	if err != nil {
		return "", originalWorkspace, fmt.Errorf("security block: cd target rejected: %w", err)
	}

	info, err := os.Stat(targetDir)
	if err != nil || !info.IsDir() {
		return "", originalWorkspace, fmt.Errorf("directory '%s' does not exist or is not a directory", targetDir)
	}

	return fmt.Sprintf("Working directory successfully changed to '%s'", targetDir), targetDir, nil
}

func rejectShellMetacharacters(cmdStr string) error {
	for _, r := range cmdStr {
		switch r {
		case '\n', '\r', ';', '|', '&', '>', '<', '`', '$', '(', ')', '{', '}':
			return fmt.Errorf("SECURITY BLOCK: shell metacharacter/operator %q is forbidden", r)
		}
	}
	return nil
}

func splitCommandFields(cmdStr string) ([]string, error) {
	var fields []string
	var b strings.Builder
	var quote rune

	flush := func() {
		if b.Len() > 0 {
			fields = append(fields, b.String())
			b.Reset()
		}
	}

	for _, r := range cmdStr {
		switch {
		case quote != 0:
			if r == quote {
				quote = 0
				continue
			}
			b.WriteRune(r)
		case r == '\'' || r == '"':
			quote = r
		case r == ' ' || r == '\t':
			flush()
		default:
			b.WriteRune(r)
		}
	}
	if quote != 0 {
		return nil, fmt.Errorf("security block: unterminated quoted command argument")
	}
	flush()
	return fields, nil
}

func validateAllowedCommand(fields []string, workspace string, root string) error {
	exe := strings.ToLower(fields[0])
	if strings.ContainsAny(exe, `/\`) {
		return fmt.Errorf("SECURITY BLOCK: executable paths and scripts are forbidden: %s", fields[0])
	}

	if isBlockedExecutable(exe) {
		return fmt.Errorf("SECURITY BLOCK: command '%s' is forbidden", fields[0])
	}

	args := fields[1:]
	if err := validateGenericCommandArgs(args, workspace, root); err != nil {
		return err
	}

	switch exe {
	case "pwd":
		return validatePwdArgs(args)
	case "ls", "cat", "head", "tail", "wc":
		return validateFileCommandArgs(exe, args, workspace, root)
	case "grep", "rg":
		return validateSearchCommandArgs(args, workspace, root)
	case "go":
		return validateGoCommand(args, workspace, root)
	case "git":
		return validateGitCommand(args, workspace, root)
	default:
		return fmt.Errorf("SECURITY BLOCK: command '%s' is not in the workspace command allowlist", fields[0])
	}
}

func isBlockedExecutable(exe string) bool {
	if _, destructive := destructiveCommandNames[exe]; destructive {
		return true
	}
	blocked := map[string]struct{}{
		"bash": {}, "dash": {}, "fish": {}, "make": {}, "node": {}, "npm": {}, "npx": {},
		"perl": {}, "python": {}, "python2": {}, "python3": {}, "ruby": {}, "sh": {}, "zsh": {},
		"find": {},
	}
	_, ok := blocked[exe]
	return ok
}

func validatePwdArgs(args []string) error {
	for _, arg := range args {
		if !strings.HasPrefix(arg, "-") {
			return fmt.Errorf("SECURITY BLOCK: pwd only accepts option flags")
		}
	}
	return nil
}

func validateGoCommand(args []string, workspace string, root string) error {
	if len(args) == 0 {
		return fmt.Errorf("SECURITY BLOCK: go requires an explicit safe subcommand")
	}
	subcmd := args[0]
	switch subcmd {
	case "test", "vet", "build", "list", "version", "env":
	case "mod":
		if len(args) < 2 {
			return fmt.Errorf("SECURITY BLOCK: go mod requires a safe subcommand")
		}
		switch args[1] {
		case "download", "tidy":
		default:
			return fmt.Errorf("SECURITY BLOCK: go mod subcommand '%s' is not allowed", args[1])
		}
	default:
		return fmt.Errorf("SECURITY BLOCK: go subcommand '%s' is not allowed", subcmd)
	}
	return validatePathArgsInList(args[1:], workspace, root, false)
}

func validateGitCommand(args []string, workspace string, root string) error {
	if len(args) == 0 {
		return fmt.Errorf("SECURITY BLOCK: git requires an explicit read-only subcommand")
	}
	subcmd := args[0]
	switch subcmd {
	case "status", "diff", "show", "log", "rev-parse", "branch":
	default:
		return fmt.Errorf("SECURITY BLOCK: git subcommand '%s' is not allowed", subcmd)
	}
	if subcmd == "branch" {
		for _, arg := range args[1:] {
			if !isReadOnlyGitBranchArg(arg) {
				return fmt.Errorf("SECURITY BLOCK: git branch option/argument '%s' is not read-only allowlisted", arg)
			}
		}
	}
	for _, arg := range args[1:] {
		if arg == "-c" || strings.HasPrefix(arg, "-c=") ||
			arg == "--config" || strings.HasPrefix(arg, "--config=") ||
			arg == "--ext-diff" || arg == "--textconv" || arg == "--external" ||
			strings.HasPrefix(arg, "--exec-path") {
			return fmt.Errorf("SECURITY BLOCK: git option '%s' can alter command execution and is forbidden", arg)
		}
	}
	return validatePathArgsInList(args[1:], workspace, root, false)
}

func isReadOnlyGitBranchArg(arg string) bool {
	switch arg {
	case "-a", "--all", "-r", "--remotes", "-v", "-vv", "--verbose", "--list", "--show-current":
		return true
	default:
		return false
	}
}

func validateFileCommandArgs(exe string, args []string, workspace string, root string) error {
	return validatePathArgsInList(args, workspace, root, true)
}

func validateSearchCommandArgs(args []string, workspace string, root string) error {
	for _, arg := range args {
		if arg == "--pre" || strings.HasPrefix(arg, "--pre=") ||
			arg == "--pre-glob" || strings.HasPrefix(arg, "--pre-glob=") ||
			arg == "-z" || arg == "--search-zip" {
			return fmt.Errorf("SECURITY BLOCK: search option '%s' can launch secondary executables and is forbidden", arg)
		}
	}
	return validatePathArgsInList(args, workspace, root, false)
}

func validateGenericCommandArgs(args []string, workspace string, root string) error {
	return validatePathArgsInList(args, workspace, root, false)
}

func validatePathArgsInList(args []string, workspace string, root string, allNonFlagsArePaths bool) error {
	stopOptions := false
	for i := 0; i < len(args); i++ {
		arg := strings.TrimSpace(args[i])
		if arg == "" {
			continue
		}
		if strings.ContainsRune(arg, '\x00') {
			return fmt.Errorf("SECURITY BLOCK: null bytes are forbidden in command arguments")
		}
		if arg == "--" {
			stopOptions = true
			continue
		}

		if strings.Contains(arg, "=") {
			parts := strings.SplitN(arg, "=", 2)
			if looksPathLike(parts[1]) {
				if err := validateCommandPathArg(parts[1], workspace, root); err != nil {
					return err
				}
			}
		}

		if !stopOptions && strings.HasPrefix(arg, "-") {
			if takesPathValue(arg) && i+1 < len(args) {
				i++
				if err := validateCommandPathArg(args[i], workspace, root); err != nil {
					return err
				}
			}
			continue
		}

		if allNonFlagsArePaths || looksPathLike(arg) {
			if err := validateCommandPathArg(arg, workspace, root); err != nil {
				return err
			}
		}
	}
	return nil
}

func takesPathValue(flag string) bool {
	switch flag {
	case "-C", "-o", "--output", "--output-dir", "--coverprofile", "-coverprofile":
		return true
	default:
		return false
	}
}

func looksPathLike(arg string) bool {
	if arg == "." || arg == ".." || strings.HasPrefix(arg, "./") || strings.HasPrefix(arg, "../") || strings.HasPrefix(arg, "~") {
		return true
	}
	expanded := ExpandTilde(arg)
	return filepath.IsAbs(expanded) || strings.ContainsAny(arg, `/\`)
}

func validateCommandPathArg(arg string, workspace string, root string) error {
	if strings.TrimSpace(arg) == "" {
		return fmt.Errorf("SECURITY BLOCK: empty path argument is forbidden")
	}
	if _, err := IsPathSafeFrom(arg, workspace, root); err != nil {
		return fmt.Errorf("SECURITY BLOCK: command path argument '%s' rejected: %w", arg, err)
	}
	return nil
}

func sandboxEnv(root string) ([]string, error) {
	sandboxRoot := filepath.Join(root, ".olli_sandbox")
	paths := map[string]struct {
		path string
		perm os.FileMode
	}{
		"HOME":            {filepath.Join(sandboxRoot, "home"), 0700},
		"TMPDIR":          {filepath.Join(sandboxRoot, "tmp"), 0700},
		"TMP":             {filepath.Join(sandboxRoot, "tmp"), 0700},
		"TEMP":            {filepath.Join(sandboxRoot, "tmp"), 0700},
		"GOCACHE":         {filepath.Join(sandboxRoot, "go-cache"), 0755},
		"GOMODCACHE":      {filepath.Join(sandboxRoot, "go-mod-cache"), 0755},
		"GOPATH":          {filepath.Join(sandboxRoot, "go-path"), 0755},
		"XDG_CONFIG_HOME": {filepath.Join(sandboxRoot, "xdg-config"), 0700},
		"XDG_CACHE_HOME":  {filepath.Join(sandboxRoot, "xdg-cache"), 0700},
	}

	if err := os.MkdirAll(sandboxRoot, 0700); err != nil {
		return nil, err
	}
	for _, setting := range paths {
		if _, err := IsPathSafeFrom(setting.path, root, root); err != nil {
			return nil, err
		}
		if err := os.MkdirAll(setting.path, setting.perm); err != nil {
			return nil, err
		}
		if err := os.Chmod(setting.path, setting.perm); err != nil {
			return nil, err
		}
	}

	const safePath = "/usr/bin:/bin:/usr/sbin:/sbin:/usr/local/bin:/opt/homebrew/bin:/usr/local/go/bin"
	env := make([]string, 0, len(os.Environ())+len(paths)+1)
	for _, entry := range os.Environ() {
		key := entry
		if idx := strings.Index(entry, "="); idx >= 0 {
			key = entry[:idx]
		}
		if _, overridden := paths[key]; overridden {
			continue
		}
		if strings.HasPrefix(key, "GIT_") || strings.HasPrefix(key, "DYLD_") || strings.HasPrefix(key, "LD_") {
			continue
		}
		switch key {
		case "PATH", "EDITOR", "VISUAL", "PAGER", "MANPAGER", "RIPGREP_CONFIG_PATH", "SSH_ASKPASS":
			continue
		}
		env = append(env, entry)
	}
	for key, setting := range paths {
		env = append(env, key+"="+setting.path)
	}
	env = append(env,
		"PATH="+safePath,
		"GIT_CONFIG_NOSYSTEM=1",
		"GIT_CONFIG_GLOBAL=/dev/null",
		"GIT_CONFIG_SYSTEM=/dev/null",
		"GIT_EXTERNAL_DIFF=",
		"GIT_PAGER=cat",
		"PAGER=cat",
	)
	return env, nil
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
