package subagent

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/c86j224s/olli/tools"
)

type artifactRequirement struct {
	required    bool
	extension   string
	description string
}

func artifactRequirementForSubagent(subType string) artifactRequirement {
	switch strings.ToLower(strings.TrimSpace(subType)) {
	case strings.ToLower(string(TypeDocumenter)):
		return artifactRequirement{
			required:    true,
			extension:   ".md",
			description: "Markdown documentation (*.md)",
		}
	case strings.ToLower(string(TypePresenter)):
		return artifactRequirement{
			required:    true,
			extension:   ".html",
			description: "HTML presentation (*.html)",
		}
	default:
		return artifactRequirement{}
	}
}

func isArtifactWriteTool(toolName string) bool {
	switch toolName {
	case "edit_file", "insert_content", "append_file":
		return true
	default:
		return false
	}
}

func artifactCandidatePath(args map[string]interface{}, workspace string, workspaceRoot string) (string, bool, bool) {
	rawPath, _ := args["file_path"].(string)
	rawPath = strings.TrimSpace(rawPath)
	if rawPath == "" {
		return "", false, false
	}

	safePath, err := tools.IsPathSafeFrom(rawPath, workspace, workspaceRoot)
	if err != nil {
		return "", false, false
	}

	_, err = os.Lstat(safePath)
	switch {
	case err == nil:
		return safePath, true, true
	case os.IsNotExist(err):
		return safePath, false, true
	default:
		return "", false, false
	}
}

func appendUniquePath(paths []string, path string) []string {
	for _, existing := range paths {
		if existing == path {
			return paths
		}
	}
	return append(paths, path)
}

func pathListOrNone(paths []string) string {
	if len(paths) == 0 {
		return "none"
	}
	return strings.Join(paths, ", ")
}

func validateArtifactPath(path string, workspaceRoot string, req artifactRequirement) error {
	if strings.TrimSpace(path) == "" {
		return fmt.Errorf("artifact path is empty")
	}
	if req.extension != "" && !strings.EqualFold(filepath.Ext(path), req.extension) {
		return fmt.Errorf("artifact path '%s' does not match required extension %s", path, req.extension)
	}

	lexicalPath, lexicalRoot, err := artifactLexicalPath(path, workspaceRoot)
	if err != nil {
		return err
	}
	if err := rejectArtifactSymlinkComponents(lexicalPath, lexicalRoot); err != nil {
		return err
	}

	safePath, err := tools.IsPathSafeFrom(path, workspaceRoot, workspaceRoot)
	if err != nil {
		return fmt.Errorf("artifact path rejected: %w", err)
	}
	if safePath != path {
		path = safePath
	}

	lstat, err := os.Lstat(path)
	if err != nil {
		return fmt.Errorf("artifact path '%s' cannot be inspected: %w", path, err)
	}
	if lstat.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("artifact path '%s' is a symlink and is not allowed", path)
	}
	if !lstat.Mode().IsRegular() {
		return fmt.Errorf("artifact path '%s' is not a regular file", path)
	}
	if lstat.Size() == 0 {
		return fmt.Errorf("artifact path '%s' is empty", path)
	}
	return nil
}

func artifactLexicalPath(path string, workspaceRoot string) (string, string, error) {
	root := strings.TrimSpace(tools.ExpandTilde(workspaceRoot))
	if root == "" {
		return "", "", fmt.Errorf("workspace root cannot be empty")
	}
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return "", "", fmt.Errorf("invalid workspace root: %w", err)
	}
	absRoot = filepath.Clean(absRoot)
	canonicalRoot, err := filepath.EvalSymlinks(absRoot)
	if err != nil {
		return "", "", fmt.Errorf("workspace root must be resolvable: %w", err)
	}
	canonicalRoot = filepath.Clean(canonicalRoot)

	target := strings.TrimSpace(tools.ExpandTilde(path))
	if !filepath.IsAbs(target) {
		target = filepath.Join(canonicalRoot, target)
	}
	absTarget, err := filepath.Abs(target)
	if err != nil {
		return "", "", fmt.Errorf("invalid artifact path: %w", err)
	}
	absTarget = filepath.Clean(absTarget)

	if pathContainedBy(absTarget, absRoot) {
		return absTarget, absRoot, nil
	}
	if pathContainedBy(absTarget, canonicalRoot) {
		return absTarget, canonicalRoot, nil
	}
	return "", "", fmt.Errorf("artifact path '%s' escapes workspace root '%s'", absTarget, canonicalRoot)
}

func pathContainedBy(path string, root string) bool {
	rel, err := filepath.Rel(filepath.Clean(root), filepath.Clean(path))
	if err != nil {
		return false
	}
	return rel == "." || (!strings.HasPrefix(rel, ".."+string(os.PathSeparator)) && rel != ".." && !filepath.IsAbs(rel))
}

func rejectArtifactSymlinkComponents(path string, root string) error {
	rel, err := filepath.Rel(filepath.Clean(root), filepath.Clean(path))
	if err != nil {
		return fmt.Errorf("cannot compare artifact path '%s' with workspace root '%s': %w", path, root, err)
	}

	current := filepath.Clean(root)
	for _, component := range strings.Split(rel, string(os.PathSeparator)) {
		if component == "" || component == "." {
			continue
		}
		current = filepath.Join(current, component)
		info, err := os.Lstat(current)
		if err != nil {
			return fmt.Errorf("artifact path '%s' cannot be inspected: %w", current, err)
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("artifact path '%s' is a symlink and is not allowed", current)
		}
	}
	return nil
}

func validArtifactFiles(paths []string, workspaceRoot string, req artifactRequirement) []string {
	valid := make([]string, 0, len(paths))
	for _, path := range paths {
		if err := validateArtifactPath(path, workspaceRoot, req); err == nil {
			valid = appendUniquePath(valid, path)
		}
	}
	return valid
}

func ValidateResultArtifacts(report *ResultReport, workspaceRoot string) error {
	if report == nil {
		return fmt.Errorf("subagent report is nil")
	}

	req := artifactRequirementForSubagent(report.Type)
	if !req.required {
		return nil
	}

	if len(report.ArtifactFiles) == 0 {
		return fmt.Errorf("%s subagent did not report a required artifact file (%s)", report.Type, req.description)
	}

	for _, path := range report.ArtifactFiles {
		if err := validateArtifactPath(path, workspaceRoot, req); err != nil {
			return err
		}
	}
	return nil
}
