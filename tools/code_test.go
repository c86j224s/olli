package tools

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFileMutationToolsRejectFinalSymlinkTargetsTempOnly(t *testing.T) {
	root := t.TempDir()
	realFile := filepath.Join(root, "real.txt")
	if err := os.WriteFile(realFile, []byte("anchor\n"), 0600); err != nil {
		t.Fatalf("failed to create temp real file: %v", err)
	}
	linkPath := filepath.Join(root, "linked.txt")
	if err := os.Symlink(realFile, linkPath); err != nil {
		t.Skipf("symlink creation unavailable: %v", err)
	}

	mutations := []struct {
		name string
		run  func() error
	}{
		{
			name: "edit",
			run: func() error {
				_, err := EditFile("linked.txt", "", "bad\n", root, root)
				return err
			},
		},
		{
			name: "insert",
			run: func() error {
				_, err := InsertContent("linked.txt", "anchor", "after", "bad\n", root, root)
				return err
			},
		},
		{
			name: "append",
			run: func() error {
				_, err := AppendFile("linked.txt", "bad\n", root, root)
				return err
			},
		},
	}

	for _, mutation := range mutations {
		err := mutation.run()
		if err == nil || !strings.Contains(err.Error(), "symlink") {
			t.Fatalf("expected %s mutation to reject symlink target, got: %v", mutation.name, err)
		}
		data, readErr := os.ReadFile(realFile)
		if readErr != nil {
			t.Fatalf("failed to read temp real file: %v", readErr)
		}
		if string(data) != "anchor\n" {
			t.Fatalf("%s mutation changed symlink target: %q", mutation.name, string(data))
		}
	}
}

func TestFileMutationToolsRejectSymlinkDirectoryComponentTempOnly(t *testing.T) {
	root := t.TempDir()
	realDir := filepath.Join(root, "real")
	if err := os.Mkdir(realDir, 0755); err != nil {
		t.Fatalf("failed to create temp real dir: %v", err)
	}
	linkDir := filepath.Join(root, "linked-dir")
	if err := os.Symlink(realDir, linkDir); err != nil {
		t.Skipf("symlink creation unavailable: %v", err)
	}

	_, err := EditFile(filepath.Join("linked-dir", "new.txt"), "", "bad\n", root, root)
	if err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("expected symlink directory component to be rejected, got: %v", err)
	}
	if _, statErr := os.Stat(filepath.Join(realDir, "new.txt")); !os.IsNotExist(statErr) {
		t.Fatalf("write followed symlink directory component; stat err: %v", statErr)
	}
}
