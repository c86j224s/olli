package workflow

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func publicationPaths(engine *Engine, runID string) (string, string, string) {
	dir := filepath.Join(engine.root, "sessions", "workflows")
	return filepath.Join(dir, runID+".jsonl"), filepath.Join(dir, markerName(runID)), filepath.Join(dir, "."+runID+".jsonl.partial")
}

func assertPublicationState(t *testing.T, engine *Engine, runID string, wantMode os.FileMode, wantPartial bool) (os.FileInfo, os.FileInfo) {
	t.Helper()
	finalPath, markerPath, partialPath := publicationPaths(engine, runID)
	finalInfo, err := os.Lstat(finalPath)
	if err != nil {
		t.Fatalf("final log: %v", err)
	}
	markerInfo, err := os.Lstat(markerPath)
	if err != nil {
		t.Fatalf("commit marker: %v", err)
	}
	if !finalInfo.Mode().IsRegular() || !markerInfo.Mode().IsRegular() {
		t.Fatalf("publication nodes are not regular: final=%s marker=%s", finalInfo.Mode(), markerInfo.Mode())
	}
	if finalInfo.Mode().Perm() != wantMode.Perm() || markerInfo.Mode().Perm() != wantMode.Perm() {
		t.Fatalf("publication modes final=%#o marker=%#o want %#o", finalInfo.Mode().Perm(), markerInfo.Mode().Perm(), wantMode.Perm())
	}
	if !os.SameFile(finalInfo, markerInfo) {
		t.Fatal("final and commit marker do not share an inode")
	}
	if !wantPartial {
		if _, err := os.Lstat(partialPath); !os.IsNotExist(err) {
			t.Fatalf("partial path still exists: %v", err)
		}
	}
	return finalInfo, markerInfo
}

func TestPublicationMatrixSuccessCreatesCommittedPair(t *testing.T) {
	const runID = "oaw_00000000000000000000000000000101"
	engine, writer := prepareTerminalWriter(t, "publication-success", runID)
	defer writer.close()
	defer engine.Close()

	if _, err := writer.finalize(runID); err != nil {
		t.Fatal(err)
	}
	assertPublicationState(t, engine, runID, 0400, false)
	if _, err := engine.ReadLog(runID); err != nil {
		t.Fatalf("committed publication rejected: %v", err)
	}
}

func TestPublicationMatrixPreexistingTargetsArePreserved(t *testing.T) {
	const runID = "oaw_00000000000000000000000000000102"
	engine, writer := prepareTerminalWriter(t, "publication-existing", runID)
	defer writer.close()
	defer engine.Close()
	finalPath, markerPath, _ := publicationPaths(engine, runID)
	if err := os.WriteFile(finalPath, []byte("existing-final"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(markerPath, []byte("existing-marker"), 0600); err != nil {
		t.Fatal(err)
	}
	finalBefore, err := os.Lstat(finalPath)
	if err != nil {
		t.Fatal(err)
	}
	markerBefore, err := os.Lstat(markerPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := writer.finalize(runID); err == nil {
		t.Fatal("pre-existing publication target was replaced")
	}
	finalAfter, _ := os.Lstat(finalPath)
	markerAfter, _ := os.Lstat(markerPath)
	if !os.SameFile(finalBefore, finalAfter) || !os.SameFile(markerBefore, markerAfter) {
		t.Fatal("pre-existing publication target inode changed")
	}
	finalData, _ := os.ReadFile(finalPath)
	markerData, _ := os.ReadFile(markerPath)
	if string(finalData) != "existing-final" || string(markerData) != "existing-marker" {
		t.Fatal("pre-existing publication target content changed")
	}
}

func TestPublicationMatrixSyncDirFailureLeavesUncommittedPair(t *testing.T) {
	const runID = "oaw_00000000000000000000000000000103"
	engine, writer := prepareTerminalWriter(t, "publication-sync-failure", runID)
	defer writer.close()
	defer engine.Close()
	fault := errors.New("sync directory failure")
	writer.ops.syncDir = func(*os.File) error { return fault }
	if _, err := writer.finalize(runID); !errors.Is(err, fault) {
		t.Fatalf("syncDir failure = %v", err)
	}
	assertPublicationState(t, engine, runID, 0600, false)
	if _, err := engine.ReadLog(runID); err == nil {
		t.Fatal("ReadLog accepted an uncommitted publication")
	}
}

func TestPublicationMatrixPreDirectoryFsyncReadRejectsPair(t *testing.T) {
	const runID = "oaw_00000000000000000000000000000104"
	engine, writer := prepareTerminalWriter(t, "publication-barrier", runID)
	defer writer.close()
	defer engine.Close()
	entered := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	originalSync := writer.ops.syncDir
	writer.ops.syncDir = func(dir *os.File) error {
		once.Do(func() { close(entered) })
		<-release
		return originalSync(dir)
	}
	result := make(chan error, 1)
	go func() {
		_, err := writer.finalize(runID)
		result <- err
	}()
	<-entered
	if _, err := engine.ReadLog(runID); err == nil {
		t.Fatal("ReadLog accepted final and marker before directory fsync")
	}
	close(release)
	if err := <-result; err != nil {
		t.Fatal(err)
	}
}

func TestPublicationMatrixReplacementAfterRenameIsPreserved(t *testing.T) {
	const runID = "oaw_00000000000000000000000000000105"
	engine, writer := prepareTerminalWriter(t, "publication-replacement", runID)
	defer writer.close()
	defer engine.Close()
	finalPath, markerPath, _ := publicationPaths(engine, runID)
	originalRename := writer.ops.renameNoReplace
	writer.ops.renameNoReplace = func(fromDir int, from string, toDir int, to string) error {
		if err := originalRename(fromDir, from, toDir, to); err != nil {
			return err
		}
		if err := os.Remove(finalPath); err != nil {
			return err
		}
		return os.WriteFile(finalPath, []byte("replacement"), 0600)
	}
	if _, err := writer.finalize(runID); err == nil {
		t.Fatal("replacement final was accepted")
	}
	finalData, err := os.ReadFile(finalPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(finalData) != "replacement" {
		t.Fatal("replacement final was deleted or changed")
	}
	markerInfo, err := os.Lstat(markerPath)
	if err != nil {
		t.Fatal(err)
	}
	if markerInfo.Mode().Perm() != 0600 {
		t.Fatalf("marker unexpectedly committed: %#o", markerInfo.Mode().Perm())
	}
	if _, err := engine.ReadLog(runID); err == nil {
		t.Fatal("ReadLog accepted mismatched replacement final")
	}
}

func TestPublicationMatrixRenameAndChmodFaultsPreserveEvidence(t *testing.T) {
	cases := []struct {
		name      string
		fault     func(*eventWriter, error)
		wantFinal bool
	}{
		{
			name: "rename",
			fault: func(writer *eventWriter, fault error) {
				writer.ops.renameNoReplace = func(int, string, int, string) error { return fault }
			},
			wantFinal: false,
		},
		{
			name: "chmod",
			fault: func(writer *eventWriter, fault error) {
				writer.ops.chmod = func(*os.File, uint32) error { return fault }
			},
			wantFinal: true,
		},
	}
	for i, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			runID := fmt.Sprintf("oaw_%032x", 0x106+i)
			engine, writer := prepareTerminalWriter(t, "publication-fault-"+tc.name, runID)
			defer writer.close()
			defer engine.Close()
			fault := errors.New(tc.name + " failure")
			tc.fault(writer, fault)
			if _, err := writer.finalize(runID); !errors.Is(err, fault) {
				t.Fatalf("fault = %v", err)
			}
			finalPath, markerPath, partialPath := publicationPaths(engine, runID)
			if tc.wantFinal {
				assertPublicationState(t, engine, runID, 0600, false)
				if _, err := engine.ReadLog(runID); err == nil {
					t.Fatal("ReadLog accepted chmod-failed publication")
				}
			} else {
				if _, err := os.Lstat(finalPath); !os.IsNotExist(err) {
					t.Fatalf("rename fault left final: %v", err)
				}
				if _, err := os.Lstat(markerPath); err != nil {
					t.Fatalf("rename fault lost marker evidence: %v", err)
				}
				markerInfo, err := os.Lstat(markerPath)
				if err != nil || markerInfo.Mode().Perm() != 0600 {
					t.Fatalf("rename fault changed marker evidence: %v", err)
				}
				if _, err := os.Lstat(partialPath); err != nil {
					t.Fatalf("rename fault lost staging evidence: %v", err)
				}
			}
		})
	}
}
