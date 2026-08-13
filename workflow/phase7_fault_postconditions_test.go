package workflow

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"golang.org/x/sys/unix"
)

func faultOnceEventOps(base eventOps, operation string, fault error) eventOps {
	var fired atomic.Bool
	failOnce := func() error {
		if fired.CompareAndSwap(false, true) {
			return fault
		}
		return nil
	}
	switch operation {
	case "syncFile":
		base.syncFile = func(file *os.File) error {
			if err := failOnce(); err != nil {
				return err
			}
			return defaultEventOps().syncFile(file)
		}
	case "fstat":
		base.fstat = func(file *os.File, stat *unix.Stat_t) error {
			if err := failOnce(); err != nil {
				return err
			}
			return defaultEventOps().fstat(file, stat)
		}
	case "fstatat":
		base.fstatat = func(directory int, name string, stat *unix.Stat_t, flags int) error {
			if err := failOnce(); err != nil {
				return err
			}
			return defaultEventOps().fstatat(directory, name, stat, flags)
		}
	case "linkat":
		base.linkat = func(oldDirectory int, oldName string, newDirectory int, newName string, flags int) error {
			if err := failOnce(); err != nil {
				return err
			}
			return defaultEventOps().linkat(oldDirectory, oldName, newDirectory, newName, flags)
		}
	case "renameNoReplace":
		base.renameNoReplace = func(fromDirectory int, fromName string, toDirectory int, toName string) error {
			if err := failOnce(); err != nil {
				return err
			}
			return defaultEventOps().renameNoReplace(fromDirectory, fromName, toDirectory, toName)
		}
	case "chmod":
		base.chmod = func(file *os.File, mode uint32) error {
			if err := failOnce(); err != nil {
				return err
			}
			return defaultEventOps().chmod(file, mode)
		}
	case "syncDir":
		base.syncDir = func(directory *os.File) error {
			if err := failOnce(); err != nil {
				return err
			}
			return defaultEventOps().syncDir(directory)
		}
	default:
		panic("unknown event operation " + operation)
	}
	return base
}

func assertFaultPathMissing(t *testing.T, path string) {
	t.Helper()
	info, err := os.Lstat(path)
	if err == nil {
		t.Fatalf("unexpected path %q with mode %s", path, info.Mode())
	}
	if !os.IsNotExist(err) {
		t.Fatalf("stat %q: %v", path, err)
	}
}

func assertFaultPartialState(t *testing.T, path string, wantPresent bool, want []byte) {
	t.Helper()
	if !wantPresent {
		assertFaultPathMissing(t, path)
		return
	}
	info, err := os.Lstat(path)
	if err != nil {
		t.Fatalf("staging log missing: %v", err)
	}
	if !info.Mode().IsRegular() {
		t.Fatalf("staging log is not regular: %s", info.Mode())
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read staging log: %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("staging log changed: got %q want %q", got, want)
	}
}

func assertFaultUnrelatedFile(t *testing.T, path string, want []byte, wantInfo os.FileInfo) {
	t.Helper()
	info, err := os.Lstat(path)
	if err != nil {
		t.Fatalf("unrelated file missing: %v", err)
	}
	if !os.SameFile(wantInfo, info) || !info.Mode().IsRegular() || info.Mode().Perm() != wantInfo.Mode().Perm() {
		t.Fatalf("unrelated file metadata changed: mode=%s", info.Mode())
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read unrelated file: %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("unrelated file changed: got %q want %q", got, want)
	}
}

func TestEventFinalizeFaultPostconditions(t *testing.T) {
	const unrelatedName = "unrelated-preserved.txt"
	cases := []struct {
		operation   string
		wantPartial bool
		wantFinal   bool
		wantMarker  bool
	}{
		{operation: "syncFile", wantPartial: true},
		{operation: "fstat", wantPartial: true},
		{operation: "fstatat", wantPartial: true},
		{operation: "linkat", wantPartial: true},
		{operation: "renameNoReplace", wantPartial: true, wantMarker: true},
		{operation: "syncDir", wantFinal: true, wantMarker: true},
		{operation: "chmod", wantFinal: true, wantMarker: true},
	}
	for i, tc := range cases {
		t.Run(tc.operation, func(t *testing.T) {
			name := "fault-" + strings.ToLower(tc.operation) + "-" + fmt.Sprint(i)
			id := fmt.Sprintf("oaw_%032x", i+80)
			engine, writer := prepareTerminalWriter(t, name, id)
			defer engine.Close()
			defer writer.close()

			unrelatedPath := filepath.Join(engine.root, "sessions", "workflows", unrelatedName)
			unrelatedBytes := []byte("unrelated content must remain byte-for-byte unchanged")
			if err := os.WriteFile(unrelatedPath, unrelatedBytes, 0600); err != nil {
				t.Fatal(err)
			}
			unrelatedInfo, err := os.Lstat(unrelatedPath)
			if err != nil {
				t.Fatal(err)
			}
			partialPath := filepath.Join(engine.root, "sessions", "workflows", writer.partial)
			partialBefore, err := os.ReadFile(partialPath)
			if err != nil {
				t.Fatal(err)
			}
			finalPath := filepath.Join(engine.root, "sessions", "workflows", id+".jsonl")
			markerPath := filepath.Join(engine.root, "sessions", "workflows", markerName(id))

			fault := errors.New(tc.operation + " injected failure")
			writer.ops = faultOnceEventOps(writer.ops, tc.operation, fault)
			_, finalizeErr := writer.finalize(id)
			if finalizeErr == nil || !errors.Is(finalizeErr, fault) {
				t.Fatalf("%s failure not surfaced exactly: %v", tc.operation, finalizeErr)
			}

			assertFaultPartialState(t, partialPath, tc.wantPartial, partialBefore)
			for path, want := range map[string]bool{finalPath: tc.wantFinal, markerPath: tc.wantMarker} {
				if !want {
					assertFaultPathMissing(t, path)
					continue
				}
				info, err := os.Lstat(path)
				if err != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0600 {
					t.Fatalf("uncommitted publication evidence %q invalid: mode=%v err=%v", path, info, err)
				}
			}
			assertFaultUnrelatedFile(t, unrelatedPath, unrelatedBytes, unrelatedInfo)
			if _, readErr := engine.ReadLog(id); readErr == nil {
				t.Fatalf("%s failure exposed a readable finalized log", tc.operation)
			}
		})
	}
}

func TestEngineLogUnavailableOnDeterministicFinalCollision(t *testing.T) {
	doc := workflowDoc("fault-engine-collision", map[string]any{"value": "{{inputs.value}}"})
	executor := &testExecutor{fn: func(context.Context, string, map[string]any) (string, error) {
		return `{"value":"ok"}`, nil
	}}
	engine := setupEngine(t, doc, executor)
	defer engine.Close()
	const runID = "oaw_00000000000000000000000000000091"
	engine.runIDGenerator = func() (string, error) { return runID, nil }

	first := engine.Run(context.Background(), "fault-engine-collision", map[string]any{"value": "x"}, allow)
	if first.Status != "succeeded" || first.LogUnavailable || first.LogPath == "" {
		t.Fatalf("first run did not publish a log: %#v", first)
	}
	second := engine.Run(context.Background(), "fault-engine-collision", map[string]any{"value": "x"}, allow)
	if second.Status != "failed" || !second.LogUnavailable || second.Error != "log_unavailable" {
		t.Fatalf("collision did not surface LogUnavailable: %#v", second)
	}
	finalPath := filepath.Join(engine.root, "sessions", "workflows", runID+".jsonl")
	if _, err := os.ReadFile(finalPath); err != nil {
		t.Fatalf("existing finalized log was not preserved: %v", err)
	}
}
