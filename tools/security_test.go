package tools_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/c86j224s/olli/tools"
)

func TestProtectedWorkspaceAndSecurity(t *testing.T) {
	homeDir, _ := os.UserHomeDir()

	// 1. Safe workspace subfolder (e.g. project folder)
	workspaceDir, err := filepath.Abs(".")
	if err != nil {
		t.Fatalf("failed to get current dir: %v", err)
	}

	err = tools.IsWorkspaceLocationSafe(workspaceDir)
	if err != nil {
		t.Errorf("expected project workspace dir to be safe, got: %v", err)
	}

	// 2. $HOME as workspace - Must Block!
	if homeDir != "" {
		err = tools.IsWorkspaceLocationSafe(homeDir)
		if err == nil {
			t.Errorf("expected $HOME as workspace location to be BLOCKED, but it passed!")
		}
	}

	// 3. Root '/' as workspace - Must Block!
	err = tools.IsWorkspaceLocationSafe("/")
	if err == nil {
		t.Errorf("expected Root '/' as workspace location to be BLOCKED, but it passed!")
	}

	// 4. Deleting '.' when current dir is Home - Must Block!
	if homeDir != "" {
		err = tools.ValidateCommandSafety("rm -rf .", homeDir)
		if err == nil {
			t.Errorf("expected 'rm -rf .' in Home directory to be BLOCKED, but it passed!")
		}
	}

	// 5. Deleting '.' in safe workspace
	safeFile := filepath.Join(workspaceDir, "temp_test.txt")
	_, err = tools.IsPathSafe(safeFile, workspaceDir)
	if err != nil {
		t.Errorf("expected safe file in workspace to pass, got: %v", err)
	}
}
