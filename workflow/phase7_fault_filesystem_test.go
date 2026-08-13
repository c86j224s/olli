package workflow

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/unix"
)

type phase7FaultRegularSnapshot struct {
	data []byte
	info os.FileInfo
}

func phase7SnapshotRegular(t *testing.T, path string) phase7FaultRegularSnapshot {
	t.Helper()
	info, err := os.Lstat(path)
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return phase7FaultRegularSnapshot{data: data, info: info}
}

func phase7AssertRegularUnchanged(t *testing.T, path string, before phase7FaultRegularSnapshot) {
	t.Helper()
	after, err := os.Lstat(path)
	if err != nil {
		t.Fatal(err)
	}
	if !os.SameFile(before.info, after) {
		t.Fatalf("inode changed for %s", path)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before.data, data) {
		t.Fatalf("content changed for %s", path)
	}
}

func phase7FailOnceOps(base eventOps, operation string, failure error) eventOps {
	failed := false
	failOnce := func() bool {
		if failed {
			return false
		}
		failed = true
		return true
	}
	switch operation {
	case "syncFile":
		original := base.syncFile
		base.syncFile = func(file *os.File) error {
			if failOnce() {
				return failure
			}
			return original(file)
		}
	case "fstat":
		original := base.fstat
		base.fstat = func(file *os.File, stat *unix.Stat_t) error {
			if failOnce() {
				return failure
			}
			return original(file, stat)
		}
	case "fstatat":
		original := base.fstatat
		base.fstatat = func(dirfd int, name string, stat *unix.Stat_t, flags int) error {
			if failOnce() {
				return failure
			}
			return original(dirfd, name, stat, flags)
		}
	case "linkat":
		original := base.linkat
		base.linkat = func(fromDir int, fromName string, toDir int, toName string, flags int) error {
			if failOnce() {
				return failure
			}
			return original(fromDir, fromName, toDir, toName, flags)
		}
	case "renameNoReplace":
		original := base.renameNoReplace
		base.renameNoReplace = func(fromDir int, fromName string, toDir int, toName string) error {
			if failOnce() {
				return failure
			}
			return original(fromDir, fromName, toDir, toName)
		}
	case "chmod":
		original := base.chmod
		base.chmod = func(file *os.File, mode uint32) error {
			if failOnce() {
				return failure
			}
			return original(file, mode)
		}
	case "syncDir":
		original := base.syncDir
		base.syncDir = func(file *os.File) error {
			if failOnce() {
				return failure
			}
			return original(file)
		}
	default:
		panic("unknown filesystem operation: " + operation)
	}
	return base
}

func phase7AssertAbsent(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Lstat(path); !os.IsNotExist(err) {
		t.Fatalf("%s exists after failed promotion: %v", path, err)
	}
}

func phase7AssertRawFinalUnsafe(t *testing.T, path string) {
	t.Helper()
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return
	}
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().IsRegular() {
		if _, err := os.ReadFile(path); err != nil {
			t.Fatal(err)
		}
		return
	}
	if info.Mode()&os.ModeSymlink != 0 {
		t.Fatalf("failed promotion left final symlink: %s", path)
	}
	t.Fatalf("failed promotion left unsafe final node: %s", path)
}

func phase7AssertFinalNotReadLogAccepted(t *testing.T, engine *Engine, runID string) {
	t.Helper()
	if _, err := engine.ReadLog(runID); err == nil {
		t.Fatal("failed promotion exposed a ReadLog-accepted final")
	}
}

func TestFaultFilesystemFinalizeMatrix(t *testing.T) {
	cases := []struct {
		operation      string
		workflowName   string
		partialPresent bool
		finalPresent   bool
		markerPresent  bool
	}{
		{operation: "syncFile", workflowName: "fault-sync-file", partialPresent: true},
		{operation: "fstat", workflowName: "fault-fstat", partialPresent: true},
		{operation: "fstatat", workflowName: "fault-fstatat", partialPresent: true},
		{operation: "linkat", workflowName: "fault-linkat", partialPresent: true},
		{operation: "renameNoReplace", workflowName: "fault-rename", partialPresent: true, markerPresent: true},
		{operation: "syncDir", workflowName: "fault-sync-dir", finalPresent: true, markerPresent: true},
		{operation: "chmod", workflowName: "fault-chmod", finalPresent: true, markerPresent: true},
	}

	for i, tc := range cases {
		t.Run(tc.workflowName, func(t *testing.T) {
			id := fmt.Sprintf("oaw_%032x", i+70)
			engine, writer := prepareTerminalWriter(t, tc.workflowName, id)
			t.Cleanup(writer.abort)

			workflowDir := filepath.Join(engine.root, "sessions", "workflows")
			unrelatedPath := filepath.Join(workflowDir, "unrelated.txt")
			invalidPath := filepath.Join(workflowDir, id+".jsonl.invalid")
			if err := os.WriteFile(unrelatedPath, []byte("unrelated-preserved"), 0600); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(invalidPath, []byte("invalid-preserved"), 0600); err != nil {
				t.Fatal(err)
			}
			unrelatedBefore := phase7SnapshotRegular(t, unrelatedPath)
			invalidBefore := phase7SnapshotRegular(t, invalidPath)
			partialPath := filepath.Join(workflowDir, writer.partial)
			partialBefore := phase7SnapshotRegular(t, partialPath)

			failure := errors.New(tc.operation + " injected failure")
			writer.ops = phase7FailOnceOps(writer.ops, tc.operation, failure)
			if _, err := writer.finalize(id); err == nil || !errors.Is(err, failure) {
				t.Fatalf("%s failure was not surfaced: %v", tc.operation, err)
			}

			finalPath := filepath.Join(workflowDir, id+".jsonl")
			markerPath := filepath.Join(workflowDir, markerName(id))
			if tc.finalPresent {
				phase7AssertRawFinalUnsafe(t, finalPath)
				info, err := os.Lstat(finalPath)
				if err != nil || info.Mode().Perm() != 0600 {
					t.Fatalf("uncommitted final evidence mode = %v, err = %v", info, err)
				}
			} else {
				phase7AssertAbsent(t, finalPath)
			}
			if tc.markerPresent {
				info, err := os.Lstat(markerPath)
				if err != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0600 {
					t.Fatalf("uncommitted marker evidence mode = %v, err = %v", info, err)
				}
			} else {
				phase7AssertAbsent(t, markerPath)
			}
			if tc.partialPresent {
				phase7AssertRegularUnchanged(t, partialPath, partialBefore)
			} else {
				phase7AssertAbsent(t, partialPath)
			}
			phase7AssertRegularUnchanged(t, unrelatedPath, unrelatedBefore)
			phase7AssertRegularUnchanged(t, invalidPath, invalidBefore)
			phase7AssertFinalNotReadLogAccepted(t, engine, id)
		})
	}
}

func TestFaultFilesystemExistingFinalPreserved(t *testing.T) {
	cases := []struct {
		name  string
		make  func(t *testing.T, path string) phase7FaultNodeSnapshot
		check func(t *testing.T, path string, before phase7FaultNodeSnapshot)
	}{
		{
			name: "regular",
			make: func(t *testing.T, path string) phase7FaultNodeSnapshot {
				if err := os.WriteFile(path, []byte("final-preserved"), 0600); err != nil {
					t.Fatal(err)
				}
				return phase7CaptureFaultNode(t, path, false)
			},
			check: phase7AssertFaultNodeUnchanged,
		},
		{
			name: "symlink",
			make: func(t *testing.T, path string) phase7FaultNodeSnapshot {
				target := filepath.Join(t.TempDir(), "target")
				if err := os.WriteFile(target, []byte("target-preserved"), 0600); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(target, path); err != nil {
					t.Fatal(err)
				}
				return phase7CaptureFaultNode(t, path, true)
			},
			check: phase7AssertFaultNodeUnchanged,
		},
		{
			name: "directory",
			make: func(t *testing.T, path string) phase7FaultNodeSnapshot {
				if err := os.Mkdir(path, 0700); err != nil {
					t.Fatal(err)
				}
				return phase7CaptureFaultNode(t, path, false)
			},
			check: phase7AssertFaultNodeUnchanged,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			id := "oaw_00000000000000000000000000000080"
			engine, writer := prepareTerminalWriter(t, "fault-final-"+tc.name, id)
			t.Cleanup(writer.abort)
			workflowDir := filepath.Join(engine.root, "sessions", "workflows")
			finalPath := filepath.Join(workflowDir, id+".jsonl")
			unrelatedPath := filepath.Join(workflowDir, "unrelated.txt")
			invalidPath := filepath.Join(workflowDir, id+".jsonl.invalid")
			if err := os.WriteFile(unrelatedPath, []byte("unrelated-preserved"), 0600); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(invalidPath, []byte("invalid-preserved"), 0600); err != nil {
				t.Fatal(err)
			}
			finalBefore := tc.make(t, finalPath)
			unrelatedBefore := phase7SnapshotRegular(t, unrelatedPath)
			invalidBefore := phase7SnapshotRegular(t, invalidPath)

			if _, err := writer.finalize(id); err == nil {
				t.Fatal("existing final was replaced")
			}
			tc.check(t, finalPath, finalBefore)
			phase7AssertRegularUnchanged(t, unrelatedPath, unrelatedBefore)
			phase7AssertRegularUnchanged(t, invalidPath, invalidBefore)
			if _, err := engine.ReadLog(id); err == nil {
				t.Fatal("existing final unexpectedly became accepted")
			}
		})
	}
}

type phase7FaultNodeSnapshot struct {
	info   os.FileInfo
	isLink bool
	data   []byte
	target string
}

func phase7CaptureFaultNode(t *testing.T, path string, isLink bool) phase7FaultNodeSnapshot {
	t.Helper()
	info, err := os.Lstat(path)
	if err != nil {
		t.Fatal(err)
	}
	snapshot := phase7FaultNodeSnapshot{info: info, isLink: isLink}
	if isLink {
		snapshot.target, err = os.Readlink(path)
		if err != nil {
			t.Fatal(err)
		}
	} else if info.IsDir() {
		snapshot.data = nil
	} else {
		snapshot.data, err = os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
	}
	return snapshot
}

func phase7AssertFaultNodeUnchanged(t *testing.T, path string, before phase7FaultNodeSnapshot) {
	t.Helper()
	after, err := os.Lstat(path)
	if err != nil {
		t.Fatal(err)
	}
	if !os.SameFile(before.info, after) {
		t.Fatalf("existing node inode changed for %s", path)
	}
	if before.isLink {
		target, err := os.Readlink(path)
		if err != nil {
			t.Fatal(err)
		}
		if target != before.target {
			t.Fatalf("existing symlink target changed for %s", path)
		}
		return
	}
	if before.info.IsDir() {
		return
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before.data, data) {
		t.Fatalf("existing final content changed for %s", path)
	}
}
