package workflow

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func sameFile(t *testing.T, path string, before os.FileInfo) {
	t.Helper()
	after, err := os.Lstat(path)
	if err != nil {
		t.Fatal(err)
	}
	if !os.SameFile(before, after) {
		t.Fatalf("%s inode changed", path)
	}
}

func TestPhase7StagingBasenameSymlinkRaceFailsClosed(t *testing.T) {
	const (
		workflowName = "phase7-staging-race"
		runID        = "oaw_00000000000000000000000000000041"
	)
	engine, writer := prepareTerminalWriter(t, workflowName, runID)
	baseLinkat := writer.ops.linkat
	defer func() {
		writer.ops.linkat = baseLinkat
		writer.abort()
	}()

	parent := filepath.Join(engine.root, "sessions", "workflows")
	partialPath := filepath.Join(parent, writer.partial)
	movedPath := partialPath + ".moved"
	finalPath := filepath.Join(parent, runID+".jsonl")
	invalidPath := finalPath + ".invalid"
	outside := t.TempDir()
	outsideMarker := filepath.Join(outside, "marker")
	if err := os.WriteFile(outsideMarker, []byte("outside-preserve"), 0600); err != nil {
		t.Fatal(err)
	}
	outsideBefore, err := os.Lstat(outsideMarker)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(invalidPath, []byte("invalid-preserve"), 0600); err != nil {
		t.Fatal(err)
	}
	invalidBefore, err := os.Lstat(invalidPath)
	if err != nil {
		t.Fatal(err)
	}
	partialBefore, err := os.ReadFile(partialPath)
	if err != nil {
		t.Fatal(err)
	}

	entered := make(chan struct{})
	releaseCh := make(chan struct{})
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(releaseCh) }) }
	defer release()
	writer.ops.linkat = func(fromDir int, fromName string, toDir int, toName string, flags int) error {
		close(entered)
		<-releaseCh
		return baseLinkat(fromDir, fromName, toDir, toName, flags)
	}
	result := make(chan error, 1)
	go func() {
		_, err := writer.finalize(runID)
		result <- err
	}()
	select {
	case <-entered:
	case <-time.After(2 * time.Second):
		t.Fatal("finalize did not reach staging basename race barrier")
	}
	if err := os.Rename(partialPath, movedPath); err != nil {
		release()
		t.Fatal(err)
	}
	movedBefore, err := os.Lstat(movedPath)
	if err != nil {
		release()
		t.Fatal(err)
	}
	if err := os.Symlink(outsideMarker, partialPath); err != nil {
		release()
		t.Fatal(err)
	}
	release()
	select {
	case err := <-result:
		if err == nil {
			t.Fatal("finalize accepted staging basename replacement by symlink")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("finalize did not return after staging race release")
	}
	writer.ops.linkat = baseLinkat
	writer.abort()

	if _, err := engine.ReadLog(runID); err == nil {
		t.Fatal("ReadLog accepted a non-finalized staging race")
	}
	if _, err := os.Lstat(finalPath); !os.IsNotExist(err) {
		t.Fatalf("unexpected final path after staging race: %v", err)
	}
	sameFile(t, movedPath, movedBefore)
	moved, err := os.ReadFile(movedPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(moved, partialBefore) {
		t.Fatal("renamed staging content changed")
	}
	if string(readBytes(t, outsideMarker)) != "outside-preserve" {
		t.Fatal("outside marker content changed")
	}
	sameFile(t, outsideMarker, outsideBefore)
	if string(readBytes(t, invalidPath)) != "invalid-preserve" {
		t.Fatal("existing invalid quarantine changed")
	}
	sameFile(t, invalidPath, invalidBefore)
}

func TestPhase7ParentRenameSymlinkRaceFailsClosed(t *testing.T) {
	const (
		workflowName = "phase7-parent-race"
		runID        = "oaw_00000000000000000000000000000042"
	)
	engine, writer := prepareTerminalWriter(t, workflowName, runID)
	baseFstat := writer.ops.fstat
	defer func() {
		writer.ops.fstat = baseFstat
		writer.abort()
	}()

	parent := filepath.Join(engine.root, "sessions", "workflows")
	movedParent := filepath.Join(engine.root, "sessions", "workflows.moved")
	outside := t.TempDir()
	outsideFinal := filepath.Join(outside, runID+".jsonl")
	if err := os.WriteFile(outsideFinal, []byte("outside-final-preserve"), 0600); err != nil {
		t.Fatal(err)
	}
	outsideBefore, err := os.Lstat(outsideFinal)
	if err != nil {
		t.Fatal(err)
	}

	entered := make(chan struct{})
	releaseCh := make(chan struct{})
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(releaseCh) }) }
	defer release()
	first := true
	writer.ops.fstat = func(file *os.File, stat *unix.Stat_t) error {
		if first {
			first = false
			close(entered)
			<-releaseCh
		}
		return baseFstat(file, stat)
	}
	result := make(chan error, 1)
	go func() {
		_, err := writer.finalize(runID)
		result <- err
	}()
	select {
	case <-entered:
	case <-time.After(2 * time.Second):
		t.Fatal("finalize did not reach parent race barrier")
	}
	if err := os.Rename(parent, movedParent); err != nil {
		release()
		t.Fatal(err)
	}
	if err := os.Symlink(outside, parent); err != nil {
		release()
		t.Fatal(err)
	}
	release()
	select {
	case err := <-result:
		if err == nil {
			t.Fatal("finalize accepted sessions/workflows parent replacement by symlink")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("finalize did not return after parent race release")
	}
	writer.ops.fstat = baseFstat
	writer.abort()

	if _, err := engine.ReadLog(runID); err == nil {
		t.Fatal("ReadLog accepted a log beneath a symlink parent")
	}
	if string(readBytes(t, outsideFinal)) != "outside-final-preserve" {
		t.Fatal("outside final content changed")
	}
	sameFile(t, outsideFinal, outsideBefore)
	movedFinal := filepath.Join(movedParent, runID+".jsonl")
	movedMarker := filepath.Join(movedParent, markerName(runID))
	finalInfo, err := os.Lstat(movedFinal)
	if err != nil || !finalInfo.Mode().IsRegular() || finalInfo.Mode().Perm() != 0600 {
		t.Fatalf("uncommitted moved final evidence invalid: mode=%v err=%v", finalInfo, err)
	}
	markerInfo, err := os.Lstat(movedMarker)
	if err != nil || !markerInfo.Mode().IsRegular() || markerInfo.Mode().Perm() != 0600 {
		t.Fatalf("uncommitted moved marker evidence invalid: mode=%v err=%v", markerInfo, err)
	}
	if !os.SameFile(finalInfo, markerInfo) {
		t.Fatal("moved final and marker evidence do not share an inode")
	}
	movedPartial := filepath.Join(movedParent, writer.partial)
	if _, err := os.Lstat(movedPartial); !os.IsNotExist(err) {
		t.Fatalf("staging basename remained after atomic promotion: %v", err)
	}
}

func TestPhase7FinalBasenameRacesPreserveExistingEntries(t *testing.T) {
	kinds := []string{"regular", "symlink", "directory"}
	for i, kind := range kinds {
		t.Run(kind, func(t *testing.T) {
			workflowName := "phase7-final-" + kind
			runID := fmt.Sprintf("oaw_%032x", i+67)
			engine, writer := prepareTerminalWriter(t, workflowName, runID)
			baseLinkat := writer.ops.linkat
			defer func() {
				writer.ops.linkat = baseLinkat
				writer.abort()
			}()

			parent := filepath.Join(engine.root, "sessions", "workflows")
			finalPath := filepath.Join(parent, runID+".jsonl")
			invalidPath := finalPath + ".invalid"
			if err := os.WriteFile(invalidPath, []byte("invalid-preserve"), 0600); err != nil {
				t.Fatal(err)
			}
			invalidBefore, err := os.Lstat(invalidPath)
			if err != nil {
				t.Fatal(err)
			}
			outside := t.TempDir()
			var outsideTarget string
			entered := make(chan struct{})
			releaseCh := make(chan struct{})
			var releaseOnce sync.Once
			release := func() { releaseOnce.Do(func() { close(releaseCh) }) }
			defer release()
			writer.ops.linkat = func(fromDir int, fromName string, toDir int, toName string, flags int) error {
				close(entered)
				<-releaseCh
				return baseLinkat(fromDir, fromName, toDir, toName, flags)
			}
			result := make(chan error, 1)
			go func() {
				_, err := writer.finalize(runID)
				result <- err
			}()
			select {
			case <-entered:
			case <-time.After(2 * time.Second):
				t.Fatal("finalize did not reach final promotion barrier")
			}

			switch kind {
			case "regular":
				if err := os.WriteFile(finalPath, []byte("final-preserve"), 0600); err != nil {
					t.Fatal(err)
				}
			case "symlink":
				outsideTarget = filepath.Join(outside, "target")
				if err := os.WriteFile(outsideTarget, []byte("target-preserve"), 0600); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(outsideTarget, finalPath); err != nil {
					t.Fatal(err)
				}
			case "directory":
				if err := os.Mkdir(finalPath, 0700); err != nil {
					t.Fatal(err)
				}
			}
			finalBefore, err := os.Lstat(finalPath)
			if err != nil {
				t.Fatal(err)
			}
			var targetBefore os.FileInfo
			if outsideTarget != "" {
				targetBefore, err = os.Lstat(outsideTarget)
				if err != nil {
					t.Fatal(err)
				}
			}
			release()
			select {
			case err := <-result:
				if err == nil {
					t.Fatalf("finalize accepted %s final collision", kind)
				}
			case <-time.After(2 * time.Second):
				t.Fatal("finalize did not return after final collision release")
			}
			writer.ops.linkat = baseLinkat
			writer.abort()

			if _, err := engine.ReadLog(runID); err == nil {
				t.Fatalf("ReadLog accepted existing %s final", kind)
			}
			sameFile(t, finalPath, finalBefore)
			if kind == "regular" && string(readBytes(t, finalPath)) != "final-preserve" {
				t.Fatal("existing regular final content changed")
			}
			if kind == "symlink" {
				if string(readBytes(t, outsideTarget)) != "target-preserve" {
					t.Fatal("outside symlink target content changed")
				}
				sameFile(t, outsideTarget, targetBefore)
			}
			if string(readBytes(t, invalidPath)) != "invalid-preserve" {
				t.Fatal("existing invalid quarantine changed")
			}
			sameFile(t, invalidPath, invalidBefore)
		})
	}
}
